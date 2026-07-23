package domain

import (
	"strings"
	"time"
)

type AuthFile struct {
	AuthIndex     string `json:"auth_index"`
	Name          string `json:"name"`
	Provider      string `json:"provider,omitempty"`
	Type          string `json:"type,omitempty"`
	AccountType   string `json:"account_type,omitempty"`
	AuthType      string `json:"auth_type,omitempty"`
	Email         string `json:"email,omitempty"`
	Priority      int    `json:"priority"`
	Disabled      bool   `json:"disabled"`
	Unavailable   bool   `json:"unavailable,omitempty"`
	Status        string `json:"status,omitempty"`
	StatusMessage string `json:"status_message,omitempty"`
	// Host lifetime request counters from CPA host.auth.list (not plugin usage ledger).
	Success int64 `json:"success,omitempty"`
	Failed  int64 `json:"failed,omitempty"`
}

// HostRequestBaseline anchors host lifetime counters to the current usage period so
// panel request counts can compensate for usage.handle under-report without breaking
// daily reset (display uses host delta since period start, not raw host totals).
type HostRequestBaseline struct {
	Success              int64     `json:"success"`
	Failed               int64     `json:"failed"`
	BoundPeriodStartedAt time.Time `json:"bound_period_started_at"`
}

// Probe status values (persisted lowercase). UI: 正常/无效/死号/限流/未知/异常.
const (
	ProbeStatusLive      = "live"
	ProbeStatusInvalid   = "invalid"
	ProbeStatusDead      = "dead"
	ProbeStatusThrottled = "throttled"
	ProbeStatusError     = "error"
	ProbeStatusUnknown   = "unknown"
	// Legacy aliases kept for NormalizeProbeStatus input only.
	ProbeStatusExceed  = "exceed"  // → invalid
	ProbeStatusCooling = "cooling" // → throttled
)

type QuotaSnapshot struct {
	Plan      string    `json:"plan,omitempty"`
	Used      float64   `json:"used,omitempty"`
	Limit     float64   `json:"limit,omitempty"`
	Unit      string    `json:"unit,omitempty"`
	Source    string    `json:"source,omitempty"`
	FetchedAt time.Time `json:"fetched_at,omitempty"`
	Error     string    `json:"error,omitempty"`
	// 测活结果（存活列）；与套餐字段独立，批量刷新套餐不得清掉。
	// probe_status: live | invalid | dead | throttled | error | unknown（空=未知/未测）
	ProbeStatus string    `json:"probe_status,omitempty"`
	ProbeHTTP   int       `json:"probe_http,omitempty"`
	ProbeAt     time.Time `json:"probe_at,omitempty"`
	ProbeError  string    `json:"probe_error,omitempty"`
}

type AccountState struct {
	ExactFileName       string               `json:"exact_file_name,omitempty"`
	Usage               UsageCounters        `json:"usage"`
	Quota               QuotaSnapshot        `json:"quota"`
	Failure             FailureState         `json:"failure"`
	Demotion            DemotionState        `json:"demotion"` // legacy JSON; ignored by v0.7.0 policy
	HostRequestBaseline *HostRequestBaseline `json:"host_request_baseline,omitempty"`
	FirstSeenAt         time.Time            `json:"first_seen_at,omitempty"`
	LastSeenAt          time.Time            `json:"last_seen_at,omitempty"`
}

type FailureState struct {
	ConsecutiveAttributedFailures int        `json:"consecutive_attributed_failures"`
	DebtScore                     float64    `json:"debt_score"`
	LastEvidenceAt                *time.Time `json:"last_evidence_at,omitempty"`
	LastFailureAt                 *time.Time `json:"last_failure_at,omitempty"`
	LastFailureCode               string     `json:"last_failure_code,omitempty"`
}

// DemotionState is retained for JSON compatibility with pre-v0.7.0 state files.
// v0.7.0 policy uses probe_status + priority only.
type DemotionState struct {
	State                string     `json:"state"`
	Class                string     `json:"class"`
	BaselinePriority     *int       `json:"baseline_priority,omitempty"`
	TargetPriority       *int       `json:"target_priority,omitempty"`
	TriggeredAt          *time.Time `json:"triggered_at,omitempty"`
	NextProbeAt          *time.Time `json:"next_probe_at,omitempty"`
	RestoreCooldownHours int        `json:"restore_cooldown_hours,omitempty"`
	HalfOpenSince        *time.Time `json:"half_open_since,omitempty"`
	HalfOpenSuccesses    int        `json:"half_open_successes,omitempty"`
	FailureCode          string     `json:"failure_code,omitempty"`
}

