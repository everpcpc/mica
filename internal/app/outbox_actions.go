package app

import (
	"context"
	"time"

	"mica/internal/mail"
	"mica/locales"
)

func (a *App) retryOutbox() {
	if a.compose != nil || a.accountTransition != nil || a.outboxBusy != "" {
		return
	}
	id := outboxRecordKey(a.selectedThread)
	record, found := a.outboxRecords[id]
	if !found {
		return
	}
	owner := normalizedEmail(record.AccountEmail)
	if owner == "" || owner != a.activeComposeOwner() {
		return
	}
	store := a.outbox
	service, generation := a.ownerService(owner)
	if store == nil || service == nil {
		return
	}
	manager, hasDrafts := service.(mail.DraftManager)
	drafts, _ := a.store.(draftStore)
	a.outboxBusy = id
	a.cacheState.writes.Add(1)
	a.schedule(func() {
		defer a.cacheState.writes.Done()
		previous, found, err := store.OutboxMessage(context.Background(), owner, id)
		result := outboxAttemptResult{err: err}
		if err == nil {
			if !found {
				result.err = errOutboxUnavailable
			} else {
				record = previous
				record.Attempt++
				record.State = mail.OutboxSending
				record.Updated = time.Now()
				result = attemptOutboxSend(store, owner, record, nil, service, manager, hasDrafts)
			}
		}
		if result.sent && result.err == nil {
			clearOutboxSnapshot(drafts, owner, id)
		}
		a.publish(func() {
			if a.outboxBusy == id {
				a.outboxBusy = ""
			}
			if !a.ownerOperationCurrent(owner, generation) {
				return
			}
			switch {
			case result.err != nil && result.sent:
				a.notice = locales.OutboxCleanupFailed(result.err.Error())
			case result.err != nil:
				a.notice = locales.SendFailed(result.err.Error())
			default:
				a.notice = locales.SendSucceeded()
				if outboxRecordKey(a.selectedThread) == id {
					a.clearThread()
				}
			}
			a.refresh()
		})
	})
}

func clearOutboxSnapshot(store draftStore, owner, id string) {
	if store == nil {
		return
	}
	snapshot, found, err := store.DraftSnapshot(context.Background(), owner)
	if err == nil && found && snapshot.OutboxID == id {
		logCacheError(store.DeleteDraftSnapshot(context.Background(), owner))
	}
}

func (a *App) requestDeleteOutbox() {
	if a.compose != nil || a.accountTransition != nil || a.outboxBusy != "" {
		return
	}
	id := outboxRecordKey(a.selectedThread)
	if _, found := a.outboxRecords[id]; !found {
		return
	}
	a.outboxDeleteID = id
	a.outboxDeleteOpen = true
}

func (a *App) deleteOutbox() {
	if a.outboxBusy != "" || a.accountTransition != nil || a.compose != nil {
		return
	}
	id := a.outboxDeleteID
	record, found := a.outboxRecords[id]
	if !found {
		a.outboxDeleteOpen = false
		return
	}
	owner := normalizedEmail(record.AccountEmail)
	if owner == "" || owner != a.activeComposeOwner() {
		return
	}
	store := a.outbox
	if store == nil {
		return
	}
	_, generation := a.ownerService(owner)
	a.outboxBusy = id
	a.cacheState.writes.Add(1)
	a.schedule(func() {
		defer a.cacheState.writes.Done()
		err := store.DeleteOutboxMessage(context.Background(), owner, id, record.Attempt)
		a.publish(func() {
			if a.outboxBusy == id {
				a.outboxBusy = ""
			}
			if !a.ownerOperationCurrent(owner, generation) {
				return
			}
			a.outboxDeleteOpen = false
			a.outboxDeleteID = ""
			if err != nil {
				a.notice = locales.OutboxDeleteFailed(err.Error())
				return
			}
			if outboxRecordKey(a.selectedThread) == id {
				a.clearThread()
			}
			a.refresh()
		})
	})
}
