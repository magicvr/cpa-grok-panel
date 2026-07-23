package application

import (
	"sync"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/domain"
	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

const (
	probeQueueSize    = 256
	probeScanInterval = 30 * time.Second
)

// ProbeWorker runs automatic alive probes from debt-triggered enqueue.
// v0.7.0: no watch/anomaly scheduled re-probe chain.
type ProbeWorker struct {
	accounts *AccountsService
	store    *stateinfra.Store
	queue    chan string
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
	interval time.Duration
}

func NewProbeWorker(accounts *AccountsService, store *stateinfra.Store) *ProbeWorker {
	return &ProbeWorker{
		accounts: accounts,
		store:    store,
		queue:    make(chan string, probeQueueSize),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		interval: probeScanInterval,
	}
}

func (worker *ProbeWorker) Start() {
	go worker.run()
}

func (worker *ProbeWorker) EnqueueProbe(authIndex string) {
	if authIndex == "" {
		return
	}
	// Never auto-probe dead accounts.
	probe := worker.store.View().Accounts[authIndex].Quota.ProbeStatus
	if domain.NormalizeProbeStatus(probe, 0) == domain.ProbeStatusDead {
		return
	}
	select {
	case worker.queue <- authIndex:
	default:
	}
}

func (worker *ProbeWorker) Stop() {
	worker.once.Do(func() { close(worker.stop) })
	<-worker.done
}

func (worker *ProbeWorker) ProcessOnce() {
	// v0.7.0: no scheduled NextProbeAt scan.
}

func (worker *ProbeWorker) run() {
	defer close(worker.done)
	ticker := time.NewTicker(worker.interval)
	defer ticker.Stop()
	for {
		select {
		case authIndex := <-worker.queue:
			probe := worker.store.View().Accounts[authIndex].Quota.ProbeStatus
			if domain.NormalizeProbeStatus(probe, 0) == domain.ProbeStatusDead {
				continue
			}
			_, _ = worker.accounts.ProbeAccount(authIndex, "", domain.ProbeSourceAuto)
		case <-ticker.C:
			// reserved for future periodic work
		case <-worker.stop:
			return
		}
	}
}
