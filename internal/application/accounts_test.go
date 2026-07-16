package application_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/application"
	"github.com/magicvr/cpa-grok-panel/internal/cpaabi"
	"github.com/magicvr/cpa-grok-panel/internal/domain"
	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

func TestAccountsListComputesDemotionFromConfiguredPriority(t *testing.T) {
	baseline, recordedTarget := 8, -55
	store := stateinfra.OpenMemory(time.Now().UTC())
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Accounts["recorded"] = domain.AccountState{Demotion: domain.DemotionState{
			State: "applied", BaselinePriority: &baseline, TargetPriority: &recordedTarget,
		}}
		snapshot.Accounts["superseded"] = domain.AccountState{Demotion: domain.DemotionState{
			State: "applied", BaselinePriority: &baseline, TargetPriority: &recordedTarget,
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	host := &accountHost{files: []domain.AuthFile{
		xaiFile("external", "xai-external.json", -77),
		xaiFile("recorded", "xai-recorded.json", -78),
		xaiFile("superseded", "xai-superseded.json", 4),
	}}
	settings := application.DefaultSettings()
	settings.DemotionPriority = -77
	service := application.NewAccountsService(host, store, time.Now, settings)

	items, _, err := service.List("")
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]domain.AccountView, len(items))
	for _, item := range items {
		byID[item.AuthIndex] = item
	}
	if !byID["external"].IsDemoted || !byID["external"].CanRestore {
		t.Fatalf("external=%+v", byID["external"])
	}
	if !byID["recorded"].IsDemoted || !byID["recorded"].CanRestore {
		t.Fatalf("recorded=%+v", byID["recorded"])
	}
	if byID["superseded"].IsDemoted || byID["superseded"].CanRestore {
		t.Fatalf("superseded=%+v", byID["superseded"])
	}
}

func TestAccountsListDetectsBotFlagFromAccessTokens(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	host := &accountHost{
		files: []domain.AuthFile{
			xaiFile("flagged", "xai-flagged.json", 0),
			xaiFile("clean", "xai-clean.json", 0),
			xaiFile("invalid", "xai-invalid.json", 0),
			xaiFile("no-token", "xai-no-token.json", 0),
			xaiFile("credentials", "xai-credentials.json", 0),
			xaiFile("token-priority", "xai-token-priority.json", 0),
			xaiFile("nested-wins", "xai-nested-wins.json", 0),
			xaiFile("get-error", "xai-get-error.json", 0),
		},
		documents: map[string]cpaabi.AuthDocument{
			"flagged":     {"access_token": testJWT(t, map[string]any{"bot_flag_source": 1})},
			"clean":       {"access_token": testJWT(t, map[string]any{"sub": "clean"})},
			"invalid":     {"access_token": "not-a-jwt"},
			"no-token":    {"refresh_token": "present"},
			"credentials": {"credentials": map[string]any{"access_token": testJWT(t, map[string]any{"user": map[string]any{"bot_flag_source": "1"}})}},
			"token-priority": {
				"access_token": testJWT(t, map[string]any{"sub": "direct-clean"}),
				"credentials":  map[string]any{"access_token": testJWT(t, map[string]any{"bot_flag_source": 1})},
			},
			"nested-wins": {"access_token": testJWT(t, map[string]any{"bot_flag_source": 0, "bot": map[string]any{"bot_flag_source": 1}})},
		},
		getErrors: map[string]error{"get-error": errors.New("host unavailable")},
	}
	service := application.NewAccountsService(host, store, time.Now)

	items, _, err := service.List("")
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]domain.AccountView, len(items))
	for _, item := range items {
		byID[item.AuthIndex] = item
	}
	if !byID["flagged"].BotFlagged || !byID["flagged"].BotFlagKnown || fmt.Sprint(byID["flagged"].BotFlagSource) != "1" {
		t.Fatalf("flagged=%+v", byID["flagged"])
	}
	if byID["clean"].BotFlagged || !byID["clean"].BotFlagKnown {
		t.Fatalf("clean=%+v", byID["clean"])
	}
	if byID["invalid"].BotFlagged || byID["invalid"].BotFlagKnown {
		t.Fatalf("invalid=%+v", byID["invalid"])
	}
	if byID["no-token"].BotFlagged || byID["no-token"].BotFlagKnown {
		t.Fatalf("no-token=%+v", byID["no-token"])
	}
	if !byID["credentials"].BotFlagged || !byID["credentials"].BotFlagKnown || byID["credentials"].BotFlagSource != "1" {
		t.Fatalf("credentials=%+v", byID["credentials"])
	}
	if byID["token-priority"].BotFlagged || !byID["token-priority"].BotFlagKnown {
		t.Fatalf("token-priority=%+v", byID["token-priority"])
	}
	if !byID["nested-wins"].BotFlagged || !byID["nested-wins"].BotFlagKnown || fmt.Sprint(byID["nested-wins"].BotFlagSource) != "1" {
		t.Fatalf("nested-wins=%+v", byID["nested-wins"])
	}
	if byID["get-error"].BotFlagged || byID["get-error"].BotFlagKnown {
		t.Fatalf("get-error=%+v", byID["get-error"])
	}
}

