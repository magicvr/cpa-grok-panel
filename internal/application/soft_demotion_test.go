package application_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/application"
	"github.com/magicvr/cpa-grok-panel/internal/cpaabi"
	"github.com/magicvr/cpa-grok-panel/internal/domain"
	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

func TestFailureDebtDecaysWithoutClearingHistory(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store := stateinfra.OpenMemory(now)
	settings := application.DefaultSettings()
	settings.DebtProbeThreshold = 100
	service := application.NewUsageServiceWithDemotion(store, func() time.Time { return now }, settings, nil)

	handleUsage(t, service, usageEvent("debt-fail", now, "failure", 401))
	handleUsage(t, service, usageEvent("debt-success", now.Add(time.Minute), "success", 0))

	failure := store.View().Accounts["idx-state-machine"].Failure
	if failure.ConsecutiveAttributedFailures != 0 || failure.DebtScore != 0.5 {
		t.Fatalf("failure=%+v want streak=0 debt=0.5", failure)
	}
}

func TestDebtThresholdRequestsAutoProbe(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store, _, usage, _, settings := newStateMachineServices(now)
	// default debt_fail_401=1.5, threshold=2.0 → second 401 triggers probe
	if result := handleUsage(t, usage, usageEvent("soft-1", now, "failure", 401)); result.ProbeRequested || result.DemotionRequested {
		t.Fatalf("first failure should not probe: %+v", result)
	}
	if store.View().Accounts["idx-state-machine"].Failure.DebtScore != settings.DebtFail401 {
		t.Fatalf("debt=%v", store.View().Accounts["idx-state-machine"].Failure.DebtScore)
	}
	result := handleUsage(t, usage, usageEvent("soft-2", now.Add(time.Minute), "failure", 401))
	if !result.ProbeRequested || result.DemotionRequested {
		t.Fatalf("second failure should request probe only: %+v", result)
	}
	// debt zeroed on threshold
	if store.View().Accounts["idx-state-machine"].Failure.DebtScore != 0 {
		t.Fatalf("debt should be zeroed: %v", store.View().Accounts["idx-state-machine"].Failure.DebtScore)
	}
}

func TestWatchAnomalyDoNotAccrueDebt(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store, _, usage, _, _ := newStateMachineServices(now)
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		state := snapshot.Accounts["idx-state-machine"]
		state.Demotion = domain.DemotionState{State: "applied", Class: domain.DemotionClassWatch}
		state.Failure.DebtScore = 0
		snapshot.Accounts["idx-state-machine"] = state
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result := handleUsage(t, usage, usageEvent("watch-fail", now, "failure", 401))
	if result.ProbeRequested || result.DemotionRequested {
		t.Fatalf("watch must not trigger: %+v", result)
	}
	if store.View().Accounts["idx-state-machine"].Failure.DebtScore != 0 {
		t.Fatalf("watch debt should freeze: %v", store.View().Accounts["idx-state-machine"].Failure.DebtScore)
	}
}

func TestDeadFreezesDebt(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store, _, usage, _, _ := newStateMachineServices(now)
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		state := snapshot.Accounts["idx-state-machine"]
		state.Demotion = domain.DemotionState{State: "applied", Class: domain.DemotionClassDead}
		state.Failure.DebtScore = 1.5
		snapshot.Accounts["idx-state-machine"] = state
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = handleUsage(t, usage, usageEvent("dead-fail", now, "failure", 401))
	if store.View().Accounts["idx-state-machine"].Failure.DebtScore != 1.5 {
		t.Fatalf("dead debt frozen: %v", store.View().Accounts["idx-state-machine"].Failure.DebtScore)
	}
}

