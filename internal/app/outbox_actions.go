package app

import (
	"context"
	"errors"
	"time"

	"mica/internal/mail"
	"mica/locales"
)

func (a *App) retryOutbox() {
	if a.compose != nil || a.accountTransition != nil || a.outboxBusy != "" {
		return
	}
	id := outboxRecordKey(a.selectedThread)
	if a.outboxReviewOpen && a.outboxReviewID != id {
		return
	}
	record, found := a.outboxRecords[id]
	if !found {
		return
	}
	owner := normalizedEmail(record.AccountEmail)
	if owner == "" || owner != a.activeComposeOwner() {
		return
	}
	if !a.composeOwnerVerified(owner) {
		a.notice = locales.DraftSaveFailedReason("account is not verified")
		return
	}
	store := a.outbox
	service, generation := a.ownerService(owner)
	if store == nil || service == nil {
		return
	}
	if record.State == mail.OutboxScheduled {
		a.runScheduledOutbox(record, true, 0)
		return
	}
	manager, hasDrafts := service.(mail.DraftManager)
	drafts, _ := a.store.(draftStore)
	allowUncertain := a.outboxReviewOpen && a.outboxReviewID == id && a.outboxReviewed[id]
	a.outboxReviewOpen = false
	a.outboxReviewID = ""
	delete(a.outboxReviewed, id)
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
				result = attemptOutboxSend(store, owner, record, nil, service, manager, hasDrafts, allowUncertain)
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
			if errors.Is(result.err, errOutboxReviewRequired) {
				if a.outboxReviewed == nil {
					a.outboxReviewed = make(map[string]bool)
				}
				a.outboxReviewed[id] = true
				a.outboxReviewID = id
				a.outboxReviewOpen = true
				a.refresh()
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

func discardOutboxDraft(store outboxStore, owner, outboxID string, manager mail.DraftManager, draftID string) error {
	if outboxID == "" {
		if draftID == "" {
			return nil
		}
		return manager.DeleteDraft(context.Background(), draftID)
	}
	if store == nil {
		return errOutboxUnavailable
	}
	record, found, err := store.OutboxMessage(context.Background(), owner, outboxID)
	if err != nil {
		return err
	}
	if !found {
		if draftID == "" {
			return nil
		}
		return manager.DeleteDraft(context.Background(), draftID)
	}
	if err := store.CompleteOutbox(context.Background(), owner, outboxID, record.Attempt); err != nil {
		return err
	}
	if draftID == "" {
		return nil
	}
	if err := manager.DeleteDraft(context.Background(), draftID); err != nil {
		snapshot := mail.DraftSnapshot{DraftID: record.DraftID, OutboxID: record.ID, Compose: record.Compose}
		if restoreErr := store.EnqueueOutbox(context.Background(), owner, record, snapshot); restoreErr != nil {
			return errors.Join(err, restoreErr)
		}
		return err
	}
	return nil
}

func (a *App) requestDeleteOutbox() {
	if a.compose != nil || a.accountTransition != nil || a.outboxBusy != "" || a.outboxReviewOpen {
		return
	}
	id := outboxRecordKey(a.selectedThread)
	record, found := a.outboxRecords[id]
	if !found || record.State == mail.OutboxScheduled {
		return
	}
	a.outboxDeleteID = id
	a.outboxDeleteOpen = true
}

func (a *App) deleteOutbox() {
	if a.outboxBusy != "" || a.accountTransition != nil || a.compose != nil || a.outboxReviewOpen {
		return
	}
	id := a.outboxDeleteID
	record, found := a.outboxRecords[id]
	if found && record.State == mail.OutboxScheduled {
		a.outboxDeleteOpen = false
		a.outboxDeleteID = ""
		return
	}
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
		err := store.CompleteOutbox(context.Background(), owner, id, record.Attempt)
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
