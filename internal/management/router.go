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
	BasePath = "/plugins/cpa-grok-panel"
	APIPath  = BasePath + "/api/v1"
)

type Request struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Query   map[string][]string `json:"query,omitempty"`
	Headers map[string]string   `json:"headers,omitempty"`
	Body    string              `json:"body,omitempty"`
}

type Router struct {
	accounts *application.AccountsService
	store    *stateinfra.Store
}

func NewRouter(accounts *application.AccountsService, store *stateinfra.Store) *Router {
	return &Router{accounts: accounts, store: store}
}

func Registration() map[string]any {
	routes := []map[string]any{
		{"Method": "GET", "Path": BasePath + "/", "Menu": "Grok 账号", "Description": "Grok 账号只读面板"},
		{"Method": "GET", "Path": APIPath + "/meta", "Description": "插件元信息"},
		{"Method": "GET", "Path": APIPath + "/accounts", "Description": "账号列表"},
		{"Method": "GET", "Path": APIPath + "/settings", "Description": "只读设置"},
	}
	resources := []map[string]any{
		{"Path": BasePath + "/", "Menu": "Grok 账号", "Description": "Grok 账号只读面板"},
		{"Path": "/panel", "Menu": "Grok 账号", "Description": "兼容 panel 入口"},
	}
	return map[string]any{"routes": routes, "resources": resources}
}

func (router *Router) Handle(request Request) cpaabi.ManagementResponse {
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	path, query := normalizePath(request.Path, request.Query)
	if method != "GET" {
		return jsonResponse(405, map[string]any{"error": map[string]any{"code": "read_only", "message": "M1 仅支持只读 GET 请求"}})
	}
	switch path {
	case BasePath, BasePath + "/", "/panel", "/panel/":
		return htmlResponse(web.PanelHTML)
	case APIPath + "/meta":
		return jsonResponse(200, application.BuildMeta(router.store.View()))
	case APIPath + "/settings":
		return jsonResponse(200, application.ReadOnlySettings())
	case APIPath + "/accounts":
		items, snapshotAt, err := router.accounts.List(firstQuery(query, "search"))
		if err != nil {
			return jsonResponse(503, map[string]any{"error": map[string]any{"code": "host_unavailable", "message": err.Error(), "retryable": true}})
		}
		return jsonResponse(200, map[string]any{"items": items, "next_cursor": nil, "snapshot_at": snapshotAt, "host_snapshot_revision": nil, "stale": false})
	default:
		return jsonResponse(404, map[string]any{"error": map[string]any{"code": "not_found", "message": "接口不存在"}})
	}
}

func DecodeRequest(data []byte) (Request, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Request{}, fmt.Errorf("decode management request: %w", err)
	}
	if value, ok := first(raw, "request", "Request", "params", "Params"); ok {
		return DecodeRequest(value)
	}
	var request Request
	decode(raw, &request.Method, "method", "Method")
	decode(raw, &request.Path, "path", "Path", "url", "URL")
	decode(raw, &request.Query, "query", "Query")
	decode(raw, &request.Headers, "headers", "Headers")
	decode(raw, &request.Body, "body", "Body")
	if request.Method == "" {
		request.Method = "GET"
	}
	return request, nil
}

func normalizePath(path string, query map[string][]string) (string, map[string][]string) {
	parsed, err := url.Parse(path)
	if err == nil && parsed.Path != "" {
		path = parsed.Path
		if query == nil {
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
	return cpaabi.ManagementResponse{StatusCode: status, Headers: map[string]string{
		"Content-Type": "application/json; charset=utf-8", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff",
	}, Body: data}
}

func htmlResponse(body string) cpaabi.ManagementResponse {
	return cpaabi.ManagementResponse{StatusCode: 200, Headers: map[string]string{
		"Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff",
		"Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'self'",
	}, Body: []byte(body)}
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
