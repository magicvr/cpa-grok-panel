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

func TestUsageServiceExactDedupe(t *testing.T) {
	dir := t.TempDir()
	store, err := stateinfra.Open(dir, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := application.NewUsageService(store, time.Now)
	in := int64(1)
	event := domain.UsageEvent{AuthIndex: "a1", EventID: "same", Outcome: "success", Usage: domain.TokenUsage{Input: &in}, OccurredAt: time.Now().UTC()}
	r1, err := svc.Handle(event)
	if err != nil || r1.Duplicate {
		t.Fatalf("first=%+v err=%v", r1, err)
	}
	r2, err := svc.Handle(event)
	if err != nil || !r2.Duplicate || r2.DedupeMode != "exact" {
		t.Fatalf("second=%+v err=%v", r2, err)
	}
	snap := store.View()
	if snap.Accounts["a1"].Usage.SuccessfulRequests != 1 {
		t.Fatalf("counted twice: %+v", snap.Accounts["a1"].Usage)
	}
}

func TestUsageServiceWeakDedupe(t *testing.T) {
	dir := t.TempDir()
	store, err := stateinfra.Open(dir, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fixed := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	svc := application.NewUsageService(store, func() time.Time { return fixed })
	in := int64(2)
	event := domain.UsageEvent{AuthIndex: "a2", Outcome: "failure", StatusCode: 401, Usage: domain.TokenUsage{Input: &in}, OccurredAt: fixed}
	if _, err := svc.Handle(event); err != nil {
		t.Fatal(err)
	}
	r2, err := svc.Handle(event)
	if err != nil || !r2.Duplicate || r2.DedupeMode != "weak" {
		t.Fatalf("weak dedupe failed: %+v err=%v", r2, err)
	}
}

func TestUsageDemotion401Immediate(t *testing.T) {
	dir := t.TempDir()
	store, err := stateinfra.Open(dir, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settings := application.DefaultSettings()
	settings.AttributedFailureThreshold = 3
	svc := application.NewUsageServiceWithDemotion(store, time.Now, settings, nil)
	event := domain.UsageEvent{AuthIndex: "a1", Outcome: "failure", StatusCode: 401, Provider: "xai", OccurredAt: time.Now().UTC()}
	result, err := svc.Handle(event)
	if err != nil {
		t.Fatal(err)
	}
	if !result.DemotionRequested {
		t.Fatalf("401 should demote immediately: %+v state=%+v", result, store.View().Accounts["a1"].Demotion)
	}
}

func TestUsageDemotion429NeedsThreshold(t *testing.T) {
	dir := t.TempDir()
	store, err := stateinfra.Open(dir, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settings := application.DefaultSettings()
	settings.AttributedFailureThreshold = 3
	settings.CountStatus429 = true
	svc := application.NewUsageServiceWithDemotion(store, time.Now, settings, nil)
	for i := 1; i <= 2; i++ {
		event := domain.UsageEvent{AuthIndex: "a2", EventID: fmt.Sprintf("e%d", i), Outcome: "failure", StatusCode: 429, Provider: "xai", OccurredAt: time.Now().UTC()}
		result, err := svc.Handle(event)
		if err != nil {
			t.Fatal(err)
		}
		if result.DemotionRequested {
			t.Fatalf("429 should not demote before threshold on hit %d", i)
		}
	}
	event := domain.UsageEvent{AuthIndex: "a2", EventID: "e3", Outcome: "failure", StatusCode: 429, Provider: "xai", OccurredAt: time.Now().UTC()}
	result, err := svc.Handle(event)
	if err != nil {
		t.Fatal(err)
	}
	if !result.DemotionRequested {
		t.Fatalf("429 should demote at threshold: streak=%d", store.View().Accounts["a2"].Failure.ConsecutiveAttributedFailures)
	}
}

func TestUsageDemotionSuccessClearsStreak(t *testing.T) {
	dir := t.TempDir()
	store, err := stateinfra.Open(dir, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settings := application.DefaultSettings()
	settings.AttributedFailureThreshold = 3
	settings.CountStatus429 = true
	svc := application.NewUsageServiceWithDemotion(store, time.Now, settings, nil)
	for i := 1; i <= 2; i++ {
		_, _ = svc.Handle(domain.UsageEvent{AuthIndex: "a3", EventID: fmt.Sprintf("f%d", i), Outcome: "failure", StatusCode: 429, Provider: "xai", OccurredAt: time.Now().UTC()})
	}
	_, _ = svc.Handle(domain.UsageEvent{AuthIndex: "a3", EventID: "ok", Outcome: "success", Provider: "xai", OccurredAt: time.Now().UTC()})
	if store.View().Accounts["a3"].Failure.ConsecutiveAttributedFailures != 0 {
		t.Fatalf("success should clear streak")
	}
	result, err := svc.Handle(domain.UsageEvent{AuthIndex: "a3", EventID: "f3", Outcome: "failure", StatusCode: 429, Provider: "xai", OccurredAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if result.DemotionRequested {
		t.Fatalf("single 429 after clear should not demote")
	}
}

func TestAccountsFilterNonXAI(t *testing.T) {
	dir := t.TempDir()
	store, err := stateinfra.Open(dir, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	lister := fakeLister{files: []domain.AuthFile{
		{AuthIndex: "1", Name: "xai-a.json", Provider: "xai", Type: "xai", AccountType: "oauth"},
		{AuthIndex: "2", Name: "openai.json", Provider: "openai", Type: "openai", AccountType: "oauth"},
	}}
	svc := application.NewAccountsService(lister, store, time.Now)
	items, _, err := svc.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].AuthIndex != "1" {
		t.Fatalf("items=%+v", items)
	}
}

type fakeLister struct{ files []domain.AuthFile }

func (f fakeLister) ListAuthFiles() ([]domain.AuthFile, error) { return f.files, nil }
func (fakeLister) GetAuthFile(string) (cpaabi.AuthDocument, error) {
	return cpaabi.AuthDocument{}, nil
}
func (fakeLister) SaveAuthFile(string, cpaabi.AuthDocument) error { return nil }
