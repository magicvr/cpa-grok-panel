package application_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/application"
	"github.com/magicvr/cpa-grok-panel/internal/cpaabi"
	"github.com/magicvr/cpa-grok-panel/internal/domain"
	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

func TestResignRefreshTokenSuccessUpdatesAuthFile(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	host := &accountHost{
		files: []domain.AuthFile{xaiFile("idx-resign", "xai-resign.json", 5)},
		documents: map[string]cpaabi.AuthDocument{
			"idx-resign": {
				"access_token":  "old-access",
				"refresh_token": "rt-old",
				"id_token":      "old-id",
				"email":         "user@example.com",
				"priority":      5,
				"disabled":      false,
				"client_id":     "client-from-file",
				"sso":           map[string]any{"keep": true},
			},
		},
	}
	service := application.NewAccountsService(host, store, time.Now().UTC)
	var gotForm string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "application/x-www-form-urlencoded") {
			t.Fatalf("content-type=%q", ct)
		}
		body, _ := io.ReadAll(r.Body)
		gotForm = string(body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access",
			"refresh_token": "rt-new",
			"id_token":      "new-id",
			"expires_in":    3600,
			"token_type":    "Bearer",
		})
	}))
	defer server.Close()
	service.SetTokenRefresher(&application.HTTPTokenRefresher{URL: server.URL, Client: server.Client()})

	// Preserve demotion state across resign.
	if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		snapshot.Accounts["idx-resign"] = domain.AccountState{
			ExactFileName: "xai-resign.json",
			Demotion: domain.DemotionState{
				State: "applied", Class: domain.DemotionClassHard, BaselinePriority: intPtr(5),
			},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	view, err := service.Resign("idx-resign", "xai-resign.json")
	if err != nil {
		t.Fatalf("Resign: %v", err)
	}
	if view.AuthIndex != "idx-resign" {
		t.Fatalf("view=%+v", view)
	}
	if !strings.Contains(gotForm, "grant_type=refresh_token") || !strings.Contains(gotForm, "refresh_token=rt-old") || !strings.Contains(gotForm, "client_id=client-from-file") {
		t.Fatalf("form=%q", gotForm)
	}
	if host.savedName != "xai-resign.json" {
		t.Fatalf("saved name=%q", host.savedName)
	}
	doc := host.savedDocument
	if doc["access_token"] != "new-access" || doc["refresh_token"] != "rt-new" || doc["id_token"] != "new-id" {
		t.Fatalf("tokens not updated: %#v", doc)
	}
	if doc["email"] != "user@example.com" || doc["priority"].(float64) != 5 || doc["disabled"] != false {
		t.Fatalf("preserved fields lost: %#v", doc)
	}
	if _, ok := doc["sso"].(map[string]any); !ok {
		t.Fatalf("sso not preserved: %#v", doc["sso"])
	}
	// Demotion must remain applied — resign must not auto-restore.
	state := store.View().Accounts["idx-resign"]
	if state.Demotion.State != "applied" || state.Demotion.Class != domain.DemotionClassHard {
		t.Fatalf("demotion changed: %+v", state.Demotion)
	}
}

func TestResignMissingRefreshToken(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	host := &accountHost{
		files: []domain.AuthFile{xaiFile("idx-missing", "xai-missing.json", 1)},
		documents: map[string]cpaabi.AuthDocument{
			"idx-missing": {"access_token": "only-access"},
		},
	}
	service := application.NewAccountsService(host, store, time.Now().UTC)
	service.SetTokenRefresher(stubTokenRefresher{err: nil, result: application.TokenRefreshResult{AccessToken: "x"}})
	_, err := service.Resign("idx-missing", "xai-missing.json")
	accountErr := application.AsAccountError(err)
	if accountErr.Code != "missing_refresh_token" {
		t.Fatalf("code=%q err=%v", accountErr.Code, err)
	}
	if host.savedName != "" {
		t.Fatalf("should not save: %q", host.savedName)
	}
}

func TestResignTokenEndpointErrors(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized} {
		status := status
		t.Run(http.StatusText(status), func(t *testing.T) {
			store := stateinfra.OpenMemory(time.Now().UTC())
			host := &accountHost{
				files: []domain.AuthFile{xaiFile("idx-http", "xai-http.json", 1)},
				documents: map[string]cpaabi.AuthDocument{
					"idx-http": {"refresh_token": "rt", "access_token": "old"},
				},
			}
			service := application.NewAccountsService(host, store, time.Now().UTC)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, `{"error":"invalid_grant"}`, status)
			}))
			defer server.Close()
			service.SetTokenRefresher(&application.HTTPTokenRefresher{URL: server.URL, Client: server.Client()})
			_, err := service.Resign("idx-http", "xai-http.json")
			accountErr := application.AsAccountError(err)
			if accountErr.Code != "token_refresh_failed" {
				t.Fatalf("code=%q err=%v", accountErr.Code, err)
			}
			if host.savedName != "" {
				t.Fatalf("should not save on token failure")
			}
		})
	}
}

func TestResignRejectsAuthIndexFileNameMismatch(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	host := &accountHost{
		files: []domain.AuthFile{xaiFile("idx-map", "xai-current.json", 1)},
		documents: map[string]cpaabi.AuthDocument{
			"idx-map": {"refresh_token": "rt", "access_token": "old"},
		},
	}
	service := application.NewAccountsService(host, store, time.Now().UTC)
	service.SetTokenRefresher(stubTokenRefresher{result: application.TokenRefreshResult{AccessToken: "new"}})
	_, err := service.Resign("idx-map", "xai-stale.json")
	accountErr := application.AsAccountError(err)
	if accountErr.Code != "account_mapping_changed" {
		t.Fatalf("code=%q err=%v", accountErr.Code, err)
	}
	if host.savedName != "" {
		t.Fatalf("should not save: %q", host.savedName)
	}
}

func TestHTTPTokenRefresherUsesDefaultClientIDFallbackInResign(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	host := &accountHost{
		files: []domain.AuthFile{xaiFile("idx-default-client", "xai-default.json", 1)},
		documents: map[string]cpaabi.AuthDocument{
			"idx-default-client": {"refresh_token": "rt-1", "access_token": "old-a"},
		},
	}
	service := application.NewAccountsService(host, store, time.Now().UTC)
	var clientID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		clientID = r.Form.Get("client_id")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "new-a", "refresh_token": "rt-2", "expires_in": 60})
	}))
	defer server.Close()
	service.SetTokenRefresher(&application.HTTPTokenRefresher{URL: server.URL, Client: server.Client()})
	if _, err := service.Resign("idx-default-client", "xai-default.json"); err != nil {
		t.Fatal(err)
	}
	if clientID != application.DefaultXAIOAuthClientID {
		t.Fatalf("client_id=%q want default", clientID)
	}
}

type stubTokenRefresher struct {
	result application.TokenRefreshResult
	err    error
}

func (s stubTokenRefresher) Refresh(context.Context, string, string) (application.TokenRefreshResult, error) {
	return s.result, s.err
}

func intPtr(v int) *int { return &v }
