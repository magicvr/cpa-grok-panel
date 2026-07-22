package application_test

import (
	"testing"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/application"
	"github.com/magicvr/cpa-grok-panel/internal/domain"
	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

func TestUsageResetWorkerResetsOnceAfterConfiguredLocalTime(t *testing.T) {
	location := time.FixedZone("server-local", 8*60*60)
	current := time.Date(2026, 7, 15, 8, 29, 0, 0, location)
	store := stateinfra.OpenMemory(current.UTC())
	settings := application.DefaultSettings()
	settings.DailyUsageResetEnabled = true
	settings.DailyUsageResetTime = "08:30"
	periodStarted := current.Add(-time.Hour).UTC()
	baseline := 12
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Settings = &settings
		snapshot.Accounts["idx-1"] = domain.AccountState{
			ExactFileName: "xai-a.json",
			Usage: domain.UsageCounters{
				InputTokens: 10, OutputTokens: 20, TotalTokens: 30,
				SuccessfulRequests: 4, FailedRequests: 3, CancelledRequests: 2,
				EventsWithMissingUsage: 1, EventsWithInconsistentUsage: 1,
				PeriodStartedAt: periodStarted, LastEventID: "event-before-reset", DedupeMode: "exact",
			},
			HostRequestBaseline: &domain.HostRequestBaseline{
				Success: 100, Failed: 20, BoundPeriodStartedAt: periodStarted,
			},
			Failure:  domain.FailureState{ConsecutiveAttributedFailures: 3, LastFailureCode: "http_500"},
			Demotion: domain.DemotionState{State: "applied", BaselinePriority: &baseline},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	worker := application.NewUsageResetWorkerWithClock(store, settings, func() time.Time { return current }, location)

	if reset, err := worker.RunOnce(); err != nil || reset {
		t.Fatalf("before due reset=%t err=%v", reset, err)
	}
	current = time.Date(2026, 7, 15, 8, 30, 5, 0, location)
	if reset, err := worker.RunOnce(); err != nil || !reset {
		t.Fatalf("at due reset=%t err=%v", reset, err)
	}
	snapshot := store.View()
	account := snapshot.Accounts["idx-1"]
	if account.Usage.InputTokens != 0 || account.Usage.OutputTokens != 0 || account.Usage.TotalTokens != 0 ||
		account.Usage.SuccessfulRequests != 0 || account.Usage.FailedRequests != 0 || account.Usage.CancelledRequests != 0 ||
		account.Usage.EventsWithMissingUsage != 0 || account.Usage.EventsWithInconsistentUsage != 0 {
		t.Fatalf("usage not reset: %+v", account.Usage)
	}
	if !account.Usage.PeriodStartedAt.Equal(current.UTC()) || account.Usage.DedupeMode != "exact" {
		t.Fatalf("usage metadata=%+v", account.Usage)
	}
	if account.Failure.ConsecutiveAttributedFailures != 0 || account.Failure.LastFailureCode != "http_500" {
		t.Fatalf("failure state=%+v", account.Failure)
	}
	if account.Demotion.State != "applied" || account.Demotion.BaselinePriority == nil || *account.Demotion.BaselinePriority != baseline {
		t.Fatalf("demotion state=%+v", account.Demotion)
	}
	if account.HostRequestBaseline != nil {
		t.Fatalf("host request baseline should be cleared on daily reset: %+v", account.HostRequestBaseline)
	}
	if snapshot.LastUsageResetDate != "2026-07-15" || !snapshot.StatisticsStartedAt.Equal(current.UTC()) {
		t.Fatalf("snapshot reset markers=%+v", snapshot)
	}
	if reset, err := worker.RunOnce(); err != nil || reset {
		t.Fatalf("same-day duplicate reset=%t err=%v", reset, err)
	}
}

func TestValidateDailyUsageResetTime(t *testing.T) {
	for _, valid := range []string{"00:00", "09:05", "23:59"} {
		if err := application.ValidateDailyUsageResetTime(valid); err != nil {
			t.Fatalf("valid %q: %v", valid, err)
		}
	}
	for _, invalid := range []string{"0:00", "9:05", "24:00", "12:60", "noon"} {
		if err := application.ValidateDailyUsageResetTime(invalid); err == nil {
			t.Fatalf("invalid %q accepted", invalid)
		}
	}
}
