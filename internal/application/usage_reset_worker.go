package application

import (
	"fmt"
	"sync"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/domain"
	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

const usageResetInterval = 30 * time.Second

type UsageResetWorker struct {
	store            *stateinfra.Store
	settingsFallback Settings
	now              func() time.Time
	location         *time.Location
	stop             chan struct{}
	done             chan struct{}
	once             sync.Once
}

func NewUsageResetWorker(store *stateinfra.Store, settings Settings) *UsageResetWorker {
	return NewUsageResetWorkerWithClock(store, settings, time.Now, time.Local)
}

func NewUsageResetWorkerWithClock(store *stateinfra.Store, settings Settings, now func() time.Time, location *time.Location) *UsageResetWorker {
	if now == nil {
		now = time.Now
	}
	if location == nil {
		location = time.Local
	}
	return &UsageResetWorker{
		store: store, settingsFallback: settings, now: now, location: location,
		stop: make(chan struct{}), done: make(chan struct{}),
	}
}

func (worker *UsageResetWorker) Start() { go worker.run() }

func (worker *UsageResetWorker) Stop() {
	worker.once.Do(func() { close(worker.stop) })
	<-worker.done
}

func (worker *UsageResetWorker) RunOnce() (bool, error) {
	settings := worker.settings()
	if !settings.DailyUsageResetEnabled {
		return false, nil
	}
	if err := ValidateDailyUsageResetTime(settings.DailyUsageResetTime); err != nil {
		return false, err
	}

	now := worker.now().In(worker.location)
	resetClock, err := time.ParseInLocation("15:04", settings.DailyUsageResetTime, worker.location)
	if err != nil {
		return false, fmt.Errorf("parse daily usage reset time: %w", err)
	}
	dueAt := time.Date(now.Year(), now.Month(), now.Day(), resetClock.Hour(), resetClock.Minute(), 0, 0, worker.location)
	date := now.Format("2006-01-02")
	if now.Before(dueAt) || worker.store.View().LastUsageResetDate == date {
		return false, nil
	}

	resetAt := now.UTC()
	reset := false
	err = worker.store.Update(func(snapshot *stateinfra.Snapshot) error {
		current := worker.settingsFallback
		if snapshot.Settings != nil {
			current = *snapshot.Settings
		}
		if !current.DailyUsageResetEnabled || current.DailyUsageResetTime != settings.DailyUsageResetTime || snapshot.LastUsageResetDate == date {
			return nil
		}
		for authIndex, account := range snapshot.Accounts {
			dedupeMode := account.Usage.DedupeMode
			account.Usage = domain.UsageCounters{PeriodStartedAt: resetAt, DedupeMode: dedupeMode}
			account.Failure.ConsecutiveAttributedFailures = 0
			snapshot.Accounts[authIndex] = account
		}
		snapshot.StatisticsStartedAt = resetAt
		snapshot.LastUsageResetDate = date
		reset = true
		return nil
	})
	return reset, err
}

func (worker *UsageResetWorker) run() {
	defer close(worker.done)
	_, _ = worker.RunOnce()
	ticker := time.NewTicker(usageResetInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_, _ = worker.RunOnce()
		case <-worker.stop:
			return
		}
	}
}

func (worker *UsageResetWorker) settings() Settings {
	if settings := worker.store.View().Settings; settings != nil {
		return *settings
	}
	return worker.settingsFallback
}
