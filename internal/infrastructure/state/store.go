package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/domain"
)

const (
	SchemaVersion = 1
	PluginID      = "cpa-grok-panel"
	PluginVersion = "0.1.3"
)

type DedupeState struct {
	ExactIDs      map[string]time.Time `json:"exact_ids"`
	WeakKeys      map[string]time.Time `json:"weak_keys"`
	WeakModeUsed  bool                 `json:"weak_mode_used"`
	PolicyVersion int                  `json:"policy_version"`
}

type Snapshot struct {
	SchemaVersion       int                            `json:"schema_version"`
	PluginID            string                         `json:"plugin_id"`
	PluginVersion       string                         `json:"plugin_version"`
	SavedAt             time.Time                      `json:"saved_at"`
	StatisticsStartedAt time.Time                      `json:"statistics_started_at"`
	Accounts            map[string]domain.AccountState `json:"accounts"`
	EventDedupe         DedupeState                    `json:"event_dedupe"`
}

type Store struct {
	mu       sync.RWMutex
	dir      string
	path     string
	lockFile *os.File
	snapshot Snapshot
}

func Open(dir string, now time.Time) (*Store, error) {
	if dir == "" {
		return nil, errors.New("plugin data dir is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create plugin data dir: %w", err)
	}
	locksDir := filepath.Join(dir, "locks")
	if err := os.MkdirAll(locksDir, 0o700); err != nil {
		return nil, fmt.Errorf("create locks dir: %w", err)
	}
	lockFile, err := os.OpenFile(filepath.Join(locksDir, "instance.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		// Non-fatal: continue without lock file if FS rejects create.
		lockFile = nil
	} else if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		// Another process holds the lock; still open store so register does not fail hard.
		_ = lockFile.Close()
		lockFile = nil
	}

	store := &Store{dir: dir, path: filepath.Join(dir, "state.json"), lockFile: lockFile}
	store.snapshot = newSnapshot(now)
	if err := store.load(); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

func newSnapshot(now time.Time) Snapshot {
	return Snapshot{
		SchemaVersion: SchemaVersion, PluginID: PluginID, PluginVersion: PluginVersion,
		StatisticsStartedAt: now.UTC(), Accounts: make(map[string]domain.AccountState),
		EventDedupe: DedupeState{ExactIDs: make(map[string]time.Time), WeakKeys: make(map[string]time.Time), PolicyVersion: 1},
	}
}

func (store *Store) load() error {
	data, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return store.save(store.snapshot)
	}
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		backupData, backupErr := os.ReadFile(store.path + ".bak")
		if backupErr != nil || json.Unmarshal(backupData, &snapshot) != nil {
			return fmt.Errorf("decode state and backup: %w", err)
		}
	}
	if snapshot.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported state schema version %d", snapshot.SchemaVersion)
	}
	if snapshot.PluginID != PluginID {
		return fmt.Errorf("state belongs to plugin %q", snapshot.PluginID)
	}
	normalizeSnapshot(&snapshot)
	store.snapshot = snapshot
	return nil
}

func normalizeSnapshot(snapshot *Snapshot) {
	if snapshot.Accounts == nil {
		snapshot.Accounts = make(map[string]domain.AccountState)
	}
	if snapshot.EventDedupe.ExactIDs == nil {
		snapshot.EventDedupe.ExactIDs = make(map[string]time.Time)
	}
	if snapshot.EventDedupe.WeakKeys == nil {
		snapshot.EventDedupe.WeakKeys = make(map[string]time.Time)
	}
	if snapshot.EventDedupe.PolicyVersion == 0 {
		snapshot.EventDedupe.PolicyVersion = 1
	}
}

func (store *Store) View() Snapshot {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return cloneSnapshot(store.snapshot)
}

func OpenMemory(now time.Time) *Store {
	store := &Store{dir: "memory", path: "", lockFile: nil}
	store.snapshot = newSnapshot(now)
	return store
}

func (store *Store) Update(update func(*Snapshot) error) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	next := cloneSnapshot(store.snapshot)
	if err := update(&next); err != nil {
		return err
	}
	next.SavedAt = time.Now().UTC()
	next.PluginVersion = PluginVersion
	if store.path != "" {
		if err := store.save(next); err != nil {
			return err
		}
	}
	store.snapshot = next
	return nil
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	data, _ := json.Marshal(snapshot)
	var clone Snapshot
	_ = json.Unmarshal(data, &clone)
	normalizeSnapshot(&clone)
	return clone
}

func (store *Store) save(snapshot Snapshot) error {
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	temporary, err := os.OpenFile(store.path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open temporary state: %w", err)
	}
	writeErr := func() error {
		if _, err := temporary.Write(data); err != nil {
			return err
		}
		return temporary.Sync()
	}()
	closeErr := temporary.Close()
	if writeErr != nil {
		_ = os.Remove(store.path + ".tmp")
		return fmt.Errorf("write temporary state: %w", writeErr)
	}
	if closeErr != nil {
		_ = os.Remove(store.path + ".tmp")
		return fmt.Errorf("close temporary state: %w", closeErr)
	}
	if err := copyFile(store.path, store.path+".bak"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("backup state: %w", err)
	}
	if err := os.Rename(store.path+".tmp", store.path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	directory, err := os.Open(store.dir)
	if err == nil {
		err = directory.Sync()
		_ = directory.Close()
	}
	if err != nil {
		return fmt.Errorf("sync state directory: %w", err)
	}
	return nil
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	return os.Rename(destination+".tmp", destination)
}

func (store *Store) Close() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.lockFile == nil {
		return nil
	}
	_ = syscall.Flock(int(store.lockFile.Fd()), syscall.LOCK_UN)
	err := store.lockFile.Close()
	store.lockFile = nil
	return err
}