func TestAccountsListFindsAccessTokenInsideNestedJSON(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	host := &accountHost{
		files: []domain.AuthFile{xaiFile("nested-json", "xai-nested-json.json", 0)},
		documents: map[string]cpaabi.AuthDocument{
			"nested-json": {"json": map[string]any{"oauth": map[string]any{"access_token": testJWT(t, map[string]any{"bot": map[string]any{"bot_flag_source": 1}})}}},
		},
	}
	service := application.NewAccountsService(host, store, time.Now)

	items, _, err := service.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !items[0].BotFlagged || !items[0].BotFlagKnown {
		t.Fatalf("items=%+v", items)
	}
}

func TestAccountsListBoundsConcurrentAuthGetsAtTen(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	files := make([]domain.AuthFile, 24)
	for index := range files {
		files[index] = xaiFile(fmt.Sprintf("idx-%02d", index), fmt.Sprintf("xai-%02d.json", index), 0)
	}
	host := &concurrentGetHost{files: files}
	settings := application.DefaultSettings()
	settings.BatchOperationConcurrency = 50
	service := application.NewAccountsService(host, store, time.Now, settings)

	items, _, err := service.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != len(files) {
		t.Fatalf("items=%d want=%d", len(items), len(files))
	}
	if maximum := host.maximum.Load(); maximum <= 1 || maximum > 10 {
		t.Fatalf("maximum concurrent host.auth.get=%d want 2..10", maximum)
	}
}

func TestSetEnabledPreservesDocumentAndVerifiesDisabled(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	host := &accountHost{
		files: []domain.AuthFile{xaiFile("idx-1", "xai-a.json", 7)},
		documents: map[string]cpaabi.AuthDocument{"idx-1": {
			"priority": 7, "disabled": false, "refresh_token": "keep-me", "nested": map[string]any{"scope": "all"},
		}},
	}
	service := application.NewAccountsService(host, store, time.Now)

	account, err := service.SetEnabled("idx-1", "xai-a.json", false)
	if err != nil {
		t.Fatal(err)
	}
	if account.Enabled || !host.files[0].Disabled {
		t.Fatalf("account=%+v file=%+v", account, host.files[0])
	}
	if host.savedName != "xai-a.json" || host.savedDocument["disabled"] != true {
		t.Fatalf("saved name=%q document=%#v", host.savedName, host.savedDocument)
	}
	if host.savedDocument["refresh_token"] != "keep-me" || host.savedDocument["nested"] == nil {
		t.Fatalf("full auth document was not preserved: %#v", host.savedDocument)
	}
}

