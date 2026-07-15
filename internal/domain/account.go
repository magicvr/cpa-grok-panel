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
}

type AccountState struct {
	ExactFileName string        `json:"exact_file_name,omitempty"`
	Usage         UsageCounters `json:"usage"`
	Failure       FailureState  `json:"failure"`
	Demotion      DemotionState `json:"demotion"`
	FirstSeenAt   time.Time     `json:"first_seen_at,omitempty"`
	LastSeenAt    time.Time     `json:"last_seen_at,omitempty"`
}

type FailureState struct {
	ConsecutiveAttributedFailures int        `json:"consecutive_attributed_failures"`
	LastFailureAt                 *time.Time `json:"last_failure_at,omitempty"`
	LastFailureCode               string     `json:"last_failure_code,omitempty"`
}

type DemotionState struct {
	State            string     `json:"state"`
	BaselinePriority *int       `json:"baseline_priority,omitempty"`
	TargetPriority   *int       `json:"target_priority,omitempty"`
	TriggeredAt      *time.Time `json:"triggered_at,omitempty"`
	FailureCode      string     `json:"failure_code,omitempty"`
}

func (state DemotionState) Normalized() DemotionState {
	if state.State == "" {
		state.State = "none"
	}
	return state
}

type AccountView struct {
	AuthIndex     string        `json:"auth_index"`
	ExactFileName string        `json:"exact_file_name"`
	Email         string        `json:"email,omitempty"`
	Enabled       bool          `json:"enabled"`
	Unavailable   bool          `json:"unavailable"`
	Status        string        `json:"status,omitempty"`
	StatusMessage string        `json:"status_message,omitempty"`
	Priority      int           `json:"priority"`
	Provider      string        `json:"provider"`
	AuthType      string        `json:"auth_type"`
	Usage         UsageCounters `json:"usage"`
	Failure       FailureState  `json:"failure"`
	Demotion      DemotionState `json:"demotion"`
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
	// oauth preferred; empty treated as acceptable for file-backed xai credentials
	if accountType == "" && authType == "" {
		return true
	}
	return accountType == "oauth" || authType == "oauth"
}

func ProjectAccount(file AuthFile, state AccountState, now time.Time, demotionPriority int) AccountView {
	usage := state.Usage
	if usage.DedupeMode == "" {
		usage.DedupeMode = "weak"
	}
	if usage.PeriodStartedAt.IsZero() {
		usage.PeriodStartedAt = now.UTC()
	}
	demotion := state.Demotion.Normalized()
	isDemoted := file.Priority <= demotionPriority
	return AccountView{
		AuthIndex: file.AuthIndex, ExactFileName: file.Name, Email: file.Email,
		Enabled: !file.Disabled, Unavailable: file.Unavailable, Status: file.Status,
		StatusMessage: file.StatusMessage, Priority: file.Priority, Provider: "xai",
		AuthType: "oauth", Usage: usage, Failure: state.Failure, Demotion: demotion,
		IsDemoted: isDemoted, CanRestore: isDemoted,
		LastSeenAt: now.UTC(), WriteMode: "managed",
	}
}
