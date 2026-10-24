package app

import (
	"context"
	"sort"

	"mica/internal/mail"
)

type unifiedState struct {
	accounts map[string]*unifiedAccountStream
	modified map[string]mail.ThreadSummary
	removed  map[string]bool
	query    mail.Query
	pending  int
}

func (a *App) localUnifiedAccounts() []mail.Account {
	seen := make(map[string]bool)
	accounts := make([]mail.Account, 0, len(a.accountClients)+1)
	if !a.account.Demo && normalizedEmail(a.account.Email) != "" {
		account := a.account
		account.Email = normalizedEmail(account.Email)
		accounts = append(accounts, account)
		seen[account.Email] = true
	}
	for _, client := range a.accountClients {
		if seen[client.account.Email] {
			continue
		}
		seen[client.account.Email] = true
		accounts = append(accounts, client.account)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Email < accounts[j].Email })
	return accounts
}

func (a *App) unifiedValid(state *unifiedState, request int) bool {
	return a.unified == state && a.request == request && a.compositeThreads && a.selectedLabel == mail.Unified
}

func unifiedStateError(state *unifiedState) string {
	for _, stream := range state.accounts {
		if stream.err != nil {
			return stream.err.Error()
		}
	}
	return ""
}

func (a *App) unifiedAccountCurrent(email string, generation uint64) bool {
	_, current := a.ownerService(email)
	return current == generation
}

func (a *App) refreshUnifiedAfterAccountRemoval(email string) {
	email = normalizedEmail(email)
	if a.selectedLabel != mail.Unified {
		if accountInboxLabel(a.selectedLabel) == email {
			a.selectedLabel = mail.Inbox
			a.clearThread()
			a.refresh()
		}
		return
	}
	a.undo = nil
	for id := range a.threadSelection.All() {
		if normalizedEmail(parseThreadRef(id).AccountEmail) == email {
			a.threadSelection.Remove(id)
		}
	}
	if normalizedEmail(parseThreadRef(a.selectedThread).AccountEmail) == email {
		a.clearThread()
	}
	a.refresh()
}

func (a *App) refreshUnified() {
	a.request++
	request := a.request
	a.nextPageToken = ""
	a.loadingMore = false
	a.loading = true
	a.err = ""
	a.compositeThreads = true
	state := &unifiedState{
		accounts: make(map[string]*unifiedAccountStream),
		removed:  make(map[string]bool),
		query:    a.currentQuery(),
	}
	a.unified = state
	store := a.store
	var accounts []mail.Account
	if store == nil {
		accounts = a.localUnifiedAccounts()
	}
	a.schedule(func() {
		var err error
		if store != nil {
			accounts, err = store.Accounts(context.Background())
		}
		a.publish(func() {
			if !a.unifiedValid(state, request) {
				return
			}
			if err != nil {
				a.loading = false
				a.err = err.Error()
				return
			}
			a.accountCount = len(accounts)
			a.unifiedAccounts = accounts
			if len(accounts) < 2 {
				a.selectedLabel = mail.Inbox
				a.compositeThreads = false
				a.unified = nil
				a.loading = false
				a.clearThread()
				a.refresh()
				return
			}
			state.pending = len(accounts)
			generations := make(map[string]uint64, len(accounts))
			for _, account := range accounts {
				email := normalizedEmail(account.Email)
				state.accounts[email] = newUnifiedAccountStream(email, nil)
				_, generations[email] = a.ownerService(email)
			}
			store, query := a.store, state.query
			a.schedule(func() {
				type cachedAccount struct {
					threads []mail.ThreadSummary
					found   bool
				}
				cachedAccounts := make(map[string]cachedAccount, len(accounts))
				if store != nil {
					for _, account := range accounts {
						email := normalizedEmail(account.Email)
						cached, found, cacheErr := store.ThreadList(context.Background(), email, query)
						logCacheError(cacheErr)
						if cacheErr == nil {
							cachedAccounts[email] = cachedAccount{threads: cached, found: found}
						}
					}
				}
				a.publish(func() {
					if !a.unifiedValid(state, request) {
						return
					}
					for _, account := range accounts {
						email := normalizedEmail(account.Email)
						cached := cachedAccounts[email]
						if cached.found && a.unifiedAccountCurrent(email, generations[email]) {
							resetUnifiedStream(state, state.accounts[email], cached.threads, "")
						}
					}
					a.applyUnifiedMerge(state, threadPageSize)
					for _, account := range accounts {
						a.loadUnifiedAccount(state, request, normalizedEmail(account.Email))
					}
				})
			})
		})
	})
}

