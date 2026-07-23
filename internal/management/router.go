package management

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/application"
	"github.com/magicvr/cpa-grok-panel/internal/cpaabi"
	"github.com/magicvr/cpa-grok-panel/internal/domain"
	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
	"github.com/magicvr/cpa-grok-panel/web"
)

const (
	// Management API paths are mounted under /v0/management by CPA.
	APIPrefix = "/cpa-grok-panel/api/v1"
	// Resource path is mounted under /v0/resource/plugins/cpa-grok-panel
	ResourcePanelPath = "/panel"
)

type Request struct {
	Method  string              `json:"Method"`
	Path    string              `json:"Path"`
	Query   map[string][]string `json:"Query,omitempty"`
	Headers map[string][]string `json:"Headers,omitempty"`
	Body    []byte              `json:"Body,omitempty"`
}

type Router struct {
	accounts         *application.AccountsService
	store            *stateinfra.Store
	settingsFallback application.Settings
}

func NewRouter(accounts *application.AccountsService, store *stateinfra.Store, configured ...application.Settings) *Router {
	settings := application.DefaultSettings()
	if len(configured) > 0 {
		settings = configured[0]
	}
	return &Router{accounts: accounts, store: store, settingsFallback: settings}
}

func Registration() map[string]any {
	// Resource: browser panel (no management auth).
	// Routes: authenticated management APIs.
	routes := []map[string]any{
		{"Method": "GET", "Path": APIPrefix + "/meta", "Description": "插件元信息"},
		{"Method": "GET", "Path": APIPrefix + "/accounts", "Description": "账号列表"},
		{"Method": "GET", "Path": APIPrefix + "/settings", "Description": "只读设置"},
		{"Method": "PUT", "Path": APIPrefix + "/settings", "Description": "更新插件设置"},
		{"Method": "PATCH", "Path": APIPrefix + "/settings", "Description": "部分更新插件设置"},
		{"Method": "POST", "Path": APIPrefix + "/accounts/demote", "Description": "手动降低账号优先级"},
		{"Method": "POST", "Path": APIPrefix + "/accounts/restore-priority", "Description": "恢复账号优先级"},
		{"Method": "POST", "Path": APIPrefix + "/accounts/set-enabled", "Description": "启用或停用账号"},
		{"Method": "POST", "Path": APIPrefix + "/accounts/clear-diagnostic", "Description": "清空账号失败诊断"},
		{"Method": "POST", "Path": APIPrefix + "/accounts/priority-written", "Description": "确认 Management 优先级写入并更新插件状态"},
		{"Method": "POST", "Path": APIPrefix + "/accounts/clear-state", "Description": "删除账号后清理插件本地状态"},
		{"Method": "POST", "Path": APIPrefix + "/accounts/quota", "Description": "保存账号额度快照"},
		{"Method": "POST", "Path": APIPrefix + "/accounts/apply-probe", "Description": "应用测活结果并按规则改档"},
		{"Method": "POST", "Path": APIPrefix + "/accounts/resign", "Description": "用 refresh_token 重签并写回 CPA auth 文件"},
	}
	resources := []map[string]any{
		{"Path": ResourcePanelPath, "Menu": "Grok 账号", "Description": "Grok 账号管理面板"},
	}
	return map[string]any{"routes": routes, "resources": resources}
}

