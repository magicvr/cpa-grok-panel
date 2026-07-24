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
	"github.com/magicvr/cpa-grok-panel/internal/domain"
	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

type Settings = config.Settings

func DefaultSettings() Settings {
	return Settings{
		Revision: 1, AutoRefreshEnabled: true, AutoRefreshIntervalSeconds: 5,
		DailyUsageResetEnabled: false, DailyUsageResetTime: "00:00",
		OperationConcurrency: 1, BatchOperationConcurrency: 10, AttributedFailureThreshold: 3,
		AttributedFailureStatuses: []int{401, 403},
		DebtProbeThreshold:        2.0,
		DebtFail401:               1.5, DebtFail429: 0.5, DebtSuccessDecay: 1.0,
		PriorityLive: 0, PriorityInvalid: -50, PriorityDead: -100,
		PriorityThrottled: -50, PriorityUnknown: -10, PriorityError: -50,
		// Legacy mirrors (ignored by v0.7 policy; kept for older JSON readers).
		WatchPriority: -10, AnomalyPriority: -50, DeadPriority: -100,
		DefaultRestorePriority: 0, DemotionPriority: -100, SoftDemotionPriority: -10,
		SoftDebtThreshold: 2.0, HardDebtThreshold: 4.5,
		ProtectionLevel: "strict", FreeUserDailyTokenLimit: 2_000_000,
		CountStatus429: false, CountStatus5XX: false,
		DefaultTokenCapacity: 1_000_000, PerAccountTokenCapacity: map[string]uint64{},
		HealthStaleAfterSeconds: 86400, OperationTimeoutSeconds: 60, WriteMode: "managed",
		OutboundProxyURL: "",
	}
}

// PriorityForProbeStatus returns the configured priority for a canonical probe status.
func PriorityForProbeStatus(settings Settings, status string) int {
	settings = NormalizeSettings(settings)
	switch domain.CanonicalProbeStatus(status, 0) {
	case domain.ProbeStatusLive:
		return settings.PriorityLive
	case domain.ProbeStatusInvalid:
		return settings.PriorityInvalid
	case domain.ProbeStatusDead:
		return settings.PriorityDead
	case domain.ProbeStatusThrottled:
		return settings.PriorityThrottled
	case domain.ProbeStatusError:
		return settings.PriorityError
	default:
		return settings.PriorityUnknown
	}
}

// ValidateOutboundProxyURL accepts empty (env fallback) or a parseable absolute proxy URL.
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
	settings.PriorityLive = envInt("CPA_GROK_PRIORITY_LIVE", settings.PriorityLive, -1_000_000, 1_000_000)
	settings.PriorityInvalid = envInt("CPA_GROK_PRIORITY_INVALID", settings.PriorityInvalid, -1_000_000, 1_000_000)
	settings.PriorityDead = envInt("CPA_GROK_PRIORITY_DEAD", settings.PriorityDead, -1_000_000, 1_000_000)
	settings.PriorityThrottled = envInt("CPA_GROK_PRIORITY_THROTTLED", settings.PriorityThrottled, -1_000_000, 1_000_000)
	settings.PriorityUnknown = envInt("CPA_GROK_PRIORITY_UNKNOWN", settings.PriorityUnknown, -1_000_000, 1_000_000)
	settings.PriorityError = envInt("CPA_GROK_PRIORITY_ERROR", settings.PriorityError, -1_000_000, 1_000_000)
	// Legacy env aliases → new fields when new env unset.
	if v := strings.TrimSpace(os.Getenv("CPA_GROK_DEAD_PRIORITY")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if strings.TrimSpace(os.Getenv("CPA_GROK_PRIORITY_DEAD")) == "" {
				settings.PriorityDead = n
			}
			settings.DeadPriority = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("CPA_GROK_DEMOTION_PRIORITY")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if strings.TrimSpace(os.Getenv("CPA_GROK_PRIORITY_DEAD")) == "" && strings.TrimSpace(os.Getenv("CPA_GROK_DEAD_PRIORITY")) == "" {
				settings.PriorityDead = n
			}
			settings.DemotionPriority = n
			settings.DeadPriority = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("CPA_GROK_DEFAULT_RESTORE_PRIORITY")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if strings.TrimSpace(os.Getenv("CPA_GROK_PRIORITY_LIVE")) == "" {
				settings.PriorityLive = n
			}
			settings.DefaultRestorePriority = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("CPA_GROK_ANOMALY_PRIORITY")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if strings.TrimSpace(os.Getenv("CPA_GROK_PRIORITY_ERROR")) == "" {
				settings.PriorityError = n
				settings.PriorityInvalid = n
				settings.PriorityThrottled = n
			}
			settings.AnomalyPriority = n
		}
	}
	settings.CountStatus429 = envBool("CPA_GROK_COUNT_429", false)
	settings.CountStatus5XX = envBool("CPA_GROK_COUNT_5XX", false)
	return NormalizeSettings(settings)
}

