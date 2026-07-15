package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/domain"
	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

const (
	weakDedupeTTL = 2 * time.Minute
	maxDedupeKeys = 4096
)

type UsageResult struct {
	Accepted   bool   `json:"accepted"`
	Duplicate  bool   `json:"duplicate"`
	DedupeMode string `json:"dedupe_mode"`
}

type UsageService struct {
	store *stateinfra.Store
	now   func() time.Time
}

func NewUsageService(store *stateinfra.Store, now func() time.Time) *UsageService {
	return &UsageService{store: store, now: now}
}

func (service *UsageService) Handle(event domain.UsageEvent) (UsageResult, error) {
	if event.OccurredAt.IsZero() {
		event.OccurredAt = service.now().UTC()
	}
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
	return result, err
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
	decodeFirst(raw, &event.AuthIndex, "auth_index", "AuthIndex", "authIndex", "auth_id", "AuthID")
	decodeFirst(raw, &event.Name, "name", "Name", "auth_name", "AuthName")
	decodeFirst(raw, &event.RequestID, "request_id", "RequestID", "requestId")
	decodeFirst(raw, &event.Model, "model", "Model")
	decodeFirst(raw, &event.Outcome, "outcome", "Outcome", "status", "Status")
	decodeFirst(raw, &event.StatusCode, "status_code", "StatusCode", "statusCode")
	decodeFirst(raw, &event.Provider, "provider", "Provider")
	decodeFirst(raw, &event.AuthType, "auth_type", "AuthType", "authType", "executor_type", "ExecutorType")
	var occurred string
	decodeFirst(raw, &occurred, "occurred_at", "OccurredAt", "timestamp", "Timestamp", "time", "Time")
	if occurred != "" {
		event.OccurredAt, _ = time.Parse(time.RFC3339Nano, occurred)
	}
	if usageRaw, ok := firstRaw(raw, "usage", "Usage", "detail", "Detail"); ok {
		parseTokenUsage(usageRaw, &event.Usage)
	} else {
		parseTokenUsage(data, &event.Usage)
	}
	var failed bool
	if decodeFirst(raw, &failed, "failed", "Failed") && event.Outcome == "" {
		if failed {
			event.Outcome = "failure"
		} else {
			event.Outcome = "success"
		}
	}
	if event.Outcome == "" {
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

func firstRaw(raw map[string]json.RawMessage, keys ...string) (json.RawMessage, bool) {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			return value, true
		}
	}
	return nil, false
}