func (router *Router) Handle(request Request) cpaabi.ManagementResponse {
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if method == "" {
		method = "GET"
	}
	path, query := normalizePath(request.Path, request.Query)
	switch {
	case method == "GET" && isPanelPath(path):
		return htmlResponse(web.PanelHTML)
	case method == "GET" && matchesPath(path, "/meta"):
		return jsonResponse(200, application.BuildMeta(router.store.View(), router.store.Info()))
	case method == "GET" && matchesPath(path, "/settings"):
		return jsonResponse(200, router.settingsResponse())
	case (method == "PUT" || method == "PATCH") && matchesPath(path, "/settings"):
		var body settingsUpdateRequest
		if err := decodeStrictBody(request.Body, &body); err != nil {
			return apiError(400, "invalid_argument", err.Error(), false)
		}
		settings, err := router.updateSettings(body)
		if err != nil {
			return apiError(400, "invalid_argument", err.Error(), false)
		}
		return jsonResponse(200, settingsResponse{Settings: settings, Source: "state"})
	case method == "GET" && matchesPath(path, "/accounts"):
		items, snapshotAt, err := router.accounts.List(firstQuery(query, "search"))
		if err != nil {
			return jsonResponse(503, map[string]any{"error": map[string]any{"code": "host_unavailable", "message": err.Error(), "retryable": true}})
		}
		return jsonResponse(200, map[string]any{
			"items": items, "next_cursor": nil, "snapshot_at": snapshotAt,
			"host_snapshot_revision": nil, "stale": false,
		})
	case method == "POST" && matchesPath(path, "/accounts/restore-priority"):
		var body accountTargetRequest
		if err := decodeStrictBody(request.Body, &body); err != nil {
			return apiError(400, "invalid_argument", err.Error(), false)
		}
		account, err := router.accounts.RestorePriority(body.AuthIndex, body.ExactFileName)
		if err != nil {
			return accountErrorResponse(err)
		}
		return jsonResponse(200, map[string]any{"account": account})
	case method == "POST" && matchesPath(path, "/accounts/demote"):
		var body accountTargetRequest
		if err := decodeStrictBody(request.Body, &body); err != nil {
			return apiError(400, "invalid_argument", err.Error(), false)
		}
		account, err := router.accounts.Demote(body.AuthIndex, body.ExactFileName)
		if err != nil {
			return accountErrorResponse(err)
		}
		return jsonResponse(200, map[string]any{"account": account})
	case method == "POST" && matchesPath(path, "/accounts/set-enabled"):
		var body setEnabledRequest
		if err := decodeStrictBody(request.Body, &body); err != nil {
			return apiError(400, "invalid_argument", err.Error(), false)
		}
		if body.Enabled == nil {
			return apiError(400, "invalid_argument", "enabled 为必填布尔值", false)
		}
		account, err := router.accounts.SetEnabled(body.AuthIndex, body.ExactFileName, *body.Enabled)
		if err != nil {
			return accountErrorResponse(err)
		}
		return jsonResponse(200, map[string]any{"account": account})
	case method == "POST" && matchesPath(path, "/accounts/clear-diagnostic"):
		var body accountTargetRequest
		if err := decodeStrictBody(request.Body, &body); err != nil {
			return apiError(400, "invalid_argument", err.Error(), false)
		}
		if err := router.accounts.ClearDiagnostic(body.AuthIndex, body.ExactFileName); err != nil {
			return accountErrorResponse(err)
		}
		return jsonResponse(200, map[string]any{"cleared": true})
	case method == "POST" && matchesPath(path, "/accounts/priority-written"):
		var body priorityWrittenRequest
		if err := decodeStrictBody(request.Body, &body); err != nil {
			return apiError(400, "invalid_argument", err.Error(), false)
		}
		account, err := router.accounts.ConfirmPriorityWrite(body.AuthIndex, body.ExactFileName, body.Operation, body.Priority, body.PreviousPriority)
		if err != nil {
			return accountErrorResponse(err)
		}
		return jsonResponse(200, map[string]any{"account": account})
	case method == "POST" && matchesPath(path, "/accounts/clear-state"):
		var body accountTargetRequest
		if err := decodeStrictBody(request.Body, &body); err != nil {
			return apiError(400, "invalid_argument", err.Error(), false)
		}
		if err := router.accounts.ClearState(body.AuthIndex, body.ExactFileName); err != nil {
			return accountErrorResponse(err)
		}
		return jsonResponse(200, map[string]any{"cleared": true})
	case method == "POST" && matchesPath(path, "/accounts/resign"):
		var body accountTargetRequest
		if err := decodeStrictBody(request.Body, &body); err != nil {
			return apiError(400, "invalid_argument", err.Error(), false)
		}
		account, err := router.accounts.Resign(body.AuthIndex, body.ExactFileName)
		if err != nil {
			return accountErrorResponse(err)
		}
		return jsonResponse(200, map[string]any{"account": account})
	case method == "POST" && matchesPath(path, "/accounts/quota"):
		var body quotaSnapshotRequest
		if err := decodeStrictBody(request.Body, &body); err != nil {
			return apiError(400, "invalid_argument", err.Error(), false)
		}
		if strings.TrimSpace(body.AuthIndex) == "" {
			return apiError(400, "invalid_argument", "quota 快照缺少 auth_index", false)
		}
		plan := strings.TrimSpace(body.Quota.Plan)
		if plan == "" {
			plan = "unknown"
			body.Quota.Plan = plan
		}
		// Allowed plans only.
		switch plan {
		case "SuperGrok", "SuperGrok Heavy", "Free", "unknown":
		default:
			return apiError(400, "invalid_argument", "plan 必须是 SuperGrok / SuperGrok Heavy / Free / unknown", false)
		}
		source := strings.TrimSpace(body.Quota.Source)
		switch source {
		case "billing", "local_estimate", "refresh_failed", "":
			if source == "" {
				if plan == "unknown" {
					body.Quota.Source = "refresh_failed"
				} else if plan == "Free" && body.Quota.Limit <= 0 {
					body.Quota.Source = "local_estimate"
				} else {
					body.Quota.Source = "billing"
				}
			}
		default:
			return apiError(400, "invalid_argument", "quota source 不受支持", false)
		}
		// Normalize legacy probe labels when panel still posts failure/unusual.
		if body.Quota.ProbeStatus != "" {
			body.Quota.ProbeStatus = domain.NormalizeProbeStatus(body.Quota.ProbeStatus, body.Quota.ProbeHTTP)
		}
		// Manual refresh always overwrites the full cached snapshot (plan permanence until next manual refresh).
		if body.Quota.FetchedAt.IsZero() {
			body.Quota.FetchedAt = time.Now().UTC()
		}
		if err := router.store.Update(func(snapshot *stateinfra.Snapshot) error {
			state := snapshot.Accounts[body.AuthIndex]
			state.Quota = body.Quota
			snapshot.Accounts[body.AuthIndex] = state
			return nil
		}); err != nil {
			return apiError(503, "state_write_failed", "保存套餐/额度快照失败: "+err.Error(), true)
		}
		return jsonResponse(200, map[string]any{"saved": true, "quota": body.Quota})
	case method == "POST" && matchesPath(path, "/accounts/apply-probe"):
		var body applyProbeRequest
		if err := decodeStrictBody(request.Body, &body); err != nil {
			return apiError(400, "invalid_argument", err.Error(), false)
		}
		if strings.TrimSpace(body.AuthIndex) == "" {
			return apiError(400, "invalid_argument", "auth_index 为必填", false)
		}
		source := strings.TrimSpace(body.Source)
		if source == "" {
			source = domain.ProbeSourceManual
		}
		account, err := router.accounts.ApplyProbeResult(body.AuthIndex, application.ProbeResult{
			Status: body.Status, HTTPStatus: body.HTTPStatus, Error: body.Error,
		}, source)
		if err != nil {
			return accountErrorResponse(err)
		}
		return jsonResponse(200, map[string]any{"account": account})
	case method != "GET" && method != "POST" && method != "PUT" && method != "PATCH":
		return apiError(405, "method_not_allowed", "请求方法不受支持", false)
	case method == "POST" || method == "PUT" || method == "PATCH":
		return apiError(404, "not_found", "接口不存在: "+path, false)
	default:
		return jsonResponse(404, map[string]any{"error": map[string]any{"code": "not_found", "message": "接口不存在: " + path}})
	}
}

