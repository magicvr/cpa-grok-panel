package application_test

import (
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
	// success while still unknown/empty probe marks live and clears debt
	if failure.DebtScore != 0 {
		t.Fatalf("failure=%+v want debt=0 after success heal", failure)
	}
}

func TestDebtThresholdRequestsAutoProbe(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store, _, usage, _, settings := newStateMachineServices(now)
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
	if store.View().Accounts["idx-state-machine"].Failure.DebtScore != 0 {
		t.Fatalf("debt should be zeroed: %v", store.View().Accounts["idx-state-machine"].Failure.DebtScore)
	}
}

func TestDeadProbeFreezesDebt(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store, _, usage, _, _ := newStateMachineServices(now)
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		state := snapshot.Accounts["idx-state-machine"]
		state.Quota.ProbeStatus = domain.ProbeStatusDead
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

func TestApplyProbeAlwaysBindsPriority(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	_, host, _, accounts, settings := newStateMachineServices(now)
	view, err := accounts.ApplyProbeResult("idx-state-machine", application.ProbeResult{Status: "live", HTTPStatus: 200}, domain.ProbeSourceManual)
	if err != nil {
		t.Fatal(err)
	}
	if view.Quota.ProbeStatus != domain.ProbeStatusLive {
		t.Fatalf("probe=%+v", view.Quota)
	}
	if host.files[0].Priority != settings.PriorityLive {
		t.Fatalf("priority=%d want %d", host.files[0].Priority, settings.PriorityLive)
	}
}

func TestApplyProbeDeadWritesPriorityDead(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	_, host, _, accounts, settings := newStateMachineServices(now)
	view, err := accounts.ApplyProbeResult("idx-state-machine", application.ProbeResult{Status: "dead", HTTPStatus: 403}, domain.ProbeSourceManual)
	if err != nil {
		t.Fatal(err)
	}
	if !view.IsDemoted || view.Quota.ProbeStatus != domain.ProbeStatusDead {
		t.Fatalf("view=%+v", view)
	}
	if host.files[0].Priority != settings.PriorityDead {
		t.Fatalf("priority=%d want %d", host.files[0].Priority, settings.PriorityDead)
	}
}

func TestApplyProbeInvalidAndThrottled(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	_, host, _, accounts, settings := newStateMachineServices(now)
	view, err := accounts.ApplyProbeResult("idx-state-machine", application.ProbeResult{Status: "exceed", HTTPStatus: 401}, domain.ProbeSourceAuto)
	if err != nil {
		t.Fatal(err)
	}
	if view.Quota.ProbeStatus != domain.ProbeStatusInvalid || host.files[0].Priority != settings.PriorityInvalid {
		t.Fatalf("invalid: status=%s prio=%d", view.Quota.ProbeStatus, host.files[0].Priority)
	}
	view, err = accounts.ApplyProbeResult("idx-state-machine", application.ProbeResult{Status: "cooling", HTTPStatus: 429}, domain.ProbeSourceAuto)
	if err != nil {
		t.Fatal(err)
	}
	if view.Quota.ProbeStatus != domain.ProbeStatusThrottled || host.files[0].Priority != settings.PriorityThrottled {
		t.Fatalf("throttled: status=%s prio=%d", view.Quota.ProbeStatus, host.files[0].Priority)
	}
}

func TestSuccessHealsToLive(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store, host, usage, accounts, settings := newStateMachineServices(now)
	if _, err := accounts.ApplyProbeResult("idx-state-machine", application.ProbeResult{Status: "error", HTTPStatus: 500}, domain.ProbeSourceAuto); err != nil {
		t.Fatal(err)
	}
	if host.files[0].Priority != settings.PriorityError {
		t.Fatalf("priority=%d", host.files[0].Priority)
	}
	result := handleUsage(t, usage, usageEvent("ok-1", now.Add(time.Minute), "success", 0))
	if !result.PriorityRequested {
		t.Fatalf("success should request priority heal: %+v", result)
	}
	if err := accounts.ApplyRequestedDemotion("idx-state-machine", settings.PriorityLive); err != nil {
		t.Fatal(err)
	}
	state := store.View().Accounts["idx-state-machine"]
	if host.files[0].Priority != settings.PriorityLive {
		t.Fatalf("priority=%d want %d", host.files[0].Priority, settings.PriorityLive)
	}
	if state.Failure.DebtScore != 0 {
		t.Fatalf("debt should clear: %v", state.Failure.DebtScore)
	}
	if state.Quota.ProbeStatus != domain.ProbeStatusLive {
		t.Fatalf("probe should be live: %s", state.Quota.ProbeStatus)
	}
}

func TestStreakDoesNotTriggerProbe(t *testing.T) {
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
	for hit := 1; hit <= settings.AttributedFailureThreshold+2; hit++ {
		result := handleUsage(t, usage, usageEvent("hard-"+string(rune('0'+hit)), now.Add(time.Duration(hit)*time.Minute), "failure", 500))
		if result.ProbeRequested {
			t.Fatalf("streak must not trigger probe: hit=%d", hit)
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
