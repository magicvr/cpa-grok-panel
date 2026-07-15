package state_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

func TestOpenLegacyStateWithoutSettings(t *testing.T) {
	dir := t.TempDir()
	store, err := stateinfra.Open(dir, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"settings"`) {
		t.Fatalf("fixture unexpectedly contains settings: %s", data)
	}
	data = bytes.ReplaceAll(data, []byte(`"plugin_version": "0.2.7"`), []byte(`"plugin_version": "0.2.6"`))
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	reopened, err := stateinfra.Open(dir, time.Now().UTC())
	if err != nil {
		t.Fatalf("open legacy state: %v", err)
	}
	defer reopened.Close()
	if reopened.View().Settings != nil {
		t.Fatalf("legacy settings should remain absent until runtime initialization")
	}
}

func TestOpenLegacySettingsAppliesAutoRefreshDefaults(t *testing.T) {
	dir := t.TempDir()
	state := map[string]any{
		"schema_version":        1,
		"plugin_id":             "cpa-grok-panel",
		"plugin_version":        "0.2.5",
		"statistics_started_at": time.Now().UTC(),
		"settings":              map[string]any{"revision": 7, "attributed_failure_threshold": 3},
		"accounts":              map[string]any{},
		"event_dedupe":          map[string]any{"exact_ids": map[string]any{}, "weak_keys": map[string]any{}, "policy_version": 1},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := stateinfra.Open(dir, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settings := store.View().Settings
	if settings == nil || !settings.AutoRefreshEnabled || settings.AutoRefreshIntervalSeconds != 5 {
		t.Fatalf("normalized settings=%+v", settings)
	}
}