type accountTargetRequest struct {
	AuthIndex     string `json:"auth_index"`
	ExactFileName string `json:"exact_file_name"`
}

type applyProbeRequest struct {
	AuthIndex  string `json:"auth_index"`
	Status     string `json:"status"`
	HTTPStatus int    `json:"http_status"`
	Error      string `json:"error,omitempty"`
	Source     string `json:"source"`
}

type quotaSnapshotRequest struct {
	AuthIndex string               `json:"auth_index"`
	Quota     domain.QuotaSnapshot `json:"quota"`
}

type setEnabledRequest struct {
	AuthIndex     string `json:"auth_index"`
	ExactFileName string `json:"exact_file_name"`
	Enabled       *bool  `json:"enabled"`
}

type priorityWrittenRequest struct {
	AuthIndex        string `json:"auth_index"`
	ExactFileName    string `json:"exact_file_name"`
	Operation        string `json:"operation"`
	Priority         int    `json:"priority"`
	PreviousPriority *int   `json:"previous_priority,omitempty"`
}

type settingsUpdateRequest struct {
	AutoRefreshEnabled         *bool    `json:"auto_refresh_enabled"`
	AutoRefreshIntervalSeconds *int     `json:"auto_refresh_interval_seconds"`
	DailyUsageResetEnabled     *bool    `json:"daily_usage_reset_enabled"`
	DailyUsageResetTime        *string  `json:"daily_usage_reset_time"`
	BatchOperationConcurrency  *int     `json:"batch_operation_concurrency"`
	AttributedFailureThreshold *int     `json:"attributed_failure_threshold"`
	CountStatus429             *bool    `json:"count_status_429"`
	CountStatus5XX             *bool    `json:"count_status_5xx"`
	DebtProbeThreshold         *float64 `json:"debt_probe_threshold"`
	DebtFail401                *float64 `json:"debt_fail_401"`
	DebtFail429                *float64 `json:"debt_fail_429"`
	DebtSuccessDecay           *float64 `json:"debt_success_decay"`
	WatchPriority              *int     `json:"watch_priority"`
	AnomalyPriority            *int     `json:"anomaly_priority"`
	DeadPriority               *int     `json:"dead_priority"`
	DefaultRestorePriority     *int     `json:"default_restore_priority"`
	WatchReprobeMinutes        *int     `json:"watch_reprobe_minutes"`
	AnomalyReprobeHours        *int     `json:"anomaly_reprobe_hours"`
	// Legacy aliases (accepted, mapped into v0.6.0 fields).
	SoftDemotionPriority *int     `json:"soft_demotion_priority"`
	SoftDebtThreshold    *float64 `json:"soft_debt_threshold"`
	HardDebtThreshold    *float64 `json:"hard_debt_threshold"`
	DemotionPriority     *int     `json:"demotion_priority"`
	FreeUserDailyTokenLimit *uint64 `json:"free_user_daily_token_limit"`
	OutboundProxyURL        *string `json:"outbound_proxy_url"`
}

