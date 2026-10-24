package app

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"mica/internal/mail"
)

var errAccountClientUnavailable = errors.New("mail: account is unavailable")

type accountClient struct {
	service    mail.Service
	account    mail.Account
	labels     []mail.Label
	verified   bool
	generation atomic.Uint64
}

func (a *App) fetchAccountClient(ctx context.Context, email string) (mail.Service, mail.Account, []mail.Label, error) {
	email = normalizedEmail(email)
	restore := a.accountService
	if restore == nil {
		restore = restoreStoredAccount
	}
	service, err := restore(ctx, email)
	if err != nil {
		return nil, mail.Account{}, nil, err
	}
	if service == nil {
		return nil, mail.Account{}, nil, fmt.Errorf("mail: account %s is unavailable", email)
	}
	account, err := service.Account(ctx)
	if err != nil {
		deactivateMailService(service)
		return nil, mail.Account{}, nil, err
	}
	if normalizedEmail(account.Email) != email {
		deactivateMailService(service)
		return nil, mail.Account{}, nil, fmt.Errorf("mail: signed in to %s, expected %s", account.Email, email)
	}
	labels, err := service.Labels(ctx)
	if err != nil {
		deactivateMailService(service)
		return nil, mail.Account{}, nil, err
	}
	return service, account, labels, nil
}

func (a *App) nextAccountGeneration(email string) uint64 {
	a.cacheState.mu.Lock()
	defer a.cacheState.mu.Unlock()
	if a.accountGenerations == nil {
		a.accountGenerations = make(map[string]uint64)
	}
	a.accountGenerationCounter++
	a.accountGenerations[email] = a.accountGenerationCounter
	return a.accountGenerationCounter
}

func (a *App) cancelAccountClientLoads(email string) {
	if a.accountClientLoads == nil {
		a.accountClientLoads = make(map[string]uint64)
	}
	a.accountClientLoads[email]++
	callbacks := a.accountClientPending[email]
	delete(a.accountClientPending, email)
	for _, done := range callbacks {
		done(errAccountClientUnavailable)
	}
}

func (a *App) registerAccountClient(service mail.Service, account mail.Account, labels []mail.Label, verified bool) {
	email := normalizedEmail(account.Email)
	if email == "" {
		return
	}
	a.cancelAccountClientLoads(email)
	account.Email = email
	client := &accountClient{service: service, account: account, labels: labels, verified: verified}
	client.generation.Store(a.nextAccountGeneration(email))
	if a.accountClients == nil {
		a.accountClients = make(map[string]*accountClient)
	}
	if previous := a.accountClients[email]; previous != nil {
		previous.generation.Store(client.generation.Load())
		if previous.service != service {
			deactivateMailService(previous.service)
		}
	}
	a.accountClients[email] = client
}

func (a *App) removeAccountClient(email string) {
	email = normalizedEmail(email)
	a.cancelAccountClientLoads(email)
	generation := a.nextAccountGeneration(email)
	a.removeSnoozesForAccount(email)
	a.removeScheduledOutboxForAccount(email)
	client := a.accountClients[email]
	if client == nil {
		return
	}
	client.generation.Store(generation)
	delete(a.accountClients, email)
	deactivateMailService(client.service)
}

func (a *App) ensureAccountClient(email string, done func(error)) {
	email = normalizedEmail(email)
	if email == "" {
		done(errAccountClientUnavailable)
		return
	}
	if service, _ := a.ownerService(email); service != nil {
		done(nil)
		return
	}
	if a.accountClientPending == nil {
		a.accountClientPending = make(map[string][]func(error))
	}
	a.accountClientPending[email] = append(a.accountClientPending[email], done)
	if len(a.accountClientPending[email]) > 1 {
		return
	}
	if a.accountClientLoads == nil {
		a.accountClientLoads = make(map[string]uint64)
	}
	a.accountClientLoads[email]++
	loadID := a.accountClientLoads[email]
	a.schedule(func() {
		service, account, labels, err := a.fetchAccountClient(context.Background(), email)
		a.publish(func() {
			if a.accountClientLoads[email] != loadID {
				return
			}
			callbacks := a.accountClientPending[email]
			delete(a.accountClientPending, email)
			if err == nil {
				a.registerAccountClient(service, account, labels, true)
			}
			for _, callback := range callbacks {
				callback(err)
			}
		})
	})
}

func deactivateMailService(service mail.Service) {
	if service, ok := service.(interface{ Deactivate() }); ok {
		service.Deactivate()
	}
}

func (a *App) activeGenerationKey() string {
	return normalizedEmail(a.storeAccount)
}

func (a *App) ownerService(email string) (mail.Service, uint64) {
	email = normalizedEmail(email)
	if email == "" || email == normalizedEmail(a.storeAccount) || email == normalizedEmail(a.account.Email) {
		key := email
		if key == "" {
			key = a.activeGenerationKey()
		}
		if generation, ok := a.accountGenerations[key]; ok {
			return a.service, generation
		}
		return a.service, uint64(a.serviceVersion)
	}
	if client := a.accountClients[email]; client != nil {
		return client.service, client.generation.Load()
	}
	return nil, 0
}

func (a *App) ownerVerified(email string) bool {
	email = normalizedEmail(email)
	if email == "" || email == normalizedEmail(a.storeAccount) || email == normalizedEmail(a.account.Email) {
		return a.accountVerified
	}
	if client := a.accountClients[email]; client != nil {
		return client.verified
	}
	return false
}

func (a *App) ownerLabels(email string) []mail.Label {
	email = normalizedEmail(email)
	if email == "" || email == normalizedEmail(a.storeAccount) || email == normalizedEmail(a.account.Email) {
		return a.labels
	}
	if client := a.accountClients[email]; client != nil {
		return client.labels
	}
	return nil
}

func (a *App) serviceForThreadKey(key string) mail.Service {
	service, _ := a.ownerService(parseThreadRef(key).AccountEmail)
	return service
}

func (a *App) generationForThreadKey(key string) uint64 {
	_, generation := a.ownerService(parseThreadRef(key).AccountEmail)
	return generation
}
