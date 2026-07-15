package management

import (
	"encoding/json"
	"fmt"
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
	accounts *application.AccountsService
	store    *stateinfra.Store
}

func NewRouter(accounts *application.AccountsService, store *stateinfra.Store) *Router {
	return &Router{accounts: accounts, store: store}
}

func Registration() map[string]any {
	// Resource: browser panel (no management auth).
	// Routes: authenticated management APIs.
	routes := []map[string]any{
		{"Method": "GET", "Path": APIPrefix + "/meta", "Description": "插件元信息"},
		{"Method": "GET", "Path": APIPrefix + "/accounts", "Description": "账号列表"},
		{"Method": "GET", "Path": APIPrefix + "/settings", "Description": "只读设置"},
	}
	resources := []map[string]any{
		{"Path": ResourcePanelPath, "Menu": "Grok 账号", "Description": "Grok 账号只读面板"},
	}
	return map[string]any{"routes": routes, "resources": resources}
}

func (router *Router) Handle(request Request) cpaabi.ManagementResponse {
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if method == "" {
		method = "GET"
	}
	path, query := normalizePath(request.Path, request.Query)
	if method != "GET" {
		return jsonResponse(405, map[string]any{"error": map[string]any{"code": "read_only", "message": "M1 仅支持只读 GET 请求"}})
	}

	switch {
	case isPanelPath(path):
		return htmlResponse(web.PanelHTML)
	case strings.HasSuffix(path, "/meta") || path == APIPrefix+"/meta" || path == "/v0/management"+APIPrefix+"/meta":
		return jsonResponse(200, application.BuildMeta(router.store.View()))
	case strings.HasSuffix(path, "/settings") || path == APIPrefix+"/settings" || path == "/v0/management"+APIPrefix+"/settings":
		return jsonResponse(200, application.ReadOnlySettings())
	case strings.HasSuffix(path, "/accounts") || path == APIPrefix+"/accounts" || path == "/v0/management"+APIPrefix+"/accounts":
		items, snapshotAt, err := router.accounts.List(firstQuery(query, "search"))
		if err != nil {
			return jsonResponse(503, map[string]any{"error": map[string]any{"code": "host_unavailable", "message": err.Error(), "retryable": true}})
		}
		return jsonResponse(200, map[string]any{
			"items": items, "next_cursor": nil, "snapshot_at": snapshotAt,
			"host_snapshot_revision": nil, "stale": false,
		})
	default:
		return jsonResponse(404, map[string]any{"error": map[string]any{"code": "not_found", "message": "接口不存在: " + path}})
	}
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
