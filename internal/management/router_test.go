package management_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/application"
	"github.com/magicvr/cpa-grok-panel/internal/cpaabi"
	"github.com/magicvr/cpa-grok-panel/internal/domain"
	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
	"github.com/magicvr/cpa-grok-panel/internal/management"
)

func TestRouterRejectsUnknownWriteRoute(t *testing.T) {
	dir := t.TempDir()
	store, err := stateinfra.Open(dir, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	router := management.NewRouter(application.NewAccountsService(fakeLister{}, store, time.Now), store)
	resp := router.Handle(management.Request{Method: "POST", Path: "/v0/management/cpa-grok-panel/api/v1/accounts"})
	if resp.StatusCode != 404 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if !strings.Contains(string(resp.Body), "not_found") {
		t.Fatalf("body=%s", resp.Body)
	}
	if len(resp.Headers["Content-Type"]) == 0 {
		t.Fatal("headers must be multi-value map")
	}
}

func TestRouterPanelPath(t *testing.T) {
	dir := t.TempDir()
	store, err := stateinfra.Open(dir, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	router := management.NewRouter(application.NewAccountsService(fakeLister{}, store, time.Now), store)
	resp := router.Handle(management.Request{Method: "GET", Path: "/v0/resource/plugins/cpa-grok-panel/panel"})
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if !strings.Contains(string(resp.Body), "Grok") {
		t.Fatalf("not html panel: %s", string(resp.Body)[:80])
	}
}

func TestRouterSetEnabled(t *testing.T) {
	store, err := stateinfra.Open(t.TempDir(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	host := &writableHost{files: []domain.AuthFile{{
		AuthIndex: "idx-1", Name: "xai-a.json", Provider: "xai", Type: "xai", AccountType: "oauth",
	}}}
	router := management.NewRouter(application.NewAccountsService(host, store, time.Now), store)
	body := []byte(`{"auth_index":"idx-1","exact_file_name":"xai-a.json","enabled":false}`)
	response := router.Handle(management.Request{Method: "POST", Path: "/v0/management/cpa-grok-panel/api/v1/accounts/set-enabled", Body: body})
	if response.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	if !host.files[0].Disabled {
		t.Fatalf("file was not disabled: %+v", host.files[0])
	}
}

func TestRouterRestorePriority(t *testing.T) {
	store, err := stateinfra.Open(t.TempDir(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	baseline, target := 10, -100
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Accounts["idx-1"] = domain.AccountState{Demotion: domain.DemotionState{
			State: "applied", BaselinePriority: &baseline, TargetPriority: &target,
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	host := &writableHost{files: []domain.AuthFile{{
		AuthIndex: "idx-1", Name: "xai-a.json", Provider: "xai", Type: "xai", AccountType: "oauth", Priority: target,
	}}}
	router := management.NewRouter(application.NewAccountsService(host, store, time.Now), store)
	body := []byte(`{"auth_index":"idx-1","exact_file_name":"xai-a.json"}`)
	response := router.Handle(management.Request{Method: "POST", Path: "/v0/management/cpa-grok-panel/api/v1/accounts/restore-priority", Body: body})
	if response.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	if host.files[0].Priority != baseline {
		t.Fatalf("priority=%d want=%d", host.files[0].Priority, baseline)
	}
	if state := store.View().Accounts["idx-1"].Demotion.State; state != "restored" {
		t.Fatalf("demotion state=%s", state)
	}
}

func TestRouterDemote(t *testing.T) {
	store, err := stateinfra.Open(t.TempDir(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	host := &writableHost{files: []domain.AuthFile{{
		AuthIndex: "idx-1", Name: "xai-a.json", Provider: "xai", Type: "xai", AccountType: "oauth", Priority: 10,
	}}}
	router := management.NewRouter(application.NewAccountsService(host, store, time.Now), store)
	body := []byte(`{"auth_index":"idx-1","exact_file_name":"xai-a.json"}`)
	response := router.Handle(management.Request{Method: "POST", Path: "/v0/management/cpa-grok-panel/api/v1/accounts/demote", Body: body})
	if response.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	if host.files[0].Priority != -100 {
		t.Fatalf("priority=%d", host.files[0].Priority)
	}
	state := store.View().Accounts["idx-1"].Demotion
	if state.State != "applied" || state.BaselinePriority == nil || *state.BaselinePriority != 10 {
		t.Fatalf("demotion=%+v", state)
	}
}

type fakeLister struct{}

func (fakeLister) ListAuthFiles() ([]domain.AuthFile, error) { return nil, nil }
func (fakeLister) GetAuthFile(string) (cpaabi.AuthDocument, error) {
	return cpaabi.AuthDocument{}, nil
}
func (fakeLister) SaveAuthFile(string, cpaabi.AuthDocument) error { return nil }

type writableHost struct{ files []domain.AuthFile }

func (host *writableHost) ListAuthFiles() ([]domain.AuthFile, error) {
	return append([]domain.AuthFile(nil), host.files...), nil
}

func (host *writableHost) GetAuthFile(authIndex string) (cpaabi.AuthDocument, error) {
	for _, file := range host.files {
		if file.AuthIndex == authIndex {
			return cpaabi.AuthDocument{"disabled": file.Disabled, "priority": file.Priority}, nil
		}
	}
	return cpaabi.AuthDocument{}, nil
}

func (host *writableHost) SaveAuthFile(name string, document cpaabi.AuthDocument) error {
	for index := range host.files {
		if host.files[index].Name != name {
			continue
		}
		if disabled, ok := document["disabled"].(bool); ok {
			host.files[index].Disabled = disabled
		}
		if raw, err := json.Marshal(document["priority"]); err == nil {
			_ = json.Unmarshal(raw, &host.files[index].Priority)
		}
	}
	return nil
}
