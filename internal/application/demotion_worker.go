package application

import (
	"sync"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/domain"
	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

const demotionQueueSize = 256

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
		queue: make(chan string, demotionQueueSize), stop: make(chan struct{}), done: make(chan struct{}),
	}
}

func (worker *DemotionWorker) Start() {
	go worker.run()
}

func (worker *DemotionWorker) Enqueue(authIndex string) {
	// Skip dead accounts for automatic priority rewrite recovery.
	demotion := worker.store.View().Accounts[authIndex].Demotion.Normalized()
	if demotion.Class == domain.DemotionClassDead && demotion.State == "applied" {
		return
	}
	select {
	case worker.queue <- authIndex:
	default:
		// The requested state is durable; the recovery scan will pick it up.
	}
}

func (worker *DemotionWorker) Stop() {
	worker.once.Do(func() { close(worker.stop) })
	<-worker.done
}

func (worker *DemotionWorker) run() {
	defer close(worker.done)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	worker.processRequested()
	for {
		select {
		case authIndex := <-worker.queue:
			_ = worker.accounts.ApplyRequestedDemotion(authIndex, worker.settings().DeadPriority)
		case <-ticker.C:
			worker.processRequested()
		case <-worker.stop:
			return
		}
	}
}

func (worker *DemotionWorker) processRequested() {
	_ = worker.accounts.ReconcileDemotions()
	for authIndex, account := range worker.store.View().Accounts {
		demotion := account.Demotion.Normalized()
		if demotion.State != "requested" {
			continue
		}
		// Still apply requested writes for dead (manual demote path); only skip re-enqueue of applied dead.
		_ = worker.accounts.ApplyRequestedDemotion(authIndex, worker.settings().DeadPriority)
	}
}

func (worker *DemotionWorker) settings() Settings {
	if settings := worker.store.View().Settings; settings != nil {
		return NormalizeSettings(*settings)
	}
	return NormalizeSettings(worker.settingsFallback)
}