// Legacy demotion class constants (no longer used by policy).
const (
	DemotionClassNone     = "none"
	DemotionClassWatch    = "watch"
	DemotionClassAnomaly  = "anomaly"
	DemotionClassDead     = "dead"
	DemotionClassSoft     = "soft"
	DemotionClassHard     = "hard"
	DemotionClassHalfOpen = "half_open"
)

const (
	ProbeSourceManual = "manual"
	ProbeSourceAuto   = "auto"
)

func (state DemotionState) Normalized() DemotionState {
	if state.State == "" {
		state.State = "none"
	}
	switch state.Class {
	case DemotionClassSoft, DemotionClassHalfOpen:
		state.Class = DemotionClassWatch
	case DemotionClassHard:
		state.Class = DemotionClassDead
	}
	if state.Class == "" {
		switch state.State {
		case "requested", "applied", "failed":
			state.Class = DemotionClassDead
		default:
			state.Class = DemotionClassNone
		}
	}
	return state
}

func IsActiveDemotionClass(class string) bool {
	switch class {
	case DemotionClassWatch, DemotionClassAnomaly, DemotionClassDead,
		DemotionClassSoft, DemotionClassHard, DemotionClassHalfOpen:
		return true
	default:
		return false
	}
}

// NormalizeProbeStatus maps HTTP status / legacy labels to canonical probe_status.
func NormalizeProbeStatus(status string, httpStatus int) string {
	s := strings.ToLower(strings.TrimSpace(status))
	switch s {
	case ProbeStatusLive, ProbeStatusInvalid, ProbeStatusDead, ProbeStatusThrottled, ProbeStatusError, ProbeStatusUnknown:
		return s
	case ProbeStatusExceed, "failure":
		return ProbeStatusInvalid
	case ProbeStatusCooling:
		return ProbeStatusThrottled
	case "unusual":
		if httpStatus == 429 {
			return ProbeStatusThrottled
		}
		return ProbeStatusError
	case "":
		if httpStatus > 0 {
			return ClassifyProbeHTTP(httpStatus)
		}
		return ""
	}
	if httpStatus > 0 {
		return ClassifyProbeHTTP(httpStatus)
	}
	return s
}

// ClassifyProbeHTTP maps HTTP codes to probe_status (v0.7.0).
func ClassifyProbeHTTP(httpStatus int) string {
	switch {
	case httpStatus >= 200 && httpStatus < 300:
		return ProbeStatusLive
	case httpStatus == 401:
		return ProbeStatusInvalid
	case httpStatus == 403:
		return ProbeStatusDead
	case httpStatus == 429:
		return ProbeStatusThrottled
	case httpStatus == 0:
		return ProbeStatusError
	default:
		return ProbeStatusError
	}
}

// CanonicalProbeStatus returns normalized status for comparison; empty and "unknown" are equivalent.
func CanonicalProbeStatus(status string, httpStatus int) string {
	s := NormalizeProbeStatus(status, httpStatus)
	if s == "" || s == ProbeStatusUnknown {
		return ProbeStatusUnknown
	}
	return s
}

// IsUnknownProbe reports empty / unknown (never probed or cleared after resign).
func IsUnknownProbe(status string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	return s == "" || s == ProbeStatusUnknown
}

// IsLiveProbe reports healthy live status.
func IsLiveProbe(status string) bool {
	return strings.ToLower(strings.TrimSpace(status)) == ProbeStatusLive
}

// IsUnhealthyProbe is true when status is neither live nor unknown (convenient is_demoted).
func IsUnhealthyProbe(status string) bool {
	s := CanonicalProbeStatus(status, 0)
	return s != ProbeStatusLive && s != ProbeStatusUnknown
}

type AccountView struct {
	AuthIndex     string        `json:"auth_index"`
	ExactFileName string        `json:"exact_file_name"`
	Email         string        `json:"email,omitempty"`
	BotFlagged    bool          `json:"bot_flagged"`
	BotFlagKnown  bool          `json:"bot_flag_known,omitempty"`
	BotFlagSource any           `json:"bot_flag_source,omitempty"`
	Enabled       bool          `json:"enabled"`
	Unavailable   bool          `json:"unavailable"`
	Status        string        `json:"status,omitempty"`
	StatusMessage string        `json:"status_message,omitempty"`
	Priority      int           `json:"priority"`
	Provider      string        `json:"provider"`
	AuthType      string        `json:"auth_type"`
	Usage         UsageCounters `json:"usage"`
	Quota         QuotaSnapshot `json:"quota"`
	Failure       FailureState  `json:"failure"`
	Demotion      DemotionState `json:"demotion"`
	DebtScore     float64       `json:"debt_score"`
	Class         string        `json:"class"`
	IsDemoted     bool          `json:"is_demoted"`
	CanRestore    bool          `json:"can_restore"`
	LastSeenAt    time.Time     `json:"last_seen_at"`
	WriteMode     string        `json:"write_mode"`
}