type settingsResponse struct {
	application.Settings
	Source string `json:"source"`
}

func (router *Router) settingsResponse() settingsResponse {
	settings := application.NormalizeSettings(router.settingsFallback)
	source := "default"
	if persisted := router.store.View().Settings; persisted != nil {
		settings = application.NormalizeSettings(*persisted)
		source = "state"
	}
	return settingsResponse{Settings: settings, Source: source}
}

func (router *Router) updateSettings(update settingsUpdateRequest) (application.Settings, error) {
	if update.AutoRefreshEnabled == nil && update.AutoRefreshIntervalSeconds == nil && update.DailyUsageResetEnabled == nil && update.DailyUsageResetTime == nil &&
		update.BatchOperationConcurrency == nil &&
		update.AttributedFailureThreshold == nil && update.CountStatus429 == nil && update.CountStatus5XX == nil &&
		update.DebtProbeThreshold == nil && update.DebtFail401 == nil && update.DebtFail429 == nil && update.DebtSuccessDecay == nil &&
		update.WatchPriority == nil && update.AnomalyPriority == nil && update.DeadPriority == nil &&
		update.DefaultRestorePriority == nil && update.WatchReprobeMinutes == nil && update.AnomalyReprobeHours == nil &&
		update.SoftDemotionPriority == nil && update.SoftDebtThreshold == nil && update.HardDebtThreshold == nil && update.DemotionPriority == nil &&
		update.FreeUserDailyTokenLimit == nil && update.OutboundProxyURL == nil {
		return application.Settings{}, fmt.Errorf("至少提供一个可配置字段")
	}
	if update.AutoRefreshIntervalSeconds != nil && (*update.AutoRefreshIntervalSeconds < 2 || *update.AutoRefreshIntervalSeconds > 60) {
		return application.Settings{}, fmt.Errorf("auto_refresh_interval_seconds 必须在 2..60 范围内")
	}
	if update.DailyUsageResetTime != nil {
		if err := application.ValidateDailyUsageResetTime(*update.DailyUsageResetTime); err != nil {
			return application.Settings{}, err
		}
	}
	if update.BatchOperationConcurrency != nil && (*update.BatchOperationConcurrency < 1 || *update.BatchOperationConcurrency > 50) {
		return application.Settings{}, fmt.Errorf("batch_operation_concurrency 必须在 1..50 范围内")
	}
	if update.AttributedFailureThreshold != nil && (*update.AttributedFailureThreshold < 1 || *update.AttributedFailureThreshold > 100) {
		return application.Settings{}, fmt.Errorf("attributed_failure_threshold 必须在 1..100 范围内")
	}
	for name, value := range map[string]*float64{
		"debt_probe_threshold": update.DebtProbeThreshold, "soft_debt_threshold": update.SoftDebtThreshold,
		"hard_debt_threshold": update.HardDebtThreshold,
		"debt_fail_401": update.DebtFail401, "debt_fail_429": update.DebtFail429, "debt_success_decay": update.DebtSuccessDecay,
	} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > 1_000_000) {
			return application.Settings{}, fmt.Errorf("%s 必须在 0..1000000 范围内", name)
		}
	}
	if update.DebtProbeThreshold != nil && *update.DebtProbeThreshold == 0 {
		return application.Settings{}, fmt.Errorf("debt_probe_threshold 必须大于 0")
	}
	if update.SoftDebtThreshold != nil && *update.SoftDebtThreshold == 0 {
		return application.Settings{}, fmt.Errorf("soft_debt_threshold 必须大于 0")
	}
	if update.WatchReprobeMinutes != nil && (*update.WatchReprobeMinutes < 1 || *update.WatchReprobeMinutes > 10080) {
		return application.Settings{}, fmt.Errorf("watch_reprobe_minutes 必须在 1..10080 范围内")
	}
	if update.AnomalyReprobeHours != nil && (*update.AnomalyReprobeHours < 1 || *update.AnomalyReprobeHours > 168) {
		return application.Settings{}, fmt.Errorf("anomaly_reprobe_hours 必须在 1..168 范围内")
	}
	const minPriority, maxPriority = -1_000_000, 1_000_000
	for name, value := range map[string]*int{
		"watch_priority": update.WatchPriority, "anomaly_priority": update.AnomalyPriority,
		"dead_priority": update.DeadPriority, "default_restore_priority": update.DefaultRestorePriority,
		"soft_demotion_priority": update.SoftDemotionPriority, "demotion_priority": update.DemotionPriority,
	} {
		if value != nil && (*value < minPriority || *value > maxPriority) {
			return application.Settings{}, fmt.Errorf("%s 必须在 %d..%d 范围内", name, minPriority, maxPriority)
		}
	}
	if update.FreeUserDailyTokenLimit != nil && *update.FreeUserDailyTokenLimit < 1 {
		return application.Settings{}, fmt.Errorf("free_user_daily_token_limit 必须大于等于 1")
	}
	if update.OutboundProxyURL != nil {
		if err := application.ValidateOutboundProxyURL(*update.OutboundProxyURL); err != nil {
			return application.Settings{}, err
		}
	}

	var result application.Settings
	err := router.store.Update(func(snapshot *stateinfra.Snapshot) error {
		settings := router.settingsFallback
		if snapshot.Settings != nil {
			settings = *snapshot.Settings
		}
		settings = application.NormalizeSettings(settings)
		if update.AutoRefreshEnabled != nil {
			settings.AutoRefreshEnabled = *update.AutoRefreshEnabled
		}
		if update.AutoRefreshIntervalSeconds != nil {
			settings.AutoRefreshIntervalSeconds = *update.AutoRefreshIntervalSeconds
		}
		if update.DailyUsageResetEnabled != nil {
			settings.DailyUsageResetEnabled = *update.DailyUsageResetEnabled
		}
		if update.DailyUsageResetTime != nil {
			settings.DailyUsageResetTime = *update.DailyUsageResetTime
		}
		if update.BatchOperationConcurrency != nil {
			settings.BatchOperationConcurrency = *update.BatchOperationConcurrency
		}
		if update.AttributedFailureThreshold != nil {
			settings.AttributedFailureThreshold = *update.AttributedFailureThreshold
		}
		if update.CountStatus429 != nil {
			settings.CountStatus429 = *update.CountStatus429
		}
		if update.CountStatus5XX != nil {
			settings.CountStatus5XX = *update.CountStatus5XX
		}
		if update.DebtProbeThreshold != nil {
			settings.DebtProbeThreshold = *update.DebtProbeThreshold
		} else if update.SoftDebtThreshold != nil {
			settings.DebtProbeThreshold = *update.SoftDebtThreshold
		}
		if update.DebtFail401 != nil {
			settings.DebtFail401 = *update.DebtFail401
		}
		if update.DebtFail429 != nil {
			settings.DebtFail429 = *update.DebtFail429
		}
		if update.DebtSuccessDecay != nil {
			settings.DebtSuccessDecay = *update.DebtSuccessDecay
		}
		if update.WatchPriority != nil {
			settings.WatchPriority = *update.WatchPriority
		} else if update.SoftDemotionPriority != nil {
			settings.WatchPriority = *update.SoftDemotionPriority
		}
		if update.AnomalyPriority != nil {
			settings.AnomalyPriority = *update.AnomalyPriority
		}
		if update.DeadPriority != nil {
			settings.DeadPriority = *update.DeadPriority
		} else if update.DemotionPriority != nil {
			settings.DeadPriority = *update.DemotionPriority
		}
		settings.DemotionPriority = settings.DeadPriority
		settings.SoftDemotionPriority = settings.WatchPriority
		settings.SoftDebtThreshold = settings.DebtProbeThreshold
		if update.DefaultRestorePriority != nil {
			settings.DefaultRestorePriority = *update.DefaultRestorePriority
		}
		if update.WatchReprobeMinutes != nil {
			settings.WatchReprobeMinutes = *update.WatchReprobeMinutes
		}
		if update.AnomalyReprobeHours != nil {
			settings.AnomalyReprobeHours = *update.AnomalyReprobeHours
		}
		if update.FreeUserDailyTokenLimit != nil {
			settings.FreeUserDailyTokenLimit = *update.FreeUserDailyTokenLimit
		}
		if update.OutboundProxyURL != nil {
			settings.OutboundProxyURL = strings.TrimSpace(*update.OutboundProxyURL)
		}
		settings.Revision++
		if settings.Revision < 1 {
			settings.Revision = 1
		}
		settings = application.NormalizeSettings(settings)
		snapshot.Settings = &settings
		result = settings
		return nil
	})
	if err != nil {
		return application.Settings{}, fmt.Errorf("保存设置失败: %w", err)
	}
	return result, nil
}

