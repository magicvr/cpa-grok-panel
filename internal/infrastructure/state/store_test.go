package state_test

import (
	"bytes"
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
	data = bytes.ReplaceAll(data, []byte(`"plugin_version": "0.2.5"`), []byte(`"plugin_version": "0.2.4"`))
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
