package config

type Settings struct {
	Revision                   int               `json:"revision"`
	AutoRefreshEnabled         bool              `json:"auto_refresh_enabled"`
	AutoRefreshIntervalSeconds int               `json:"auto_refresh_interval_seconds"`
	DailyUsageResetEnabled     bool              `json:"daily_usage_reset_enabled"`
	DailyUsageResetTime        string            `json:"daily_usage_reset_time"`
	OperationConcurrency       int               `json:"operation_concurrency"`
	BatchOperationConcurrency  int               `json:"batch_operation_concurrency"`
	AttributedFailureThreshold int               `json:"attributed_failure_threshold"`
	AttributedFailureStatuses  []int             `json:"attributed_failure_statuses"`
	CountStatus429             bool              `json:"count_status_429"`
	CountStatus5XX             bool              `json:"count_status_5xx"`
	// v0.6.0: single debt threshold → auto probe (replaces soft/hard dual thresholds).
	DebtProbeThreshold float64 `json:"debt_probe_threshold"`
	DebtFail401        float64 `json:"debt_fail_401"`
	DebtFail429        float64 `json:"debt_fail_429"`
	DebtSuccessDecay   float64 `json:"debt_success_decay"`
	// Tier priorities for watch / anomaly / dead demotion classes.
	WatchPriority          int `json:"watch_priority"`
	AnomalyPriority        int `json:"anomaly_priority"`
	DeadPriority           int `json:"dead_priority"`
	DefaultRestorePriority int `json:"default_restore_priority"`
	// Scheduled re-probe for watch / anomaly.
	WatchReprobeMinutes  int `json:"watch_reprobe_minutes"`
	AnomalyReprobeHours  int `json:"anomaly_reprobe_hours"`
	// Legacy fields (pre-v0.6.0): still unmarshaled from JSON but ignored by policy.
	// DemotionPriority mirrors DeadPriority for older clients that still PATCH it.
	SoftDemotionEnabled      bool    `json:"soft_demotion_enabled,omitempty"`
	SoftDemotionPriority     int     `json:"soft_demotion_priority,omitempty"`
	SoftDebtThreshold        float64 `json:"soft_debt_threshold,omitempty"`
	HardDebtThreshold        float64 `json:"hard_debt_threshold,omitempty"`
	DemotionPriority         int     `json:"demotion_priority,omitempty"`
	CooldownRestoreEnabled     bool    `json:"cooldown_restore_enabled,omitempty"`
	CooldownRestoreSkipBots    bool    `json:"cooldown_restore_skip_bots,omitempty"`
	HalfOpenEnabled          bool    `json:"half_open_enabled,omitempty"`
	HalfOpenSuccessThreshold int     `json:"half_open_success_threshold,omitempty"`

	ProtectionLevel         string            `json:"protection_level"`
	DefaultTokenCapacity    uint64            `json:"default_token_capacity"`
	PerAccountTokenCapacity map[string]uint64 `json:"per_account_token_capacity"`
	HealthStaleAfterSeconds int               `json:"health_stale_after_seconds"`
	OperationTimeoutSeconds int               `json:"operation_timeout_seconds"`
	WriteMode               string            `json:"write_mode"`
	FreeUserDailyTokenLimit uint64            `json:"free_user_daily_token_limit"`
	// OutboundProxyURL is the optional HTTP(S)/SOCKS proxy for plugin-process
	// egress such as batch token resign and auto probe. Empty = use process
	// CPA_GROK_OUTBOUND_PROXY then HTTPS_PROXY/HTTP_PROXY. Not CPA host proxy-url.
	OutboundProxyURL string `json:"outbound_proxy_url"`
}
