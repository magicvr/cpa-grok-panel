package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/application"
	"github.com/magicvr/cpa-grok-panel/internal/cpaabi"
	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
	"github.com/magicvr/cpa-grok-panel/internal/management"
)

type Runtime struct {
	mu      sync.RWMutex
	host    *cpaabi.Host
	store   *stateinfra.Store
	usage   *application.UsageService
	worker  *application.DemotionWorker
	router  *management.Router
	dataDir string
	ready   bool
}

func NewRuntime(host *cpaabi.Host) *Runtime {
	return &Runtime{host: host}
}

func (runtime *Runtime) Call(method string, payload []byte) []byte {
	switch strings.TrimSpace(method) {
	case "plugin.register":
		return runtime.register(payload)
	case "plugin.reconfigure":
		return runtime.reconfigure(payload)
	case "usage.handle":
		return runtime.handleUsage(payload)
	case "management.register":
		return cpaabi.Success(management.Registration())
	case "management.handle", "management.http": // http is legacy alias
		return runtime.handleManagement(payload)
	default:
		return cpaabi.Failure("method_not_found", "unsupported plugin method: "+method, false)
	}
}

func (runtime *Runtime) register(payload []byte) []byte {
	dataDir := discoverDataDir(payload)
	if err := runtime.ensureReady(dataDir); err != nil {
		return cpaabi.Failure("state_unavailable", err.Error(), false)
	}
	return cpaabi.Success(cpaabi.PluginRegistration())
}

func (runtime *Runtime) reconfigure(payload []byte) []byte {
	// CPA reuses registerRPCPlugin for plugin.reconfigure and requires the SAME
	// registration envelope (schema_version + metadata + capabilities).
	// Returning a different shape makes validPlugin fail and the host drops the plugin
	// from the active snapshot → UI shows "未注册" after management page refresh.
	dataDir := discoverDataDir(payload)
	if err := runtime.ensureReady(dataDir); err != nil {
		// Still return registration shape so host does not un-register the plugin.
		return cpaabi.Success(cpaabi.PluginRegistration())
	}
	return cpaabi.Success(cpaabi.PluginRegistration())
}

func (runtime *Runtime) ensureReady(dataDir string) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.ready {
		return nil
	}
	candidates := make([]string, 0, 4)
	if strings.TrimSpace(dataDir) != "" {
		candidates = append(candidates, dataDir)
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		candidates = append(candidates, filepath.Join(cwd, "plugins", cpaabi.PluginID))
		candidates = append(candidates, filepath.Join(cwd, "plugins", "data", cpaabi.PluginID))
	}
	candidates = append(candidates, filepath.Join(os.TempDir(), "cpa-grok-panel"))

	var store *stateinfra.Store
	var lastErr error
	var used string
	for _, dir := range candidates {
		s, err := stateinfra.Open(dir, time.Now().UTC())
		if err == nil {
			store = s
			used = dir
			break
		}
		lastErr = err
	}
	if store == nil {
		// Last resort: in-memory only so plugin still registers.
		store = stateinfra.OpenMemory(time.Now().UTC())
		used = "memory"
		_ = lastErr
	}
	settings := application.LoadSettings()
	if persisted := store.View().Settings; persisted != nil {
		settings = *persisted
	} else if err := store.Update(func(snapshot *stateinfra.Snapshot) error {
		initial := settings
		snapshot.Settings = &initial
		return nil
	}); err != nil {
		_ = store.Close()
		return fmt.Errorf("initialize settings: %w", err)
	}
	accounts := application.NewAccountsService(runtime.host, store, time.Now, settings)
	worker := application.NewDemotionWorker(accounts, store, settings)
	runtime.store = store
	runtime.worker = worker
	runtime.usage = application.NewUsageServiceWithDemotion(store, time.Now, settings, worker)
	runtime.router = management.NewRouter(accounts, store, settings)
	runtime.dataDir = used
	runtime.ready = true
	worker.Start()
	return nil
}

func (runtime *Runtime) handleUsage(payload []byte) []byte {
	runtime.mu.RLock()
	service := runtime.usage
	ready := runtime.ready
	runtime.mu.RUnlock()
	if !ready || service == nil {
		return cpaabi.Failure("not_registered", "plugin.register must be called first", true)
	}
	event, err := application.ParseUsageEvent(payload)
	if err != nil {
		return cpaabi.Failure("invalid_argument", err.Error(), false)
	}
	result, err := service.Handle(event)
	if err != nil {
		return cpaabi.Failure("usage_rejected", err.Error(), false)
	}
	return cpaabi.Success(result)
}

func (runtime *Runtime) handleManagement(payload []byte) []byte {
	runtime.mu.RLock()
	router := runtime.router
	ready := runtime.ready
	runtime.mu.RUnlock()
	if !ready || router == nil {
		return cpaabi.Failure("not_registered", "plugin.register must be called first", true)
	}
	request, err := management.DecodeRequest(payload)
	if err != nil {
		return cpaabi.Failure("invalid_argument", err.Error(), false)
	}
	return cpaabi.Success(router.Handle(request))
}

func discoverDataDir(payload []byte) string {
	if value := strings.TrimSpace(os.Getenv("CPA_PLUGIN_DATA_DIR")); value != "" {
		return value
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(payload, &raw) != nil {
		return ""
	}
	return findString(raw, "plugin_data_dir", "PluginDataDir", "data_dir", "DataDir")
}

func findString(raw map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			var text string
			if json.Unmarshal(value, &text) == nil && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	for _, key := range []string{"config", "Config", "host", "Host", "context", "Context", "params", "Params"} {
		if value, ok := raw[key]; ok {
			var nested map[string]json.RawMessage
			if json.Unmarshal(value, &nested) == nil {
				if found := findString(nested, keys...); found != "" {
					return found
				}
			}
		}
	}
	return ""
}

func (runtime *Runtime) Shutdown() error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.store == nil {
		return nil
	}
	if runtime.worker != nil {
		runtime.worker.Stop()
		runtime.worker = nil
	}
	err := runtime.store.Close()
	runtime.store = nil
	runtime.usage = nil
	runtime.router = nil
	runtime.ready = false
	return err
}

func (runtime *Runtime) String() string {
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return fmt.Sprintf("%s@%s ready=%t", cpaabi.PluginID, cpaabi.PluginVersion, runtime.ready)
}