func TestApplyProbeManualLiveDoesNotDemote(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store, host, _, accounts, settings := newStateMachineServices(now)
	view, err := accounts.ApplyProbeResult("idx-state-machine", application.ProbeResult{Status: "live", HTTPStatus: 200}, domain.ProbeSourceManual)
	if err != nil {
		t.Fatal(err)
	}
	if view.Quota.ProbeStatus != domain.ProbeStatusLive {
		t.Fatalf("probe=%+v", view.Quota)
	}
	if host.files[0].Priority != 10 {
		t.Fatalf("priority changed: %d", host.files[0].Priority)
	}
	demotion := store.View().Accounts["idx-state-machine"].Demotion.Normalized()
	if demotion.Class != domain.DemotionClassNone {
		t.Fatalf("manual live must not demote: %+v settings=%+v", demotion, settings)
	}
}

func TestApplyProbeManualDeadWritesDeadTier(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store, host, _, accounts, settings := newStateMachineServices(now)
	view, err := accounts.ApplyProbeResult("idx-state-machine", application.ProbeResult{Status: "dead", HTTPStatus: 403}, domain.ProbeSourceManual)
	if err != nil {
		t.Fatal(err)
	}
	if !view.IsDemoted || view.Class != domain.DemotionClassDead {
		t.Fatalf("view=%+v", view)
	}
	if host.files[0].Priority != settings.DeadPriority {
		t.Fatalf("priority=%d want %d", host.files[0].Priority, settings.DeadPriority)
	}
	state := store.View().Accounts["idx-state-machine"]
	if state.Demotion.Class != domain.DemotionClassDead || state.Demotion.State != "applied" {
		t.Fatalf("demotion=%+v", state.Demotion)
	}
	if state.Demotion.NextProbeAt != nil {
		t.Fatalf("dead should clear next probe: %+v", state.Demotion.NextProbeAt)
	}
}

func TestApplyProbeAutoLiveEntersWatch(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store, host, _, accounts, settings := newStateMachineServices(now)
	view, err := accounts.ApplyProbeResult("idx-state-machine", application.ProbeResult{Status: "live", HTTPStatus: 200}, domain.ProbeSourceAuto)
	if err != nil {
		t.Fatal(err)
	}
	if view.Class != domain.DemotionClassWatch || host.files[0].Priority != settings.WatchPriority {
		t.Fatalf("view class=%s priority=%d", view.Class, host.files[0].Priority)
	}
	demotion := store.View().Accounts["idx-state-machine"].Demotion
	if demotion.NextProbeAt == nil {
		t.Fatal("watch should schedule next probe")
	}
	want := now.Add(time.Duration(settings.WatchReprobeMinutes) * time.Minute)
	if !demotion.NextProbeAt.Equal(want) {
		t.Fatalf("next=%v want %v", demotion.NextProbeAt, want)
	}
}

func TestApplyProbeAutoLiveOnWatchRestoresNone(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store, host, _, accounts, settings := newStateMachineServices(now)
	// enter watch first
	if _, err := accounts.ApplyProbeResult("idx-state-machine", application.ProbeResult{Status: "live", HTTPStatus: 200}, domain.ProbeSourceAuto); err != nil {
		t.Fatal(err)
	}
	if host.files[0].Priority != settings.WatchPriority {
		t.Fatalf("priority=%d", host.files[0].Priority)
	}
	// re-probe still live → restore
	view, err := accounts.ApplyProbeResult("idx-state-machine", application.ProbeResult{Status: "live", HTTPStatus: 200}, domain.ProbeSourceAuto)
	if err != nil {
		t.Fatal(err)
	}
	if view.IsDemoted || view.Class != domain.DemotionClassNone {
		t.Fatalf("view=%+v", view)
	}
	if host.files[0].Priority != settings.DefaultRestorePriority {
		t.Fatalf("priority=%d want %d", host.files[0].Priority, settings.DefaultRestorePriority)
	}
	demotion := store.View().Accounts["idx-state-machine"].Demotion
	if demotion.State != "restored" || demotion.NextProbeAt != nil {
		t.Fatalf("demotion=%+v", demotion)
	}
}

func TestApplyProbeCoolingIsAnomaly(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	_, host, _, accounts, settings := newStateMachineServices(now)
	view, err := accounts.ApplyProbeResult("idx-state-machine", application.ProbeResult{Status: "cooling", HTTPStatus: 429}, domain.ProbeSourceAuto)
	if err != nil {
		t.Fatal(err)
	}
	if view.Class != domain.DemotionClassAnomaly || host.files[0].Priority != settings.AnomalyPriority {
		t.Fatalf("class=%s priority=%d", view.Class, host.files[0].Priority)
	}
}

