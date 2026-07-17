package application

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/config"
	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

type Settings = config.Settings

func DefaultSettings() Settings {
	return Settings{Revision: 1, AutoRefreshEnabled: true, AutoRefreshIntervalSeconds: 5,
		DailyUsageResetEnabled: false, DailyUsageResetTime: "00:00",
		OperationConcurrency: 1, BatchOperationConcurrency: 10, AttributedFailureThreshold: 3,
		// 401/403 always count toward the shared consecutive-failure threshold.
		AttributedFailureStatuses: []int{401, 403}, DemotionPriority: -100, ProtectionLevel: "strict",
		DefaultRestorePriority: 0, CooldownRestoreEnabled: true,
		// 429/5xx participate in the same threshold path when enabled.
		// Set CPA_GROK_COUNT_429 / CPA_GROK_COUNT_5XX=true to also demote after N consecutive such failures.
		CountStatus429: false, CountStatus5XX: false,
		DefaultTokenCapacity: 1_000_000, PerAccountTokenCapacity: map[string]uint64{},
		HealthStaleAfterSeconds: 86400, OperationTimeoutSeconds: 60, WriteMode: "managed"}
}

var dailyUsageResetTimePattern = regexp.MustCompile(`^\d{2}:\d{2}$`)

func ValidateDailyUsageResetTime(value string) error {
	if !dailyUsageResetTimePattern.MatchString(value) {
		return fmt.Errorf("daily_usage_reset_time 必须使用 HH:mm 格式")
	}
	if _, err := time.Parse("15:04", value); err != nil {
		return fmt.Errorf("daily_usage_reset_time 必须是有效的 24 小时时间")
	}
	return nil
}

func LoadSettings() Settings {
	settings := DefaultSettings()
	settings.BatchOperationConcurrency = envInt("CPA_GROK_BATCH_CONCURRENCY", settings.BatchOperationConcurrency, 1, 50)
	settings.AttributedFailureThreshold = envInt("CPA_GROK_FAILURE_THRESHOLD", settings.AttributedFailureThreshold, 1, 100)
	settings.DemotionPriority = envInt("CPA_GROK_DEMOTION_PRIORITY", settings.DemotionPriority, -1_000_000, 1_000_000)
	settings.DefaultRestorePriority = envInt("CPA_GROK_DEFAULT_RESTORE_PRIORITY", settings.DefaultRestorePriority, -1_000_000, 1_000_000)
	settings.CooldownRestoreEnabled = envBool("CPA_GROK_COOLDOWN_RESTORE", settings.CooldownRestoreEnabled)
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
		Capabilities:        []string{"usage", "auth_list", "auth_get", "auth_save", "management_routes", "set_enabled", "demote", "restore_priority", "auto_demotion", "cooldown_restore", "safe_delete", "daily_usage_reset"},
		UnavailableFeatures: []Unavailable{{Feature: "checks", Reason: "host.auth.invoke 未提供"}}}
}
