package application

import (
	"sync"
	"time"

	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

const demotionQueueSize = 256

type DemotionWorker struct {
	accounts *AccountsService
	store    *stateinfra.Store
	settings Settings
	queue    chan string
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
}

func NewDemotionWorker(accounts *AccountsService, store *stateinfra.Store, settings Settings) *DemotionWorker {
	return &DemotionWorker{
		accounts: accounts, store: store, settings: settings,
		queue: make(chan string, demotionQueueSize), stop: make(chan struct{}), done: make(chan struct{}),
	}
}

func (worker *DemotionWorker) Start() {
	go worker.run()
}

func (worker *DemotionWorker) Enqueue(authIndex string) {
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
			_ = worker.accounts.ApplyRequestedDemotion(authIndex, worker.settings.DemotionPriority)
		case <-ticker.C:
			worker.processRequested()
		case <-worker.stop:
			return
		}
	}
}

func (worker *DemotionWorker) processRequested() {
	for authIndex, account := range worker.store.View().Accounts {
		if account.Demotion.Normalized().State == "requested" {
			_ = worker.accounts.ApplyRequestedDemotion(authIndex, worker.settings.DemotionPriority)
		}
	}
}
