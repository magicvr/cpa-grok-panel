package application_test

import (
	"encoding/json"
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
		snapshot.Accounts["idx-target"] = domain.AccountState{Demotion: domain.DemotionState{State: "requested"}}
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

type accountHost struct {
	files              []domain.AuthFile
	documents          map[string]cpaabi.AuthDocument
	savedName          string
	savedDocument      cpaabi.AuthDocument
	ignorePrioritySave bool
}

func (host *accountHost) ListAuthFiles() ([]domain.AuthFile, error) {
	return append([]domain.AuthFile(nil), host.files...), nil
}

func (host *accountHost) GetAuthFile(authIndex string) (cpaabi.AuthDocument, error) {
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
