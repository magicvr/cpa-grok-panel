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
	settings.SoftDebtThreshold = 100
	settings.HardDebtThreshold = 200
	service := application.NewUsageServiceWithDemotion(store, func() time.Time { return now }, settings, nil)

	handleUsage(t, service, usageEvent("debt-fail", now, "failure", 401))
	handleUsage(t, service, usageEvent("debt-success", now.Add(time.Minute), "success", 0))

	failure := store.View().Accounts["idx-state-machine"].Failure
	if failure.ConsecutiveAttributedFailures != 0 || failure.DebtScore != 0.5 {
		t.Fatalf("failure=%+v want streak=0 debt=0.5", failure)
	}
}

func TestDebtSoftDemotionWritesSoftPriority(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store, host, usage, accounts, settings := newStateMachineServices(now)

	if result := handleUsage(t, usage, usageEvent("soft-1", now, "failure", 401)); result.DemotionRequested {
		t.Fatalf("first failure requested demotion: %+v", result)
	}
	if result := handleUsage(t, usage, usageEvent("soft-2", now.Add(time.Minute), "failure", 401)); !result.DemotionRequested {
		t.Fatalf("second failure did not request soft demotion: %+v", result)
	}
	if err := accounts.ApplyRequestedDemotion("idx-state-machine", settings.DemotionPriority); err != nil {
		t.Fatal(err)
	}

	state := store.View().Accounts["idx-state-machine"]
	if host.files[0].Priority != settings.SoftDemotionPriority || state.Demotion.State != "applied" || state.Demotion.Class != domain.DemotionClassSoft {
		t.Fatalf("priority=%d state=%+v", host.files[0].Priority, state)
	}
	if state.Demotion.BaselinePriority == nil || *state.Demotion.BaselinePriority != 10 {
		t.Fatalf("baseline=%v", state.Demotion.BaselinePriority)
	}
}

func TestHardStreakDemotesWithoutDebtContribution(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store, host, usage, accounts, settings := newStateMachineServices(now)
	settings.CountStatus5XX = true
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Settings = &settings
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for hit := 1; hit <= settings.AttributedFailureThreshold; hit++ {
		result := handleUsage(t, usage, usageEvent(fmt.Sprintf("hard-%d", hit), now.Add(time.Duration(hit)*time.Minute), "failure", 500))
		if result.DemotionRequested != (hit == settings.AttributedFailureThreshold) {
			t.Fatalf("hit=%d result=%+v", hit, result)
		}
	}
	if err := accounts.ApplyRequestedDemotion("idx-state-machine", settings.DemotionPriority); err != nil {
		t.Fatal(err)
	}
	state := store.View().Accounts["idx-state-machine"]
	if host.files[0].Priority != settings.DemotionPriority || state.Demotion.Class != domain.DemotionClassHard || state.Failure.DebtScore != 0 {
		t.Fatalf("priority=%d state=%+v", host.files[0].Priority, state)
	}
}

func TestSoftDemotionUpgradesToHard(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store, host, usage, accounts, settings := newStateMachineServices(now)

	handleUsage(t, usage, usageEvent("upgrade-1", now, "failure", 401))
	handleUsage(t, usage, usageEvent("upgrade-2", now.Add(time.Minute), "failure", 401))
	if err := accounts.ApplyRequestedDemotion("idx-state-machine", settings.DemotionPriority); err != nil {
		t.Fatal(err)
	}
	if host.files[0].Priority != settings.SoftDemotionPriority {
		t.Fatalf("soft priority=%d", host.files[0].Priority)
	}

	result := handleUsage(t, usage, usageEvent("upgrade-3", now.Add(2*time.Minute), "failure", 401))
	if !result.DemotionRequested {
		t.Fatalf("hard upgrade not requested: %+v", result)
	}
	if err := accounts.ApplyRequestedDemotion("idx-state-machine", settings.DemotionPriority); err != nil {
		t.Fatal(err)
	}
	state := store.View().Accounts["idx-state-machine"]
	if host.files[0].Priority != settings.DemotionPriority || state.Demotion.Class != domain.DemotionClassHard || state.Demotion.RestoreCooldownHours != 12 {
		t.Fatalf("priority=%d state=%+v", host.files[0].Priority, state)
	}
	if state.Demotion.BaselinePriority == nil || *state.Demotion.BaselinePriority != 10 {
		t.Fatalf("baseline=%v", state.Demotion.BaselinePriority)
	}
}

