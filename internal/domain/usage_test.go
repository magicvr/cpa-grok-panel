package domain_test

import (
	"testing"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/domain"
)

func TestApplyUsageExactAndWeakTokens(t *testing.T) {
	var counters domain.UsageCounters
	in, out, total := int64(10), int64(5), int64(15)
	event := domain.UsageEvent{
		AuthIndex:  "abc",
		EventID:    "e1",
		Outcome:    "success",
		OccurredAt: time.Now().UTC(),
		Usage:      domain.TokenUsage{Input: &in, Output: &out, Total: &total},
	}
	if err := domain.ApplyUsage(&counters, event); err != nil {
		t.Fatal(err)
	}
	if counters.SuccessfulRequests != 1 || counters.TotalTokens != 15 || counters.DedupeMode != "exact" {
		t.Fatalf("unexpected counters: %+v", counters)
	}
}

func TestIsXAIOAuth(t *testing.T) {
	if !domain.IsXAIOAuth(domain.AuthFile{Provider: "xai", Type: "xai", AccountType: "oauth", Name: "a.json", AuthIndex: "1"}) {
		t.Fatal("expected xai oauth")
	}
	if domain.IsXAIOAuth(domain.AuthFile{Provider: "openai", Type: "openai", AccountType: "oauth"}) {
		t.Fatal("openai should be excluded")
	}
}

func TestProjectAccountManaged(t *testing.T) {
	view := domain.ProjectAccount(domain.AuthFile{
		AuthIndex: "idx", Name: "xai-a.json", Provider: "xai", Type: "xai",
		AccountType: "oauth", Priority: 0, Disabled: false,
	}, domain.AccountState{}, time.Now().UTC(), -77)
	if view.WriteMode != "managed" || view.ExactFileName != "xai-a.json" || view.AuthIndex != "idx" {
		t.Fatalf("%+v", view)
	}
}

func TestLegacyAppliedDemotionNormalizesToHard(t *testing.T) {
	// Pre-v0.5 empty class with applied state migrates to dead (was hard).
	demotion := (domain.DemotionState{State: "applied"}).Normalized()
	if demotion.Class != domain.DemotionClassDead {
		t.Fatalf("demotion=%+v", demotion)
	}
}

func TestProjectAccountExposesDebtAndProbeBasedIsDemoted(t *testing.T) {
	// v0.7.0: is_demoted from probe_status (non-live, non-unknown); can_restore always false.
	view := domain.ProjectAccount(domain.AuthFile{
		AuthIndex: "idx", Name: "xai-a.json", Provider: "xai", Type: "xai",
		AccountType: "oauth", Priority: -50,
	}, domain.AccountState{
		Failure: domain.FailureState{DebtScore: 3.25},
		Quota:   domain.QuotaSnapshot{ProbeStatus: domain.ProbeStatusInvalid},
	}, time.Now().UTC(), -100)
	if view.DebtScore != 3.25 || !view.IsDemoted || view.CanRestore {
		t.Fatalf("view=%+v", view)
	}
	// legacy demotion class still exposed for JSON but does not drive is_demoted alone
	view2 := domain.ProjectAccount(domain.AuthFile{
		AuthIndex: "idx", Name: "xai-a.json", Provider: "xai", Type: "xai",
		AccountType: "oauth", Priority: -10,
	}, domain.AccountState{
		Failure:  domain.FailureState{DebtScore: 1},
		Demotion: domain.DemotionState{State: "applied", Class: domain.DemotionClassSoft},
	}, time.Now().UTC(), -100)
	if view2.IsDemoted || view2.CanRestore {
		t.Fatalf("unknown probe must not demote from legacy class alone: %+v", view2)
	}
	if view2.Class != domain.DemotionClassWatch {
		t.Fatalf("soft still normalizes to watch for JSON: class=%s", view2.Class)
	}
}

func TestProjectAccountIsDemotedFromProbeStatus(t *testing.T) {
	tests := []struct {
		name      string
		status    string
		isDemoted bool
	}{
		{name: "empty unknown", status: "", isDemoted: false},
		{name: "unknown", status: domain.ProbeStatusUnknown, isDemoted: false},
		{name: "live", status: domain.ProbeStatusLive, isDemoted: false},
		{name: "invalid", status: domain.ProbeStatusInvalid, isDemoted: true},
		{name: "dead", status: domain.ProbeStatusDead, isDemoted: true},
		{name: "throttled", status: domain.ProbeStatusThrottled, isDemoted: true},
		{name: "error", status: domain.ProbeStatusError, isDemoted: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			view := domain.ProjectAccount(domain.AuthFile{
				AuthIndex: "idx", Name: "xai-a.json", Provider: "xai", Type: "xai",
				AccountType: "oauth", Priority: 0,
			}, domain.AccountState{Quota: domain.QuotaSnapshot{ProbeStatus: test.status}}, time.Now().UTC(), -100)
			if view.IsDemoted != test.isDemoted || view.CanRestore {
				t.Fatalf("status=%q is_demoted=%t can_restore=%t", test.status, view.IsDemoted, view.CanRestore)
			}
		})
	}
}

func TestProjectAccountUnknownProbeNotDemoted(t *testing.T) {
	view := domain.ProjectAccount(domain.AuthFile{
		AuthIndex: "idx", Name: "xai-a.json", Provider: "xai", Type: "xai",
		AccountType: "oauth", Priority: -200,
	}, domain.AccountState{}, time.Now().UTC(), -100)
	if view.IsDemoted || view.CanRestore {
		t.Fatalf("empty probe must not be demoted: %+v", view)
	}
}

