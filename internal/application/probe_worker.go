package application

import (
	"sync"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/domain"
	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

const (
	probeQueueSize     = 256
	probeScanInterval  = 30 * time.Second
)

// ProbeWorker runs automatic alive probes: debt-triggered enqueue and scheduled
// re-probes for watch/anomaly (NextProbeAt <= now). Dead is always skipped.
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
	demotion := worker.store.View().Accounts[authIndex].Demotion.Normalized()
	if demotion.Class == domain.DemotionClassDead {
		return
	}
	select {
	case worker.queue <- authIndex:
	default:
		// Durable NextProbeAt / debt path will pick up on next scan if queue full.
	}
}

func (worker *ProbeWorker) Stop() {
	worker.once.Do(func() { close(worker.stop) })
	<-worker.done
}

func (worker *ProbeWorker) ProcessOnce() {
	// Drain due scheduled re-probes.
	now := time.Now().UTC()
	for authIndex, account := range worker.store.View().Accounts {
		demotion := account.Demotion.Normalized()
		if demotion.Class == domain.DemotionClassDead {
			continue
		}
		if demotion.Class != domain.DemotionClassWatch && demotion.Class != domain.DemotionClassAnomaly {
			continue
		}
		if demotion.NextProbeAt == nil || demotion.NextProbeAt.After(now) {
			continue
		}
		_, _ = worker.accounts.ProbeAccount(authIndex, "", domain.ProbeSourceAuto)
	}
}

func (worker *ProbeWorker) run() {
	defer close(worker.done)
	ticker := time.NewTicker(worker.interval)
	defer ticker.Stop()
	worker.ProcessOnce()
	for {
		select {
		case authIndex := <-worker.queue:
			demotion := worker.store.View().Accounts[authIndex].Demotion.Normalized()
			if demotion.Class == domain.DemotionClassDead {
				continue
			}
			_, _ = worker.accounts.ProbeAccount(authIndex, "", domain.ProbeSourceAuto)
		case <-ticker.C:
			worker.ProcessOnce()
		case <-worker.stop:
			return
		}
	}
}
