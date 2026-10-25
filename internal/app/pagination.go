package app

import (
	"context"

	"mica/internal/mail"
	"mica/locales"
)

func (a *App) resetNextPage() {
	a.nextPage = nil
	a.nextPageRequest = 0
	a.nextPageQueryToken = ""
	a.nextPageErr = nil
	a.nextPageToken = ""
	a.cacheExhausted = false
	a.loadingMore = false
}

func (a *App) loadMoreThreads() {
	if a.selectedLabel == mail.Unified {
		a.loadMoreUnified()
		return
	}
	if a.loading || a.loadingMore || a.cacheExhausted && a.nextPageToken == "" || a.err != "" && a.nextPageErr == nil && a.nextPageToken != "" {
		return
	}
	if a.nextPage != nil {
		if a.nextPageRequest != a.request || a.nextPageQueryToken != a.nextPageToken {
			a.nextPage = nil
			a.nextPageRequest = 0
			a.nextPageQueryToken = ""
		} else {
			page := a.nextPage
			a.nextPage = nil
			a.nextPageRequest = 0
			a.nextPageQueryToken = ""
			a.nextPageToken = page.NextPageToken
			a.applyHistoryChanges(page.Threads, nil, page.NextPageToken != "")
			a.prefetchNextThreads()
			return
		}
	}
	if a.nextPageErr != nil {
		err := a.nextPageErr
		a.nextPageErr = nil
		a.loadCachedNextThreads(err)
		return
	}
	if a.nextPageToken == "" {
		a.loadCachedNextThreads(nil)
		return
	}
	a.prefetchNextThreads()
}

func (a *App) prefetchNextThreads() {
	if a.nextPageToken == "" || a.loading || a.loadingMore || a.nextPage != nil || a.nextPageErr != nil {
		return
	}
	a.loadingMore = true
	service, version := a.service, a.serviceVersion
	request := a.request
	query := a.currentQuery()
	query.PageToken = a.nextPageToken
	a.schedule(func() {
		page, err := listThreadPage(context.Background(), service, query)
		a.publish(func() {
			if version != a.serviceVersion || request != a.request || query.PageToken != a.nextPageToken {
				return
			}
			a.loadingMore = false
			if err != nil {
				a.nextPageErr = err
				return
			}
			a.nextPage = &page
			a.nextPageRequest = request
			a.nextPageQueryToken = query.PageToken
		})
	})
}

func (a *App) loadCachedNextThreads(cause error) {
	if a.loadingMore {
		return
	}
	store, email := a.store, a.storeAccount
	if email == "" {
		email = normalizedEmail(a.account.Email)
	}
	if store == nil || email == "" {
		a.cacheExhausted = true
		if cause != nil {
			a.err = cause.Error()
		}
		return
	}
	version, request := a.serviceVersion, a.request
	query := a.currentQuery()
	snoozed := a.selectedLabel == mail.Snoozed
	var snoozeCache snoozeCacheOperation
	if snoozed {
		snoozeCache = a.snoozeCache(email)
	}
	seen := make(map[string]bool, len(a.threads))
	for _, summary := range a.threads {
		seen[summary.ID] = true
	}
	a.loadingMore = true
	a.schedule(func() {
		cached, found, cacheErr := store.ThreadList(context.Background(), email, query)
		if cacheErr != nil {
			logCacheError(cacheErr)
		}
		if !found {
			if searchStore, ok := store.(offlineSearchStore); ok {
				query.Limit = len(seen) + threadPageSize
				cached, cacheErr = searchStore.SearchThreads(context.Background(), email, query)
				if cacheErr != nil {
					logCacheError(cacheErr)
				}
			}
		}
		if snoozed {
			var records []mail.SnoozeRecord
			recordsLoaded := false
			if snoozeCache != nil {
				if err := snoozeCache(func(store snoozeStore) error {
					var err error
					records, err = store.SnoozeRecords(context.Background(), email)
					return err
				}); err != nil {
					logCacheError(err)
				} else {
					recordsLoaded = true
				}
			}
			cached = mergeSnoozedSummaries(cached, records, recordsLoaded)
		}
		next := make([]mail.ThreadSummary, 0, threadPageSize)
		for _, summary := range cached {
			if seen[summary.ID] || !historyListContains(query, summary) {
				continue
			}
			next = append(next, summary)
			if len(next) == threadPageSize {
				break
			}
		}
		a.publish(func() {
			if version != a.serviceVersion || request != a.request {
				return
			}
			a.loadingMore = false
			if len(next) == 0 {
				a.cacheExhausted = true
				if snoozed && a.accountVerified {
					a.persistList(query, a.threads)
				}
				if cause != nil {
					a.err = cause.Error()
				}
				return
			}
			a.cacheExhausted = false
			a.nextPageToken = ""
			a.applyHistoryChanges(next, nil, true)
			if cause != nil {
				a.err = locales.OfflineResults()
			}
		})
	})
}