func TestSuccessRestoresDemotedAccount(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store, host, usage, accounts, settings := newStateMachineServices(now)
	if _, err := accounts.ApplyProbeResult("idx-state-machine", application.ProbeResult{Status: "error", HTTPStatus: 500}, domain.ProbeSourceAuto); err != nil {
		t.Fatal(err)
	}
	if host.files[0].Priority != settings.AnomalyPriority {
		t.Fatalf("priority=%d", host.files[0].Priority)
	}
	result := handleUsage(t, usage, usageEvent("ok-1", now.Add(time.Minute), "success", 0))
	if !result.DemotionRequested {
		t.Fatalf("success should request restore: %+v", result)
	}
	if err := accounts.ApplyRequestedDemotion("idx-state-machine", settings.DefaultRestorePriority); err != nil {
		t.Fatal(err)
	}
	state := store.View().Accounts["idx-state-machine"]
	if host.files[0].Priority != settings.DefaultRestorePriority || state.Demotion.Class != domain.DemotionClassNone {
		t.Fatalf("priority=%d demotion=%+v", host.files[0].Priority, state.Demotion)
	}
	if state.Failure.DebtScore != 0 {
		t.Fatalf("debt should clear: %v", state.Failure.DebtScore)
	}
	if state.Quota.ProbeStatus != domain.ProbeStatusLive {
		t.Fatalf("probe should be live: %s", state.Quota.ProbeStatus)
	}
}

func TestLegacyClassMigration(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"soft", domain.DemotionClassWatch},
		{"half_open", domain.DemotionClassWatch},
		{"hard", domain.DemotionClassDead},
	} {
		got := domain.DemotionState{Class: tc.in, State: "applied"}.Normalized().Class
		if got != tc.want {
			t.Fatalf("%s → %s want %s", tc.in, got, tc.want)
		}
	}
}

func TestHardStreakTriggersProbeNotDirectDead(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store, _, usage, _, settings := newStateMachineServices(now)
	settings.CountStatus5XX = true
	settings.DebtProbeThreshold = 100 // prevent debt path
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Settings = &settings
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for hit := 1; hit <= settings.AttributedFailureThreshold; hit++ {
		result := handleUsage(t, usage, usageEvent(fmt.Sprintf("hard-%d", hit), now.Add(time.Duration(hit)*time.Minute), "failure", 500))
		if result.ProbeRequested != (hit == settings.AttributedFailureThreshold) {
			t.Fatalf("hit=%d result=%+v", hit, result)
		}
		if result.DemotionRequested {
			t.Fatalf("should not direct-demote: hit=%d", hit)
		}
	}
}

func newStateMachineServices(now time.Time) (*stateinfra.Store, *accountHost, *application.UsageService, *application.AccountsService, application.Settings) {
	store := stateinfra.OpenMemory(now)
	settings := application.DefaultSettings()
	host := &accountHost{
		files:     []domain.AuthFile{xaiFile("idx-state-machine", "xai-state-machine.json", 10)},
		documents: map[string]cpaabi.AuthDocument{"idx-state-machine": {"priority": 10, "disabled": false}},
	}
	usage := application.NewUsageServiceWithDemotion(store, func() time.Time { return now }, settings, nil)
	accounts := application.NewAccountsService(host, store, func() time.Time { return now }, settings)
	return store, host, usage, accounts, settings
}

func usageEvent(eventID string, occurredAt time.Time, outcome string, status int) domain.UsageEvent {
	return domain.UsageEvent{
		AuthIndex: "idx-state-machine", EventID: eventID, OccurredAt: occurredAt,
		Outcome: outcome, StatusCode: status, Provider: "xai", AuthType: "oauth",
	}
}

func handleUsage(t *testing.T, service *application.UsageService, event domain.UsageEvent) application.UsageResult {
	t.Helper()
	result, err := service.Handle(event)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
