package app

import (
	"context"
	"time"

	"mica/internal/mail"
)

const (
	visibleThreadPrefetchLimit = 5
	threadPrefetchDelay        = 120 * time.Millisecond
)

type threadPrefetchWorker struct {
	epoch   uint64
	pending []string
	current *threadPrefetchRequest
}

type threadPrefetchRequest struct {
	key      string
	selected bool
}

func (a *App) invalidateThreadPrefetch() {
	a.threadEpoch++
}

func (a *App) EnableThreadPrefetch() {
	a.threadPrefetchEnabled = true
}

func (a *App) prefetchVisibleThreads() {
	if !a.threadPrefetchEnabled || a.cacheQuitting() || a.loading || a.selectedLabel == mail.Draft || a.selectedLabel == mail.Outbox {
		return
	}
	worker := a.threadPrefetch
	if worker == nil || worker.epoch != a.threadEpoch {
		worker = &threadPrefetchWorker{epoch: a.threadEpoch}
		a.threadPrefetch = worker
	}
	for _, summary := range a.visibleThreads() {
		worker.pending = append(worker.pending, a.summaryKey(summary))
		if len(worker.pending) >= visibleThreadPrefetchLimit {
			break
		}
	}
	epoch := a.threadEpoch
	if !a.admitCacheWork() {
		return
	}
	a.schedule(func() {
		defer a.cacheState.writes.Done()
		time.Sleep(threadPrefetchDelay)
		a.publish(func() {
			if a.threadPrefetch == worker && a.threadEpoch == epoch {
				a.runThreadPrefetch(worker)
			}
		})
	})
}

func (a *App) runThreadPrefetch(worker *threadPrefetchWorker) {
	if worker.current != nil || worker.epoch != a.threadEpoch || a.accountTransition != nil || a.mutationsPending() {
		return
	}
	for len(worker.pending) > 0 {
		key := worker.pending[0]
		worker.pending = worker.pending[1:]
		if key == a.selectedThread && a.threadLoading {
			continue
		}
		if _, ok := a.threadCache[key]; ok {
			continue
		}
		ref := parseThreadRef(key)
		service, generation := a.ownerService(ref.AccountEmail)
		if service == nil {
			continue
		}
		request := &threadPrefetchRequest{key: key}
		worker.current = request
		owner := normalizedEmail(ref.AccountEmail)
		if owner == "" {
			owner = a.storeAccount
		}
		email := ref.AccountEmail
		if email == "" {
			email = a.storeAccount
		}
		cacheVersion, cacheRevision := a.nextThreadCacheVersion(email, ref.ID)
		if !a.admitCacheWork() {
			worker.current = nil
			return
		}
		store := a.store
		a.schedule(func() {
			defer a.cacheState.writes.Done()
			var cached mail.Thread
			found := false
			if store != nil && email != "" {
				var err error
				cached, found, err = store.Thread(context.Background(), email, ref.ID)
				logCacheError(err)
				cached = withThreadOwner(cached, owner)
			}
			thread, err := service.Thread(context.Background(), ref.ID)
			thread = withThreadOwner(thread, owner)
			a.publish(func() {
				if a.threadPrefetch != worker || worker.epoch != a.threadEpoch || worker.current != request {
					return
				}
				worker.current = nil
				selected := request.selected
				if generation == a.generationForThreadKey(key) && err == nil && cacheVersion.Load() == cacheRevision {
					if selected {
						a.threadFreshCall = a.threadCall
					}
					if a.threadCache == nil {
						a.threadCache = make(map[string]mail.Thread)
					}
					a.threadCache[key] = thread
					if selected && a.selectedThread == key && a.threadLoading {
						a.showThread(key, thread)
					}
					if a.ownerVerified(ref.AccountEmail) {
						a.persistThread(thread)
					}
				} else if selected && a.selectedThread == key && a.threadLoading {
					a.threadLoading = false
					if a.thread.ID != ref.ID {
						a.threadErr = err.Error()
					}
				}
				if found && generation == a.generationForThreadKey(key) && cacheVersion.Load() == cacheRevision {
					if a.selectedThread == key && a.threadLoading && a.threadFreshCall < a.threadCall {
						a.showThread(key, cached)
					}
				}
				a.runThreadPrefetch(worker)
			})
		})
		return
	}
}
