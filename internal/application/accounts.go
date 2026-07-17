package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/cpaabi"
	"github.com/magicvr/cpa-grok-panel/internal/domain"
	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

type AuthHost interface {
	ListAuthFiles() ([]domain.AuthFile, error)
	GetAuthFile(authIndex string) (cpaabi.AuthDocument, error)
	SaveAuthFile(name string, document cpaabi.AuthDocument) error
}

type AccountsService struct {
	host             AuthHost
	store            *stateinfra.Store
	now              func() time.Time
	settingsFallback Settings
	priorityWriter   PriorityWriter
	write            sync.Mutex
}

func NewAccountsService(host AuthHost, store *stateinfra.Store, now func() time.Time, configured ...Settings) *AccountsService {
	settings := DefaultSettings()
	if len(configured) > 0 {
		settings = configured[0]
	}
	return &AccountsService{host: host, store: store, now: now, settingsFallback: settings}
}

func (service *AccountsService) SetPriorityWriter(writer PriorityWriter) {
	service.write.Lock()
	defer service.write.Unlock()
	if isNilPriorityWriter(writer) {
		writer = nil
	}
	service.priorityWriter = writer
}

type AccountError struct {
	Code       string
	Message    string
	HTTPStatus int
	Retryable  bool
}

func (err *AccountError) Error() string { return err.Message }

func AsAccountError(err error) *AccountError {
	var accountErr *AccountError
	if errors.As(err, &accountErr) {
		return accountErr
	}
	return &AccountError{Code: "host_write_failed", Message: err.Error(), HTTPStatus: 502, Retryable: true}
}

func (service *AccountsService) List(search string) ([]domain.AccountView, time.Time, error) {
	files, err := service.host.ListAuthFiles()
	if err != nil {
		return nil, time.Time{}, err
	}
	now := service.now().UTC()
	settings := service.settings()
	if err := service.reconcileAppliedDemotions(files, settings); err != nil {
		return nil, time.Time{}, err
	}
	snapshot := service.store.View()
	items := make([]domain.AccountView, 0, len(files))
	for _, file := range files {
		if !domain.IsXAIOAuth(file) || file.AuthIndex == "" || !strings.HasSuffix(file.Name, ".json") {
			continue
		}
		if search != "" && !containsFold(file.AuthIndex, search) && !containsFold(file.Name, search) && !containsFold(file.Email, search) {
			continue
		}
		state := snapshot.Accounts[file.AuthIndex]
		// Display-only overlay for Free/unknown usage bars; cached plan is preserved forever until manual refresh.
		state.Quota = DisplayQuota(state, settings.FreeUserDailyTokenLimit)
		items = append(items, domain.ProjectAccount(file, state, now, settings.DemotionPriority))
	}
	service.decorateBotFlags(items, settings.BatchOperationConcurrency)
	sort.Slice(items, func(i, j int) bool {
		return items[i].ExactFileName < items[j].ExactFileName
	})
	return items, now, nil
}

// ReconcileDemotions makes an applied record retryable when the host priority
// has drifted above the currently configured demotion priority.
func (service *AccountsService) ReconcileDemotions() error {
	files, err := service.host.ListAuthFiles()
	if err != nil {
		return hostError("auth_list_failed", err)
	}
	return service.reconcileAppliedDemotions(files, service.settings())
}

func (service *AccountsService) reconcileAppliedDemotions(files []domain.AuthFile, settings Settings) error {
	priorities := make(map[string]domain.AuthFile, len(files))
	for _, file := range files {
		priorities[file.AuthIndex] = file
	}
	current := service.store.View()
	drifted := make(map[string]domain.AuthFile)
	for authIndex, state := range current.Accounts {
		file, exists := priorities[authIndex]
		demotion := state.Demotion.Normalized()
		if exists && demotion.State == "applied" && file.Priority > settings.DemotionPriority {
			drifted[authIndex] = file
		}
	}
	if len(drifted) == 0 {
		return nil
	}
	err := service.store.Update(func(snapshot *stateinfra.Snapshot) error {
		for authIndex, file := range drifted {
			state := snapshot.Accounts[authIndex]
			demotion := state.Demotion.Normalized()
			if demotion.State != "applied" {
				continue
			}
			target := settings.DemotionPriority
			demotion.State = "requested"
			demotion.TargetPriority = &target
			demotion.FailureCode = "priority_drift"
			state.ExactFileName = file.Name
			state.Demotion = demotion
			snapshot.Accounts[authIndex] = state
		}
		return nil
	})
	if err != nil {
		return &AccountError{Code: "state_write_failed", Message: err.Error(), HTTPStatus: 503, Retryable: true}
	}
	return nil
}

