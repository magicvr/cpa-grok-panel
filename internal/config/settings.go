package config

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
