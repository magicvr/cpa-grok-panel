package management_test

import (
	"context"
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
	for _, marker := range []string{
		"v0.7.2", "account_file_filter", "cpa_management_bearer", "data-1p-ignore",
		"测活积分阈值", "debt_probe_threshold", "priority_live", "priority_invalid", "priority_dead",
		"priority_throttled", "priority_unknown", "priority_error", "priority-live", "priority-unknown",
		"data-sort=\"bot\"", "id=\"bot-filter\"", "matchesBot", "id=\"plan-filter\"", "matchesPlan",
		"id=\"alive-filter\"", "matchesAlive", "批量刷新套餐", "data-batch-action=\"refresh-plan\"", "performBatchRefreshPlans",
		"批量测活", "data-batch-action=\"probe\"", "performBatchProbe", "probeLiveForItem", "XAI_PROBE_URL",
		"/v1/responses", "max_output_tokens", "classifyProbeStatus", "Reply with exactly OK",
		"x-authenticateresponse", "x-grok-client-identifier", "api-call", "禁止直连", "payload.data",
		"CLIProxyAPI", "空 body", "存活", "data-sort=\"alive\"", "alive-badge", "aliveCell", "probe_status",
		"aliveLabel", "正常", "无效", "死号", "限流", "异常", "未知", "批量重签", "data-batch-action=\"resign\"",
		"/accounts/resign", "performBatchResign", "clearDiagnostic", "/accounts/clear-diagnostic", ">诊断<",
		"bot_flag_known", "首页", "末页", "跳转", "page-input", "清除选中", "全部选中", "select-filtered",
		"apply-probe", "/accounts/apply-probe", "source:'manual'", "normalizeProbeStatus", ">风控<",
		"debt-probe-threshold", "cpa-grok-panel.theme_preference", "data-panel-theme",
		"html[data-panel-theme=\"light\"]", "外观 / 主题", "跟随系统（跟随 CPA）", "debt_fail_401", "debt_fail_429",
		"debt_success_decay", "outbound_proxy_url", "出站代理（批量重签）", "CPA_GROK_OUTBOUND_PROXY",
		"executeAccountAction", "statusCell(item)", "unavailable=true", "CPA 标记该凭证当前不可调度",
		"id=\"alive-summary\"", "isDemotedEffective", "matchesAliveFilter",
		"data-batch-action=\"refresh-priority\"", "刷新优先级", "performBatchRefreshPriority", "/accounts/sync-priority",
		"syncPriorityForItem", "performRowResign", "performRowProbe", "data-action=\"probe\"", ">测活<", ">启用<",
		"priority_unknown:-10", "默认 -10",
		"runConcurrent(targets,batchConcurrency()", "成功 ${succeeded} · 失败 ${failed}",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("panel missing %q", marker)
		}
	}
	for _, forbidden := range []string{
		`data-action="refresh-plan"`, "performRowRefreshPlan",
		`data-action="demote"`, "matchesDemotionFilter", "demotionClassLabel",
		`id="demoted-card"`, "watch_reprobe_minutes", "观察复测",
		`data-batch-action="set-priority"`, "performBatchSetPriority", "批量设置优先级",
		`id="alive-breakdown"`, "Math.min(3,batchConcurrency())",
		">激活<",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("panel should not contain %q", forbidden)
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
		AuthIndex: "idx-1", Name: "xai-a.json", Provider: "xai", Type: "xai", AccountType: "oauth", Priority: 12,
	}}}
	router := management.NewRouter(application.NewAccountsService(host, store, time.Now), store)
	// demote/restore confirmations removed in v0.7.0
	body := []byte(`{"auth_index":"idx-1","exact_file_name":"xai-a.json","operation":"demote","priority":-100,"previous_priority":7}`)
	response := router.Handle(management.Request{Method: "POST", Path: management.APIPrefix + "/accounts/priority-written", Body: body})
	if response.StatusCode != 410 {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	// set confirms Management already wrote priority (host must already match)
	body = []byte(`{"auth_index":"idx-1","exact_file_name":"xai-a.json","operation":"set","priority":12}`)
	response = router.Handle(management.Request{Method: "POST", Path: management.APIPrefix + "/accounts/priority-written", Body: body})
	if response.StatusCode != 200 {
		t.Fatalf("set status=%d body=%s", response.StatusCode, response.Body)
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
	host := &writableHost{files: []domain.AuthFile{{
		AuthIndex: "idx-1", Name: "xai-a.json", Provider: "xai", Type: "xai", AccountType: "oauth", Priority: -100,
	}}}
	router := management.NewRouter(application.NewAccountsService(host, store, time.Now), store)
	body := []byte(`{"auth_index":"idx-1","exact_file_name":"xai-a.json"}`)
	response := router.Handle(management.Request{Method: "POST", Path: "/v0/management/cpa-grok-panel/api/v1/accounts/restore-priority", Body: body})
	if response.StatusCode != 410 {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	if !strings.Contains(string(response.Body), "gone") {
		t.Fatalf("body=%s", response.Body)
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
	if response.StatusCode != 410 {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	if !strings.Contains(string(response.Body), "gone") {
		t.Fatalf("body=%s", response.Body)
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
	body := []byte(`{"auto_refresh_enabled":false,"auto_refresh_interval_seconds":12,"batch_operation_concurrency":17,"daily_usage_reset_enabled":true,"daily_usage_reset_time":"03:45","attributed_failure_threshold":7,"count_status_429":true,"count_status_5xx":true,"debt_probe_threshold":3.5,"debt_fail_401":2.5,"debt_fail_429":0.75,"debt_success_decay":1.25,"priority_live":12,"priority_invalid":-20,"priority_dead":-250,"priority_throttled":-60,"priority_unknown":15,"priority_error":-55}`)
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
	if got.AutoRefreshEnabled || got.AutoRefreshIntervalSeconds != 12 || got.BatchOperationConcurrency != 17 || !got.DailyUsageResetEnabled || got.DailyUsageResetTime != "03:45" || got.AttributedFailureThreshold != 7 || !got.CountStatus429 || !got.CountStatus5XX || got.DebtProbeThreshold != 3.5 || got.DebtFail401 != 2.5 || got.DebtFail429 != 0.75 || got.DebtSuccessDecay != 1.25 || got.PriorityLive != 12 || got.PriorityInvalid != -20 || got.PriorityDead != -250 || got.PriorityThrottled != -60 || got.PriorityUnknown != 15 || got.PriorityError != -55 {
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
	response = router.Handle(management.Request{Method: "PATCH", Path: management.APIPrefix + "/settings", Body: []byte(`{"debt_probe_threshold":0}`)})
	if response.StatusCode != 400 || !strings.Contains(string(response.Body), "大于 0") {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	response = router.Handle(management.Request{Method: "PATCH", Path: management.APIPrefix + "/settings", Body: []byte(`{"priority_dead":1000001}`)})
	if response.StatusCode != 400 || !strings.Contains(string(response.Body), "priority_dead") {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	response = router.Handle(management.Request{Method: "PATCH", Path: management.APIPrefix + "/settings", Body: []byte(`{"priority_unknown":-1000001}`)})
	if response.StatusCode != 400 || !strings.Contains(string(response.Body), "priority_unknown") {
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
	if settings.DebtProbeThreshold != 2 {
		t.Fatalf("debt_probe_threshold=%v", settings.DebtProbeThreshold)
	}
	if settings.PriorityLive != 0 || settings.PriorityInvalid != -50 || settings.PriorityDead != -100 ||
		settings.PriorityThrottled != -50 || settings.PriorityUnknown != -10 || settings.PriorityError != -50 {
		t.Fatalf("priority_* defaults=%+v", settings)
	}
	if settings.DebtFail401 != 1.5 || settings.DebtFail429 != 0.5 || settings.DebtSuccessDecay != 1 {
		t.Fatalf("debt score defaults=%+v", settings)
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

func TestLoadSettingsSoftDemotionFromEnvironment(t *testing.T) {
	t.Setenv("CPA_GROK_DEBT_PROBE_THRESHOLD", "3.25")
	t.Setenv("CPA_GROK_PRIORITY_DEAD", "-90")
	t.Setenv("CPA_GROK_PRIORITY_INVALID", "-40")
	t.Setenv("CPA_GROK_PRIORITY_UNKNOWN", "11")
	t.Setenv("CPA_GROK_DEBT_FAIL_401", "2")
	t.Setenv("CPA_GROK_DEBT_FAIL_429", "0.75")
	t.Setenv("CPA_GROK_DEBT_SUCCESS_DECAY", "1.25")
	settings := application.LoadSettings()
	if settings.DebtProbeThreshold != 3.25 || settings.PriorityDead != -90 || settings.PriorityInvalid != -40 || settings.PriorityUnknown != 11 || settings.DebtFail401 != 2 || settings.DebtFail429 != 0.75 || settings.DebtSuccessDecay != 1.25 {
		t.Fatalf("settings=%+v", settings)
	}
}

func TestLoadSettingsCooldownRestoreFromEnvironment(t *testing.T) {
	// Legacy dead/anomaly env aliases map into priority_* when new env unset.
	t.Setenv("CPA_GROK_DEAD_PRIORITY", "-88")
	t.Setenv("CPA_GROK_ANOMALY_PRIORITY", "-44")
	t.Setenv("CPA_GROK_DEFAULT_RESTORE_PRIORITY", "3")
	settings := application.LoadSettings()
	if settings.PriorityDead != -88 || settings.PriorityError != -44 || settings.PriorityLive != 3 {
		t.Fatalf("settings=%+v", settings)
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

type writableHost struct {
	files         []domain.AuthFile
	documents     map[string]cpaabi.AuthDocument
	savedName     string
	savedDocument cpaabi.AuthDocument
}

func (host *writableHost) ListAuthFiles() ([]domain.AuthFile, error) {
	return append([]domain.AuthFile(nil), host.files...), nil
}

func (host *writableHost) GetAuthFile(authIndex string) (cpaabi.AuthDocument, error) {
	if host.documents != nil {
		if document, ok := host.documents[authIndex]; ok {
			raw, _ := json.Marshal(document)
			var clone cpaabi.AuthDocument
			_ = json.Unmarshal(raw, &clone)
			return clone, nil
		}
	}
	for _, file := range host.files {
		if file.AuthIndex == authIndex {
			return cpaabi.AuthDocument{"disabled": file.Disabled, "priority": file.Priority}, nil
		}
	}
	return cpaabi.AuthDocument{}, nil
}

func (host *writableHost) SaveAuthFile(name string, document cpaabi.AuthDocument) error {
	raw, _ := json.Marshal(document)
	var clone cpaabi.AuthDocument
	_ = json.Unmarshal(raw, &clone)
	host.savedName = name
	host.savedDocument = clone
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
		if host.documents == nil {
			host.documents = map[string]cpaabi.AuthDocument{}
		}
		host.documents[host.files[index].AuthIndex] = clone
	}
	return nil
}

func TestRouterResignRoute(t *testing.T) {
	store, err := stateinfra.Open(t.TempDir(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	host := &writableHost{
		files: []domain.AuthFile{{AuthIndex: "idx-1", Name: "xai-a.json", Provider: "xai", Type: "xai", AccountType: "oauth", Priority: 3}},
		documents: map[string]cpaabi.AuthDocument{
			"idx-1": {"access_token": "old", "refresh_token": "rt", "priority": 3},
		},
	}
	service := application.NewAccountsService(host, store, time.Now)
	service.SetTokenRefresher(resignStub{result: application.TokenRefreshResult{AccessToken: "new", RefreshToken: "rt2", ExpiresIn: 10}})
	router := management.NewRouter(service, store)
	body := []byte(`{"auth_index":"idx-1","exact_file_name":"xai-a.json"}`)
	response := router.Handle(management.Request{Method: "POST", Path: management.APIPrefix + "/accounts/resign", Body: body})
	if response.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	if host.savedName != "xai-a.json" || host.savedDocument["access_token"] != "new" {
		t.Fatalf("saved=%q doc=%#v", host.savedName, host.savedDocument)
	}
}

type resignStub struct {
	result application.TokenRefreshResult
}

func (s resignStub) Refresh(ctx context.Context, refreshToken, clientID string) (application.TokenRefreshResult, error) {
	return s.result, nil
}


func TestRouterSyncPriority(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	settings := application.DefaultSettings()
	host := &fakeSyncHost{
		files: []domain.AuthFile{{
			AuthIndex: "idx-sync", Name: "xai-sync.json", Provider: "xai", Type: "xai", AccountType: "oauth", Priority: 99,
		}},
		documents: map[string]cpaabi.AuthDocument{"idx-sync": {"priority": 99}},
	}
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		state := snapshot.Accounts["idx-sync"]
		state.ExactFileName = "xai-sync.json"
		state.Quota.ProbeStatus = domain.ProbeStatusDead
		snapshot.Accounts["idx-sync"] = state
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := application.NewAccountsService(host, store, time.Now, settings)
	router := management.NewRouter(service, store, settings)
	body := []byte(`{"auth_index":"idx-sync","exact_file_name":"xai-sync.json"}`)
	response := router.Handle(management.Request{Method: "POST", Path: management.APIPrefix + "/accounts/sync-priority", Body: body})
	if response.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(response.Body, &got); err != nil {
		t.Fatal(err)
	}
	if got["skipped"] == true {
		t.Fatalf("expected write, got skipped: %+v", got)
	}
	if int(got["target_priority"].(float64)) != settings.PriorityDead {
		t.Fatalf("target=%v want=%d", got["target_priority"], settings.PriorityDead)
	}
	if host.files[0].Priority != settings.PriorityDead {
		t.Fatalf("priority=%d", host.files[0].Priority)
	}
	// second call must still write (never skip when priority already matches)
	response = router.Handle(management.Request{Method: "POST", Path: management.APIPrefix + "/accounts/sync-priority", Body: body})
	if response.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	if err := json.Unmarshal(response.Body, &got); err != nil {
		t.Fatal(err)
	}
	if got["skipped"] == true {
		t.Fatalf("expected write again (no skip): %+v", got)
	}
}

type fakeSyncHost struct {
	files     []domain.AuthFile
	documents map[string]cpaabi.AuthDocument
}

func (host *fakeSyncHost) ListAuthFiles() ([]domain.AuthFile, error) {
	return append([]domain.AuthFile(nil), host.files...), nil
}

func (host *fakeSyncHost) GetAuthFile(authIndex string) (cpaabi.AuthDocument, error) {
	if doc := host.documents[authIndex]; doc != nil {
		clone := cpaabi.AuthDocument{}
		for k, v := range doc {
			clone[k] = v
		}
		return clone, nil
	}
	for _, file := range host.files {
		if file.AuthIndex == authIndex {
			return cpaabi.AuthDocument{"priority": file.Priority}, nil
		}
	}
	return nil, nil
}

func (host *fakeSyncHost) SaveAuthFile(name string, document cpaabi.AuthDocument) error {
	for i := range host.files {
		if host.files[i].Name != name {
			continue
		}
		if priority, ok := document["priority"].(int); ok {
			host.files[i].Priority = priority
		} else if priority, ok := document["priority"].(float64); ok {
			host.files[i].Priority = int(priority)
		}
		if host.documents == nil {
			host.documents = map[string]cpaabi.AuthDocument{}
		}
		clone := cpaabi.AuthDocument{}
		for k, v := range document {
			clone[k] = v
		}
		host.documents[host.files[i].AuthIndex] = clone
	}
	return nil
}