func matchesPath(path, endpoint string) bool {
	return path == APIPrefix+endpoint || path == "/v0/management"+APIPrefix+endpoint || strings.HasSuffix(path, APIPrefix+endpoint)
}

func decodeStrictBody(body []byte, target any) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return fmt.Errorf("请求体不能为空")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("请求 JSON 无效: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("请求体只能包含一个 JSON 对象")
	}
	return nil
}

func accountErrorResponse(err error) cpaabi.ManagementResponse {
	accountErr := application.AsAccountError(err)
	return apiError(accountErr.HTTPStatus, accountErr.Code, accountErr.Message, accountErr.Retryable)
}

func apiError(status int, code, message string, retryable bool) cpaabi.ManagementResponse {
	return jsonResponse(status, map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": retryable}})
}

func isPanelPath(path string) bool {
	path = strings.TrimSuffix(path, "/")
	return path == ResourcePanelPath ||
		strings.HasSuffix(path, "/panel") ||
		strings.HasSuffix(path, "/cpa-grok-panel") ||
		strings.Contains(path, "/resource/plugins/cpa-grok-panel/panel") ||
		strings.HasSuffix(path, "/plugins/cpa-grok-panel")
}

func DecodeRequest(data []byte) (Request, error) {
	if len(data) == 0 {
		return Request{Method: "GET"}, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Request{}, fmt.Errorf("decode management request: %w", err)
	}
	// Unwrap nested envelopes if present.
	for _, key := range []string{"request", "Request", "params", "Params", "ManagementRequest"} {
		if value, ok := raw[key]; ok {
			return DecodeRequest(value)
		}
	}
	var request Request
	_ = decode(raw, &request.Method, "Method", "method")
	_ = decode(raw, &request.Path, "Path", "path", "URL", "url")
	_ = decode(raw, &request.Query, "Query", "query")
	// Headers may be map[string][]string or map[string]string
	if !decode(raw, &request.Headers, "Headers", "headers") {
		var simple map[string]string
		if decode(raw, &simple, "Headers", "headers") {
			request.Headers = make(map[string][]string, len(simple))
			for k, v := range simple {
				request.Headers[k] = []string{v}
			}
		}
	}
	// Body may be []byte (base64) or string
	if !decode(raw, &request.Body, "Body", "body") {
		var asString string
		if decode(raw, &asString, "Body", "body") {
			request.Body = []byte(asString)
		}
	}
	if request.Method == "" {
		request.Method = "GET"
	}
	return request, nil
}

func normalizePath(path string, query map[string][]string) (string, map[string][]string) {
	parsed, err := url.Parse(path)
	if err == nil && parsed.Path != "" {
		path = parsed.Path
		if query == nil && parsed.RawQuery != "" {
			query = parsed.Query()
		}
	}
	if path != "/" {
		path = strings.TrimSuffix(path, "/")
	}
	return path, query
}

func firstQuery(query map[string][]string, key string) string {
	if values := query[key]; len(values) > 0 {
		return values[0]
	}
	return ""
}

func jsonResponse(status int, body any) cpaabi.ManagementResponse {
	data, _ := json.Marshal(body)
	return cpaabi.ManagementResponse{
		StatusCode: status,
		Headers: map[string][]string{
			"Content-Type":           {"application/json; charset=utf-8"},
			"Cache-Control":          {"no-store"},
			"X-Content-Type-Options": {"nosniff"},
		},
		Body: data,
	}
}

func htmlResponse(body string) cpaabi.ManagementResponse {
	return cpaabi.ManagementResponse{
		StatusCode: 200,
		Headers: map[string][]string{
			"Content-Type":           {"text/html; charset=utf-8"},
			"Cache-Control":          {"no-store"},
			"X-Content-Type-Options": {"nosniff"},
			"Content-Security-Policy": {
				"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'self'",
			},
		},
		Body: []byte(body),
	}
}

func decode(raw map[string]json.RawMessage, target any, keys ...string) bool {
	value, ok := first(raw, keys...)
	return ok && json.Unmarshal(value, target) == nil
}

func first(raw map[string]json.RawMessage, keys ...string) (json.RawMessage, bool) {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			return value, true
		}
	}
	return nil, false
}
