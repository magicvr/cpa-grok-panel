package application_test

import (
	"testing"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/application"
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