func IsXAIOAuth(file AuthFile) bool {
	provider := strings.ToLower(strings.TrimSpace(file.Provider))
	typeName := strings.ToLower(strings.TrimSpace(file.Type))
	accountType := strings.ToLower(strings.TrimSpace(file.AccountType))
	authType := strings.ToLower(strings.TrimSpace(file.AuthType))
	isXAI := strings.Contains(provider, "xai") || strings.Contains(typeName, "xai") || strings.Contains(provider, "grok") || strings.Contains(typeName, "grok")
	if !isXAI {
		return false
	}
	if accountType == "" && authType == "" {
		return true
	}
	return accountType == "oauth" || authType == "oauth"
}

// ProjectAccount builds the list-row view. is_demoted = probe not live and not unknown.
func ProjectAccount(file AuthFile, state AccountState, now time.Time, _ int) AccountView {
	usage := state.Usage
	if usage.DedupeMode == "" {
		usage.DedupeMode = "weak"
	}
	if usage.PeriodStartedAt.IsZero() {
		usage.PeriodStartedAt = now.UTC()
	}
	usage = ApplyHostRequestDisplay(usage, file.Success, file.Failed, state.HostRequestBaseline)
	demotion := state.Demotion.Normalized()
	quota := state.Quota
	if strings.TrimSpace(quota.Plan) == "" {
		quota.Plan = "unknown"
	}
	if quota.ProbeStatus != "" {
		quota.ProbeStatus = NormalizeProbeStatus(quota.ProbeStatus, quota.ProbeHTTP)
	}
	isDemoted := IsUnhealthyProbe(quota.ProbeStatus)
	return AccountView{
		AuthIndex: file.AuthIndex, ExactFileName: file.Name, Email: file.Email,
		Enabled: !file.Disabled, Unavailable: file.Unavailable, Status: file.Status,
		StatusMessage: file.StatusMessage, Priority: file.Priority, Provider: "xai",
		AuthType: "oauth", Usage: usage, Quota: quota, Failure: state.Failure, Demotion: demotion,
		DebtScore: state.Failure.DebtScore, Class: demotion.Class,
		IsDemoted: isDemoted, CanRestore: false,
		LastSeenAt: now.UTC(), WriteMode: "managed",
	}
}

// NeedsHostRequestBaselineBind reports whether ListAccounts should (re)bind host counters
// for this account before projecting the view.
func NeedsHostRequestBaselineBind(state AccountState) bool {
	period := state.Usage.PeriodStartedAt
	if state.HostRequestBaseline == nil {
		return true
	}
	return !state.HostRequestBaseline.BoundPeriodStartedAt.Equal(period)
}

// BindHostRequestBaseline chooses the host snapshot for the current usage period.
func BindHostRequestBaseline(state AccountState, hostSuccess, hostFailed int64, periodStartedAt time.Time) HostRequestBaseline {
	if periodStartedAt.IsZero() {
		periodStartedAt = time.Now().UTC()
	}
	if state.HostRequestBaseline == nil {
		if state.Usage.SuccessfulRequests+state.Usage.FailedRequests > 0 {
			return HostRequestBaseline{Success: 0, Failed: 0, BoundPeriodStartedAt: periodStartedAt}
		}
		return HostRequestBaseline{Success: hostSuccess, Failed: hostFailed, BoundPeriodStartedAt: periodStartedAt}
	}
	return HostRequestBaseline{Success: hostSuccess, Failed: hostFailed, BoundPeriodStartedAt: periodStartedAt}
}

// ApplyHostRequestDisplay overlays host period-delta onto request counters for AccountView only.
func ApplyHostRequestDisplay(usage UsageCounters, hostSuccess, hostFailed int64, baseline *HostRequestBaseline) UsageCounters {
	if baseline == nil {
		return usage
	}
	hostDeltaSuccess := maxInt64(0, hostSuccess-baseline.Success)
	hostDeltaFailed := maxInt64(0, hostFailed-baseline.Failed)
	if hostDelta := nonNegInt64(hostDeltaSuccess); hostDelta > usage.SuccessfulRequests {
		usage.SuccessfulRequests = hostDelta
	}
	if hostDelta := nonNegInt64(hostDeltaFailed); hostDelta > usage.FailedRequests {
		usage.FailedRequests = hostDelta
	}
	return usage
}

func nonNegInt64(value int64) uint64 {
	if value <= 0 {
		return 0
	}
	return uint64(value)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
