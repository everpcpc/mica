package app

import (
	"context"
	"log"
	"slices"
	"sync"

	"mica/internal/mail"
)

type cacheState struct {
	mu      sync.Mutex
	version int
}

type mailStore interface {
	LastAccount(context.Context) (mail.Account, bool, error)
	SaveAccount(context.Context, mail.Account) error
	Labels(context.Context, string) ([]mail.Label, bool, error)
	SaveLabels(context.Context, string, []mail.Label) error
	ThreadList(context.Context, string, mail.Query) ([]mail.ThreadSummary, bool, error)
	SaveThreadList(context.Context, string, mail.Query, []mail.ThreadSummary) error
	Thread(context.Context, string, string) (mail.Thread, bool, error)
	SaveThread(context.Context, string, mail.Thread) error
	DeleteThread(context.Context, string, string) error
}

func (a *App) SetMailStore(store mailStore) {
	a.cacheState.mu.Lock()
	defer a.cacheState.mu.Unlock()
	a.store = store
	a.cacheState.version = a.serviceVersion
}

func (a *App) persistAccount(account mail.Account) {
	if account.Demo || account.Email == "" {
		return
	}
	a.storeAccount = account.Email
	a.persist(func(store mailStore, email string) error {
		return store.SaveAccount(context.Background(), account)
	})
}

func (a *App) persistLabels(labels []mail.Label) {
	labels = slices.Clone(labels)
	a.persist(func(store mailStore, email string) error {
		return store.SaveLabels(context.Background(), email, labels)
	})
}

func (a *App) persistList(query mail.Query, threads []mail.ThreadSummary) {
	threads = cloneThreadSummaries(threads)
	a.persist(func(store mailStore, email string) error {
		return store.SaveThreadList(context.Background(), email, query, threads)
	})
}

func (a *App) persistThread(thread mail.Thread) {
	thread = cloneMailThread(thread)
	a.persist(func(store mailStore, email string) error {
		return store.SaveThread(context.Background(), email, thread)
	})
}

func (a *App) persist(write func(mailStore, string) error) {
	store, email, version := a.store, a.storeAccount, a.serviceVersion
	if store == nil || email == "" {
		return
	}
	a.schedule(func() {
		a.cacheState.mu.Lock()
		defer a.cacheState.mu.Unlock()
		if version != a.cacheState.version {
			return
		}
		if err := write(store, email); err != nil {
			log.Printf("mica: write mail cache: %v", err)
		}
	})
}

func logCacheError(err error) {
	if err != nil {
		log.Printf("mica: read mail cache: %v", err)
	}
}

func cloneThreadSummaries(threads []mail.ThreadSummary) []mail.ThreadSummary {
	cloned := slices.Clone(threads)
	for i := range cloned {
		cloned[i].LabelIDs = slices.Clone(cloned[i].LabelIDs)
	}
	return cloned
}

func cloneMailThread(thread mail.Thread) mail.Thread {
	cloned := thread
	cloned.LabelIDs = slices.Clone(thread.LabelIDs)
	cloned.Messages = slices.Clone(thread.Messages)
	for i := range cloned.Messages {
		cloned.Messages[i].LabelIDs = slices.Clone(thread.Messages[i].LabelIDs)
		cloned.Messages[i].Attachments = slices.Clone(thread.Messages[i].Attachments)
	}
	return cloned
}
