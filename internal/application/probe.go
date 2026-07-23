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
	Status     string // live | exceed | dead | cooling | error
	HTTPStatus int
	Error      string
}

// ApplyProbeResult updates quota.probe_* and, when required, demotion class/priority.
//
// Rules:
//   - always write probe_* + probe_at
//   - manual + live: probe only (no class/priority change)
//   - manual + non-live OR auto: reclassify tier
//   - auto + live while already watch: restore to none (scheduled re-probe success)
//   - auto + live otherwise: enter watch
//   - exceed/dead → dead; cooling/error → anomaly
func (service *AccountsService) ApplyProbeResult(authIndex string, result ProbeResult, source string) (domain.AccountView, error) {
	service.write.Lock()
	defer service.write.Unlock()

	source = strings.ToLower(strings.TrimSpace(source))
	if source != domain.ProbeSourceManual && source != domain.ProbeSourceAuto {
		source = domain.ProbeSourceManual
	}
	status := domain.NormalizeProbeStatus(result.Status, result.HTTPStatus)
	if status == "" {
		status = domain.ClassifyProbeHTTP(result.HTTPStatus)
	}
	if status == "" {
		status = domain.ProbeStatusError
	}

	settings := service.settings()
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

	// manual + live → probe only
	if source == domain.ProbeSourceManual && status == domain.ProbeStatusLive {
		return service.project(file), nil
	}

	current := service.store.View().Accounts[authIndex].Demotion.Normalized()

	var (
		targetClass string
		targetPrio  int
		nextProbe   *time.Time
		restoreNone bool
	)

	switch status {
	case domain.ProbeStatusLive:
		if source == domain.ProbeSourceAuto && current.Class == domain.DemotionClassWatch {
			restoreNone = true
			targetClass = domain.DemotionClassNone
			targetPrio = settings.DefaultRestorePriority
			nextProbe = nil
		} else if source == domain.ProbeSourceAuto {
			targetClass = domain.DemotionClassWatch
			targetPrio = settings.WatchPriority
			at := now.Add(time.Duration(settings.WatchReprobeMinutes) * time.Minute)
			nextProbe = &at
		} else {
			return service.project(file), nil
		}
	case domain.ProbeStatusExceed, domain.ProbeStatusDead:
		targetClass = domain.DemotionClassDead
		targetPrio = settings.DeadPriority
		nextProbe = nil
	default: // cooling, error, unknown
		targetClass = domain.DemotionClassAnomaly
		targetPrio = settings.AnomalyPriority
		at := now.Add(time.Duration(settings.AnomalyReprobeHours) * time.Hour)
		nextProbe = &at
	}

	// Already at desired applied tier with matching priority: only refresh schedule.
	if !restoreNone && current.State == "applied" && current.Class == targetClass && file.Priority == targetPrio {
		_ = service.store.Update(func(snapshot *stateinfra.Snapshot) error {
			state := snapshot.Accounts[authIndex]
			demotion := state.Demotion.Normalized()
			demotion.NextProbeAt = nextProbe
			demotion.TriggeredAt = &now
			state.Demotion = demotion
			snapshot.Accounts[authIndex] = state
			return nil
		})
		return service.project(file), nil
	}

	if err := service.store.Update(func(snapshot *stateinfra.Snapshot) error {
		state := snapshot.Accounts[authIndex]
		demotion := state.Demotion.Normalized()
		if demotion.BaselinePriority == nil && domain.IsActiveDemotionClass(targetClass) {
			if demotion.Class == domain.DemotionClassNone || demotion.Class == "" {
				demotion.BaselinePriority = intPointer(file.Priority)
			}
		}
		demotion.State = "requested"
		demotion.Class = targetClass
		demotion.TargetPriority = intPointer(targetPrio)
		demotion.TriggeredAt = &now
		demotion.NextProbeAt = nextProbe
		demotion.FailureCode = ""
		if restoreNone {
			demotion.Class = domain.DemotionClassNone
			demotion.TargetPriority = intPointer(settings.DefaultRestorePriority)
			demotion.NextProbeAt = nil
			state.Failure.DebtScore = 0
			state.Failure.ConsecutiveAttributedFailures = 0
		}
		state.ExactFileName = file.Name
		state.Demotion = demotion
		snapshot.Accounts[authIndex] = state
		return nil
	}); err != nil {
		return domain.AccountView{}, &AccountError{Code: "state_write_failed", Message: err.Error(), HTTPStatus: 503, Retryable: true}
	}

	if err := service.applyRequestedDemotionLocked(authIndex, targetPrio); err != nil {
		return domain.AccountView{}, err
	}
	verified, err := service.resolveExact(authIndex, file.Name)
	if err != nil {
		return domain.AccountView{}, err
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
	// Skip dead for auto enqueue-style probes unless explicitly manual source (manual uses panel).
	demotion := service.store.View().Accounts[authIndex].Demotion.Normalized()
	if source == domain.ProbeSourceAuto && demotion.Class == domain.DemotionClassDead {
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
		// Nested common locations
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
