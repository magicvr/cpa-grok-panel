package application_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	if doc["email"] != "user@example.com" || doc["disabled"] != false {
		t.Fatalf("preserved fields lost: %#v", doc)
	}
	if _, ok := doc["sso"].(map[string]any); !ok {
		t.Fatalf("sso not preserved: %#v", doc["sso"])
	}
	// v0.7.1: resign → probe unknown + priority_unknown (default -10).
	settings := application.DefaultSettings()
	if int(doc["priority"].(float64)) != settings.PriorityUnknown {
		t.Fatalf("priority after resign=%v want=%d", doc["priority"], settings.PriorityUnknown)
	}
	if host.files[0].Priority != settings.PriorityUnknown {
		t.Fatalf("host priority=%d", host.files[0].Priority)
	}
	state := store.View().Accounts["idx-resign"]
	if state.Quota.ProbeStatus != "" {
		t.Fatalf("probe should be cleared unknown: %+v", state.Quota)
	}
	if state.Demotion.State != "none" || state.Demotion.Class != domain.DemotionClassNone {
		t.Fatalf("legacy demotion should be cleared: %+v", state.Demotion)
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

func TestHTTPTokenRefresherUsesExplicitProxy(t *testing.T) {
	// Token endpoint that would only be reached if the client ignored Proxy.
	tokenHit := false
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenHit = true
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "should-not"})
	}))
	defer tokenServer.Close()

	proxyHits := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits++
		// Absolute-form request to the token URL when using HTTP proxy.
		if r.Method != http.MethodPost {
			t.Fatalf("proxy method=%s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "via-proxy", "refresh_token": "rt-p", "expires_in": 60, "token_type": "Bearer",
		})
	}))
	defer proxy.Close()

	refresher := &application.HTTPTokenRefresher{URL: tokenServer.URL, ProxyURL: proxy.URL}
	result, err := refresher.Refresh(context.Background(), "rt", "cid")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if result.AccessToken != "via-proxy" {
		t.Fatalf("token=%q", result.AccessToken)
	}
	if proxyHits != 1 {
		t.Fatalf("proxyHits=%d", proxyHits)
	}
	if tokenHit {
		t.Fatal("token server should not be hit when proxy is set")
	}
}

func TestHTTPTokenRefresherMapsDeadlineExceeded(t *testing.T) {
	refresher := &application.HTTPTokenRefresher{
		URL: "http://127.0.0.1:1",
		Client: &http.Client{
			Timeout: 50 * time.Millisecond,
			Transport: &http.Transport{
				// Force hang until client timeout: dial a blackhole with short timeout path.
				Proxy: func(*http.Request) (*url.URL, error) {
					return nil, context.DeadlineExceeded
				},
			},
		},
	}
	_, err := refresher.Refresh(context.Background(), "rt", "cid")
	accountErr := application.AsAccountError(err)
	if accountErr.Code != "token_refresh_failed" {
		t.Fatalf("code=%q err=%v", accountErr.Code, err)
	}
	if !strings.Contains(accountErr.Message, "访问 auth.x.ai 超时") {
		t.Fatalf("message=%q", accountErr.Message)
	}
	if !accountErr.Retryable {
		t.Fatal("expected retryable")
	}
}

func TestResignConcurrentDoesNotHoldWriteLockOverNetwork(t *testing.T) {
	store := stateinfra.OpenMemory(time.Now().UTC())
	const n = 4
	files := make([]domain.AuthFile, 0, n)
	docs := map[string]cpaabi.AuthDocument{}
	for i := 0; i < n; i++ {
		idx := fmt.Sprintf("idx-c%d", i)
		name := fmt.Sprintf("xai-c%d.json", i)
		files = append(files, xaiFile(idx, name, 1))
		docs[idx] = cpaabi.AuthDocument{"refresh_token": "rt", "access_token": "old"}
	}
	host := &accountHost{files: files, documents: docs}
	service := application.NewAccountsService(host, store, time.Now().UTC)

	// Slow refresher: if Resign held write.Lock across Refresh, concurrent
	// Resign would serialize to ~n * delay (here ≥ 4*120ms).
	delay := 120 * time.Millisecond
	service.SetTokenRefresher(slowTokenRefresher{
		delay:  delay,
		result: application.TokenRefreshResult{AccessToken: "new", RefreshToken: "rt2"},
	})

	start := time.Now()
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			_, err := service.Resign(fmt.Sprintf("idx-c%d", i), fmt.Sprintf("xai-c%d.json", i))
			errCh <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("Resign: %v", err)
		}
	}
	elapsed := time.Since(start)
	// Parallel should finish near one delay, not n delays. Allow headroom for CI.
	if elapsed >= time.Duration(n)*delay-20*time.Millisecond {
		t.Fatalf("elapsed=%v suggests serial lock-over-network (n*delay≈%v)", elapsed, time.Duration(n)*delay)
	}
}

func TestNewOutboundHTTPClientRejectsInvalidProxy(t *testing.T) {
	if _, err := application.NewOutboundHTTPClient("not-a-url", time.Second); err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveOutboundProxyURLPrefersSettings(t *testing.T) {
	t.Setenv(application.EnvOutboundProxy, "http://env-proxy:1")
	settings := application.DefaultSettings()
	settings.OutboundProxyURL = "http://settings-proxy:2"
	if got := application.ResolveOutboundProxyURL(settings); got != "http://settings-proxy:2" {
		t.Fatalf("got %q", got)
	}
	settings.OutboundProxyURL = ""
	if got := application.ResolveOutboundProxyURL(settings); got != "http://env-proxy:1" {
		t.Fatalf("env fallback got %q", got)
	}
}

type stubTokenRefresher struct {
	result application.TokenRefreshResult
	err    error
}

func (s stubTokenRefresher) Refresh(context.Context, string, string) (application.TokenRefreshResult, error) {
	return s.result, s.err
}

type slowTokenRefresher struct {
	delay  time.Duration
	result application.TokenRefreshResult
}

func (s slowTokenRefresher) Refresh(context.Context, string, string) (application.TokenRefreshResult, error) {
	time.Sleep(s.delay)
	return s.result, nil
}

func intPtr(v int) *int { return &v }