func (service *AccountsService) decorateBotFlags(items []domain.AccountView, configuredConcurrency int) {
	if len(items) == 0 {
		return
	}
	concurrency := configuredConcurrency
	if concurrency < 1 {
		concurrency = 10
	}
	if concurrency > 10 {
		concurrency = 10
	}
	jobs := make(chan int)
	var workers sync.WaitGroup
	workers.Add(concurrency)
	for range concurrency {
		go func() {
			defer workers.Done()
			for index := range jobs {
				document, err := service.host.GetAuthFile(items[index].AuthIndex)
				if err != nil {
					continue
				}
				result := detectBotFlag(document)
				items[index].BotFlagged = result.flagged
				items[index].BotFlagKnown = result.known
				items[index].BotFlagSource = result.source
			}
		}()
	}
	for index := range items {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
}

// SetEnabled is kept for API completeness, but CPA host.auth.save does not apply
// metadata.disabled onto runtime auth.Disabled (buildAuthFromFileData always
// leaves StatusActive). The panel uses PATCH /v0/management/auth-files/status.
func (service *AccountsService) SetEnabled(authIndex, exactFileName string, enabled bool) (domain.AccountView, error) {
	service.write.Lock()
	defer service.write.Unlock()

	file, err := service.resolveExact(authIndex, exactFileName)
	if err != nil {
		return domain.AccountView{}, err
	}
	if file.Disabled == !enabled {
		return service.project(file), nil
	}
	document, err := service.host.GetAuthFile(authIndex)
	if err != nil {
		return domain.AccountView{}, hostError("auth_get_failed", err)
	}
	document["disabled"] = !enabled
	if err := service.host.SaveAuthFile(exactFileName, document); err != nil {
		return domain.AccountView{}, hostError("auth_save_failed", err)
	}
	// Prefer runtime list; if CPA ignored disabled, surface a clear error so UI
	// can fall back to Management status (panel already uses that path).
	verified, err := service.resolveExact(authIndex, exactFileName)
	if err != nil {
		return domain.AccountView{}, err
	}
	if verified.Disabled != !enabled {
		return domain.AccountView{}, &AccountError{
			Code:       "host_disabled_not_applied",
			Message:    "host.auth.save 已写文件但运行时未应用 disabled；请使用 Management PATCH /auth-files/status",
			HTTPStatus: 502, Retryable: true,
		}
	}
	return service.project(verified), nil
}

func (service *AccountsService) RestorePriority(authIndex, exactFileName string) (domain.AccountView, error) {
	service.write.Lock()
	defer service.write.Unlock()

	file, err := service.resolveExact(authIndex, exactFileName)
	if err != nil {
		return domain.AccountView{}, err
	}
	settings := service.settings()
	demotion := service.store.View().Accounts[authIndex].Demotion.Normalized()
	restorePriority, recordedRestore := service.restoreTarget(file.Priority, demotion, settings)
	if restorePriority == nil {
		return domain.AccountView{}, &AccountError{Code: "demotion_not_applied", Message: "该账号当前不在降权档位", HTTPStatus: 409}
	}
	document, err := service.host.GetAuthFile(authIndex)
	if err != nil {
		return domain.AccountView{}, hostError("auth_get_failed", err)
	}
	if priority, ok := documentInt(document, "priority"); ok && priority != file.Priority {
		return domain.AccountView{}, &AccountError{Code: "priority_superseded", Message: "当前优先级已被其他操作修改，请刷新后确认", HTTPStatus: 409}
	}
	return service.restorePriorityLocked(file, settings, *restorePriority, recordedRestore, document, true)
}

// RestorePriorityAfterCooldown restores one eligible account. A failed or
// unknown bot check is allowed; only an explicit bot flag keeps the account at
// the demoted priority.
func (service *AccountsService) RestorePriorityAfterCooldown(authIndex string) (bool, error) {
	service.write.Lock()
	defer service.write.Unlock()

	settings := service.settings()
	if !settings.CooldownRestoreEnabled {
		return false, nil
	}
	account, exists := service.store.View().Accounts[authIndex]
	if !exists {
		return false, nil
	}
	demotion := account.Demotion.Normalized()
	if demotion.TriggeredAt == nil || demotion.RestoreCooldownHours <= 0 {
		return false, nil
	}
	eligibleAt := demotion.TriggeredAt.Add(time.Duration(demotion.RestoreCooldownHours) * time.Hour)
	if service.now().UTC().Before(eligibleAt) {
		return false, nil
	}
	file, err := service.resolveByAuthIndex(authIndex)
	if err != nil {
		return false, err
	}
	if file.Priority > settings.DemotionPriority {
		return false, nil
	}
	restorePriority, recordedRestore := service.restoreTarget(file.Priority, demotion, settings)
	if restorePriority == nil {
		return false, nil
	}
	document, err := service.host.GetAuthFile(authIndex)
	if err != nil {
		return false, hostError("auth_get_failed", err)
	}
	if bot := detectBotFlag(document); bot.known && bot.flagged {
		return false, nil
	}
	if priority, ok := documentInt(document, "priority"); ok && priority != file.Priority {
		return false, &AccountError{Code: "priority_superseded", Message: "当前优先级已被其他操作修改，请刷新后确认", HTTPStatus: 409}
	}
	if _, err := service.restorePriorityLocked(file, settings, *restorePriority, recordedRestore, document, false); err != nil {
		return false, err
	}
	return true, nil
}

func (service *AccountsService) restorePriorityLocked(file domain.AuthFile, settings Settings, restorePriority int, recordedRestore bool, document cpaabi.AuthDocument, manual bool) (domain.AccountView, error) {
	if err := service.writePriority(file, restorePriority, document); err != nil {
		service.recordDemotionFailure(file.AuthIndex, "restore_save_failed", false)
		return domain.AccountView{}, err
	}
	verified, err := service.resolveExact(file.AuthIndex, file.Name)
	if err != nil {
		service.recordDemotionFailure(file.AuthIndex, "restore_verify_failed", false)
		return domain.AccountView{}, err
	}
	if verified.Priority != restorePriority {
		service.recordDemotionFailure(file.AuthIndex, "restore_verify_failed", false)
		return domain.AccountView{}, &AccountError{Code: "write_verification_failed", Message: "优先级恢复写后校验不一致", HTTPStatus: 502, Retryable: true}
	}
	if err := service.store.Update(func(snapshot *stateinfra.Snapshot) error {
		state := snapshot.Accounts[file.AuthIndex]
		state.ExactFileName = file.Name
		if !recordedRestore {
			state.Demotion.BaselinePriority = intPointer(restorePriority)
			state.Demotion.TargetPriority = intPointer(settings.DemotionPriority)
		}
		state.Demotion.State = "restored"
		state.Demotion.FailureCode = ""
		if manual {
			state.Demotion.RestoreCooldownHours = 0
		}
		state.Failure = domain.FailureState{}
		snapshot.Accounts[file.AuthIndex] = state
		return nil
	}); err != nil {
		return domain.AccountView{}, &AccountError{Code: "state_write_failed", Message: err.Error(), HTTPStatus: 503, Retryable: true}
	}
	return service.project(verified), nil
}

func (service *AccountsService) Demote(authIndex, exactFileName string) (domain.AccountView, error) {
	service.write.Lock()
	defer service.write.Unlock()

	file, err := service.resolveExact(authIndex, exactFileName)
	if err != nil {
		return domain.AccountView{}, err
	}
	targetPriority := service.settings().DemotionPriority
	if file.Priority <= targetPriority {
		return service.project(file), nil
	}
	document, err := service.host.GetAuthFile(authIndex)
	if err != nil {
		return domain.AccountView{}, hostError("auth_get_failed", err)
	}
	if priority, ok := documentInt(document, "priority"); ok && priority != file.Priority {
		return domain.AccountView{}, &AccountError{Code: "priority_superseded", Message: "降权前优先级已变化", HTTPStatus: 409}
	}
	now := service.now().UTC()
	previousDemotion := service.store.View().Accounts[authIndex].Demotion.Normalized()
	demotion := domain.DemotionState{
		State: "requested", BaselinePriority: intPointer(file.Priority), TargetPriority: intPointer(targetPriority), TriggeredAt: &now,
		RestoreCooldownHours: previousDemotion.RestoreCooldownHours,
	}
	if err := service.store.Update(func(snapshot *stateinfra.Snapshot) error {
		state := snapshot.Accounts[authIndex]
		state.ExactFileName = exactFileName
		state.Demotion = demotion
		snapshot.Accounts[authIndex] = state
		return nil
	}); err != nil {
		return domain.AccountView{}, &AccountError{Code: "state_write_failed", Message: err.Error(), HTTPStatus: 503, Retryable: true}
	}
	if err := service.writePriority(file, targetPriority, document); err != nil {
		service.recordDemotionFailure(authIndex, AsAccountError(err).Code, false)
		return domain.AccountView{}, err
	}
	verified, err := service.resolveExact(authIndex, exactFileName)
	if err != nil {
		failureCode := demotionVerificationFailureCode(err)
		service.recordDemotionFailure(authIndex, failureCode, isPermanentDemotionFailure(failureCode))
		return domain.AccountView{}, err
	}
	if verified.Priority != targetPriority {
		service.recordDemotionFailure(authIndex, "demotion_verify_failed", false)
		return domain.AccountView{}, &AccountError{Code: "write_verification_failed", Message: "降权写后校验不一致", HTTPStatus: 502, Retryable: true}
	}
	if err := service.markDemotionApplied(authIndex, exactFileName); err != nil {
		return domain.AccountView{}, &AccountError{Code: "state_write_failed", Message: err.Error(), HTTPStatus: 503, Retryable: true}
	}
	return service.project(verified), nil
}

func (service *AccountsService) ApplyRequestedDemotion(authIndex string, targetPriority int) error {
	service.write.Lock()
	defer service.write.Unlock()

	account := service.store.View().Accounts[authIndex]
	demotion := account.Demotion.Normalized()
	if demotion.State != "requested" {
		return nil
	}
	file, err := service.resolveByAuthIndex(authIndex)
	if err != nil {
		failureCode := AsAccountError(err).Code
		service.recordDemotionFailure(authIndex, failureCode, isPermanentDemotionFailure(failureCode))
		return err
	}
	if account.ExactFileName != "" && account.ExactFileName != file.Name {
		err := &AccountError{Code: "account_mapping_changed", Message: "账号文件映射已变化，请刷新列表", HTTPStatus: 409}
		service.recordDemotionFailure(authIndex, err.Code, true)
		return err
	}
	if demotion.TargetPriority == nil {
		demotion.TargetPriority = intPointer(targetPriority)
	}
	if file.Priority <= *demotion.TargetPriority {
		if demotion.BaselinePriority == nil {
			demotion.BaselinePriority = intPointer(service.settings().DefaultRestorePriority)
		}
		if err := service.store.Update(func(snapshot *stateinfra.Snapshot) error {
			state := snapshot.Accounts[authIndex]
			state.ExactFileName = file.Name
			demotion.State = "applied"
			demotion.FailureCode = ""
			now := service.now().UTC()
			demotion.TriggeredAt = &now
			demotion.RestoreCooldownHours = nextRestoreCooldownHours(demotion.RestoreCooldownHours)
			state.Demotion = demotion
			snapshot.Accounts[authIndex] = state
			return nil
		}); err != nil {
			return &AccountError{Code: "state_write_failed", Message: err.Error(), HTTPStatus: 503, Retryable: true}
		}
		return nil
	}
	priorityDrift := demotion.FailureCode == "priority_drift"
	if demotion.BaselinePriority != nil && !priorityDrift {
		if file.Priority != *demotion.BaselinePriority {
			err := &AccountError{Code: "priority_superseded", Message: "降权前优先级已变化", HTTPStatus: 409}
			service.recordDemotionFailure(authIndex, err.Code, true)
			return err
		}
	} else if demotion.BaselinePriority == nil {
		demotion.BaselinePriority = intPointer(file.Priority)
		if err := service.store.Update(func(snapshot *stateinfra.Snapshot) error {
			state := snapshot.Accounts[authIndex]
			state.ExactFileName = file.Name
			state.Demotion = demotion
			snapshot.Accounts[authIndex] = state
			return nil
		}); err != nil {
			service.recordDemotionFailure(authIndex, "state_write_failed", false)
			return &AccountError{Code: "state_write_failed", Message: err.Error(), HTTPStatus: 503, Retryable: true}
		}
	}
	var document cpaabi.AuthDocument
	if service.priorityWriter == nil {
		document, err = service.host.GetAuthFile(authIndex)
		if err != nil {
			service.recordDemotionFailure(authIndex, "auth_get_failed", false)
			return hostError("auth_get_failed", err)
		}
		if priority, ok := documentInt(document, "priority"); ok && priority != file.Priority {
			err := &AccountError{Code: "priority_superseded", Message: "降权前优先级已变化", HTTPStatus: 409}
			service.recordDemotionFailure(authIndex, err.Code, true)
			return err
		}
	}
	if err := service.writePriority(file, *demotion.TargetPriority, document); err != nil {
		service.recordDemotionFailure(authIndex, AsAccountError(err).Code, false)
		return err
	}
	verified, err := service.resolveExact(authIndex, file.Name)
	if err != nil {
		failureCode := demotionVerificationFailureCode(err)
		terminal := isPermanentDemotionFailure(failureCode)
		service.recordDemotionFailure(authIndex, failureCode, terminal)
		if terminal {
			return err
		}
		return &AccountError{Code: "demotion_verify_failed", Message: err.Error(), HTTPStatus: 502, Retryable: true}
	}
	if verified.Priority != *demotion.TargetPriority {
		service.recordDemotionFailure(authIndex, "demotion_verify_failed", false)
		return &AccountError{Code: "write_verification_failed", Message: "降权写后校验不一致", HTTPStatus: 502, Retryable: true}
	}
	if err := service.markDemotionApplied(authIndex, file.Name); err != nil {
		service.recordDemotionFailure(authIndex, "state_write_failed", false)
		return &AccountError{Code: "state_write_failed", Message: err.Error(), HTTPStatus: 503, Retryable: true}
	}
	return nil
}

// ConfirmPriorityWrite verifies a panel Management fields PATCH before changing
// plugin demotion/failure state.
func (service *AccountsService) ConfirmPriorityWrite(authIndex, exactFileName, operation string, priority int, previousPriority *int) (domain.AccountView, error) {
	service.write.Lock()
	defer service.write.Unlock()

	file, err := service.resolveExact(authIndex, exactFileName)
	if err != nil {
		return domain.AccountView{}, err
	}
	if file.Priority != priority {
		return domain.AccountView{}, &AccountError{Code: "write_verification_failed", Message: "Management 优先级写后校验不一致", HTTPStatus: 502, Retryable: true}
	}
	settings := service.settings()
	now := service.now().UTC()
	err = service.store.Update(func(snapshot *stateinfra.Snapshot) error {
		state := snapshot.Accounts[authIndex]
		state.ExactFileName = exactFileName
		switch operation {
		case "demote":
			if previousPriority == nil {
				return &AccountError{Code: "invalid_argument", Message: "降权确认需要写前优先级", HTTPStatus: 400}
			}
			previous := *previousPriority
			state.Demotion = domain.DemotionState{
				State: "applied", BaselinePriority: &previous, TargetPriority: intPointer(priority), TriggeredAt: &now,
				RestoreCooldownHours: nextRestoreCooldownHours(state.Demotion.RestoreCooldownHours),
			}
		case "restore":
			if state.Demotion.BaselinePriority == nil {
				state.Demotion.BaselinePriority = intPointer(priority)
			}
			state.Demotion.TargetPriority = intPointer(settings.DemotionPriority)
			state.Demotion.State = "restored"
			state.Demotion.FailureCode = ""
			state.Demotion.RestoreCooldownHours = 0
		case "set":
			state.Demotion = domain.DemotionState{State: "none"}
		default:
			return &AccountError{Code: "invalid_argument", Message: "operation 必须是 demote、restore 或 set", HTTPStatus: 400}
		}
		state.Failure = domain.FailureState{}
		snapshot.Accounts[authIndex] = state
		return nil
	})
	if err != nil {
		return domain.AccountView{}, err
	}
	return service.project(file), nil
}

func (service *AccountsService) ClearState(authIndex, exactFileName string) error {
	if strings.TrimSpace(authIndex) == "" || strings.TrimSpace(exactFileName) == "" {
		return &AccountError{Code: "invalid_argument", Message: "auth_index 与 exact_file_name 均为必填", HTTPStatus: 400}
	}
	return service.store.Update(func(snapshot *stateinfra.Snapshot) error {
		state, exists := snapshot.Accounts[authIndex]
		if !exists {
			return nil
		}
		if state.ExactFileName != "" && state.ExactFileName != exactFileName {
			return &AccountError{Code: "account_mapping_changed", Message: "本地账号状态映射已变化，未清理 state", HTTPStatus: 409}
		}
		delete(snapshot.Accounts, authIndex)
		return nil
	})
}

func (service *AccountsService) ClearDiagnostic(authIndex, exactFileName string) error {
	if strings.TrimSpace(authIndex) == "" || strings.TrimSpace(exactFileName) == "" {
		return &AccountError{Code: "invalid_argument", Message: "auth_index 与 exact_file_name 均为必填", HTTPStatus: 400}
	}
	return service.store.Update(func(snapshot *stateinfra.Snapshot) error {
		state, exists := snapshot.Accounts[authIndex]
		if !exists {
			return nil
		}
		if state.ExactFileName != "" && state.ExactFileName != exactFileName {
			return &AccountError{Code: "account_mapping_changed", Message: "本地账号状态映射已变化，未清空诊断", HTTPStatus: 409}
		}
		state.ExactFileName = exactFileName
		state.Failure = domain.FailureState{}
		snapshot.Accounts[authIndex] = state
		return nil
	})
}

func (service *AccountsService) resolveExact(authIndex, exactFileName string) (domain.AuthFile, error) {
	if strings.TrimSpace(authIndex) == "" || strings.TrimSpace(exactFileName) == "" {
		return domain.AuthFile{}, &AccountError{Code: "invalid_argument", Message: "auth_index 与 exact_file_name 均为必填", HTTPStatus: 400}
	}
	files, err := service.host.ListAuthFiles()
	if err != nil {
		return domain.AuthFile{}, hostError("auth_list_failed", err)
	}
	for _, file := range files {
		if file.AuthIndex == authIndex {
			if !domain.IsXAIOAuth(file) || !strings.HasSuffix(file.Name, ".json") {
				return domain.AuthFile{}, &AccountError{Code: "unsupported_account", Message: "仅支持 xAI OAuth 账号", HTTPStatus: 409}
			}
			if file.Name != exactFileName {
				return domain.AuthFile{}, &AccountError{Code: "account_mapping_changed", Message: "账号文件映射已变化，请刷新列表", HTTPStatus: 409}
			}
			return file, nil
		}
	}
	return domain.AuthFile{}, &AccountError{Code: "account_not_found", Message: "账号不存在", HTTPStatus: 404}
}

func (service *AccountsService) resolveByAuthIndex(authIndex string) (domain.AuthFile, error) {
	files, err := service.host.ListAuthFiles()
	if err != nil {
		return domain.AuthFile{}, hostError("auth_list_failed", err)
	}
	for _, file := range files {
		if file.AuthIndex == authIndex {
			if !domain.IsXAIOAuth(file) || !strings.HasSuffix(file.Name, ".json") {
				return domain.AuthFile{}, &AccountError{Code: "unsupported_account", Message: "仅支持 xAI OAuth 账号", HTTPStatus: 409}
			}
			return file, nil
		}
	}
	return domain.AuthFile{}, &AccountError{Code: "account_not_found", Message: "账号不存在", HTTPStatus: 404}
}

// localQuotaFallback kept as alias for older tests; prefer DisplayQuota.
func localQuotaFallback(state domain.AccountState, limit uint64) domain.QuotaSnapshot {
	return DisplayQuota(state, limit)
}

func (service *AccountsService) project(file domain.AuthFile) domain.AccountView {
	return domain.ProjectAccount(file, service.store.View().Accounts[file.AuthIndex], service.now().UTC(), service.settings().DemotionPriority)
}

func (service *AccountsService) restoreTarget(priority int, demotion domain.DemotionState, settings Settings) (*int, bool) {
	if priority > settings.DemotionPriority {
		return nil, false
	}
	if demotion.BaselinePriority != nil {
		return intPointer(*demotion.BaselinePriority), true
	}
	return intPointer(settings.DefaultRestorePriority), false
}

func (service *AccountsService) settings() Settings {
	if settings := service.store.View().Settings; settings != nil {
		return *settings
	}
	return service.settingsFallback
}

func (service *AccountsService) markDemotionApplied(authIndex, exactFileName string) error {
	return service.store.Update(func(snapshot *stateinfra.Snapshot) error {
		state := snapshot.Accounts[authIndex]
		state.ExactFileName = exactFileName
		state.Demotion.State = "applied"
		state.Demotion.FailureCode = ""
		now := service.now().UTC()
		state.Demotion.TriggeredAt = &now
		state.Demotion.RestoreCooldownHours = nextRestoreCooldownHours(state.Demotion.RestoreCooldownHours)
		snapshot.Accounts[authIndex] = state
		return nil
	})
}

func (service *AccountsService) writePriority(file domain.AuthFile, priority int, document cpaabi.AuthDocument) error {
	if service.priorityWriter != nil {
		if err := service.priorityWriter.SetPriority(file.Name, priority); err != nil {
			return hostError("management_fields_failed", err)
		}
		return nil
	}
	if document == nil {
		var err error
		document, err = service.host.GetAuthFile(file.AuthIndex)
		if err != nil {
			return hostError("auth_get_failed", err)
		}
	}
	document["priority"] = priority
	if err := service.host.SaveAuthFile(file.Name, document); err != nil {
		return hostError("auth_save_failed", err)
	}
	return nil
}

func (service *AccountsService) recordDemotionFailure(authIndex, code string, terminal bool) {
	_ = service.store.Update(func(snapshot *stateinfra.Snapshot) error {
		state := snapshot.Accounts[authIndex]
		if terminal {
			state.Demotion.State = "failed"
		} else {
			state.Demotion.State = "requested"
		}
		state.Demotion.FailureCode = code
		snapshot.Accounts[authIndex] = state
		return nil
	})
}

func demotionVerificationFailureCode(err error) string {
	code := AsAccountError(err).Code
	if isPermanentDemotionFailure(code) {
		return code
	}
	return "demotion_verify_failed"
}

func isPermanentDemotionFailure(code string) bool {
	switch code {
	case "priority_superseded", "account_not_found", "account_mapping_changed":
		return true
	default:
		return false
	}
}

func hostError(code string, err error) *AccountError {
	return &AccountError{Code: code, Message: err.Error(), HTTPStatus: 502, Retryable: true}
}

func documentInt(document cpaabi.AuthDocument, key string) (int, bool) {
	value, ok := document[key]
	if !ok {
		return 0, false
	}
	data, err := json.Marshal(value)
	if err != nil {
		return 0, false
	}
	var number int
	if err := json.Unmarshal(data, &number); err != nil {
		return 0, false
	}
	return number, true
}

func intPointer(value int) *int { return &value }

func nextRestoreCooldownHours(current int) int {
	switch {
	case current <= 0:
		return 6
	case current <= 6:
		return 12
	default:
		return 24
	}
}

func (service *AccountsService) String() string {
	return fmt.Sprintf("accounts service managed=%t", service != nil && service.host != nil)
}

func containsFold(value, search string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(strings.TrimSpace(search)))
}
