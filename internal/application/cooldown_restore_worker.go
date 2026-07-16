package application

import (
	"sync"
	"time"

	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

const cooldownRestoreInterval = 30 * time.Second

type CooldownRestoreWorker struct {
	accounts *AccountsService
	store    *stateinfra.Store
	interval time.Duration
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
}

func NewCooldownRestoreWorker(accounts *AccountsService, store *stateinfra.Store) *CooldownRestoreWorker {
	return &CooldownRestoreWorker{
		accounts: accounts,
		store:    store,
		interval: cooldownRestoreInterval,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

func (worker *CooldownRestoreWorker) Start() {
	go worker.run()
}

func (worker *CooldownRestoreWorker) Stop() {
	worker.once.Do(func() { close(worker.stop) })
	<-worker.done
}

func (worker *CooldownRestoreWorker) ProcessOnce() {
	for authIndex := range worker.store.View().Accounts {
		_, _ = worker.accounts.RestorePriorityAfterCooldown(authIndex)
	}
}

func (worker *CooldownRestoreWorker) run() {
	defer close(worker.done)
	worker.ProcessOnce()
	ticker := time.NewTicker(worker.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			worker.ProcessOnce()
		case <-worker.stop:
			return
		}
	}
}
