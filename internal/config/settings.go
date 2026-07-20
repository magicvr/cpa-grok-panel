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
	SoftDemotionEnabled        bool              `json:"soft_demotion_enabled"`
	SoftDemotionPriority       int               `json:"soft_demotion_priority"`
	SoftDebtThreshold          float64           `json:"soft_debt_threshold"`
	HardDebtThreshold          float64           `json:"hard_debt_threshold"`
	DebtFail401                float64           `json:"debt_fail_401"`
	DebtFail429                float64           `json:"debt_fail_429"`
	DebtSuccessDecay           float64           `json:"debt_success_decay"`
	DemotionPriority           int               `json:"demotion_priority"`
	DefaultRestorePriority     int               `json:"default_restore_priority"`
	CooldownRestoreEnabled     bool              `json:"cooldown_restore_enabled"`
	HalfOpenEnabled            bool              `json:"half_open_enabled"`
	HalfOpenSuccessThreshold   int               `json:"half_open_success_threshold"`
	ProtectionLevel            string            `json:"protection_level"`
	DefaultTokenCapacity       uint64            `json:"default_token_capacity"`
	PerAccountTokenCapacity    map[string]uint64 `json:"per_account_token_capacity"`
	HealthStaleAfterSeconds    int               `json:"health_stale_after_seconds"`
	OperationTimeoutSeconds    int               `json:"operation_timeout_seconds"`
	WriteMode                  string            `json:"write_mode"`
	FreeUserDailyTokenLimit    uint64            `json:"free_user_daily_token_limit"`
}
