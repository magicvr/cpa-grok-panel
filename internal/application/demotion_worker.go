package application

import (
	"sync"
	"time"

	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

// DemotionWorker in v0.7.0 applies success-heal priority writes (priority_live).
// Name kept for runtime wiring compatibility.
type DemotionWorker struct {
	accounts         *AccountsService
	store            *stateinfra.Store
	settingsFallback Settings
	queue            chan string
	stop             chan struct{}
	done             chan struct{}
	once             sync.Once
}

func NewDemotionWorker(accounts *AccountsService, store *stateinfra.Store, settings Settings) *DemotionWorker {
	return &DemotionWorker{
		accounts: accounts, store: store, settingsFallback: NormalizeSettings(settings),
		queue: make(chan string, 256), stop: make(chan struct{}), done: make(chan struct{}),
	}
}

func (worker *DemotionWorker) Start() {
	go worker.run()
}

func (worker *DemotionWorker) Enqueue(authIndex string) {
	if authIndex == "" {
		return
	}
	select {
	case worker.queue <- authIndex:
	default:
	}
}

func (worker *DemotionWorker) Stop() {
	worker.once.Do(func() { close(worker.stop) })
	<-worker.done
}

func (worker *DemotionWorker) run() {
	defer close(worker.done)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case authIndex := <-worker.queue:
			// Success heal: ensure priority_live is applied.
			_, _ = worker.accounts.ApplyAliveStatus(authIndex, "live", true)
		case <-ticker.C:
			// no-op scan (legacy requested demotions no longer used)
		case <-worker.stop:
			return
		}
	}
}

func (worker *DemotionWorker) settings() Settings {
	if settings := worker.store.View().Settings; settings != nil {
		return NormalizeSettings(*settings)
	}
	return NormalizeSettings(worker.settingsFallback)
}
