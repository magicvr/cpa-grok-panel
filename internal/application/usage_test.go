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

func TestParseCPAUsageRecordCounts401TowardThreshold(t *testing.T) {
	payload := []byte(`{"AuthIndex":"a1","Provider":"xai","AuthType":"oauth","Failed":true,"Failure":{"StatusCode":401,"Body":"unauthorized"},"Detail":{"InputTokens":0,"OutputTokens":0,"TotalTokens":0}}`)
	event, err := application.ParseUsageEvent(payload)
	if err != nil {
		t.Fatal(err)
	}
	if event.AuthIndex != "a1" || event.Provider != "xai" || event.AuthType != "oauth" || event.Outcome != "failure" || event.StatusCode != 401 {
		t.Fatalf("event=%+v", event)
	}
	if event.Usage.Input == nil || *event.Usage.Input != 0 || event.Usage.Output == nil || *event.Usage.Output != 0 || event.Usage.Total == nil || *event.Usage.Total != 0 {
		t.Fatalf("usage=%+v", event.Usage)
	}

	store := stateinfra.OpenMemory(time.Now().UTC())
	service := application.NewUsageServiceWithDemotion(store, time.Now, application.DefaultSettings(), nil)
	for hit := 1; hit <= 3; hit++ {
		event.EventID = fmt.Sprintf("401-%d", hit)
		result, err := service.Handle(event)
		if err != nil {
			t.Fatal(err)
		}
		// debt_fail_401=1.5, threshold=2.0 → probe at hit 2; debt zeroed then hit 3 accrues again without threshold
		if hit == 2 && !result.ProbeRequested {
			t.Fatalf("hit=%d expected probe: %+v", hit, result)
		}
		if result.DemotionRequested {
			t.Fatalf("hit=%d should not direct demote: %+v", hit, result)
		}
	}
}


