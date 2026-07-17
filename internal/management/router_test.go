package management_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/application"
	"github.com/magicvr/cpa-grok-panel/internal/cpaabi"
	"github.com/magicvr/cpa-grok-panel/internal/domain"
	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
	"github.com/magicvr/cpa-grok-panel/internal/management"
)

func TestRouterRejectsUnknownWriteRoute(t *testing.T) {
	dir := t.TempDir()
	store, err := stateinfra.Open(dir, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	router := management.NewRouter(application.NewAccountsService(fakeLister{}, store, time.Now), store)
	resp := router.Handle(management.Request{Method: "POST", Path: "/v0/management/cpa-grok-panel/api/v1/accounts"})
	if resp.StatusCode != 404 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if !strings.Contains(string(resp.Body), "not_found") {
		t.Fatalf("body=%s", resp.Body)
	}
	if len(resp.Headers["Content-Type"]) == 0 {
		t.Fatal("headers must be multi-value map")
	}
}

func TestRouterPanelPath(t *testing.T) {
	dir := t.TempDir()
	store, err := stateinfra.Open(dir, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	router := management.NewRouter(application.NewAccountsService(fakeLister{}, store, time.Now), store)
	resp := router.Handle(management.Request{Method: "GET", Path: "/v0/resource/plugins/cpa-grok-panel/panel"})
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	body := string(resp.Body)
	if !strings.Contains(body, "Grok") {
		t.Fatalf("not html panel: %s", string(resp.Body)[:80])
	}
	for _, marker := range []string{"v0.3.10", "优先级冷却恢复", "cooldown_restore_enabled", "6h → 12h → 24h", "data-sort=\"bot\"", "id=\"bot-filter\"", "matchesBot", "clearDiagnostic", "/accounts/clear-diagnostic", ">诊断<", "bot_flag_known", "首页", "末页", "跳转", "page-input", "清除选中", "全部选中", "批量启用", "批量停用", "批量降权", "批量解除降权", "批量设置优先级", "data-batch-action=\"set-priority\"", "批量安全删除", "批量操作并发数", "batch_operation_concurrency", "runConcurrent", "每日清零", "allItems.find", "Number.isInteger(previousPriority)", "item.demotion?.baseline_priority", "clearDiagnostic(target)"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("panel missing %q", marker)
		}
	}
}

func TestRouterMetaReportsMemoryState(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	router := management.NewRouter(application.NewAccountsService(fakeLister{}, store, time.Now), store)
	response := router.Handle(management.Request{Method: "GET", Path: management.APIPrefix + "/meta"})
	if response.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	var meta application.Meta
	if err := json.Unmarshal(response.Body, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.StateStatus != "memory" || meta.StateBackend != "memory" || meta.DataDir != "" {
		t.Fatalf("meta=%+v", meta)
	}
}

func TestRouterClearDiagnostic(t *testing.T) {
	store, err := stateinfra.Open(t.TempDir(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	failureAt := time.Now().UTC()
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Accounts["idx-1"] = domain.AccountState{
			ExactFileName: "xai-a.json",
			Usage:         domain.UsageCounters{TotalTokens: 99},
			Failure: domain.FailureState{
				ConsecutiveAttributedFailures: 3,
				LastFailureAt:                 &failureAt,
				LastFailureCode:               "http_500",
			},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	router := management.NewRouter(application.NewAccountsService(fakeLister{}, store, time.Now), store)
	body := []byte(`{"auth_index":"idx-1","exact_file_name":"xai-a.json"}`)
	response := router.Handle(management.Request{Method: "POST", Path: "/v0/management/cpa-grok-panel/api/v1/accounts/clear-diagnostic", Body: body})
	if response.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	state := store.View().Accounts["idx-1"]
	if state.Failure != (domain.FailureState{}) || state.Usage.TotalTokens != 99 {
		t.Fatalf("state=%+v", state)
	}
}

func TestRouterConfirmsManagementPriorityWrite(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	host := &writableHost{files: []domain.AuthFile{{
		AuthIndex: "idx-1", Name: "xai-a.json", Provider: "xai", Type: "xai", AccountType: "oauth", Priority: -100,
	}}}
	router := management.NewRouter(application.NewAccountsService(host, store, time.Now), store)
	body := []byte(`{"auth_index":"idx-1","exact_file_name":"xai-a.json","operation":"demote","priority":-100,"previous_priority":7}`)
	response := router.Handle(management.Request{Method: "POST", Path: management.APIPrefix + "/accounts/priority-written", Body: body})
	if response.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	state := store.View().Accounts["idx-1"].Demotion
	if state.State != "applied" || state.BaselinePriority == nil || *state.BaselinePriority != 7 {
		t.Fatalf("state=%+v", state)
	}
}

func TestRouterSetEnabled(t *testing.T) {
	store, err := stateinfra.Open(t.TempDir(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	host := &writableHost{files: []domain.AuthFile{{
		AuthIndex: "idx-1", Name: "xai-a.json", Provider: "xai", Type: "xai", AccountType: "oauth",
	}}}
	router := management.NewRouter(application.NewAccountsService(host, store, time.Now), store)
	body := []byte(`{"auth_index":"idx-1","exact_file_name":"xai-a.json","enabled":false}`)
	response := router.Handle(management.Request{Method: "POST", Path: "/v0/management/cpa-grok-panel/api/v1/accounts/set-enabled", Body: body})
	if response.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	if !host.files[0].Disabled {
		t.Fatalf("file was not disabled: %+v", host.files[0])
	}
}

func TestRouterRestorePriority(t *testing.T) {
	store, err := stateinfra.Open(t.TempDir(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	baseline, target := 10, -100
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Accounts["idx-1"] = domain.AccountState{Demotion: domain.DemotionState{
			State: "applied", BaselinePriority: &baseline, TargetPriority: &target,
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	host := &writableHost{files: []domain.AuthFile{{
		AuthIndex: "idx-1", Name: "xai-a.json", Provider: "xai", Type: "xai", AccountType: "oauth", Priority: target,
	}}}
	router := management.NewRouter(application.NewAccountsService(host, store, time.Now), store)
	body := []byte(`{"auth_index":"idx-1","exact_file_name":"xai-a.json"}`)
	response := router.Handle(management.Request{Method: "POST", Path: "/v0/management/cpa-grok-panel/api/v1/accounts/restore-priority", Body: body})
	if response.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	if host.files[0].Priority != baseline {
		t.Fatalf("priority=%d want=%d", host.files[0].Priority, baseline)
	}
	if state := store.View().Accounts["idx-1"].Demotion.State; state != "restored" {
		t.Fatalf("demotion state=%s", state)
	}
}

func TestRouterDemote(t *testing.T) {
	store, err := stateinfra.Open(t.TempDir(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	host := &writableHost{files: []domain.AuthFile{{
		AuthIndex: "idx-1", Name: "xai-a.json", Provider: "xai", Type: "xai", AccountType: "oauth", Priority: 10,
	}}}
	router := management.NewRouter(application.NewAccountsService(host, store, time.Now), store)
	body := []byte(`{"auth_index":"idx-1","exact_file_name":"xai-a.json"}`)
	response := router.Handle(management.Request{Method: "POST", Path: "/v0/management/cpa-grok-panel/api/v1/accounts/demote", Body: body})
	if response.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	if host.files[0].Priority != -100 {
		t.Fatalf("priority=%d", host.files[0].Priority)
	}
	state := store.View().Accounts["idx-1"].Demotion
	if state.State != "applied" || state.BaselinePriority == nil || *state.BaselinePriority != 10 {
		t.Fatalf("demotion=%+v", state)
	}
}

func TestRouterUpdateSettingsThenGet(t *testing.T) {
	store, err := stateinfra.Open(t.TempDir(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	defaults := application.DefaultSettings()
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Settings = &defaults
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	router := management.NewRouter(application.NewAccountsService(fakeLister{}, store, time.Now, defaults), store, defaults)
	body := []byte(`{"auto_refresh_enabled":false,"auto_refresh_interval_seconds":12,"batch_operation_concurrency":17,"daily_usage_reset_enabled":true,"daily_usage_reset_time":"03:45","attributed_failure_threshold":7,"count_status_429":true,"count_status_5xx":true,"demotion_priority":-250,"default_restore_priority":12,"cooldown_restore_enabled":false}`)
	response := router.Handle(management.Request{Method: "PUT", Path: "/v0/management/cpa-grok-panel/api/v1/settings", Body: body})
	if response.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}

	response = router.Handle(management.Request{Method: "GET", Path: "/v0/management/cpa-grok-panel/api/v1/settings"})
	if response.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	var got struct {
		application.Settings
		Source string `json:"source"`
	}
	if err := json.Unmarshal(response.Body, &got); err != nil {
		t.Fatal(err)
	}
	if got.AutoRefreshEnabled || got.AutoRefreshIntervalSeconds != 12 || got.BatchOperationConcurrency != 17 || !got.DailyUsageResetEnabled || got.DailyUsageResetTime != "03:45" || got.AttributedFailureThreshold != 7 || !got.CountStatus429 || !got.CountStatus5XX || got.DemotionPriority != -250 || got.DefaultRestorePriority != 12 || got.CooldownRestoreEnabled {
		t.Fatalf("settings=%+v", got.Settings)
	}
	if got.Revision != defaults.Revision+1 || got.Source != "state" {
		t.Fatalf("revision=%d source=%q", got.Revision, got.Source)
	}
}

func TestRouterRejectsInvalidSettings(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	router := management.NewRouter(application.NewAccountsService(fakeLister{}, store, time.Now), store)
	response := router.Handle(management.Request{Method: "PATCH", Path: management.APIPrefix + "/settings", Body: []byte(`{"attributed_failure_threshold":0}`)})
	if response.StatusCode != 400 || !strings.Contains(string(response.Body), "1..100") {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	response = router.Handle(management.Request{Method: "PATCH", Path: management.APIPrefix + "/settings", Body: []byte(`{"auto_refresh_interval_seconds":1}`)})
	if response.StatusCode != 400 || !strings.Contains(string(response.Body), "2..60") {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	response = router.Handle(management.Request{Method: "PATCH", Path: management.APIPrefix + "/settings", Body: []byte(`{"daily_usage_reset_time":"9:00"}`)})
	if response.StatusCode != 400 || !strings.Contains(string(response.Body), "HH:mm") {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	response = router.Handle(management.Request{Method: "PATCH", Path: management.APIPrefix + "/settings", Body: []byte(`{"daily_usage_reset_time":"24:00"}`)})
	if response.StatusCode != 400 || !strings.Contains(string(response.Body), "24 小时") {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	response = router.Handle(management.Request{Method: "PATCH", Path: management.APIPrefix + "/settings", Body: []byte(`{"batch_operation_concurrency":51}`)})
	if response.StatusCode != 400 || !strings.Contains(string(response.Body), "1..50") {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
}

func TestDefaultAutoRefreshSettings(t *testing.T) {
	settings := application.DefaultSettings()
	if !settings.AutoRefreshEnabled || settings.AutoRefreshIntervalSeconds != 5 {
		t.Fatalf("auto refresh defaults=%+v", settings)
	}
	if settings.DailyUsageResetEnabled || settings.DailyUsageResetTime != "00:00" {
		t.Fatalf("daily usage reset defaults=%+v", settings)
	}
	if settings.BatchOperationConcurrency != 10 {
		t.Fatalf("batch operation concurrency=%d", settings.BatchOperationConcurrency)
	}
	if !settings.CooldownRestoreEnabled {
		t.Fatal("cooldown restore should default to enabled")
	}
}

func TestLoadSettingsBatchConcurrencyFromEnvironment(t *testing.T) {
	t.Setenv("CPA_GROK_BATCH_CONCURRENCY", "23")
	if got := application.LoadSettings().BatchOperationConcurrency; got != 23 {
		t.Fatalf("batch operation concurrency=%d want=23", got)
	}
	t.Setenv("CPA_GROK_BATCH_CONCURRENCY", "51")
	if got := application.LoadSettings().BatchOperationConcurrency; got != 10 {
		t.Fatalf("invalid environment fallback=%d want=10", got)
	}
}

func TestLoadSettingsCooldownRestoreFromEnvironment(t *testing.T) {
	t.Setenv("CPA_GROK_COOLDOWN_RESTORE", "false")
	if application.LoadSettings().CooldownRestoreEnabled {
		t.Fatal("cooldown restore environment default was not applied")
	}
}

func TestRouterClearState(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Accounts["idx-1"] = domain.AccountState{ExactFileName: "xai-a.json"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	router := management.NewRouter(application.NewAccountsService(fakeLister{}, store, time.Now), store)
	response := router.Handle(management.Request{Method: "POST", Path: management.APIPrefix + "/accounts/clear-state", Body: []byte(`{"auth_index":"idx-1","exact_file_name":"xai-a.json"}`)})
	if response.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	if _, exists := store.View().Accounts["idx-1"]; exists {
		t.Fatal("account state was not cleared")
	}
}

type fakeLister struct{}

func (fakeLister) ListAuthFiles() ([]domain.AuthFile, error) { return nil, nil }
func (fakeLister) GetAuthFile(string) (cpaabi.AuthDocument, error) {
	return cpaabi.AuthDocument{}, nil
}
func (fakeLister) SaveAuthFile(string, cpaabi.AuthDocument) error { return nil }

type writableHost struct{ files []domain.AuthFile }

func (host *writableHost) ListAuthFiles() ([]domain.AuthFile, error) {
	return append([]domain.AuthFile(nil), host.files...), nil
}

func (host *writableHost) GetAuthFile(authIndex string) (cpaabi.AuthDocument, error) {
	for _, file := range host.files {
		if file.AuthIndex == authIndex {
			return cpaabi.AuthDocument{"disabled": file.Disabled, "priority": file.Priority}, nil
		}
	}
	return cpaabi.AuthDocument{}, nil
}

func (host *writableHost) SaveAuthFile(name string, document cpaabi.AuthDocument) error {
	for index := range host.files {
		if host.files[index].Name != name {
			continue
		}
		if disabled, ok := document["disabled"].(bool); ok {
			host.files[index].Disabled = disabled
		}
		if raw, err := json.Marshal(document["priority"]); err == nil {
			_ = json.Unmarshal(raw, &host.files[index].Priority)
		}
	}
	return nil
}