func TestClearStateRequiresMatchingStoredFileName(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Accounts["idx-1"] = domain.AccountState{ExactFileName: "xai-a.json"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := application.NewAccountsService(&accountHost{}, store, time.Now)
	if err := service.ClearState("idx-1", "xai-other.json"); application.AsAccountError(err).Code != "account_mapping_changed" {
		t.Fatalf("error=%v", err)
	}
	if err := service.ClearState("idx-1", "xai-a.json"); err != nil {
		t.Fatal(err)
	}
	if _, exists := store.View().Accounts["idx-1"]; exists {
		t.Fatal("account state was not removed")
	}
}

func TestClearDiagnosticClearsOnlyFailureForMatchingAccount(t *testing.T) {
	now := time.Now().UTC()
	baseline, target := 10, -100
	store := stateinfra.OpenMemory(now)
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Accounts["idx-1"] = domain.AccountState{
			ExactFileName: "xai-a.json",
			Usage:         domain.UsageCounters{SuccessfulRequests: 7, FailedRequests: 3, TotalTokens: 1234},
			Failure: domain.FailureState{
				ConsecutiveAttributedFailures: 3,
				LastFailureAt:                 &now,
				LastFailureCode:               "http_500",
			},
			Demotion: domain.DemotionState{State: "applied", BaselinePriority: &baseline, TargetPriority: &target},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := application.NewAccountsService(&accountHost{}, store, time.Now)

	if err := service.ClearDiagnostic("idx-1", "xai-a.json"); err != nil {
		t.Fatal(err)
	}
	state := store.View().Accounts["idx-1"]
	if state.Failure != (domain.FailureState{}) {
		t.Fatalf("failure=%+v", state.Failure)
	}
	if state.Usage.TotalTokens != 1234 || state.Usage.SuccessfulRequests != 7 || state.Demotion.State != "applied" {
		t.Fatalf("unrelated account state changed: %+v", state)
	}
}

func TestClearDiagnosticRequiresMatchingStoredFileName(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Accounts["idx-1"] = domain.AccountState{
			ExactFileName: "xai-a.json",
			Failure:       domain.FailureState{ConsecutiveAttributedFailures: 2, LastFailureCode: "http_429"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := application.NewAccountsService(&accountHost{}, store, time.Now)

	err := service.ClearDiagnostic("idx-1", "xai-other.json")
	if application.AsAccountError(err).Code != "account_mapping_changed" {
		t.Fatalf("error=%v", err)
	}
	if got := store.View().Accounts["idx-1"].Failure.ConsecutiveAttributedFailures; got != 2 {
		t.Fatalf("failure streak=%d want=2", got)
	}
}

func TestDemoteRecordsBaselineAndUsesConfiguredTarget(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	host := &accountHost{
		files: []domain.AuthFile{xaiFile("idx-1", "xai-a.json", 12)},
		documents: map[string]cpaabi.AuthDocument{"idx-1": {
			"priority": 12, "disabled": false, "refresh_token": "keep-me",
		}},
	}
	settings := application.DefaultSettings()
	settings.DemotionPriority = -77
	service := application.NewAccountsService(host, store, time.Now, settings)

	account, err := service.Demote("idx-1", "xai-a.json")
	if err != nil {
		t.Fatal(err)
	}
	if account.Priority != -77 || !account.IsDemoted || !account.CanRestore {
		t.Fatalf("account=%+v", account)
	}
	state := store.View().Accounts["idx-1"].Demotion
	if state.State != "applied" || state.BaselinePriority == nil || *state.BaselinePriority != 12 || state.TargetPriority == nil || *state.TargetPriority != -77 {
		t.Fatalf("demotion=%+v", state)
	}
	if host.savedDocument["refresh_token"] != "keep-me" {
		t.Fatalf("full auth document was not preserved: %#v", host.savedDocument)
	}
}

func TestDemoteUsesUpdatedTarget(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	host := &accountHost{
		files:     []domain.AuthFile{xaiFile("idx-hot", "xai-hot.json", 12)},
		documents: map[string]cpaabi.AuthDocument{"idx-hot": {"priority": 12, "disabled": false}},
	}
	initial := application.DefaultSettings()
	service := application.NewAccountsService(host, store, time.Now, initial)
	updated := initial
	updated.Revision++
	updated.DemotionPriority = -250
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Settings = &updated
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	account, err := service.Demote("idx-hot", "xai-hot.json")
	if err != nil {
		t.Fatal(err)
	}
	if account.Priority != -250 {
		t.Fatalf("priority=%d", account.Priority)
	}
}

func TestDemotionWorkerUsesUpdatedTarget(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	host := &accountHost{
		files:     []domain.AuthFile{xaiFile("idx-worker", "xai-worker.json", 10)},
		documents: map[string]cpaabi.AuthDocument{"idx-worker": {"priority": 10, "disabled": false}},
	}
	initial := application.DefaultSettings()
	accounts := application.NewAccountsService(host, store, time.Now, initial)
	worker := application.NewDemotionWorker(accounts, store, initial)
	updated := initial
	updated.Revision++
	updated.DemotionPriority = -300
	baseline := 10
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Settings = &updated
		snapshot.Accounts["idx-worker"] = domain.AccountState{ExactFileName: "xai-worker.json", Demotion: domain.DemotionState{State: "requested", BaselinePriority: &baseline}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	worker.Start()
	defer worker.Stop()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if host.files[0].Priority == -300 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("priority=%d state=%+v", host.files[0].Priority, store.View().Accounts["idx-worker"].Demotion)
}

func TestApplyRequestedDemotionAlreadyAtTargetMarksApplied(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	settings := application.DefaultSettings()
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Accounts["idx-target"] = domain.AccountState{
			Failure:  domain.FailureState{ConsecutiveAttributedFailures: 4, LastFailureCode: "http_403"},
			Demotion: domain.DemotionState{State: "requested"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	host := &accountHost{
		files:     []domain.AuthFile{xaiFile("idx-target", "xai-target.json", settings.DemotionPriority)},
		documents: map[string]cpaabi.AuthDocument{"idx-target": {"priority": settings.DemotionPriority, "disabled": false}},
	}
	service := application.NewAccountsService(host, store, time.Now, settings)

	if err := service.ApplyRequestedDemotion("idx-target", settings.DemotionPriority); err != nil {
		t.Fatal(err)
	}
	state := store.View().Accounts["idx-target"].Demotion
	if state.State != "applied" || state.TargetPriority == nil || *state.TargetPriority != settings.DemotionPriority || state.BaselinePriority == nil || *state.BaselinePriority != settings.DefaultRestorePriority {
		t.Fatalf("state=%+v", state)
	}
	if failure := store.View().Accounts["idx-target"].Failure; failure.ConsecutiveAttributedFailures != 4 || failure.LastFailureCode != "http_403" {
		t.Fatalf("automatic demotion cleared failure diagnostics: %+v", failure)
	}
	if host.savedName != "" {
		t.Fatalf("already-target demotion should not save auth file: saved=%q", host.savedName)
	}
}

func TestApplyRequestedDemotionVerificationFailureIsRetryable(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	baseline := 0
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Accounts["idx-verify"] = domain.AccountState{Demotion: domain.DemotionState{State: "requested", BaselinePriority: &baseline}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	host := &accountHost{
		files:              []domain.AuthFile{xaiFile("idx-verify", "xai-verify.json", baseline)},
		documents:          map[string]cpaabi.AuthDocument{"idx-verify": {"priority": baseline, "disabled": false}},
		ignorePrioritySave: true,
	}
	service := application.NewAccountsService(host, store, time.Now)

	err := service.ApplyRequestedDemotion("idx-verify", application.DefaultSettings().DemotionPriority)
	accountErr := application.AsAccountError(err)
	if err == nil || !accountErr.Retryable || accountErr.Code != "write_verification_failed" {
		t.Fatalf("error=%+v", accountErr)
	}
	state := store.View().Accounts["idx-verify"].Demotion
	if state.State != "failed" || state.FailureCode != "demotion_verify_failed" {
		t.Fatalf("state=%+v", state)
	}
}

func TestRestorePriorityWithoutPluginRecordUsesConfiguredDefault(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	host := &accountHost{
		files:     []domain.AuthFile{xaiFile("idx-1", "xai-a.json", -78)},
		documents: map[string]cpaabi.AuthDocument{"idx-1": {"priority": -78, "disabled": false}},
	}
	settings := application.DefaultSettings()
	settings.DemotionPriority = -77
	settings.DefaultRestorePriority = 3
	service := application.NewAccountsService(host, store, time.Now, settings)

	account, err := service.RestorePriority("idx-1", "xai-a.json")
	if err != nil {
		t.Fatal(err)
	}
	if account.Priority != 3 || account.IsDemoted {
		t.Fatalf("account=%+v", account)
	}
	state := store.View().Accounts["idx-1"]
	if state.Demotion.State != "restored" || state.Demotion.BaselinePriority == nil || *state.Demotion.BaselinePriority != 3 || state.Demotion.TargetPriority == nil || *state.Demotion.TargetPriority != -77 {
		t.Fatalf("state=%+v", state)
	}
}

func TestRestorePriorityBelowConfiguredTargetUsesRecordedBaseline(t *testing.T) {
	baseline, recordedTarget := 9, -55
	store := stateinfra.OpenMemory(time.Now().UTC())
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Accounts["idx-1"] = domain.AccountState{Demotion: domain.DemotionState{
			State: "failed", BaselinePriority: &baseline, TargetPriority: &recordedTarget,
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	host := &accountHost{
		files:     []domain.AuthFile{xaiFile("idx-1", "xai-a.json", -78)},
		documents: map[string]cpaabi.AuthDocument{"idx-1": {"priority": -78, "disabled": false}},
	}
	settings := application.DefaultSettings()
	settings.DemotionPriority = -77
	service := application.NewAccountsService(host, store, time.Now, settings)

	account, err := service.RestorePriority("idx-1", "xai-a.json")
	if err != nil {
		t.Fatal(err)
	}
	if account.Priority != baseline || account.IsDemoted {
		t.Fatalf("account=%+v", account)
	}
}

func TestCooldownRestoreLadderIncrementsAndAutomaticRestorePreservesIt(t *testing.T) {
	now := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	store := stateinfra.OpenMemory(now)
	host := &accountHost{
		files:     []domain.AuthFile{xaiFile("idx-cooldown", "xai-cooldown.json", 10)},
		documents: map[string]cpaabi.AuthDocument{"idx-cooldown": {"priority": 10, "disabled": false}},
	}
	service := application.NewAccountsService(host, store, func() time.Time { return now })

	for cycle, wantHours := range []int{6, 12, 24} {
		if _, err := service.Demote("idx-cooldown", "xai-cooldown.json"); err != nil {
			t.Fatalf("cycle %d demote: %v", cycle+1, err)
		}
		demotion := store.View().Accounts["idx-cooldown"].Demotion
		if demotion.State != "applied" || demotion.RestoreCooldownHours != wantHours || demotion.TriggeredAt == nil || !demotion.TriggeredAt.Equal(now) {
			t.Fatalf("cycle %d demotion=%+v want cooldown=%d", cycle+1, demotion, wantHours)
		}
		if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
			state := snapshot.Accounts["idx-cooldown"]
			state.Failure = domain.FailureState{ConsecutiveAttributedFailures: 3, LastFailureCode: "http_403"}
			snapshot.Accounts["idx-cooldown"] = state
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Duration(wantHours) * time.Hour)
		restored, err := service.RestorePriorityAfterCooldown("idx-cooldown")
		if err != nil || !restored {
			t.Fatalf("cycle %d restored=%t err=%v", cycle+1, restored, err)
		}
		demotion = store.View().Accounts["idx-cooldown"].Demotion
		if demotion.State != "restored" || demotion.RestoreCooldownHours != wantHours || host.files[0].Priority != 10 || store.View().Accounts["idx-cooldown"].Failure != (domain.FailureState{}) {
			t.Fatalf("cycle %d state=%+v priority=%d", cycle+1, store.View().Accounts["idx-cooldown"], host.files[0].Priority)
		}
	}
}

func TestCooldownRestoreSkipsExplicitBotButManualRestoreStillWorks(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	triggeredAt := now.Add(-6 * time.Hour)
	baseline, target := 10, -100
	store := stateinfra.OpenMemory(now)
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Accounts["idx-bot"] = domain.AccountState{
			ExactFileName: "xai-bot.json",
			Failure:       domain.FailureState{ConsecutiveAttributedFailures: 3, LastFailureCode: "http_403"},
			Demotion: domain.DemotionState{
				State: "applied", BaselinePriority: &baseline, TargetPriority: &target,
				TriggeredAt: &triggeredAt, RestoreCooldownHours: 6,
			},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	host := &accountHost{
		files: []domain.AuthFile{xaiFile("idx-bot", "xai-bot.json", target)},
		documents: map[string]cpaabi.AuthDocument{"idx-bot": {
			"priority": target, "disabled": false,
			"access_token": testJWT(t, map[string]any{"bot_flag_source": 1}),
		}},
	}
	service := application.NewAccountsService(host, store, func() time.Time { return now })

	application.NewCooldownRestoreWorker(service, store).ProcessOnce()
	if host.files[0].Priority != target {
		t.Fatalf("worker restored explicit bot priority=%d", host.files[0].Priority)
	}
	if _, err := service.RestorePriority("idx-bot", "xai-bot.json"); err != nil {
		t.Fatal(err)
	}
	state := store.View().Accounts["idx-bot"]
	if host.files[0].Priority != baseline || state.Demotion.State != "restored" || state.Demotion.RestoreCooldownHours != 0 || state.Failure != (domain.FailureState{}) {
		t.Fatalf("manual restore priority=%d state=%+v", host.files[0].Priority, state)
	}
}

type accountHost struct {
	files              []domain.AuthFile
	documents          map[string]cpaabi.AuthDocument
	getErrors          map[string]error
	savedName          string
	savedDocument      cpaabi.AuthDocument
	ignorePrioritySave bool
}

type concurrentGetHost struct {
	files   []domain.AuthFile
	active  atomic.Int32
	maximum atomic.Int32
}

func (host *concurrentGetHost) ListAuthFiles() ([]domain.AuthFile, error) {
	return append([]domain.AuthFile(nil), host.files...), nil
}

func (host *concurrentGetHost) GetAuthFile(string) (cpaabi.AuthDocument, error) {
	active := host.active.Add(1)
	defer host.active.Add(-1)
	for {
		maximum := host.maximum.Load()
		if active <= maximum || host.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	time.Sleep(10 * time.Millisecond)
	return cpaabi.AuthDocument{"access_token": "invalid"}, nil
}

func (host *concurrentGetHost) SaveAuthFile(string, cpaabi.AuthDocument) error { return nil }

func (host *accountHost) ListAuthFiles() ([]domain.AuthFile, error) {
	return append([]domain.AuthFile(nil), host.files...), nil
}

func (host *accountHost) GetAuthFile(authIndex string) (cpaabi.AuthDocument, error) {
	if err := host.getErrors[authIndex]; err != nil {
		return nil, err
	}
	document := host.documents[authIndex]
	if document == nil {
		for _, file := range host.files {
			if file.AuthIndex == authIndex {
				return cpaabi.AuthDocument{"priority": file.Priority, "disabled": file.Disabled}, nil
			}
		}
	}
	return cloneDocument(document), nil
}

func testJWT(t *testing.T, payload map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(raw) + ".signature"
}

func (host *accountHost) SaveAuthFile(name string, document cpaabi.AuthDocument) error {
	host.savedName = name
	host.savedDocument = cloneDocument(document)
	for index := range host.files {
		if host.files[index].Name != name {
			continue
		}
		if disabled, ok := document["disabled"].(bool); ok {
			host.files[index].Disabled = disabled
		}
		if priority, ok := numberAsInt(document["priority"]); ok && !host.ignorePrioritySave {
			host.files[index].Priority = priority
		}
		host.documents[host.files[index].AuthIndex] = cloneDocument(document)
	}
	return nil
}

func xaiFile(authIndex, name string, priority int) domain.AuthFile {
	return domain.AuthFile{AuthIndex: authIndex, Name: name, Provider: "xai", Type: "xai", AccountType: "oauth", Priority: priority}
}

func cloneDocument(document cpaabi.AuthDocument) cpaabi.AuthDocument {
	data, _ := json.Marshal(document)
	var clone cpaabi.AuthDocument
	_ = json.Unmarshal(data, &clone)
	return clone
}

func numberAsInt(value any) (int, bool) {
	data, err := json.Marshal(value)
	if err != nil {
		return 0, false
	}
	var number int
	if err := json.Unmarshal(data, &number); err != nil {
		return 0, false
	}
	return number, true
}
