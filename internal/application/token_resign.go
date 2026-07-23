package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/cpaabi"
	"github.com/magicvr/cpa-grok-panel/internal/domain"
)

const (
	// DefaultXAIOAuthClientID is the public client_id commonly used by CPA xAI OAuth.
	// Prefer client_id from the auth document when present; this is only the fallback.
	DefaultXAIOAuthClientID = "b1a00492-073a-47ea-816f-4c329264a828"
	// DefaultXAITokenURL is the public xAI OAuth token endpoint.
	DefaultXAITokenURL = "https://auth.x.ai/oauth2/token"
	defaultTokenHTTPTimeout = 20 * time.Second
	// EnvOutboundProxy is the plugin-specific outbound proxy for token resign (and similar).
	// Higher priority than process HTTPS_PROXY/HTTP_PROXY when building the default client.
	EnvOutboundProxy = "CPA_GROK_OUTBOUND_PROXY"
)

// tokenFieldPaths mirrors bot_flag nesting for OAuth token fields.
var tokenFieldPaths = [][]string{
	{}, // top-level document
	{"credentials"},
	{"auth"},
	{"oauth"},
	{"tokens"},
}

// TokenRefreshResult is the subset of OAuth token response fields we write back.
type TokenRefreshResult struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	ExpiresIn    int64
	TokenType    string
	Raw          map[string]any
}

// TokenRefresher exchanges a refresh_token for a new access token set.
// Tests inject a mock; production uses HTTPTokenRefresher.
type TokenRefresher interface {
	Refresh(ctx context.Context, refreshToken, clientID string) (TokenRefreshResult, error)
}

// HTTPTokenRefresher performs grant_type=refresh_token against the xAI token endpoint.
//
// Outbound routing (when Client is nil):
//  1. ProxyURL if non-empty (settings outbound_proxy_url or env CPA_GROK_OUTBOUND_PROXY)
//  2. else http.ProxyFromEnvironment (HTTPS_PROXY / HTTP_PROXY / NO_PROXY)
//
// Note: this does NOT read CPA host config proxy-url; package refresh goes through CPA
// api-call (host egress), while resign POSTs auth.x.ai from the plugin process itself.
type HTTPTokenRefresher struct {
	URL      string
	Client   *http.Client
	ProxyURL string // explicit proxy; never log credentials from this value
}

func (refresher *HTTPTokenRefresher) Refresh(ctx context.Context, refreshToken, clientID string) (TokenRefreshResult, error) {
	endpoint := strings.TrimSpace(refresher.URL)
	if endpoint == "" {
		endpoint = DefaultXAITokenURL
	}
	client := refresher.Client
	if client == nil {
		built, err := NewOutboundHTTPClient(refresher.ProxyURL, defaultTokenHTTPTimeout)
		if err != nil {
			return TokenRefreshResult{}, fmt.Errorf("build outbound http client: %w", err)
		}
		client = built
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenRefreshResult{}, fmt.Errorf("build token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return TokenRefreshResult{}, mapTokenRequestError(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return TokenRefreshResult{}, fmt.Errorf("read token response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		if snippet == "" {
			snippet = response.Status
		}
		return TokenRefreshResult{}, &AccountError{
			Code:       "token_refresh_failed",
			Message:    fmt.Sprintf("token 端点返回 HTTP %d: %s", response.StatusCode, snippet),
			HTTPStatus: 502,
			Retryable:  response.StatusCode >= 500 || response.StatusCode == 429,
		}
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return TokenRefreshResult{}, fmt.Errorf("decode token response: %w", err)
	}
	result := TokenRefreshResult{Raw: raw}
	result.AccessToken = firstNonEmptyString(raw, "access_token")
	result.RefreshToken = firstNonEmptyString(raw, "refresh_token")
	result.IDToken = firstNonEmptyString(raw, "id_token")
	result.TokenType = firstNonEmptyString(raw, "token_type")
	if expires, ok := numberAsInt64(raw["expires_in"]); ok {
		result.ExpiresIn = expires
	}
	if result.AccessToken == "" {
		return TokenRefreshResult{}, &AccountError{
			Code: "token_refresh_failed", Message: "token 响应缺少 access_token", HTTPStatus: 502, Retryable: true,
		}
	}
	return result, nil
}

// NewOutboundHTTPClient builds an HTTP client for plugin-process egress (e.g. auth.x.ai).
// proxyURL, when non-empty, forces that proxy; otherwise ProxyFromEnvironment is used.
// Do not log proxyURL — it may contain credentials.
func NewOutboundHTTPClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 {
		timeout = defaultTokenHTTPTimeout
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if explicit := strings.TrimSpace(proxyURL); explicit != "" {
		parsed, err := url.Parse(explicit)
		if err != nil {
			return nil, fmt.Errorf("invalid outbound proxy url: %w", err)
		}
		if parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("invalid outbound proxy url: missing scheme or host")
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}

// ResolveOutboundProxyURL picks the plugin outbound proxy (settings > CPA_GROK_OUTBOUND_PROXY).
// Empty means fall back to process HTTPS_PROXY/HTTP_PROXY via ProxyFromEnvironment.
func ResolveOutboundProxyURL(settings Settings) string {
	if settings.OutboundProxyURL != "" {
		return strings.TrimSpace(settings.OutboundProxyURL)
	}
	return strings.TrimSpace(os.Getenv(EnvOutboundProxy))
}

func mapTokenRequestError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || isTimeoutError(err) {
		return &AccountError{
			Code:       "token_refresh_failed",
			Message:    "访问 auth.x.ai 超时（请检查出站代理/网络）",
			HTTPStatus: 502,
			Retryable:  true,
		}
	}
	if errors.Is(err, context.Canceled) {
		return &AccountError{
			Code:       "token_refresh_failed",
			Message:    "访问 auth.x.ai 已取消（请检查出站代理/网络）",
			HTTPStatus: 502,
			Retryable:  true,
		}
	}
	return &AccountError{
		Code:       "token_refresh_failed",
		Message:    fmt.Sprintf("token endpoint request failed: %v", err),
		HTTPStatus: 502,
		Retryable:  true,
	}
}

func isTimeoutError(err error) bool {
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return true
	}
	// net/url wraps often include "context deadline exceeded" without unwrapping to DeadlineExceeded
	// on older paths; also "Client.Timeout exceeded".
	msg := err.Error()
	return strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "Client.Timeout exceeded") ||
		strings.Contains(msg, "i/o timeout")
}

