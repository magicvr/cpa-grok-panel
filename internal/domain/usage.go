package domain

import (
	"errors"
	"math"
	"strings"
	"time"
)

var (
	ErrMissingAuthIndex = errors.New("usage event is missing auth_index")
	ErrNegativeTokens   = errors.New("usage token values must be non-negative")
	ErrCounterOverflow  = errors.New("usage counter overflow")
)

type TokenUsage struct {
	Input  *int64 `json:"input_tokens,omitempty"`
	Output *int64 `json:"output_tokens,omitempty"`
	Total  *int64 `json:"total_tokens,omitempty"`
}

type UsageEvent struct {
	EventID    string     `json:"event_id,omitempty"`
	OccurredAt time.Time  `json:"occurred_at"`
	AuthIndex  string     `json:"auth_index"`
	Name       string     `json:"name,omitempty"`
	RequestID  string     `json:"request_id,omitempty"`
	Model      string     `json:"model,omitempty"`
	Outcome    string     `json:"outcome"`
	StatusCode int        `json:"status_code,omitempty"`
	Provider   string     `json:"provider,omitempty"`
	AuthType   string     `json:"auth_type,omitempty"`
	Usage      TokenUsage `json:"usage"`
}

type UsageCounters struct {
	InputTokens                 uint64     `json:"input_tokens"`
	OutputTokens                uint64     `json:"output_tokens"`
	TotalTokens                 uint64     `json:"total_tokens"`
	SuccessfulRequests          uint64     `json:"successful_requests"`
	FailedRequests              uint64     `json:"failed_requests"`
	CancelledRequests           uint64     `json:"cancelled_requests"`
	EventsWithMissingUsage      uint64     `json:"missing_usage_events"`
	EventsWithInconsistentUsage uint64     `json:"inconsistent_usage_events"`
	PeriodStartedAt             time.Time  `json:"period_started_at"`
	LastUsageAt                 *time.Time `json:"last_usage_at,omitempty"`
	LastEventID                 string     `json:"last_event_id,omitempty"`
	DedupeMode                  string     `json:"dedupe_mode"`
}

func ApplyUsage(counters *UsageCounters, event UsageEvent) error {
	if strings.TrimSpace(event.AuthIndex) == "" {
		return ErrMissingAuthIndex
	}
	if err := validateTokens(event.Usage); err != nil {
		return err
	}
	if counters.PeriodStartedAt.IsZero() {
		counters.PeriodStartedAt = event.OccurredAt
	}
	if counters.PeriodStartedAt.IsZero() {
		counters.PeriodStartedAt = time.Now().UTC()
	}

	for value, target := range map[*int64]*uint64{
		event.Usage.Input:  &counters.InputTokens,
		event.Usage.Output: &counters.OutputTokens,
		event.Usage.Total:  &counters.TotalTokens,
	} {
		if value != nil {
			if err := addCounter(target, uint64(*value)); err != nil {
				return err
			}
		}
	}

	if event.Usage.Input == nil || event.Usage.Output == nil || event.Usage.Total == nil {
		if err := addCounter(&counters.EventsWithMissingUsage, 1); err != nil {
			return err
		}
	} else if *event.Usage.Total != *event.Usage.Input+*event.Usage.Output {
		if err := addCounter(&counters.EventsWithInconsistentUsage, 1); err != nil {
			return err
		}
	}

	switch normalizeOutcome(event.Outcome) {
	case "success":
		if err := addCounter(&counters.SuccessfulRequests, 1); err != nil {
			return err
		}
	case "cancelled":
		if err := addCounter(&counters.CancelledRequests, 1); err != nil {
			return err
		}
	default:
		if err := addCounter(&counters.FailedRequests, 1); err != nil {
			return err
		}
	}

	occurredAt := event.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	counters.LastUsageAt = &occurredAt
	counters.LastEventID = event.EventID
	if event.EventID != "" {
		counters.DedupeMode = "exact"
	} else {
		counters.DedupeMode = "weak"
	}
	return nil
}

func validateTokens(usage TokenUsage) error {
	for _, value := range []*int64{usage.Input, usage.Output, usage.Total} {
		if value != nil && *value < 0 {
			return ErrNegativeTokens
		}
	}
	return nil
}

func addCounter(target *uint64, value uint64) error {
	if math.MaxUint64-*target < value {
		return ErrCounterOverflow
	}
	*target += value
	return nil
}

func normalizeOutcome(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "success", "succeeded", "ok":
		return "success"
	case "cancelled", "canceled":
		return "cancelled"
	default:
		return "failure"
	}
}