func TestApplyHostRequestDisplayPrefersHostDeltaWhenHigher(t *testing.T) {
	// baseline=0 → host delta equals host totals; host > plugin → show host.
	baseline := &domain.HostRequestBaseline{Success: 0, Failed: 0}
	usage := domain.UsageCounters{
		SuccessfulRequests: 2, FailedRequests: 1,
		TotalTokens: 99, InputTokens: 40, OutputTokens: 59,
	}
	got := domain.ApplyHostRequestDisplay(usage, 10, 4, baseline)
	if got.SuccessfulRequests != 10 || got.FailedRequests != 4 {
		t.Fatalf("expected host delta: %+v", got)
	}
	if got.TotalTokens != 99 || got.InputTokens != 40 || got.OutputTokens != 59 {
		t.Fatalf("tokens must stay plugin-only: %+v", got)
	}
}

func TestApplyHostRequestDisplayKeepsPluginWhenHigherThanHostDelta(t *testing.T) {
	baseline := &domain.HostRequestBaseline{Success: 100, Failed: 50}
	usage := domain.UsageCounters{SuccessfulRequests: 7, FailedRequests: 3, TotalTokens: 500}
	// host 105/52 → delta 5/2 < plugin 7/3
	got := domain.ApplyHostRequestDisplay(usage, 105, 52, baseline)
	if got.SuccessfulRequests != 7 || got.FailedRequests != 3 {
		t.Fatalf("expected plugin counts retained: %+v", got)
	}
	if got.TotalTokens != 500 {
		t.Fatalf("tokens must stay plugin-only: %+v", got)
	}
}

func TestApplyHostRequestDisplayAfterPeriodRebindShowsNearZero(t *testing.T) {
	// After daily reset: baseline rebound to current host lifetime → delta ~0.
	baseline := &domain.HostRequestBaseline{Success: 9000, Failed: 120}
	usage := domain.UsageCounters{SuccessfulRequests: 0, FailedRequests: 0, TotalTokens: 0}
	got := domain.ApplyHostRequestDisplay(usage, 9000, 120, baseline)
	if got.SuccessfulRequests != 0 || got.FailedRequests != 0 {
		t.Fatalf("after rebind display should be ~0: %+v", got)
	}
}

func TestBindHostRequestBaselineUpgradeUsesZeroWhenPluginHasCounts(t *testing.T) {
	period := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	state := domain.AccountState{
		Usage: domain.UsageCounters{SuccessfulRequests: 5, FailedRequests: 1, PeriodStartedAt: period},
	}
	got := domain.BindHostRequestBaseline(state, 80, 9, period)
	if got.Success != 0 || got.Failed != 0 || !got.BoundPeriodStartedAt.Equal(period) {
		t.Fatalf("upgrade baseline should be zero: %+v", got)
	}
}

func TestBindHostRequestBaselineNewPeriodUsesHostSnapshot(t *testing.T) {
	oldPeriod := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	newPeriod := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	state := domain.AccountState{
		Usage: domain.UsageCounters{PeriodStartedAt: newPeriod},
		HostRequestBaseline: &domain.HostRequestBaseline{
			Success: 10, Failed: 2, BoundPeriodStartedAt: oldPeriod,
		},
	}
	got := domain.BindHostRequestBaseline(state, 9000, 120, newPeriod)
	if got.Success != 9000 || got.Failed != 120 || !got.BoundPeriodStartedAt.Equal(newPeriod) {
		t.Fatalf("period change must rebind host snapshot: %+v", got)
	}
}

func TestProjectAccountAppliesHostDeltaDisplayOnly(t *testing.T) {
	period := time.Date(2026, 7, 22, 1, 0, 0, 0, time.UTC)
	view := domain.ProjectAccount(domain.AuthFile{
		AuthIndex: "idx", Name: "xai-a.json", Provider: "xai", Type: "xai",
		AccountType: "oauth", Priority: 0, Success: 50, Failed: 3,
	}, domain.AccountState{
		Usage: domain.UsageCounters{
			SuccessfulRequests: 2, FailedRequests: 1, TotalTokens: 42, PeriodStartedAt: period,
		},
		HostRequestBaseline: &domain.HostRequestBaseline{Success: 0, Failed: 0, BoundPeriodStartedAt: period},
	}, period, -100)
	if view.Usage.SuccessfulRequests != 50 || view.Usage.FailedRequests != 3 {
		t.Fatalf("display counters=%+v", view.Usage)
	}
	if view.Usage.TotalTokens != 42 {
		t.Fatalf("tokens should remain plugin: %+v", view.Usage)
	}
}

func TestNeedsHostRequestBaselineBind(t *testing.T) {
	period := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	if !domain.NeedsHostRequestBaselineBind(domain.AccountState{}) {
		t.Fatal("nil baseline needs bind")
	}
	state := domain.AccountState{
		Usage:               domain.UsageCounters{PeriodStartedAt: period},
		HostRequestBaseline: &domain.HostRequestBaseline{BoundPeriodStartedAt: period},
	}
	if domain.NeedsHostRequestBaselineBind(state) {
		t.Fatal("matching period should not rebind")
	}
	state.Usage.PeriodStartedAt = period.Add(time.Hour)
	if !domain.NeedsHostRequestBaselineBind(state) {
		t.Fatal("period mismatch needs rebind")
	}
}
