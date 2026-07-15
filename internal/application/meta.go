package application

import (
	"time"

	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

type Settings struct {
	Revision                   int               `json:"revision"`
	OperationConcurrency       int               `json:"operation_concurrency"`
	AttributedFailureThreshold int               `json:"attributed_failure_threshold"`
	AttributedFailureStatuses  []int             `json:"attributed_failure_statuses"`
	DemotionPriority           int               `json:"demotion_priority"`
	ProtectionLevel            string            `json:"protection_level"`
	DefaultTokenCapacity       uint64            `json:"default_token_capacity"`
	PerAccountTokenCapacity    map[string]uint64 `json:"per_account_token_capacity"`
	HealthStaleAfterSeconds    int               `json:"health_stale_after_seconds"`
	OperationTimeoutSeconds    int               `json:"operation_timeout_seconds"`
	WriteMode                  string            `json:"write_mode"`
}

func ReadOnlySettings() Settings {
	return Settings{Revision: 1, OperationConcurrency: 3, AttributedFailureThreshold: 3,
		AttributedFailureStatuses: []int{401, 403}, DemotionPriority: -100, ProtectionLevel: "strict",
		DefaultTokenCapacity: 1_000_000, PerAccountTokenCapacity: map[string]uint64{},
		HealthStaleAfterSeconds: 86400, OperationTimeoutSeconds: 60, WriteMode: "read_only"}
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
		WriteMode: "read_only", Status: "ready", StateStatus: "healthy",
		StatisticsStartedAt: snapshot.StatisticsStartedAt, DedupeMode: dedupeMode, ConditionalWrite: false,
		Capabilities:        []string{"usage", "auth_list", "management_routes"},
		UnavailableFeatures: []Unavailable{{Feature: "auth_writes", Reason: "M1 固定为 read_only"}, {Feature: "checks", Reason: "host.auth.invoke 未提供"}, {Feature: "cleanup", Reason: "M3 功能未启用"}}}
}