func (a *App) loadUnifiedAccount(state *unifiedState, request int, email string) {
	_, generation := a.ownerService(email)
	a.ensureAccountClient(email, func(err error) {
		if err != nil {
			a.finishUnifiedAccount(state, request, email, generation, mail.ThreadPage{}, err)
			return
		}
		service, generation := a.ownerService(email)
		if service == nil {
			a.finishUnifiedAccount(state, request, email, generation, mail.ThreadPage{}, errAccountClientUnavailable)
			return
		}
		a.schedule(func() {
			page, err := listThreadPage(context.Background(), service, state.query)
			a.finishUnifiedAccount(state, request, email, generation, page, err)
		})
	})
}

func (a *App) finishUnifiedAccount(state *unifiedState, request int, email string, generation uint64, page mail.ThreadPage, err error) {
	a.publish(func() {
		if !a.unifiedValid(state, request) {
			return
		}
		stream := state.accounts[email]
		if stream == nil {
			return
		}
		if !a.unifiedAccountCurrent(email, generation) {
			err = errAccountClientUnavailable
		}
		if err != nil {
			stream.err = err
		} else {
			stream.err = nil
			resetUnifiedStream(state, stream, page.Threads, page.NextPageToken)
			a.persistUnifiedStream(state, stream)
		}
		state.pending--
		a.loading = state.pending > 0
		if state.pending == 0 && unifiedStateError(state) == "" {
			a.listFreshRequest = request
		}
		a.err = unifiedStateError(state)
		a.applyUnifiedMerge(state, threadPageSize)
	})
}

func (a *App) applyUnifiedMerge(state *unifiedState, limit int) {
	streams := unifiedStreams(state)
	for _, stream := range streams {
		stream.cursor = 0
		stream.emitted = make(map[string]bool)
	}
	merged := mergeUnifiedAccounts(streams, limit)
	for _, stream := range streams {
		for _, summary := range merged {
			if summary.AccountEmail == stream.email {
				stream.emitted[summary.ID] = true
			}
		}
		stream.syncCursor()
	}
	a.applyThreadList(merged, false)
}

func (a *App) loadMoreUnified() {
	state := a.unified
	if state == nil || a.loading || a.loadingMore || a.err != "" || !unifiedHasMore(unifiedStreams(state)) {
		return
	}
	request := a.request
	a.loadingMore = true
	merged := mergeUnifiedAccounts(unifiedStreams(state), threadPageSize)
	if len(merged) > 0 {
		a.threads = append(a.threads, merged...)
		a.applyThreadList(a.threads, false)
		a.loadingMore = false
		return
	}
	pending := 0
	for _, stream := range state.accounts {
		if stream.nextPageToken == "" || stream.cursor < len(stream.threads) {
			continue
		}
		service, generation := a.ownerService(stream.email)
		if service == nil {
			stream.err = errAccountClientUnavailable
			continue
		}
		pending++
		email := stream.email
		query := state.query
		query.PageToken = stream.nextPageToken
		a.schedule(func() {
			page, err := listThreadPage(context.Background(), service, query)
			a.publish(func() {
				if !a.unifiedValid(state, request) {
					return
				}
				switch {
				case !a.unifiedAccountCurrent(email, generation):
					stream.err = errAccountClientUnavailable
				case err != nil:
					stream.err = err
				default:
					stream.err = nil
					appendUnifiedPage(state, stream, page)
					a.persistUnifiedStream(state, stream)
				}
				pending--
				if pending != 0 {
					return
				}
				a.loadingMore = false
				a.err = unifiedStateError(state)
				a.threads = append(a.threads, mergeUnifiedAccounts(unifiedStreams(state), threadPageSize)...)
				a.applyThreadList(a.threads, false)
			})
		})
	}
	if pending == 0 {
		a.loadingMore = false
		a.err = unifiedStateError(state)
	}
}

func unifiedStreams(state *unifiedState) []*unifiedAccountStream {
	streams := make([]*unifiedAccountStream, 0, len(state.accounts))
	for _, stream := range state.accounts {
		streams = append(streams, stream)
	}
	sort.Slice(streams, func(i, j int) bool { return streams[i].email < streams[j].email })
	return streams
}
