package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/config"
	"github.com/magicvr/cpa-grok-panel/internal/domain"
)

const (
	SchemaVersion = 1
	PluginID      = "cpa-grok-panel"
	PluginVersion = "0.5.10"
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
	LastUsageResetDate  string                         `json:"last_usage_reset_date,omitempty"`
	Settings            *config.Settings               `json:"settings,omitempty"`
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

type Info struct {
	Status  string
	Backend string
	DataDir string
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
	lf, err := os.OpenFile(filepath.Join(locksDir, "instance.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		// Non-fatal: continue without lock file if FS rejects create.
		lf = nil
	} else if err := lockFile(lf); err != nil {
		// Another process holds the lock; still open store so register does not fail hard.
		_ = lf.Close()
		lf = nil
	}

	store := &Store{dir: dir, path: filepath.Join(dir, "state.json"), lockFile: lf}
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
	if err := decodeSnapshot(data, &snapshot); err != nil {
		backupData, backupErr := os.ReadFile(store.path + ".bak")
		if backupErr != nil || decodeSnapshot(backupData, &snapshot) != nil {
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

func decodeSnapshot(data []byte, snapshot *Snapshot) error {
	if err := json.Unmarshal(data, snapshot); err != nil {
		return err
	}
	if snapshot.Settings == nil {
		return nil
	}
	var raw struct {
		Settings map[string]json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if _, exists := raw.Settings["auto_refresh_enabled"]; !exists {
		snapshot.Settings.AutoRefreshEnabled = true
	}
	if _, exists := raw.Settings["auto_refresh_interval_seconds"]; !exists {
		snapshot.Settings.AutoRefreshIntervalSeconds = 5
	}
	if _, exists := raw.Settings["daily_usage_reset_time"]; !exists {
		snapshot.Settings.DailyUsageResetTime = "00:00"
	}
	if _, exists := raw.Settings["batch_operation_concurrency"]; !exists {
		snapshot.Settings.BatchOperationConcurrency = 10
	}
	if _, exists := raw.Settings["cooldown_restore_enabled"]; !exists {
		snapshot.Settings.CooldownRestoreEnabled = true
	}
	if _, exists := raw.Settings["cooldown_restore_skip_bots"]; !exists {
		snapshot.Settings.CooldownRestoreSkipBots = true
	}
	if _, exists := raw.Settings["soft_demotion_enabled"]; !exists {
		snapshot.Settings.SoftDemotionEnabled = true
	}
	if _, exists := raw.Settings["soft_demotion_priority"]; !exists {
		snapshot.Settings.SoftDemotionPriority = -10
	}
	if _, exists := raw.Settings["soft_debt_threshold"]; !exists {
		snapshot.Settings.SoftDebtThreshold = 2.0
	}
	if _, exists := raw.Settings["hard_debt_threshold"]; !exists {
		snapshot.Settings.HardDebtThreshold = 4.5
	}
	if _, exists := raw.Settings["debt_fail_401"]; !exists {
		snapshot.Settings.DebtFail401 = 1.5
	}
	if _, exists := raw.Settings["debt_fail_429"]; !exists {
		snapshot.Settings.DebtFail429 = 0.5
	}
	if _, exists := raw.Settings["debt_success_decay"]; !exists {
		snapshot.Settings.DebtSuccessDecay = 1.0
	}
	if _, exists := raw.Settings["half_open_enabled"]; !exists {
		snapshot.Settings.HalfOpenEnabled = true
	}
	if _, exists := raw.Settings["half_open_success_threshold"]; !exists {
		snapshot.Settings.HalfOpenSuccessThreshold = 2
	}
	if _, exists := raw.Settings["free_user_daily_token_limit"]; !exists {
		snapshot.Settings.FreeUserDailyTokenLimit = 2_000_000
	}
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
	for authIndex, account := range snapshot.Accounts {
		account.Demotion = account.Demotion.Normalized()
		snapshot.Accounts[authIndex] = account
	}
}

func (store *Store) View() Snapshot {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return cloneSnapshot(store.snapshot)
}

func (store *Store) Info() Info {
	store.mu.RLock()
	defer store.mu.RUnlock()
	if store.path == "" {
		return Info{Status: "memory", Backend: "memory"}
	}
	status := "healthy"
	if store.lockFile == nil {
		status = "degraded"
	}
	return Info{Status: status, Backend: "file", DataDir: store.dir}
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
	// Directory fsync is best-effort; Windows often denies Sync on directory handles.
	if directory, err := os.Open(store.dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
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
	_ = unlockFile(store.lockFile)
	err := store.lockFile.Close()
	store.lockFile = nil
	return err
}
