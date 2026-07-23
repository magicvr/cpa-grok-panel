package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/domain"
	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

const (
	weakDedupeTTL = 2 * time.Minute
	maxDedupeKeys = 4096
)

var failureStatusPattern = regexp.MustCompile(`\b(401|403)\b`)

type UsageResult struct {
	Accepted          bool   `json:"accepted"`
	Duplicate         bool   `json:"duplicate"`
	DedupeMode        string `json:"dedupe_mode"`
	DemotionRequested bool   `json:"demotion_requested"`
	// ProbeRequested is set when debt threshold is crossed and auto probe should run.
	ProbeRequested bool `json:"probe_requested,omitempty"`
}

type DemotionEnqueuer interface {
	Enqueue(authIndex string)
}

// ProbeEnqueuer schedules an automatic alive probe for an account.
type ProbeEnqueuer interface {
	EnqueueProbe(authIndex string)
}

type UsageService struct {
	store            *stateinfra.Store
	now              func() time.Time
	settingsFallback Settings
	demotions        DemotionEnqueuer
	probes           ProbeEnqueuer
}

func NewUsageService(store *stateinfra.Store, now func() time.Time) *UsageService {
	return NewUsageServiceWithDemotion(store, now, DefaultSettings(), nil)
}

func NewUsageServiceWithDemotion(store *stateinfra.Store, now func() time.Time, settings Settings, demotions DemotionEnqueuer) *UsageService {
	return &UsageService{store: store, now: now, settingsFallback: NormalizeSettings(settings), demotions: demotions}
}

func (service *UsageService) SetProbeEnqueuer(probes ProbeEnqueuer) {
	service.probes = probes
}

func (service *UsageService) Handle(event domain.UsageEvent) (UsageResult, error) {
	if strings.TrimSpace(event.AuthIndex) == "" {
		return UsageResult{}, fmt.Errorf("auth_index is required")
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = service.now().UTC()
	}
	settings := service.settings()
	result := UsageResult{Accepted: true, DedupeMode: "weak"}
	err := service.store.Update(func(snapshot *stateinfra.Snapshot) error {
		now := service.now().UTC()
		pruneDedupe(snapshot, now)
		key, mode, duplicate := dedupe(snapshot, event, now)
		result.DedupeMode = mode
		result.Duplicate = duplicate
		if duplicate {
			return nil
		}
		account := snapshot.Accounts[event.AuthIndex]
		if account.FirstSeenAt.IsZero() {
			account.FirstSeenAt = event.OccurredAt
		}
		if event.Name != "" {
			account.ExactFileName = event.Name
		}
		account.LastSeenAt = event.OccurredAt
		if err := domain.ApplyUsage(&account.Usage, event); err != nil {
			return err
		}
		demotionReq, probeReq := service.applyFailurePolicy(&account, event, now, settings)
		result.DemotionRequested = demotionReq
		result.ProbeRequested = probeReq
		snapshot.Accounts[event.AuthIndex] = account
		if mode == "exact" {
			snapshot.EventDedupe.ExactIDs[key] = now
		} else {
			snapshot.EventDedupe.WeakKeys[key] = now
			snapshot.EventDedupe.WeakModeUsed = true
		}
		trimDedupe(snapshot)
		return nil
	})
	if err == nil && result.DemotionRequested && service.demotions != nil {
		service.demotions.Enqueue(event.AuthIndex)
	}
	if err == nil && result.ProbeRequested && service.probes != nil {
		service.probes.EnqueueProbe(event.AuthIndex)
	}
	return result, err
}

