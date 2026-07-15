package application

import (
	"sort"
	"strings"
	"time"

	"github.com/magicvr/cpa-grok-panel/internal/domain"
	stateinfra "github.com/magicvr/cpa-grok-panel/internal/infrastructure/state"
)

type AuthLister interface {
	ListAuthFiles() ([]domain.AuthFile, error)
}

type AccountsService struct {
	host  AuthLister
	store *stateinfra.Store
	now   func() time.Time
}

func NewAccountsService(host AuthLister, store *stateinfra.Store, now func() time.Time) *AccountsService {
	return &AccountsService{host: host, store: store, now: now}
}

func (service *AccountsService) List(search string) ([]domain.AccountView, time.Time, error) {
	files, err := service.host.ListAuthFiles()
	if err != nil {
		return nil, time.Time{}, err
	}
	now := service.now().UTC()
	snapshot := service.store.View()
	items := make([]domain.AccountView, 0, len(files))
	for _, file := range files {
		if !domain.IsXAIOAuth(file) || file.AuthIndex == "" || !strings.HasSuffix(file.Name, ".json") {
			continue
		}
		if search != "" && !containsFold(file.AuthIndex, search) && !containsFold(file.Name, search) && !containsFold(file.Email, search) {
			continue
		}
		items = append(items, domain.ProjectAccount(file, snapshot.Accounts[file.AuthIndex], now))
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].ExactFileName < items[j].ExactFileName
	})
	return items, now, nil
}

func containsFold(value, search string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(strings.TrimSpace(search)))
}
