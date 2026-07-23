package application

import (
	"fmt"
	"net/url"
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
	return Settings{
		Revision: 1, AutoRefreshEnabled: true, AutoRefreshIntervalSeconds: 5,
		DailyUsageResetEnabled: false, DailyUsageResetTime: "00:00",
		OperationConcurrency: 1, BatchOperationConcurrency: 10, AttributedFailureThreshold: 3,
		// 401/403 always count toward the shared consecutive-failure threshold (legacy streak;
		// v0.6.0 debt-based auto-probe is the primary path).
		AttributedFailureStatuses: []int{401, 403},
		DebtProbeThreshold:        2.0,
		DebtFail401:               1.5, DebtFail429: 0.5, DebtSuccessDecay: 1.0,
		WatchPriority: -10, AnomalyPriority: -50, DeadPriority: -100,
		DefaultRestorePriority: 0,
		WatchReprobeMinutes:    30, AnomalyReprobeHours: 6,
		// Legacy mirrors so older persisted JSON / tests that still read these fields work.
		DemotionPriority: -100, SoftDemotionPriority: -10, SoftDebtThreshold: 2.0, HardDebtThreshold: 4.5,
		SoftDemotionEnabled: true, CooldownRestoreEnabled: false, CooldownRestoreSkipBots: true,
		HalfOpenEnabled: false, HalfOpenSuccessThreshold: 2,
		ProtectionLevel: "strict", FreeUserDailyTokenLimit: 2_000_000,
		CountStatus429: false, CountStatus5XX: false,
		DefaultTokenCapacity: 1_000_000, PerAccountTokenCapacity: map[string]uint64{},
		HealthStaleAfterSeconds: 86400, OperationTimeoutSeconds: 60, WriteMode: "managed",
		OutboundProxyURL: "",
	}
}

// ValidateOutboundProxyURL accepts empty (env fallback) or a parseable absolute proxy URL.
// Does not log the value (may contain credentials).
func ValidateOutboundProxyURL(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("outbound_proxy_url 无效: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("outbound_proxy_url 须含 scheme 与 host（如 http://127.0.0.1:10808）")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks5", "socks5h":
		return nil
	default:
		return fmt.Errorf("outbound_proxy_url scheme 须为 http/https/socks5/socks5h")
	}
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
	settings.DebtProbeThreshold = envFloat("CPA_GROK_DEBT_PROBE_THRESHOLD", settings.DebtProbeThreshold, 0, 1_000_000)
	settings.DebtFail401 = envFloat("CPA_GROK_DEBT_FAIL_401", settings.DebtFail401, 0, 1_000_000)
	settings.DebtFail429 = envFloat("CPA_GROK_DEBT_FAIL_429", settings.DebtFail429, 0, 1_000_000)
	settings.DebtSuccessDecay = envFloat("CPA_GROK_DEBT_SUCCESS_DECAY", settings.DebtSuccessDecay, 0, 1_000_000)
	settings.WatchPriority = envInt("CPA_GROK_WATCH_PRIORITY", settings.WatchPriority, -1_000_000, 1_000_000)
	settings.AnomalyPriority = envInt("CPA_GROK_ANOMALY_PRIORITY", settings.AnomalyPriority, -1_000_000, 1_000_000)
	settings.DeadPriority = envInt("CPA_GROK_DEAD_PRIORITY", settings.DeadPriority, -1_000_000, 1_000_000)
	// Legacy alias: CPA_GROK_DEMOTION_PRIORITY → dead_priority
	if v := strings.TrimSpace(os.Getenv("CPA_GROK_DEMOTION_PRIORITY")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			settings.DeadPriority = n
			settings.DemotionPriority = n
		}
	} else {
		settings.DemotionPriority = settings.DeadPriority
	}
	settings.DefaultRestorePriority = envInt("CPA_GROK_DEFAULT_RESTORE_PRIORITY", settings.DefaultRestorePriority, -1_000_000, 1_000_000)
	settings.WatchReprobeMinutes = envInt("CPA_GROK_WATCH_REPROBE_MINUTES", settings.WatchReprobeMinutes, 1, 10_080)
	settings.AnomalyReprobeHours = envInt("CPA_GROK_ANOMALY_REPROBE_HOURS", settings.AnomalyReprobeHours, 1, 168)
	settings.CountStatus429 = envBool("CPA_GROK_COUNT_429", false)
	settings.CountStatus5XX = envBool("CPA_GROK_COUNT_5XX", false)
	return settings
}

// NormalizeSettings fills v0.6.0 fields from legacy values when upgrading persisted settings.
func NormalizeSettings(settings Settings) Settings {
	if settings.DebtProbeThreshold <= 0 {
		if settings.SoftDebtThreshold > 0 {
			settings.DebtProbeThreshold = settings.SoftDebtThreshold
		} else {
			settings.DebtProbeThreshold = 2.0
		}
	}
	if settings.WatchPriority == 0 && settings.SoftDemotionPriority != 0 {
		settings.WatchPriority = settings.SoftDemotionPriority
	}
	if settings.WatchPriority == 0 {
		settings.WatchPriority = -10
	}
	if settings.AnomalyPriority == 0 {
		settings.AnomalyPriority = -50
	}
	if settings.DeadPriority == 0 {
		if settings.DemotionPriority != 0 {
			settings.DeadPriority = settings.DemotionPriority
		} else {
			settings.DeadPriority = -100
		}
	}
	// Keep DemotionPriority as alias of DeadPriority for any remaining readers.
	settings.DemotionPriority = settings.DeadPriority
	if settings.WatchReprobeMinutes <= 0 {
		settings.WatchReprobeMinutes = 30
	}
	if settings.AnomalyReprobeHours <= 0 {
		settings.AnomalyReprobeHours = 6
	}
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

func envFloat(name string, fallback, minimum, maximum float64) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(name)), 64)
	if err != nil || value < minimum || value > maximum {
		return fallback
	}
	return value
}

type Meta struct {
	PluginID            string        `json:"plugin_id"`
	PluginVersion       string        `json:"plugin_version"`
	APIVersion          int           `json:"api_version"`
	WriteMode           string        `json:"write_mode"`
	Status              string        `json:"status"`
	StateStatus         string        `json:"state_status"`
	StateBackend        string        `json:"state_backend,omitempty"`
	DataDir             string        `json:"data_dir,omitempty"`
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

func BuildMeta(snapshot stateinfra.Snapshot, stateInfo ...stateinfra.Info) Meta {
	dedupeMode := "exact"
	if snapshot.EventDedupe.WeakModeUsed || len(snapshot.EventDedupe.ExactIDs) == 0 {
		dedupeMode = "weak"
	}
	info := stateinfra.Info{Status: "healthy"}
	if len(stateInfo) > 0 {
		info = stateInfo[0]
	}
	return Meta{PluginID: stateinfra.PluginID, PluginVersion: stateinfra.PluginVersion, APIVersion: 1,
		WriteMode: "managed", Status: "ready", StateStatus: info.Status, StateBackend: info.Backend, DataDir: info.DataDir,
		StatisticsStartedAt: snapshot.StatisticsStartedAt, DedupeMode: dedupeMode, ConditionalWrite: false,
		Capabilities: []string{
			"usage", "auth_list", "auth_get", "auth_save", "management_routes", "set_enabled",
			"demote", "restore_priority", "auto_demotion", "alive_probe", "watch_anomaly_dead",
			"debt_probe_threshold", "safe_delete", "daily_usage_reset", "token_resign",
		},
		UnavailableFeatures: []Unavailable{{Feature: "checks", Reason: "host.auth.invoke 未提供"}}}
}
