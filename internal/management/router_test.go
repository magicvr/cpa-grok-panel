package management_test

import (
	"strings"
	"testing"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/application"
	"github.com/magicvr/cpa-grok-panel/internal/domain"
	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
	"github.com/magicvr/cpa-grok-panel/internal/management"
)

func TestRouterReadOnlyRejectsWrite(t *testing.T) {
	dir := t.TempDir()
	store, err := stateinfra.Open(dir, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	router := management.NewRouter(application.NewAccountsService(fakeLister{}, store, time.Now), store)
	resp := router.Handle(management.Request{Method: "POST", Path: "/v0/management/cpa-grok-panel/api/v1/accounts"})
	if resp.StatusCode != 405 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if !strings.Contains(string(resp.Body), "read_only") {
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

type fakeLister struct{}

func (fakeLister) ListAuthFiles() ([]domain.AuthFile, error) { return nil, nil }
