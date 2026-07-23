package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/domain"
	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

const (
	// DefaultProbeURL is the same endpoint the panel uses for alive checks.
	DefaultProbeURL = "https://cli-chat-proxy.grok.com/v1/responses"
	// ProbeClientVersion mirrors panel / GRA client headers.
	ProbeClientVersion      = "0.2.93"
	defaultProbeHTTPTimeout = 25 * time.Second
)

// ProbeResult is the outcome of a live check (manual panel or auto Go path).
type ProbeResult struct {
	Status     string // live | invalid | dead | throttled | error
	HTTPStatus int
	Error      string
}

// ApplyProbeResult updates quota.probe_* and always ApplyAliveStatus (priority bind).
// No "manual live does not change priority" exception (v0.7.0).
func (service *AccountsService) ApplyProbeResult(authIndex string, result ProbeResult, source string) (domain.AccountView, error) {
	service.write.Lock()
	defer service.write.Unlock()

	source = strings.ToLower(strings.TrimSpace(source))
	if source != domain.ProbeSourceManual && source != domain.ProbeSourceAuto {
		source = domain.ProbeSourceManual
	}
	_ = source

	status := domain.NormalizeProbeStatus(result.Status, result.HTTPStatus)
	if status == "" {
		status = domain.ClassifyProbeHTTP(result.HTTPStatus)
	}
	if status == "" {
		status = domain.ProbeStatusError
	}
	if status == domain.ProbeStatusUnknown {
		// explicit unknown from caller → clear path uses ApplyAliveStatus separately
		status = domain.ProbeStatusError
	}

	now := service.now().UTC()
	file, err := service.resolveByAuthIndex(authIndex)
	if err != nil {
		return domain.AccountView{}, err
	}

	probeError := strings.TrimSpace(result.Error)
	if status == domain.ProbeStatusLive {
		probeError = ""
	}
	if err := service.store.Update(func(snapshot *stateinfra.Snapshot) error {
		state := snapshot.Accounts[authIndex]
		state.ExactFileName = file.Name
		state.Quota.ProbeStatus = status
		state.Quota.ProbeHTTP = result.HTTPStatus
		state.Quota.ProbeAt = now
		state.Quota.ProbeError = probeError
		if state.Quota.Plan == "" {
			state.Quota.Plan = "unknown"
		}
		snapshot.Accounts[authIndex] = state
		return nil
	}); err != nil {
		return domain.AccountView{}, &AccountError{Code: "state_write_failed", Message: err.Error(), HTTPStatus: 503, Retryable: true}
	}

	return service.applyAliveStatusLocked(authIndex, status, false)
}

// ApplyAliveStatus writes probe status (if needed) and binds CPA priority to the
// configured value for that status. clearDebt zeros failure debt when true.
func (service *AccountsService) ApplyAliveStatus(authIndex, status string, clearDebt bool) (domain.AccountView, error) {
	service.write.Lock()
	defer service.write.Unlock()
	return service.applyAliveStatusLocked(authIndex, status, clearDebt)
}

// applyAliveStatusLocked binds priority for status. Caller must hold service.write.
// Status "" or "unknown" clears probe fields and uses priority_unknown.
func (service *AccountsService) applyAliveStatusLocked(authIndex, status string, clearDebt bool) (domain.AccountView, error) {
	settings := service.settings()
	file, err := service.resolveByAuthIndex(authIndex)
	if err != nil {
		return domain.AccountView{}, err
	}

	canonical := domain.CanonicalProbeStatus(status, 0)
	targetPriority := PriorityForProbeStatus(settings, canonical)
	now := service.now().UTC()

	// Persist probe_* for unknown (clear) or when status string is canonical.
	if err := service.store.Update(func(snapshot *stateinfra.Snapshot) error {
		state := snapshot.Accounts[authIndex]
		state.ExactFileName = file.Name
		if canonical == domain.ProbeStatusUnknown {
			state.Quota.ProbeStatus = ""
			state.Quota.ProbeHTTP = 0
			state.Quota.ProbeAt = time.Time{}
			state.Quota.ProbeError = ""
		} else {
			// Keep existing http/at/error if already set for same status; only ensure status.
			if domain.NormalizeProbeStatus(state.Quota.ProbeStatus, state.Quota.ProbeHTTP) != canonical {
				state.Quota.ProbeStatus = canonical
				if state.Quota.ProbeAt.IsZero() {
					state.Quota.ProbeAt = now
				}
			} else {
				state.Quota.ProbeStatus = canonical
			}
		}
		if clearDebt {
			state.Failure.DebtScore = 0
			state.Failure.ConsecutiveAttributedFailures = 0
		}
		// Clear legacy demotion bookkeeping so list doesn't show stale tiers.
		state.Demotion = domain.DemotionState{State: "none", Class: domain.DemotionClassNone}
		snapshot.Accounts[authIndex] = state
		return nil
	}); err != nil {
		return domain.AccountView{}, &AccountError{Code: "state_write_failed", Message: err.Error(), HTTPStatus: 503, Retryable: true}
	}

	if file.Priority == targetPriority {
		return service.project(file), nil
	}

	var document map[string]any
	if service.priorityWriter == nil {
		document, err = service.host.GetAuthFile(authIndex)
		if err != nil {
			return domain.AccountView{}, hostError("auth_get_failed", err)
		}
	}
	if err := service.writePriority(file, targetPriority, document); err != nil {
		return domain.AccountView{}, err
	}
	verified, err := service.resolveExact(authIndex, file.Name)
	if err != nil {
		return domain.AccountView{}, err
	}
	if verified.Priority != targetPriority {
		return domain.AccountView{}, &AccountError{Code: "write_verification_failed", Message: "优先级写后校验不一致", HTTPStatus: 502, Retryable: true}
	}
	return service.project(verified), nil
}

