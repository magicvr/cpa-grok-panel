package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	tokenRefresher   TokenRefresher
	tokenURL         string
	httpClient       *http.Client
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
	if err == nil {
		return &AccountError{Code: "unknown", Message: "unknown error", HTTPStatus: 500, Retryable: false}
	}
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
	if err := service.bindHostRequestBaselines(files, now); err != nil {
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
		state.Quota = DisplayQuota(state, settings.FreeUserDailyTokenLimit)
		items = append(items, domain.ProjectAccount(file, state, now, 0))
	}
	service.decorateBotFlags(items, settings.BatchOperationConcurrency)
	sort.Slice(items, func(i, j int) bool {
		return items[i].ExactFileName < items[j].ExactFileName
	})
	return items, now, nil
}

func (service *AccountsService) bindHostRequestBaselines(files []domain.AuthFile, now time.Time) error {
	type bind struct {
		authIndex string
		success   int64
		failed    int64
	}
	var pending []bind
	snapshot := service.store.View()
	for _, file := range files {
		if !domain.IsXAIOAuth(file) || file.AuthIndex == "" || !strings.HasSuffix(file.Name, ".json") {
			continue
		}
		state := snapshot.Accounts[file.AuthIndex]
		if state.Usage.PeriodStartedAt.IsZero() {
			state.Usage.PeriodStartedAt = now
		}
		if !domain.NeedsHostRequestBaselineBind(state) {
			continue
		}
		pending = append(pending, bind{authIndex: file.AuthIndex, success: file.Success, failed: file.Failed})
	}
	if len(pending) == 0 {
		return nil
	}
	return service.store.Update(func(snapshot *stateinfra.Snapshot) error {
		for _, item := range pending {
			state := snapshot.Accounts[item.authIndex]
			if state.Usage.PeriodStartedAt.IsZero() {
				state.Usage.PeriodStartedAt = now
			}
			if !domain.NeedsHostRequestBaselineBind(state) {
				continue
			}
			baseline := domain.BindHostRequestBaseline(state, item.success, item.failed, state.Usage.PeriodStartedAt)
			state.HostRequestBaseline = &baseline
			snapshot.Accounts[item.authIndex] = state
		}
		return nil
	})
}

// ReconcileDemotions is a no-op in v0.7.0 (no demotion class drift).
func (service *AccountsService) ReconcileDemotions() error { return nil }

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

// SetEnabled is kept for API completeness; panel uses Management status API.
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

// RestorePriority is removed in v0.7.0.
func (service *AccountsService) RestorePriority(authIndex, exactFileName string) (domain.AccountView, error) {
	return domain.AccountView{}, &AccountError{
		Code: "gone", Message: "解除降权已移除（v0.7.0）；请使用测活或成功回血绑定 priority", HTTPStatus: 410,
	}
}

// RestorePriorityAfterCooldown is a no-op.
func (service *AccountsService) RestorePriorityAfterCooldown(authIndex string) (bool, error) {
	return false, nil
}

// Demote is removed in v0.7.0.
func (service *AccountsService) Demote(authIndex, exactFileName string) (domain.AccountView, error) {
	return domain.AccountView{}, &AccountError{
		Code: "gone", Message: "手动降权已移除（v0.7.0）；priority 随存活状态自动绑定", HTTPStatus: 410,
	}
}

// ApplyRequestedDemotion heals an account to live priority after success usage
// (PriorityEnqueuer path). v0.7.0: applies priority_live for the given auth.
func (service *AccountsService) ApplyRequestedDemotion(authIndex string, _ int) error {
	_, err := service.ApplyAliveStatus(authIndex, domain.ProbeStatusLive, true)
	return err
}

// SyncPriority forces CPA auth priority from the plugin's recorded probe_status
// (PriorityForProbeStatus + PriorityWriter). Does not change probe_status.
// skipped is true when priority already matches the target.
func (service *AccountsService) SyncPriority(authIndex, exactFileName string) (view domain.AccountView, skipped bool, targetPriority int, err error) {
	service.write.Lock()
	defer service.write.Unlock()

	var file domain.AuthFile
	if strings.TrimSpace(exactFileName) != "" {
		file, err = service.resolveExact(authIndex, exactFileName)
	} else {
		file, err = service.resolveByAuthIndex(authIndex)
	}
	if err != nil {
		return domain.AccountView{}, false, 0, err
	}

	settings := service.settings()
	state := service.store.View().Accounts[file.AuthIndex]
	status := domain.CanonicalProbeStatus(state.Quota.ProbeStatus, state.Quota.ProbeHTTP)
	targetPriority = PriorityForProbeStatus(settings, status)
	if file.Priority == targetPriority {
		return service.project(file), true, targetPriority, nil
	}

	if err := service.writePriority(file, targetPriority, nil); err != nil {
		return domain.AccountView{}, false, targetPriority, err
	}
	verified, err := service.resolveExact(file.AuthIndex, file.Name)
	if err != nil {
		return domain.AccountView{}, false, targetPriority, err
	}
	if verified.Priority != targetPriority {
		return domain.AccountView{}, false, targetPriority, &AccountError{
			Code: "write_verification_failed", Message: "优先级写后校验不一致", HTTPStatus: 502, Retryable: true,
		}
	}
	return service.project(verified), false, targetPriority, nil
}

// ConfirmPriorityWrite verifies a panel Management fields PATCH for set priority.
// demote/restore operations return gone (before host verification).
func (service *AccountsService) ConfirmPriorityWrite(authIndex, exactFileName, operation string, priority int, previousPriority *int) (domain.AccountView, error) {
	service.write.Lock()
	defer service.write.Unlock()

	_ = previousPriority
	switch strings.ToLower(strings.TrimSpace(operation)) {
	case "demote", "restore":
		return domain.AccountView{}, &AccountError{Code: "gone", Message: "demote/restore 已移除（v0.7.0）", HTTPStatus: 410}
	case "set":
		// ok
	default:
		return domain.AccountView{}, &AccountError{Code: "invalid_argument", Message: "operation 必须是 set", HTTPStatus: 400}
	}

	file, err := service.resolveExact(authIndex, exactFileName)
	if err != nil {
		return domain.AccountView{}, err
	}
	if file.Priority != priority {
		return domain.AccountView{}, &AccountError{Code: "write_verification_failed", Message: "Management 优先级写后校验不一致", HTTPStatus: 502, Retryable: true}
	}
	err = service.store.Update(func(snapshot *stateinfra.Snapshot) error {
		state := snapshot.Accounts[authIndex]
		state.ExactFileName = exactFileName
		// Manual priority set: do not change probe_status.
		state.Demotion = domain.DemotionState{State: "none", Class: domain.DemotionClassNone}
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

func localQuotaFallback(state domain.AccountState, limit uint64) domain.QuotaSnapshot {
	return DisplayQuota(state, limit)
}

func (service *AccountsService) project(file domain.AuthFile) domain.AccountView {
	return domain.ProjectAccount(file, service.store.View().Accounts[file.AuthIndex], service.now().UTC(), 0)
}

func (service *AccountsService) settings() Settings {
	if settings := service.store.View().Settings; settings != nil {
		return NormalizeSettings(*settings)
	}
	return NormalizeSettings(service.settingsFallback)
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

func (service *AccountsService) String() string {
	return fmt.Sprintf("accounts service managed=%t", service != nil && service.host != nil)
}

func containsFold(value, search string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(strings.TrimSpace(search)))
}
