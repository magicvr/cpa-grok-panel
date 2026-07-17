package application

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/domain"
)

type BillingQuota struct {
	Plan  string
	Used  float64
	Limit float64
	Unit  string
}

// ClassifyPlan maps a successful billing observation to SuperGrok / SuperGrok Heavy / Free.
// Failure paths should use "unknown" and never call this.
func ClassifyPlan(raw string) string {
	plan := strings.ToLower(strings.TrimSpace(raw))
	if strings.Contains(plan, "heavy") {
		return "SuperGrok Heavy"
	}
	if strings.Contains(plan, "supergrok") || strings.Contains(plan, "super_grok") || strings.Contains(plan, "super grok") {
		return "SuperGrok"
	}
	// Successful observation that is not SuperGrok/Heavy is Free.
	return "Free"
}

// ParseBillingQuota parses CPA api-call envelopes (or raw billing JSON).
// On HTTP 200 success, always classifies plan even when limit is 0 (Free).
func ParseBillingQuota(primary, credits []byte) (BillingQuota, error) {
	first, err := parseBillingDocument(primary)
	if err != nil {
		return BillingQuota{}, err
	}
	result := extractBilling(first)
	if len(credits) > 0 {
		if second, secondErr := parseBillingDocument(credits); secondErr == nil {
			result = mergeBilling(result, extractBilling(second))
		}
	}
	result.Plan = ClassifyPlan(result.Plan)
	if result.Unit == "" {
		if result.Limit > 0 {
			result.Unit = "billing units"
		} else {
			result.Unit = "tokens"
		}
	}
	if math.IsNaN(result.Used) || math.IsInf(result.Used, 0) {
		result.Used = 0
	}
	if math.IsNaN(result.Limit) || math.IsInf(result.Limit, 0) || result.Limit < 0 {
		result.Limit = 0
	}
	return result, nil
}

func SnapshotFromBilling(q BillingQuota, fetchedAt time.Time) domain.QuotaSnapshot {
	source := "billing"
	if q.Plan == "Free" && q.Limit <= 0 {
		source = "local_estimate"
	}
	return domain.QuotaSnapshot{
		Plan:      q.Plan,
		Used:      q.Used,
		Limit:     q.Limit,
		Unit:      q.Unit,
		Source:    source,
		FetchedAt: fetchedAt.UTC(),
		Error:     "",
	}
}

func SnapshotUnknown(err error, fetchedAt time.Time) domain.QuotaSnapshot {
	msg := "refresh failed"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		msg = err.Error()
	}
	return domain.QuotaSnapshot{
		Plan:      "unknown",
		Source:    "refresh_failed",
		FetchedAt: fetchedAt.UTC(),
		Error:     msg,
	}
}

// DisplayQuota overlays Free daily token estimate for UI only.
// Cached plan is NEVER overwritten here.
func DisplayQuota(state domain.AccountState, freeDailyLimit uint64) domain.QuotaSnapshot {
	quota := state.Quota
	if strings.TrimSpace(quota.Plan) == "" {
		quota.Plan = "unknown"
	}
	plan := quota.Plan
	// Paid plans with a usable billing limit: keep snapshot as stored.
	if (plan == "SuperGrok" || plan == "SuperGrok Heavy") && quota.Source == "billing" && quota.Limit > 0 {
		return quota
	}
	// Free / unknown / empty billing limit: usage bar from plugin daily tokens.
	if freeDailyLimit == 0 {
		freeDailyLimit = 2_000_000
	}
	quota.Used = float64(state.Usage.TotalTokens)
	quota.Limit = float64(freeDailyLimit)
	quota.Unit = "tokens"
	if plan == "Free" || plan == "unknown" {
		if quota.Source == "" || quota.Source == "billing" {
			// Keep source if already local_estimate/refresh_failed; else mark estimate for Free usage display only.
			if plan == "Free" {
				quota.Source = "local_estimate"
			}
		}
	}
	// Restore plan (must not flip Free/unknown/paid from this overlay).
	quota.Plan = plan
	return quota
}

func parseBillingDocument(raw []byte) (map[string]any, error) {
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("billing response is not JSON: %w", err)
	}
	if status, ok := numberAt(envelope, "status_code"); ok && int(status) != 200 {
		return nil, fmt.Errorf("billing HTTP status %d", int(status))
	}
	for _, key := range []string{"body", "bodyText"} {
		if value, ok := envelope[key]; ok {
			if text, ok := value.(string); ok {
				var document map[string]any
				if err := json.Unmarshal([]byte(text), &document); err != nil {
					return nil, fmt.Errorf("billing body is not JSON: %w", err)
				}
				return document, nil
			}
			if document, ok := value.(map[string]any); ok {
				return document, nil
			}
		}
	}
	return envelope, nil
}

func extractBilling(document map[string]any) BillingQuota {
	result := BillingQuota{}
	walkBilling(document, &result)
	return result
}

func walkBilling(value any, result *BillingQuota) {
	switch typed := value.(type) {
	case map[string]any:
		// Prefer nested val objects from xAI: {"monthlyLimit":{"val":N}}
		for key, child := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
			if text, ok := child.(string); ok && result.Plan == "" && isPlanKey(normalized) {
				result.Plan = text
			}
			if number, ok := numeric(child); ok {
				switch {
				case strings.Contains(normalized, "monthlylimit") || normalized == "limit" || strings.Contains(normalized, "creditlimit"):
					if result.Limit == 0 || strings.Contains(normalized, "monthly") {
						result.Limit = number
					}
				case normalized == "used" || strings.Contains(normalized, "monthlyused") || strings.Contains(normalized, "includedused") || strings.Contains(normalized, "totalused"):
					if number > result.Used {
						result.Used = number
					}
				}
			}
			if m, ok := child.(map[string]any); ok {
				if number, ok := numeric(m["val"]); ok {
					switch {
					case strings.Contains(normalized, "monthlylimit") || normalized == "limit":
						if result.Limit == 0 || strings.Contains(normalized, "monthly") {
							result.Limit = number
						}
					case normalized == "used" || strings.Contains(normalized, "used"):
						if !strings.Contains(normalized, "ondemand") && number > result.Used {
							result.Used = number
						}
					}
				}
			}
			if isUnitKey(normalized) && result.Unit == "" {
				if text, ok := child.(string); ok {
					result.Unit = text
				}
			}
			walkBilling(child, result)
		}
	case []any:
		for _, child := range typed {
			walkBilling(child, result)
		}
	}
}

func mergeBilling(left, right BillingQuota) BillingQuota {
	if left.Plan == "" {
		left.Plan = right.Plan
	}
	if left.Limit == 0 {
		left.Limit = right.Limit
	}
	if right.Used > left.Used {
		left.Used = right.Used
	}
	if left.Unit == "" {
		left.Unit = right.Unit
	}
	return left
}

func isPlanKey(key string) bool {
	return strings.Contains(key, "plan") || key == "tier" || strings.Contains(key, "subscription")
}
func isUnitKey(key string) bool { return key == "unit" || strings.Contains(key, "currency") }
func numberAt(document map[string]any, key string) (float64, bool) {
	value, ok := document[key]
	if !ok {
		return 0, false
	}
	return numeric(value)
}
func numeric(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		n, err := typed.Float64()
		return n, err == nil
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return n, err == nil
	default:
		return 0, false
	}
}