func TestParseCPAUsageRecordFieldCompatibility(t *testing.T) {
	requestedAt := "2026-07-16T12:34:56.789Z"
	event, err := application.ParseUsageEvent([]byte(`{"AuthIndex":"","AuthID":"auth-fallback","Provider":"xai","AuthType":"oauth","ExecutorType":"xai-responses","Model":"grok-4","Failed":false,"RequestedAt":"` + requestedAt + `","Detail":{"InputTokens":11,"OutputTokens":7,"TotalTokens":18}}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.AuthIndex != "auth-fallback" || event.Outcome != "success" || event.ExecutorType != "xai-responses" || event.Model != "grok-4" || event.OccurredAt.Format(time.RFC3339Nano) != requestedAt {
		t.Fatalf("event=%+v", event)
	}

	legacy, err := application.ParseUsageEvent([]byte(`{"auth_index":"legacy","AuthID":"ignored","outcome":"failure","status_code":429,"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}}`))
	if err != nil {
		t.Fatal(err)
	}
	if legacy.AuthIndex != "legacy" || legacy.Outcome != "failure" || legacy.StatusCode != 429 || legacy.Usage.Total == nil || *legacy.Usage.Total != 7 {
		t.Fatalf("legacy=%+v", legacy)
	}

	preferred, err := application.ParseUsageEvent([]byte(`{"AuthIndex":"primary","AuthID":"secondary","Failed":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if preferred.AuthIndex != "primary" || preferred.Outcome != "success" {
		t.Fatalf("preferred=%+v", preferred)
	}
}

func TestParseUsageEventFailureBodyAndUnknownOutcome(t *testing.T) {
	event, err := application.ParseUsageEvent([]byte(`{"AuthID":"a1","Failure":{"StatusCode":0,"Body":"request rejected with HTTP 403"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.StatusCode != 403 || event.Outcome != "failure" {
		t.Fatalf("event=%+v", event)
	}

	unknown, err := application.ParseUsageEvent([]byte(`{"AuthIndex":"a2","Provider":"xai"}`))
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Outcome == "success" {
		t.Fatalf("event without success evidence was marked successful: %+v", unknown)
	}

	contradictory, err := application.ParseUsageEvent([]byte(`{"AuthIndex":"a3","Failed":false,"Failure":{"StatusCode":401}}`))
	if err != nil {
		t.Fatal(err)
	}
	if contradictory.Outcome != "failure" || contradictory.StatusCode != 401 {
		t.Fatalf("status failure evidence must override Failed=false: %+v", contradictory)
	}
}

func TestUsageServiceRejectsEmptyAuthIndex(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	service := application.NewUsageService(store, time.Now)

	result, err := service.Handle(domain.UsageEvent{AuthIndex: "  ", Outcome: "success"})
	if err == nil || result.Accepted || len(store.View().Accounts) != 0 {
		t.Fatalf("result=%+v err=%v accounts=%+v", result, err, store.View().Accounts)
	}
}

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

func TestUsageDemotion401NeedsThreshold(t *testing.T) {
	// v0.7.0: only debt threshold triggers auto probe (no streak path).
	dir := t.TempDir()
	store, err := stateinfra.Open(dir, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settings := application.DefaultSettings()
	settings.AttributedFailureThreshold = 3
	settings.DebtProbeThreshold = 3.0 // 1.5+1.5=3 → probe on 2nd 401
	settings.AttributedFailureStatuses = nil
	settings.CountStatus429 = false
	settings.CountStatus5XX = false
	svc := application.NewUsageServiceWithDemotion(store, time.Now, settings, nil)
	for hit := 1; hit <= 3; hit++ {
		event := domain.UsageEvent{AuthIndex: "a1", EventID: fmt.Sprintf("401-%d", hit), Outcome: "failure", StatusCode: 401, Provider: "xai", OccurredAt: time.Now().UTC()}
		result, err := svc.Handle(event)
		if err != nil {
			t.Fatal(err)
		}
		if result.DemotionRequested {
			t.Fatalf("hit=%d should not direct-demote: %+v", hit, result)
		}
		// hit1: debt=1.5; hit2: debt=3→probe+zero; hit3: debt=1.5 no probe (streak ignored)
		wantProbe := hit == 2
		if result.ProbeRequested != wantProbe {
			t.Fatalf("hit=%d result=%+v debt=%v streak=%d", hit, result, store.View().Accounts["a1"].Failure.DebtScore, store.View().Accounts["a1"].Failure.ConsecutiveAttributedFailures)
		}
	}
}

func TestAppliedDemotionPriorityDriftReconcilesToRequested(t *testing.T) {
	t.Skip("v0.7.0: demotion class drift reconciliation removed")
}

func TestUsageDemotion429NeedsThreshold(t *testing.T) {
	// v0.7.0: debt threshold only (streak path removed). 429 weight 0.5 → probe after 4 hits at threshold 2.0.
	dir := t.TempDir()
	store, err := stateinfra.Open(dir, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settings := application.DefaultSettings()
	settings.AttributedFailureThreshold = 3
	settings.DebtProbeThreshold = 2.0
	settings.DebtFail429 = 0.5
	settings.CountStatus429 = true
	svc := application.NewUsageServiceWithDemotion(store, time.Now, settings, nil)
	for i := 1; i <= 3; i++ {
		event := domain.UsageEvent{AuthIndex: "a2", EventID: fmt.Sprintf("e%d", i), Outcome: "failure", StatusCode: 429, Provider: "xai", OccurredAt: time.Now().UTC()}
		result, err := svc.Handle(event)
		if err != nil {
			t.Fatal(err)
		}
		if result.DemotionRequested || result.ProbeRequested {
			t.Fatalf("429 should not probe before debt threshold on hit %d debt=%v", i, store.View().Accounts["a2"].Failure.DebtScore)
		}
	}
	event := domain.UsageEvent{AuthIndex: "a2", EventID: "e4", Outcome: "failure", StatusCode: 429, Provider: "xai", OccurredAt: time.Now().UTC()}
	result, err := svc.Handle(event)
	if err != nil {
		t.Fatal(err)
	}
	if result.DemotionRequested || !result.ProbeRequested {
		t.Fatalf("429 should probe at debt threshold: result=%+v debt=%v", result, store.View().Accounts["a2"].Failure.DebtScore)
	}
}

func TestUsageDemotionUsesUpdatedSettings(t *testing.T) {
	// v0.7.0: lowering debt_probe_threshold + enabling 429 scoring must take effect hot.
	store := stateinfra.OpenMemory(time.Now().UTC())
	initial := application.DefaultSettings()
	initial.AttributedFailureThreshold = 10
	initial.DebtProbeThreshold = 100
	initial.DebtFail429 = 0.5
	initial.CountStatus429 = false
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Settings = &initial
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	svc := application.NewUsageServiceWithDemotion(store, time.Now, initial, nil)
	first := domain.UsageEvent{AuthIndex: "hot", EventID: "before", Outcome: "failure", StatusCode: 429, Provider: "xai", OccurredAt: time.Now().UTC()}
	if result, err := svc.Handle(first); err != nil || result.DemotionRequested || result.ProbeRequested {
		t.Fatalf("before update result=%+v err=%v", result, err)
	}

	updated := initial
	updated.Revision++
	updated.DebtProbeThreshold = 1.0
	updated.CountStatus429 = true
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Settings = &updated
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// debt starts 0 (429 not counted before); first counted 429 adds 0.5 < 1; second reaches 1.0 → probe
	for index := 1; index <= 2; index++ {
		event := domain.UsageEvent{AuthIndex: "hot", EventID: fmt.Sprintf("after-%d", index), Outcome: "failure", StatusCode: 429, Provider: "xai", OccurredAt: time.Now().UTC()}
		result, err := svc.Handle(event)
		if err != nil {
			t.Fatal(err)
		}
		if result.DemotionRequested {
			t.Fatalf("hit=%d should not direct demote: %+v", index, result)
		}
		if (index == 2) != result.ProbeRequested {
			t.Fatalf("hit=%d result=%+v state=%+v", index, result, store.View().Accounts["hot"])
		}
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
