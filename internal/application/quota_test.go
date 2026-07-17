package application_test

import (
	"testing"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/application"
	"github.com/magicvr/cpa-grok-panel/internal/domain"
)

func TestClassifyPlan(t *testing.T) {
	if application.ClassifyPlan("super_grok_heavy") != "SuperGrok Heavy" {
		t.Fatal("heavy")
	}
	if application.ClassifyPlan("SuperGrok") != "SuperGrok" {
		t.Fatal("supergrok")
	}
	if application.ClassifyPlan("") != "Free" {
		t.Fatal("empty success => Free")
	}
	if application.ClassifyPlan("something else") != "Free" {
		t.Fatal("other => Free")
	}
}

func TestDisplayQuotaPreservesCachedPlan(t *testing.T) {
	state := domain.AccountState{
		Usage: domain.UsageCounters{TotalTokens: 123},
		Quota: domain.QuotaSnapshot{Plan: "SuperGrok", Used: 1, Limit: 10, Source: "billing"},
	}
	got := application.DisplayQuota(state, 2_000_000)
	if got.Plan != "SuperGrok" || got.Limit != 10 {
		t.Fatalf("paid plan mutated: %+v", got)
	}
	state.Quota = domain.QuotaSnapshot{Plan: "Free", Source: "local_estimate"}
	got = application.DisplayQuota(state, 2_000_000)
	if got.Plan != "Free" || got.Used != 123 || got.Limit != 2_000_000 {
		t.Fatalf("free display: %+v", got)
	}
	state.Quota = domain.QuotaSnapshot{} // never refreshed
	got = application.DisplayQuota(state, 5_000)
	if got.Plan != "unknown" {
		t.Fatalf("default plan should stay unknown, got %+v", got)
	}
}

func TestParseBillingQuotaSuccessFreeNoLimit(t *testing.T) {
	raw := []byte(`{"status_code":200,"body":{"config":{"monthlyLimit":{"val":0},"used":{"val":0}}}}`)
	q, err := application.ParseBillingQuota(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if q.Plan != "Free" {
		t.Fatalf("plan=%s", q.Plan)
	}
	_ = time.Now()
}
