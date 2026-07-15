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

func TestProjectAccountReadOnly(t *testing.T) {
	view := domain.ProjectAccount(domain.AuthFile{
		AuthIndex: "idx", Name: "xai-a.json", Provider: "xai", Type: "xai",
		AccountType: "oauth", Priority: 0, Disabled: false,
	}, domain.AccountState{}, time.Now().UTC())
	if view.WriteMode != "read_only" || view.ExactFileName != "xai-a.json" || view.AuthIndex != "idx" {
		t.Fatalf("%+v", view)
	}
}