func TestHalfOpenSuccessThresholdRestoresBaseline(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store, host, usage, accounts, settings := newHardAppliedAccount(t, now)
	now = now.Add(6 * time.Hour)
	accounts = application.NewAccountsService(host, store, func() time.Time { return now }, settings)

	entered, err := accounts.RestorePriorityAfterCooldown("idx-state-machine")
	if err != nil || !entered {
		t.Fatalf("entered=%t err=%v", entered, err)
	}
	state := store.View().Accounts["idx-state-machine"]
	if host.files[0].Priority != settings.SoftDemotionPriority || state.Demotion.Class != domain.DemotionClassHalfOpen || state.Demotion.RestoreCooldownHours != 6 {
		t.Fatalf("priority=%d state=%+v", host.files[0].Priority, state)
	}

	usage = application.NewUsageServiceWithDemotion(store, func() time.Time { return now }, settings, nil)
	first := handleUsage(t, usage, usageEvent("half-success-1", now.Add(time.Minute), "success", 0))
	if first.DemotionRequested || store.View().Accounts["idx-state-machine"].Demotion.HalfOpenSuccesses != 1 {
		t.Fatalf("first=%+v state=%+v", first, store.View().Accounts["idx-state-machine"])
	}
	second := handleUsage(t, usage, usageEvent("half-success-2", now.Add(2*time.Minute), "success", 0))
	if !second.DemotionRequested {
		t.Fatalf("second=%+v", second)
	}
	if err := accounts.ApplyRequestedDemotion("idx-state-machine", settings.DemotionPriority); err != nil {
		t.Fatal(err)
	}
	state = store.View().Accounts["idx-state-machine"]
	if host.files[0].Priority != 10 || state.Demotion.Class != domain.DemotionClassNone || state.Demotion.State != "restored" || state.Failure != (domain.FailureState{}) {
		t.Fatalf("priority=%d state=%+v", host.files[0].Priority, state)
	}
}

func TestHalfOpenSuccessThresholdRestoresLowBaselineWithoutRedemotion(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store, host, _, accounts, settings := newHardAppliedAccount(t, now)
	baseline := -200
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		state := snapshot.Accounts["idx-state-machine"]
		state.Demotion.BaselinePriority = &baseline
		snapshot.Accounts["idx-state-machine"] = state
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Hour)
	accounts = application.NewAccountsService(host, store, func() time.Time { return now }, settings)

	entered, err := accounts.RestorePriorityAfterCooldown("idx-state-machine")
	if err != nil || !entered {
		t.Fatalf("entered=%t err=%v", entered, err)
	}
	usage := application.NewUsageServiceWithDemotion(store, func() time.Time { return now }, settings, nil)
	handleUsage(t, usage, usageEvent("half-low-success-1", now.Add(time.Minute), "success", 0))
	second := handleUsage(t, usage, usageEvent("half-low-success-2", now.Add(2*time.Minute), "success", 0))
	if !second.DemotionRequested {
		t.Fatalf("second=%+v", second)
	}
	if err := accounts.ApplyRequestedDemotion("idx-state-machine", settings.DemotionPriority); err != nil {
		t.Fatal(err)
	}

	items, _, err := accounts.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Priority != baseline || items[0].IsDemoted || items[0].CanRestore {
		t.Fatalf("items=%+v", items)
	}
	state := store.View().Accounts["idx-state-machine"].Demotion
	if state.State != "restored" || state.Class != domain.DemotionClassNone {
		t.Fatalf("demotion=%+v", state)
	}
}

func TestHalfOpenAttributedFailureReturnsHardAndAdvancesCooldown(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	store, host, _, accounts, settings := newHardAppliedAccount(t, now)
	now = now.Add(6 * time.Hour)
	accounts = application.NewAccountsService(host, store, func() time.Time { return now }, settings)
	if entered, err := accounts.RestorePriorityAfterCooldown("idx-state-machine"); err != nil || !entered {
		t.Fatalf("entered=%t err=%v", entered, err)
	}

	usage := application.NewUsageServiceWithDemotion(store, func() time.Time { return now }, settings, nil)
	result := handleUsage(t, usage, usageEvent("half-failure", now.Add(time.Minute), "failure", 401))
	if !result.DemotionRequested {
		t.Fatalf("result=%+v", result)
	}
	if err := accounts.ApplyRequestedDemotion("idx-state-machine", settings.DemotionPriority); err != nil {
		t.Fatal(err)
	}
	state := store.View().Accounts["idx-state-machine"]
	if host.files[0].Priority != settings.DemotionPriority || state.Demotion.Class != domain.DemotionClassHard || state.Demotion.RestoreCooldownHours != 12 {
		t.Fatalf("priority=%d state=%+v", host.files[0].Priority, state)
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

func newHardAppliedAccount(t *testing.T, now time.Time) (*stateinfra.Store, *accountHost, *application.UsageService, *application.AccountsService, application.Settings) {
	t.Helper()
	store, host, usage, accounts, settings := newStateMachineServices(now)
	settings.CountStatus5XX = true
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Settings = &settings
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for hit := 1; hit <= settings.AttributedFailureThreshold; hit++ {
		handleUsage(t, usage, usageEvent(fmt.Sprintf("prepare-hard-%d", hit), now.Add(time.Duration(hit)*time.Minute), "failure", 500))
	}
	if err := accounts.ApplyRequestedDemotion("idx-state-machine", settings.DemotionPriority); err != nil {
		t.Fatal(err)
	}
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