// applyFailurePolicy implements v0.6.0 debt → auto-probe and success restore rules.
// Returns (demotionRequested, probeRequested).
func (service *UsageService) applyFailurePolicy(account *domain.AccountState, event domain.UsageEvent, now time.Time, settings Settings) (bool, bool) {
	evidenceAt := event.OccurredAt.UTC()
	if evidenceAt.IsZero() {
		evidenceAt = now.UTC()
	}
	demotion := account.Demotion.Normalized()
	settings = NormalizeSettings(settings)

	if isSuccessfulOutcome(event.Outcome) {
		account.Failure.ConsecutiveAttributedFailures = 0
		account.Failure.DebtScore = math.Max(0, account.Failure.DebtScore-settings.DebtSuccessDecay)
		account.Failure.LastEvidenceAt = &evidenceAt

		// Success marks probe live when previously non-live / empty.
		probe := strings.ToLower(strings.TrimSpace(account.Quota.ProbeStatus))
		if probe != domain.ProbeStatusLive {
			account.Quota.ProbeStatus = domain.ProbeStatusLive
			account.Quota.ProbeHTTP = 200
			account.Quota.ProbeAt = evidenceAt
			account.Quota.ProbeError = ""
		}

		// Success while demoted → restore to normal (default_restore_priority + clear debt + cancel re-probe).
		if demotion.Class != domain.DemotionClassNone && demotion.Class != "" {
			// Dead freezes: do not auto-restore dead on success alone — operator must manual restore
			// or probe reclassification. Spec: success restores class≠none; apply to all including dead
			// when traffic succeeds (account is working). Spec says:
			// "success：… 若 class≠none → 恢复正常"
			target := settings.DefaultRestorePriority
			demotion.State = "requested"
			demotion.Class = domain.DemotionClassNone
			demotion.TargetPriority = &target
			demotion.TriggeredAt = &evidenceAt
			demotion.NextProbeAt = nil
			demotion.FailureCode = ""
			demotion.HalfOpenSince = nil
			demotion.HalfOpenSuccesses = 0
			account.Demotion = demotion
			account.Failure.DebtScore = 0
			account.Failure.ConsecutiveAttributedFailures = 0
			return true, false
		}
		return false, false
	}

	if !isPotentialXAIOAuth(event) {
		account.Failure.ConsecutiveAttributedFailures = 0
		return false, false
	}
	if !countsThresholdStatus(settings, event.StatusCode) {
		account.Failure.ConsecutiveAttributedFailures = 0
		return false, false
	}

	// dead: freeze debt scoring
	if demotion.Class == domain.DemotionClassDead {
		account.Failure.LastEvidenceAt = &evidenceAt
		account.Failure.LastFailureAt = &evidenceAt
		account.Failure.LastFailureCode = fmt.Sprintf("http_%d", event.StatusCode)
		return false, false
	}
	// watch/anomaly: no debt scoring, no debt-triggered probe
	if demotion.Class == domain.DemotionClassWatch || demotion.Class == domain.DemotionClassAnomaly {
		account.Failure.LastEvidenceAt = &evidenceAt
		account.Failure.LastFailureAt = &evidenceAt
		account.Failure.LastFailureCode = fmt.Sprintf("http_%d", event.StatusCode)
		return false, false
	}

	// only class==none accrues debt / streak
	account.Failure.LastEvidenceAt = &evidenceAt
	account.Failure.LastFailureAt = &evidenceAt
	account.Failure.LastFailureCode = fmt.Sprintf("http_%d", event.StatusCode)
	account.Failure.ConsecutiveAttributedFailures++
	switch event.StatusCode {
	case 401, 403:
		account.Failure.DebtScore += settings.DebtFail401
	case 429:
		if settings.CountStatus429 {
			account.Failure.DebtScore += settings.DebtFail429
		}
	}

	// debt ≥ threshold → zero debt and request auto probe (not direct demotion)
	if account.Failure.DebtScore >= settings.DebtProbeThreshold {
		account.Failure.DebtScore = 0
		return false, true
	}
	// Legacy hard streak path: consecutive attributed failures still request auto probe
	// rather than jumping to dead (probe decides the tier).
	if account.Failure.ConsecutiveAttributedFailures >= settings.AttributedFailureThreshold {
		account.Failure.DebtScore = 0
		return false, true
	}
	return false, false
}

