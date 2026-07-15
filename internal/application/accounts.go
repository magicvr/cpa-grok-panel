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
	write            sync.Mutex
}

func NewAccountsService(host AuthHost, store *stateinfra.Store, now func() time.Time, configured ...Settings) *AccountsService {
	settings := DefaultSettings()
	if len(configured) > 0 {
		settings = configured[0]
	}
	return &AccountsService{host: host, store: store, now: now, settingsFallback: settings}
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
	snapshot := service.store.View()
	settings := service.settings()
	items := make([]domain.AccountView, 0, len(files))
	for _, file := range files {
		if !domain.IsXAIOAuth(file) || file.AuthIndex == "" || !strings.HasSuffix(file.Name, ".json") {
			continue
		}
		if search != "" && !containsFold(file.AuthIndex, search) && !containsFold(file.Name, search) && !containsFold(file.Email, search) {
			continue
		}
		items = append(items, domain.ProjectAccount(file, snapshot.Accounts[file.AuthIndex], now, settings.DemotionPriority))
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].ExactFileName < items[j].ExactFileName
	})
	return items, now, nil
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
	account := service.store.View().Accounts[authIndex]
	demotion := account.Demotion.Normalized()
	settings := service.settings()
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
	document["priority"] = *restorePriority
	if err := service.host.SaveAuthFile(exactFileName, document); err != nil {
		service.recordDemotionFailure(authIndex, "restore_save_failed", false)
		return domain.AccountView{}, hostError("auth_save_failed", err)
	}
	verified, err := service.resolveExact(authIndex, exactFileName)
	if err != nil {
		service.recordDemotionFailure(authIndex, "restore_verify_failed", false)
		return domain.AccountView{}, err
	}
	if verified.Priority != *restorePriority {
		service.recordDemotionFailure(authIndex, "restore_verify_failed", false)
		return domain.AccountView{}, &AccountError{Code: "write_verification_failed", Message: "优先级恢复写后校验不一致", HTTPStatus: 502, Retryable: true}
	}
	if err := service.store.Update(func(snapshot *stateinfra.Snapshot) error {
		state := snapshot.Accounts[authIndex]
		state.ExactFileName = exactFileName
		if !recordedRestore {
			state.Demotion.BaselinePriority = intPointer(*restorePriority)
			state.Demotion.TargetPriority = intPointer(settings.DemotionPriority)
		}
		state.Demotion.State = "restored"
		state.Demotion.FailureCode = ""
		snapshot.Accounts[authIndex] = state
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
	demotion := domain.DemotionState{
		State: "requested", BaselinePriority: intPointer(file.Priority), TargetPriority: intPointer(targetPriority), TriggeredAt: &now,
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
	document["priority"] = targetPriority
	if err := service.host.SaveAuthFile(exactFileName, document); err != nil {
		service.recordDemotionFailure(authIndex, "auth_save_failed", true)
		return domain.AccountView{}, hostError("auth_save_failed", err)
	}
	verified, err := service.resolveExact(authIndex, exactFileName)
	if err != nil {
		service.recordDemotionFailure(authIndex, "demotion_verify_failed", true)
		return domain.AccountView{}, err
	}
	if verified.Priority != targetPriority {
		service.recordDemotionFailure(authIndex, "demotion_verify_failed", true)
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
		service.recordDemotionFailure(authIndex, AsAccountError(err).Code, true)
		return err
	}
	if demotion.TargetPriority == nil {
		demotion.TargetPriority = intPointer(targetPriority)
	}
	if demotion.BaselinePriority != nil {
		if file.Priority == *demotion.TargetPriority && *demotion.BaselinePriority != *demotion.TargetPriority {
			return service.markDemotionApplied(authIndex, file.Name)
		}
		if file.Priority != *demotion.BaselinePriority {
			err := &AccountError{Code: "priority_superseded", Message: "降权前优先级已变化", HTTPStatus: 409}
			service.recordDemotionFailure(authIndex, err.Code, true)
			return err
		}
	} else {
		if file.Priority == *demotion.TargetPriority {
			err := &AccountError{Code: "priority_already_target", Message: "账号已处于目标优先级，无法保存可靠基线", HTTPStatus: 409}
			service.recordDemotionFailure(authIndex, err.Code, true)
			return err
		}
		demotion.BaselinePriority = intPointer(file.Priority)
		if err := service.store.Update(func(snapshot *stateinfra.Snapshot) error {
			state := snapshot.Accounts[authIndex]
			state.ExactFileName = file.Name
			state.Demotion = demotion
			snapshot.Accounts[authIndex] = state
			return nil
		}); err != nil {
			return err
		}
	}
	document, err := service.host.GetAuthFile(authIndex)
	if err != nil {
		service.recordDemotionFailure(authIndex, "auth_get_failed", true)
		return hostError("auth_get_failed", err)
	}
	if priority, ok := documentInt(document, "priority"); ok && priority != file.Priority {
		err := &AccountError{Code: "priority_superseded", Message: "降权前优先级已变化", HTTPStatus: 409}
		service.recordDemotionFailure(authIndex, err.Code, true)
		return err
	}
	document["priority"] = *demotion.TargetPriority
	if err := service.host.SaveAuthFile(file.Name, document); err != nil {
		service.recordDemotionFailure(authIndex, "auth_save_failed", true)
		return hostError("auth_save_failed", err)
	}
	verified, err := service.resolveExact(authIndex, file.Name)
	if err != nil {
		service.recordDemotionFailure(authIndex, "demotion_verify_failed", true)
		return err
	}
	if verified.Priority != *demotion.TargetPriority {
		service.recordDemotionFailure(authIndex, "demotion_verify_failed", true)
		return &AccountError{Code: "write_verification_failed", Message: "降权写后校验不一致", HTTPStatus: 502, Retryable: true}
	}
	return service.markDemotionApplied(authIndex, file.Name)
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
		snapshot.Accounts[authIndex] = state
		return nil
	})
}

func (service *AccountsService) recordDemotionFailure(authIndex, code string, terminal bool) {
	_ = service.store.Update(func(snapshot *stateinfra.Snapshot) error {
		state := snapshot.Accounts[authIndex]
		if terminal {
			state.Demotion.State = "failed"
		}
		state.Demotion.FailureCode = code
		snapshot.Accounts[authIndex] = state
		return nil
	})
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
