package application

import (
	"os"
	"strconv"
	"strings"
	"time"

	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

type Settings struct {
	Revision                   int               `json:"revision"`
	OperationConcurrency       int               `json:"operation_concurrency"`
	AttributedFailureThreshold int               `json:"attributed_failure_threshold"`
	AttributedFailureStatuses  []int             `json:"attributed_failure_statuses"`
	CountStatus429             bool              `json:"count_status_429"`
	CountStatus5XX             bool              `json:"count_status_5xx"`
	DemotionPriority           int               `json:"demotion_priority"`
	DefaultRestorePriority     int               `json:"default_restore_priority"`
	ProtectionLevel            string            `json:"protection_level"`
	DefaultTokenCapacity       uint64            `json:"default_token_capacity"`
	PerAccountTokenCapacity    map[string]uint64 `json:"per_account_token_capacity"`
	HealthStaleAfterSeconds    int               `json:"health_stale_after_seconds"`
	OperationTimeoutSeconds    int               `json:"operation_timeout_seconds"`
	WriteMode                  string            `json:"write_mode"`
}

func DefaultSettings() Settings {
	return Settings{Revision: 1, OperationConcurrency: 1, AttributedFailureThreshold: 3,
		AttributedFailureStatuses: []int{401, 403}, DemotionPriority: -100, ProtectionLevel: "strict",
		DefaultRestorePriority: 0,
		DefaultTokenCapacity:   1_000_000, PerAccountTokenCapacity: map[string]uint64{},
		HealthStaleAfterSeconds: 86400, OperationTimeoutSeconds: 60, WriteMode: "managed"}
}

func LoadSettings() Settings {
	settings := DefaultSettings()
	settings.AttributedFailureThreshold = envInt("CPA_GROK_FAILURE_THRESHOLD", settings.AttributedFailureThreshold, 1, 100)
	settings.DemotionPriority = envInt("CPA_GROK_DEMOTION_PRIORITY", settings.DemotionPriority, -1_000_000, 1_000_000)
	settings.DefaultRestorePriority = envInt("CPA_GROK_DEFAULT_RESTORE_PRIORITY", settings.DefaultRestorePriority, -1_000_000, 1_000_000)
	settings.CountStatus429 = envBool("CPA_GROK_COUNT_429", false)
	settings.CountStatus5XX = envBool("CPA_GROK_COUNT_5XX", false)
	return settings
}

func envInt(name string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value < minimum || value > maximum {
		return fallback
	}
	return value
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

type Meta struct {
	PluginID            string        `json:"plugin_id"`
	PluginVersion       string        `json:"plugin_version"`
	APIVersion          int           `json:"api_version"`
	WriteMode           string        `json:"write_mode"`
	Status              string        `json:"status"`
	StateStatus         string        `json:"state_status"`
	StatisticsStartedAt time.Time     `json:"statistics_started_at"`
	DedupeMode          string        `json:"dedupe_mode"`
	ConditionalWrite    bool          `json:"conditional_write"`
	Capabilities        []string      `json:"capabilities"`
	UnavailableFeatures []Unavailable `json:"unavailable_features"`
}

type Unavailable struct {
	Feature string `json:"feature"`
	Reason  string `json:"reason"`
}

func BuildMeta(snapshot stateinfra.Snapshot) Meta {
	dedupeMode := "exact"
	if snapshot.EventDedupe.WeakModeUsed || len(snapshot.EventDedupe.ExactIDs) == 0 {
		dedupeMode = "weak"
	}
	return Meta{PluginID: stateinfra.PluginID, PluginVersion: stateinfra.PluginVersion, APIVersion: 1,
		WriteMode: "managed", Status: "ready", StateStatus: "healthy",
		StatisticsStartedAt: snapshot.StatisticsStartedAt, DedupeMode: dedupeMode, ConditionalWrite: false,
		Capabilities:        []string{"usage", "auth_list", "auth_get", "auth_save", "management_routes", "set_enabled", "demote", "restore_priority", "auto_demotion"},
		UnavailableFeatures: []Unavailable{{Feature: "checks", Reason: "host.auth.invoke 未提供"}, {Feature: "cleanup", Reason: "M3 功能未启用"}}}
}