// All attributed statuses enter the consecutive-threshold path.
func countsThresholdStatus(settings Settings, status int) bool {
	if status == 401 || status == 403 {
		return true
	}
	for _, allowed := range settings.AttributedFailureStatuses {
		if status == allowed {
			return true
		}
	}
	if status == 429 && settings.CountStatus429 {
		return true
	}
	if status >= 500 && status <= 599 && settings.CountStatus5XX {
		return true
	}
	return false
}

// countsStatus is kept for diagnostics / tests: any status that can contribute to demotion.
func (service *UsageService) countsStatus(status int) bool {
	return countsThresholdStatus(service.settings(), status)
}

func (service *UsageService) settings() Settings {
	if settings := service.store.View().Settings; settings != nil {
		return NormalizeSettings(*settings)
	}
	return NormalizeSettings(service.settingsFallback)
}

func isSuccessfulOutcome(outcome string) bool {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "success", "succeeded", "ok":
		return true
	default:
		return false
	}
}

func isPotentialXAIOAuth(event domain.UsageEvent) bool {
	provider := strings.ToLower(strings.TrimSpace(event.Provider))
	authType := strings.ToLower(strings.TrimSpace(event.AuthType))
	executorType := strings.ToLower(strings.TrimSpace(event.ExecutorType))
	if provider != "" && !strings.Contains(provider, "xai") && !strings.Contains(provider, "grok") {
		return false
	}
	if authType != "" && authType != "oauth" && !strings.Contains(authType, "xai") && !strings.Contains(authType, "grok") {
		return false
	}
	if authType == "" && executorType != "" && executorType != "oauth" && !strings.Contains(executorType, "xai") && !strings.Contains(executorType, "grok") {
		return false
	}
	return true
}

func dedupe(snapshot *stateinfra.Snapshot, event domain.UsageEvent, now time.Time) (string, string, bool) {
	if strings.TrimSpace(event.EventID) != "" {
		key := strings.TrimSpace(event.EventID)
		_, exists := snapshot.EventDedupe.ExactIDs[key]
		return key, "exact", exists
	}
	payload, _ := json.Marshal(struct {
		AuthIndex  string            `json:"auth_index"`
		OccurredAt string            `json:"occurred_at"`
		RequestID  string            `json:"request_id"`
		Model      string            `json:"model"`
		Outcome    string            `json:"outcome"`
		StatusCode int               `json:"status_code"`
		Usage      domain.TokenUsage `json:"usage"`
	}{event.AuthIndex, event.OccurredAt.UTC().Truncate(10 * time.Second).Format(time.RFC3339), event.RequestID, event.Model, event.Outcome, event.StatusCode, event.Usage})
	sum := sha256.Sum256(payload)
	key := hex.EncodeToString(sum[:])
	seenAt, exists := snapshot.EventDedupe.WeakKeys[key]
	return key, "weak", exists && now.Sub(seenAt) <= weakDedupeTTL
}

func pruneDedupe(snapshot *stateinfra.Snapshot, now time.Time) {
	for key, seenAt := range snapshot.EventDedupe.WeakKeys {
		if now.Sub(seenAt) > weakDedupeTTL {
			delete(snapshot.EventDedupe.WeakKeys, key)
		}
	}
}

func trimDedupe(snapshot *stateinfra.Snapshot) {
	for len(snapshot.EventDedupe.ExactIDs) > maxDedupeKeys {
		deleteOldest(snapshot.EventDedupe.ExactIDs)
	}
	for len(snapshot.EventDedupe.WeakKeys) > maxDedupeKeys {
		deleteOldest(snapshot.EventDedupe.WeakKeys)
	}
}

func deleteOldest(values map[string]time.Time) {
	var oldestKey string
	var oldest time.Time
	for key, value := range values {
		if oldestKey == "" || value.Before(oldest) {
			oldestKey, oldest = key, value
		}
	}
	delete(values, oldestKey)
}