// NormalizeSettings fills v0.7.0 priority_* from legacy fields when upgrading persisted settings.
// Note: priority_live default is 0 (valid). Other priority_* zeros are filled with product defaults
// unless a fully-legacy blob is detected (all priority_* zero with legacy watch/anomaly/dead set).
func NormalizeSettings(settings Settings) Settings {
	if settings.DebtProbeThreshold <= 0 {
		if settings.SoftDebtThreshold > 0 {
			settings.DebtProbeThreshold = settings.SoftDebtThreshold
		} else {
			settings.DebtProbeThreshold = 2.0
		}
	}
	defaults := DefaultSettings()
	allNewZero := settings.PriorityLive == 0 && settings.PriorityInvalid == 0 &&
		settings.PriorityDead == 0 && settings.PriorityThrottled == 0 &&
		settings.PriorityUnknown == 0 && settings.PriorityError == 0
	legacyPresent := settings.DeadPriority != 0 || settings.AnomalyPriority != 0 ||
		settings.DemotionPriority != 0 || settings.DefaultRestorePriority != 0 ||
		settings.WatchPriority != 0 || settings.SoftDemotionPriority != 0

	if allNewZero && legacyPresent {
		if settings.DefaultRestorePriority != 0 {
			settings.PriorityLive = settings.DefaultRestorePriority
		}
		if settings.AnomalyPriority != 0 {
			settings.PriorityInvalid = settings.AnomalyPriority
			settings.PriorityThrottled = settings.AnomalyPriority
			settings.PriorityError = settings.AnomalyPriority
		} else {
			settings.PriorityInvalid = defaults.PriorityInvalid
			settings.PriorityThrottled = defaults.PriorityThrottled
			settings.PriorityError = defaults.PriorityError
		}
		if settings.DeadPriority != 0 {
			settings.PriorityDead = settings.DeadPriority
		} else if settings.DemotionPriority != 0 {
			settings.PriorityDead = settings.DemotionPriority
		} else {
			settings.PriorityDead = defaults.PriorityDead
		}
		settings.PriorityUnknown = defaults.PriorityUnknown
	} else if allNewZero {
		settings.PriorityLive = defaults.PriorityLive
		settings.PriorityInvalid = defaults.PriorityInvalid
		settings.PriorityDead = defaults.PriorityDead
		settings.PriorityThrottled = defaults.PriorityThrottled
		settings.PriorityUnknown = defaults.PriorityUnknown
		settings.PriorityError = defaults.PriorityError
	} else {
		// Partial upgrade: fill missing non-live zeros from defaults or legacy.
		if settings.PriorityDead == 0 {
			if settings.DeadPriority != 0 {
				settings.PriorityDead = settings.DeadPriority
			} else if settings.DemotionPriority != 0 {
				settings.PriorityDead = settings.DemotionPriority
			} else {
				settings.PriorityDead = defaults.PriorityDead
			}
		}
		if settings.PriorityInvalid == 0 {
			if settings.AnomalyPriority != 0 {
				settings.PriorityInvalid = settings.AnomalyPriority
			} else {
				settings.PriorityInvalid = defaults.PriorityInvalid
			}
		}
		if settings.PriorityThrottled == 0 {
			if settings.AnomalyPriority != 0 {
				settings.PriorityThrottled = settings.AnomalyPriority
			} else {
				settings.PriorityThrottled = defaults.PriorityThrottled
			}
		}
		if settings.PriorityError == 0 {
			if settings.AnomalyPriority != 0 {
				settings.PriorityError = settings.AnomalyPriority
			} else {
				settings.PriorityError = defaults.PriorityError
			}
		}
		if settings.PriorityUnknown == 0 {
			settings.PriorityUnknown = defaults.PriorityUnknown
		}
	}

	settings.DeadPriority = settings.PriorityDead
	settings.DemotionPriority = settings.PriorityDead
	settings.AnomalyPriority = settings.PriorityError
	settings.DefaultRestorePriority = settings.PriorityLive
	settings.SoftDemotionPriority = settings.WatchPriority
	settings.SoftDebtThreshold = settings.DebtProbeThreshold
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
			"alive_probe", "alive_priority_bind", "debt_probe_threshold", "safe_delete",
			"daily_usage_reset", "token_resign", "sync_priority",
		},
		UnavailableFeatures: []Unavailable{{Feature: "checks", Reason: "host.auth.invoke 未提供"}}}
}