// Resign refreshes OAuth tokens for one account via refresh_token grant and
// writes the updated document back to the same auth file name.
// It does not mint via SSO, re-login with password, or clear demotion state.
//
// Locking: short write locks around resolve/get and save only. The HTTP call
// runs outside the lock so batch concurrent resign is not serialized on network I/O.
func (service *AccountsService) Resign(authIndex, exactFileName string) (domain.AccountView, error) {
	refreshToken, clientID, previousAccess, refresher, err := service.prepareResign(authIndex, exactFileName)
	if err != nil {
		return domain.AccountView{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTokenHTTPTimeout)
	defer cancel()
	tokens, err := refresher.Refresh(ctx, refreshToken, clientID)
	if err != nil {
		var accountErr *AccountError
		if errors.As(err, &accountErr) {
			return domain.AccountView{}, accountErr
		}
		return domain.AccountView{}, mapTokenRequestError(err)
	}

	return service.commitResign(authIndex, exactFileName, previousAccess, tokens)
}

func (service *AccountsService) prepareResign(authIndex, exactFileName string) (refreshToken, clientID, previousAccess string, refresher TokenRefresher, err error) {
	service.write.Lock()
	defer service.write.Unlock()

	if _, err := service.resolveExact(authIndex, exactFileName); err != nil {
		return "", "", "", nil, err
	}
	document, err := service.host.GetAuthFile(authIndex)
	if err != nil {
		return "", "", "", nil, hostError("auth_get_failed", err)
	}
	refreshToken = extractTokenField(document, "refresh_token")
	if refreshToken == "" {
		return "", "", "", nil, &AccountError{
			Code: "missing_refresh_token", Message: "账号 auth 文件缺少 refresh_token", HTTPStatus: 400, Retryable: false,
		}
	}
	clientID = extractTokenField(document, "client_id")
	if clientID == "" {
		clientID = DefaultXAIOAuthClientID
	}
	previousAccess = extractTokenField(document, "access_token")

	refresher = service.tokenRefresher
	if refresher == nil {
		refresher = &HTTPTokenRefresher{
			URL:      service.tokenURL,
			Client:   service.httpClient,
			ProxyURL: ResolveOutboundProxyURL(service.settings()),
		}
	}
	return refreshToken, clientID, previousAccess, refresher, nil
}

func (service *AccountsService) commitResign(authIndex, exactFileName, previousAccess string, tokens TokenRefreshResult) (domain.AccountView, error) {
	service.write.Lock()
	defer service.write.Unlock()

	// Re-resolve mapping before save (same safety as demote/verify paths).
	file, err := service.resolveExact(authIndex, exactFileName)
	if err != nil {
		return domain.AccountView{}, err
	}
	document, err := service.host.GetAuthFile(authIndex)
	if err != nil {
		return domain.AccountView{}, hostError("auth_get_failed", err)
	}
	applyTokenRefresh(document, tokens, service.now().UTC())
	if err := service.host.SaveAuthFile(file.Name, document); err != nil {
		return domain.AccountView{}, hostError("auth_save_failed", err)
	}

	// Optional write-back check: access_token should differ when the endpoint rotated it.
	if verifiedDoc, verifyErr := service.host.GetAuthFile(authIndex); verifyErr == nil {
		if next := extractTokenField(verifiedDoc, "access_token"); next != "" && previousAccess != "" && next == previousAccess {
			return domain.AccountView{}, &AccountError{
				Code: "write_verification_failed", Message: "重签写后 access_token 未变化", HTTPStatus: 502, Retryable: true,
			}
		}
	}

	// v0.7.0: resign success → probe unknown + priority_unknown.
	return service.applyAliveStatusLocked(authIndex, domain.ProbeStatusUnknown, false)
}

func (service *AccountsService) SetTokenRefresher(refresher TokenRefresher) {
	service.write.Lock()
	defer service.write.Unlock()
	service.tokenRefresher = refresher
}

func extractTokenField(document cpaabi.AuthDocument, field string) string {
	if document == nil {
		return ""
	}
	if value := stringFieldAt(document, field); value != "" {
		return value
	}
	for _, prefix := range tokenFieldPaths[1:] {
		if container, ok := lookupPath(document, prefix...); ok {
			if value := stringFieldAt(container, field); value != "" {
				return value
			}
		}
	}
	if nested, ok := nestedJSONObject(document["json"]); ok {
		if value := stringFieldAt(nested, field); value != "" {
			return value
		}
		for _, prefix := range tokenFieldPaths[1:] {
			if container, ok := lookupPath(nested, prefix...); ok {
				if value := stringFieldAt(container, field); value != "" {
					return value
				}
			}
		}
	}
	return ""
}

func stringFieldAt(object any, field string) string {
	value, ok := lookupPath(object, field)
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func applyTokenRefresh(document cpaabi.AuthDocument, tokens TokenRefreshResult, now time.Time) {
	if document == nil {
		return
	}
	// Update every object that already holds an access_token or refresh_token so nested
	// CPA layouts stay consistent; always ensure top-level keys for flat auth files.
	targets := []any{document}
	for _, prefix := range tokenFieldPaths[1:] {
		if container, ok := lookupPath(document, prefix...); ok {
			if hasTokenContainer(container) {
				targets = append(targets, container)
			}
		}
	}
	if nested, ok := nestedJSONObject(document["json"]); ok {
		if hasTokenContainer(nested) {
			targets = append(targets, nested)
		}
		for _, prefix := range tokenFieldPaths[1:] {
			if container, ok := lookupPath(nested, prefix...); ok {
				if hasTokenContainer(container) {
					targets = append(targets, container)
				}
			}
		}
	}
	applied := false
	for _, target := range targets {
		if setTokenFields(target, tokens, now) {
			applied = true
		}
	}
	if !applied {
		setTokenFields(document, tokens, now)
	}
}

func hasTokenContainer(value any) bool {
	switch typed := value.(type) {
	case cpaabi.AuthDocument:
		_, hasAccess := typed["access_token"]
		_, hasRefresh := typed["refresh_token"]
		return hasAccess || hasRefresh
	case map[string]any:
		_, hasAccess := typed["access_token"]
		_, hasRefresh := typed["refresh_token"]
		return hasAccess || hasRefresh
	default:
		return false
	}
}

func setTokenFields(target any, tokens TokenRefreshResult, now time.Time) bool {
	switch typed := target.(type) {
	case cpaabi.AuthDocument:
		typed["access_token"] = tokens.AccessToken
		if tokens.RefreshToken != "" {
			typed["refresh_token"] = tokens.RefreshToken
		}
		if tokens.IDToken != "" {
			typed["id_token"] = tokens.IDToken
		}
		if tokens.TokenType != "" {
			typed["token_type"] = tokens.TokenType
		}
		if tokens.ExpiresIn > 0 {
			typed["expires_in"] = tokens.ExpiresIn
			typed["expired"] = now.Add(time.Duration(tokens.ExpiresIn) * time.Second).Format(time.RFC3339)
			typed["expires_at"] = now.Add(time.Duration(tokens.ExpiresIn) * time.Second).Format(time.RFC3339)
		}
		return true
	case map[string]any:
		typed["access_token"] = tokens.AccessToken
		if tokens.RefreshToken != "" {
			typed["refresh_token"] = tokens.RefreshToken
		}
		if tokens.IDToken != "" {
			typed["id_token"] = tokens.IDToken
		}
		if tokens.TokenType != "" {
			typed["token_type"] = tokens.TokenType
		}
		if tokens.ExpiresIn > 0 {
			typed["expires_in"] = tokens.ExpiresIn
			typed["expired"] = now.Add(time.Duration(tokens.ExpiresIn) * time.Second).Format(time.RFC3339)
			typed["expires_at"] = now.Add(time.Duration(tokens.ExpiresIn) * time.Second).Format(time.RFC3339)
		}
		return true
	default:
		return false
	}
}

func firstNonEmptyString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := raw[key].(string); ok {
			if text := strings.TrimSpace(value); text != "" {
				return text
			}
		}
	}
	return ""
}

func numberAsInt64(value any) (int64, bool) {
	data, err := json.Marshal(value)
	if err != nil {
		return 0, false
	}
	var number int64
	if err := json.Unmarshal(data, &number); err != nil {
		return 0, false
	}
	return number, true
}
