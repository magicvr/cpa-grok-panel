package management

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/magicvr/cpa-grok-panel/internal/application"
	"github.com/magicvr/cpa-grok-panel/internal/cpaabi"
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
		return jsonResponse(200, application.BuildMeta(router.store.View()))
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

type setEnabledRequest struct {
	AuthIndex     string `json:"auth_index"`
	ExactFileName string `json:"exact_file_name"`
	Enabled       *bool  `json:"enabled"`
}

type settingsUpdateRequest struct {
	AutoRefreshEnabled         *bool `json:"auto_refresh_enabled"`
	AutoRefreshIntervalSeconds *int  `json:"auto_refresh_interval_seconds"`
	AttributedFailureThreshold *int  `json:"attributed_failure_threshold"`
	CountStatus429             *bool `json:"count_status_429"`
	CountStatus5XX             *bool `json:"count_status_5xx"`
	DemotionPriority           *int  `json:"demotion_priority"`
	DefaultRestorePriority     *int  `json:"default_restore_priority"`
}

type settingsResponse struct {
	application.Settings
	Source string `json:"source"`
}

func (router *Router) settingsResponse() settingsResponse {
	settings := router.settingsFallback
	source := "default"
	if persisted := router.store.View().Settings; persisted != nil {
		settings = *persisted
		source = "state"
	}
	return settingsResponse{Settings: settings, Source: source}
}

func (router *Router) updateSettings(update settingsUpdateRequest) (application.Settings, error) {
	if update.AutoRefreshEnabled == nil && update.AutoRefreshIntervalSeconds == nil &&
		update.AttributedFailureThreshold == nil && update.CountStatus429 == nil && update.CountStatus5XX == nil &&
		update.DemotionPriority == nil && update.DefaultRestorePriority == nil {
		return application.Settings{}, fmt.Errorf("至少提供一个可配置字段")
	}
	if update.AutoRefreshIntervalSeconds != nil && (*update.AutoRefreshIntervalSeconds < 2 || *update.AutoRefreshIntervalSeconds > 60) {
		return application.Settings{}, fmt.Errorf("auto_refresh_interval_seconds 必须在 2..60 范围内")
	}
	if update.AttributedFailureThreshold != nil && (*update.AttributedFailureThreshold < 1 || *update.AttributedFailureThreshold > 100) {
		return application.Settings{}, fmt.Errorf("attributed_failure_threshold 必须在 1..100 范围内")
	}
	const minPriority, maxPriority = -1_000_000, 1_000_000
	if update.DemotionPriority != nil && (*update.DemotionPriority < minPriority || *update.DemotionPriority > maxPriority) {
		return application.Settings{}, fmt.Errorf("demotion_priority 必须在 %d..%d 范围内", minPriority, maxPriority)
	}
	if update.DefaultRestorePriority != nil && (*update.DefaultRestorePriority < minPriority || *update.DefaultRestorePriority > maxPriority) {
		return application.Settings{}, fmt.Errorf("default_restore_priority 必须在 %d..%d 范围内", minPriority, maxPriority)
	}

	var result application.Settings
	err := router.store.Update(func(snapshot *stateinfra.Snapshot) error {
		settings := router.settingsFallback
		if snapshot.Settings != nil {
			settings = *snapshot.Settings
		}
		if update.AutoRefreshEnabled != nil {
			settings.AutoRefreshEnabled = *update.AutoRefreshEnabled
		}
		if update.AutoRefreshIntervalSeconds != nil {
			settings.AutoRefreshIntervalSeconds = *update.AutoRefreshIntervalSeconds
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
		if update.DemotionPriority != nil {
			settings.DemotionPriority = *update.DemotionPriority
		}
		if update.DefaultRestorePriority != nil {
			settings.DefaultRestorePriority = *update.DefaultRestorePriority
		}
		settings.Revision++
		if settings.Revision < 1 {
			settings.Revision = 1
		}
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