func ParseUsageEvent(data []byte) (domain.UsageEvent, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return domain.UsageEvent{}, fmt.Errorf("decode usage event: %w", err)
	}
	if nested, ok := raw["event"]; ok {
		return ParseUsageEvent(nested)
	}
	var event domain.UsageEvent
	decodeFirst(raw, &event.EventID, "event_id", "EventID", "id")
	decodeFirstNonEmptyString(raw, &event.AuthIndex, "auth_index", "AuthIndex", "authIndex")
	if event.AuthIndex == "" {
		decodeFirstNonEmptyString(raw, &event.AuthIndex, "auth_id", "AuthID", "authId")
	}
	decodeFirst(raw, &event.Name, "name", "Name", "auth_name", "AuthName")
	decodeFirst(raw, &event.RequestID, "request_id", "RequestID", "requestId")
	decodeFirst(raw, &event.Model, "model", "Model")
	decodeFirst(raw, &event.Outcome, "outcome", "Outcome")
	decodeFirst(raw, &event.StatusCode, "status_code", "StatusCode", "statusCode")
	decodeFirst(raw, &event.Provider, "provider", "Provider")
	decodeFirst(raw, &event.AuthType, "auth_type", "AuthType", "authType")
	decodeFirst(raw, &event.ExecutorType, "executor_type", "ExecutorType", "executorType")
	var occurred string
	decodeFirst(raw, &occurred, "occurred_at", "OccurredAt", "requested_at", "RequestedAt", "timestamp", "Timestamp", "time", "Time")
	if occurred != "" {
		event.OccurredAt, _ = time.Parse(time.RFC3339Nano, occurred)
	}
	if usageRaw, ok := firstRaw(raw, "detail", "Detail", "usage", "Usage"); ok {
		parseTokenUsage(usageRaw, &event.Usage)
	} else {
		parseTokenUsage(data, &event.Usage)
	}
	var failed bool
	failedPresent := decodeFirst(raw, &failed, "failed", "Failed")
	failureBody := ""
	if failureRaw, ok := firstRaw(raw, "failure", "Failure", "error", "Error"); ok {
		var failure map[string]json.RawMessage
		if json.Unmarshal(failureRaw, &failure) == nil {
			if event.StatusCode == 0 {
				decodeFirst(failure, &event.StatusCode, "status_code", "StatusCode", "statusCode")
			}
			decodeFirst(failure, &failureBody, "body", "Body")
		}
	}
	if event.StatusCode == 0 {
		if match := failureStatusPattern.FindStringSubmatch(failureBody); len(match) == 2 {
			event.StatusCode, _ = strconv.Atoi(match[1])
		}
	}
	if failed || event.StatusCode >= 400 || strings.TrimSpace(failureBody) != "" {
		event.Outcome = "failure"
	} else if failedPresent {
		event.Outcome = "success"
	}
	return event, nil
}

func parseTokenUsage(data []byte, usage *domain.TokenUsage) {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return
	}
	usage.Input = decodeIntPointer(raw, "input_tokens", "InputTokens", "inputTokens", "prompt_tokens", "PromptTokens")
	usage.Output = decodeIntPointer(raw, "output_tokens", "OutputTokens", "outputTokens", "completion_tokens", "CompletionTokens")
	usage.Total = decodeIntPointer(raw, "total_tokens", "TotalTokens", "totalTokens")
}

func decodeIntPointer(raw map[string]json.RawMessage, keys ...string) *int64 {
	var value int64
	if decodeFirst(raw, &value, keys...) {
		return &value
	}
	return nil
}

func decodeFirst(raw map[string]json.RawMessage, target any, keys ...string) bool {
	value, ok := firstRaw(raw, keys...)
	if !ok || json.Unmarshal(value, target) != nil {
		return false
	}
	return true
}

func decodeFirstNonEmptyString(raw map[string]json.RawMessage, target *string, keys ...string) bool {
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		var decoded string
		if json.Unmarshal(value, &decoded) == nil && strings.TrimSpace(decoded) != "" {
			*target = decoded
			return true
		}
	}
	return false
}

func firstRaw(raw map[string]json.RawMessage, keys ...string) (json.RawMessage, bool) {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			return value, true
		}
	}
	return nil, false
}