// ProbeAccount runs a Go-side alive check (auto path) using access_token + optional proxy,
// then ApplyProbeResult(source=auto). Prefer auth file proxy_url over settings.outbound_proxy_url.
func (service *AccountsService) ProbeAccount(authIndex, exactFileName, source string) (domain.AccountView, error) {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		source = domain.ProbeSourceAuto
	}
	file, err := service.resolveByAuthIndex(authIndex)
	if err != nil {
		return domain.AccountView{}, err
	}
	if exactFileName != "" && file.Name != exactFileName {
		return domain.AccountView{}, &AccountError{Code: "account_mapping_changed", Message: "账号文件映射已变化，请刷新列表", HTTPStatus: 409}
	}
	// Skip auto probes for accounts already classified dead (frozen).
	probe := service.store.View().Accounts[authIndex].Quota.ProbeStatus
	if source == domain.ProbeSourceAuto && domain.NormalizeProbeStatus(probe, 0) == domain.ProbeStatusDead {
		return service.project(file), nil
	}

	document, err := service.host.GetAuthFile(authIndex)
	if err != nil {
		result := ProbeResult{Status: domain.ProbeStatusError, Error: "auth_get: " + err.Error()}
		return service.ApplyProbeResult(authIndex, result, source)
	}
	accessToken := extractProbeAccessToken(document)
	if accessToken == "" {
		result := ProbeResult{Status: domain.ProbeStatusError, Error: "missing access_token"}
		return service.ApplyProbeResult(authIndex, result, source)
	}
	proxyURL := firstNonEmptyString(document, "proxy_url", "proxyUrl", "proxy")
	if proxyURL == "" {
		proxyURL = nestedString(document, "proxy_url")
	}
	if proxyURL == "" {
		proxyURL = strings.TrimSpace(service.settings().OutboundProxyURL)
	}

	httpStatus, probeErr := executeProbeHTTP(context.Background(), accessToken, proxyURL, service.settings().OperationTimeoutSeconds)
	result := ProbeResult{HTTPStatus: httpStatus, Status: domain.ClassifyProbeHTTP(httpStatus)}
	if probeErr != nil {
		result.Error = probeErr.Error()
		if result.Status == "" || result.HTTPStatus == 0 {
			result.Status = domain.ProbeStatusError
		}
	}
	return service.ApplyProbeResult(authIndex, result, source)
}

func executeProbeHTTP(ctx context.Context, accessToken, proxyURL string, timeoutSeconds int) (int, error) {
	timeout := defaultProbeHTTPTimeout
	if timeoutSeconds > 0 {
		timeout = time.Duration(timeoutSeconds) * time.Second
	}
	client, err := NewOutboundHTTPClient(proxyURL, timeout)
	if err != nil {
		return 0, fmt.Errorf("build probe client: %w", err)
	}
	body := map[string]any{
		"model":             "grok-4.5",
		"input":             "Reply with exactly OK",
		"max_output_tokens": 8,
		"store":             false,
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, DefaultProbeURL, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("x-xai-token-auth", "xai-grok-cli")
	req.Header.Set("x-authenticateresponse", "authenticate-response")
	req.Header.Set("x-grok-client-identifier", "grok-pager")
	req.Header.Set("x-grok-client-version", ProbeClientVersion)
	req.Header.Set("User-Agent", fmt.Sprintf("grok-pager/%s grok-shell/%s", ProbeClientVersion, ProbeClientVersion))

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, nil
}

func extractProbeAccessToken(document map[string]any) string {
	if document == nil {
		return ""
	}
	if v := firstNonEmptyString(document, "access_token", "accessToken", "token"); v != "" {
		return v
	}
	for _, path := range []string{"credentials", "auth", "oauth", "tokens"} {
		nested, ok := document[path].(map[string]any)
		if !ok {
			continue
		}
		if v := firstNonEmptyString(nested, "access_token", "accessToken", "token"); v != "" {
			return v
		}
	}
	return ""
}

func nestedString(document map[string]any, key string) string {
	if document == nil {
		return ""
	}
	if v := firstNonEmptyString(document, key); v != "" {
		return v
	}
	for _, path := range []string{"credentials", "auth", "proxy", "network"} {
		nested, ok := document[path].(map[string]any)
		if !ok {
			continue
		}
		if v := firstNonEmptyString(nested, key, "proxy_url", "proxyUrl", "url"); v != "" {
			return v
		}
	}
	return ""
}
