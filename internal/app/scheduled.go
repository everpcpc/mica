package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"mica/internal/mail"
	"mica/locales"
)

const scheduledRetryDelay = time.Minute

type scheduledRuntime struct {
	generation  uint64
	load        uint64
	timerSeq    uint64
	timers      map[string]uint64
	owners      map[string]uint64
	running     map[string]uint64
	generations map[string]uint64
}

type scheduledRestore struct {
	store      outboxStore
	done       func()
	generation uint64
	load       uint64
	owners     map[string]uint64
}

func scheduledKey(email, id string) string {
	return normalizedEmail(email) + "\x00" + id
}

func (a *App) currentTime() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

func (a *App) resetScheduledOutbox() {
	a.scheduledRuntime.generation++
	a.scheduledRuntime.load++
	a.scheduledRuntime.timers = make(map[string]uint64)
	a.scheduledRuntime.owners = make(map[string]uint64)
	a.scheduledRuntime.running = make(map[string]uint64)
	a.scheduledRuntime.generations = make(map[string]uint64)
}

func (a *App) invalidateScheduledOutboxTimer(key string) uint64 {
	a.scheduledRuntime.timerSeq++
	if a.scheduledRuntime.timers == nil {
		a.scheduledRuntime.timers = make(map[string]uint64)
	}
	a.scheduledRuntime.timers[key] = a.scheduledRuntime.timerSeq
	return a.scheduledRuntime.timerSeq
}

func (a *App) removeScheduledOutboxForAccount(email string) {
	email = normalizedEmail(email)
	if a.scheduledRuntime.owners == nil {
		a.scheduledRuntime.owners = make(map[string]uint64)
	}
	a.scheduledRuntime.owners[email]++
	prefix := email + "\x00"
	for key := range a.scheduledRuntime.timers {
		if strings.HasPrefix(key, prefix) {
			delete(a.scheduledRuntime.timers, key)
		}
	}
}

func (a *App) beginScheduledWork(email string) (outboxStore, func(), bool) {
	email = normalizedEmail(email)
	client := a.accountClients[email]
	version := a.serviceVersion
	var generation uint64
	if email != "" {
		_, generation = a.ownerService(email)
	}
	a.cacheState.mu.Lock()
	defer a.cacheState.mu.Unlock()
	if a.outbox == nil || a.cacheState.quitting || a.cacheState.attachmentWritesBlocked || version != a.cacheState.version || client != nil && client.generation.Load() != generation {
		return nil, nil, false
	}
	a.cacheState.writes.Add(1)
	return a.outbox, a.cacheState.writes.Done, true
}

func (a *App) prepareScheduledOutboxRestore() (scheduledRestore, bool) {
	store, done, ok := a.beginScheduledWork("")
	if !ok {
		return scheduledRestore{}, false
	}
	owners := make(map[string]uint64, len(a.scheduledRuntime.owners))
	for email, version := range a.scheduledRuntime.owners {
		owners[email] = version
	}
	a.scheduledRuntime.load++
	return scheduledRestore{
		store: store, done: done, generation: a.scheduledRuntime.generation,
		load: a.scheduledRuntime.load, owners: owners,
	}, true
}

func (a *App) restoreScheduledOutbox() {
	restore, ok := a.prepareScheduledOutboxRestore()
	if !ok {
		return
	}
	a.schedule(func() { a.runScheduledOutboxRestore(restore) })
}

func (a *App) runScheduledOutboxRestore(restore scheduledRestore) {
	defer restore.done()
	records, err := restore.store.ScheduledOutboxMessages(context.Background())
	if err != nil {
		logCacheError(err)
		return
	}
	if len(records) == 0 {
		return
	}
	a.publish(func() {
		if restore.generation != a.scheduledRuntime.generation || restore.load != a.scheduledRuntime.load || a.scheduledQuitting() {
			return
		}
		for _, record := range records {
			record.AccountEmail = normalizedEmail(record.AccountEmail)
			if restore.owners[record.AccountEmail] != a.scheduledRuntime.owners[record.AccountEmail] {
				continue
			}
			record.Compose = mail.CloneCompose(record.Compose)
			a.restoreScheduledOutboxRecord(record)
		}
	})
}

func (a *App) restoreScheduledOutboxRecord(record mail.OutboxMessage) {
	key := scheduledKey(record.AccountEmail, record.ID)
	if _, running := a.scheduledRuntime.running[key]; running || record.SendAt.IsZero() {
		return
	}
	switch record.State {
	case mail.OutboxScheduled:
		a.armScheduledOutbox(record)
	case mail.OutboxSending:
		a.recoverScheduledOutbox(record)
	}
}

func (a *App) recoverScheduledOutbox(record mail.OutboxMessage) {
	owner := normalizedEmail(record.AccountEmail)
	store, done, ok := a.beginScheduledWork(owner)
	if !ok {
		return
	}
	generation := a.scheduledRuntime.generation
	ownerVersion := a.scheduledRuntime.owners[owner]
	record.Updated = a.currentTime().UTC()
	a.schedule(func() {
		defer done()
		recovered, err := store.RecoverScheduledOutbox(context.Background(), owner, record)
		if err != nil {
			logCacheError(err)
			return
		}
		a.publish(func() {
			if recovered && generation == a.scheduledRuntime.generation && ownerVersion == a.scheduledRuntime.owners[owner] && owner == a.activeComposeOwner() && a.selectedLabel == mail.Outbox {
				a.refreshOutbox()
			}
		})
	})
}

func (a *App) scheduledQuitting() bool {
	a.cacheState.mu.Lock()
	defer a.cacheState.mu.Unlock()
	return a.cacheState.quitting
}

func (a *App) armScheduledOutbox(record mail.OutboxMessage) {
	a.armScheduledOutboxDelay(record, record.SendAt.Sub(a.currentTime()), false)
}

func (a *App) armScheduledOutboxDelay(record mail.OutboxMessage, delay time.Duration, force bool) {
	owner := normalizedEmail(record.AccountEmail)
	key := scheduledKey(owner, record.ID)
	_, running := a.scheduledRuntime.running[key]
	if a.outbox == nil || a.scheduledQuitting() || running || record.State != mail.OutboxScheduled || record.SendAt.IsZero() {
		return
	}
	if delay < 0 {
		delay = 0
	}
	timer := a.invalidateScheduledOutboxTimer(key)
	generation := a.scheduledRuntime.generation
	ownerVersion := a.scheduledRuntime.owners[owner]
	record.Compose = mail.CloneCompose(record.Compose)
	a.after(delay, func() {
		a.publish(func() {
			if generation != a.scheduledRuntime.generation || ownerVersion != a.scheduledRuntime.owners[owner] || a.scheduledRuntime.timers[key] != timer || a.scheduledQuitting() {
				return
			}
			a.runScheduledOutbox(record, force, timer)
		})
	})
}

func (a *App) scheduledOutboxCurrent(key string, generation, ownerVersion, timer uint64) bool {
	return generation == a.scheduledRuntime.generation && ownerVersion == a.scheduledRuntime.owners[scheduledOwnerFromKey(key)] && a.scheduledRuntime.timers[key] == timer
}

func scheduledOwnerFromKey(key string) string {
	owner, _, _ := strings.Cut(key, "\x00")
	return owner
}

func (a *App) runScheduledOutbox(record mail.OutboxMessage, force bool, timer uint64) {
	owner := normalizedEmail(record.AccountEmail)
	key := scheduledKey(owner, record.ID)
	if force || timer == 0 {
		timer = a.invalidateScheduledOutboxTimer(key)
	}
	generation := a.scheduledRuntime.generation
	ownerVersion := a.scheduledRuntime.owners[owner]
	if !a.scheduledOutboxCurrent(key, generation, ownerVersion, timer) || a.scheduledQuitting() {
		return
	}
	if _, running := a.scheduledRuntime.running[key]; running {
		return
	}
	if owner == a.activeComposeOwner() && a.outboxBusy != "" && a.outboxBusy != record.ID {
		a.armScheduledOutboxDelay(record, scheduledRetryDelay, force)
		return
	}
	if a.scheduledRuntime.running == nil {
		a.scheduledRuntime.running = make(map[string]uint64)
	}
	a.scheduledRuntime.running[key] = timer
	if owner == a.activeComposeOwner() {
		a.outboxBusy = record.ID
	}
	if !a.composeOwnerVerified(owner) {
		if service, _ := a.ownerService(owner); service != nil {
			a.finishScheduledOutbox(record, generation, ownerVersion, timer, outboxAttemptResult{err: errAccountClientUnavailable}, false, true, force)
			return
		}
	}
	a.ensureAccountClient(owner, func(err error) {
		if err != nil {
			a.finishScheduledOutbox(record, generation, ownerVersion, timer, outboxAttemptResult{err: err}, false, true, force)
			return
		}
		a.startScheduledOutboxSend(record, generation, ownerVersion, timer, force)
	})
}

func (a *App) startScheduledOutboxSend(record mail.OutboxMessage, generation, ownerVersion, timer uint64, force bool) {
	owner := normalizedEmail(record.AccountEmail)
	key := scheduledKey(owner, record.ID)
	if !a.scheduledOutboxCurrent(key, generation, ownerVersion, timer) || a.scheduledQuitting() {
		a.finishScheduledOutbox(record, generation, ownerVersion, timer, outboxAttemptResult{}, false, false, force)
		return
	}
	service, ownerGeneration := a.ownerService(owner)
	if a.scheduledRuntime.generations == nil {
		a.scheduledRuntime.generations = make(map[string]uint64)
	}
	a.scheduledRuntime.generations[key] = ownerGeneration
	manager, hasDrafts := service.(mail.DraftManager)
	var err error
	switch {
	case service == nil || !a.composeOwnerVerified(owner):
		err = errAccountClientUnavailable
	case record.DraftID != "" && !hasDrafts:
		err = errors.New("mail: drafts are unavailable")
	}
	if err != nil {
		a.finishScheduledOutbox(record, generation, ownerVersion, timer, outboxAttemptResult{err: err}, false, true, force)
		return
	}
	store, done, ok := a.beginScheduledWork(owner)
	if !ok {
		a.finishScheduledOutbox(record, generation, ownerVersion, timer, outboxAttemptResult{err: errAccountClientUnavailable}, false, true, force)
		return
	}
	a.schedule(func() {
		defer done()
		claimedRecord, claimed, err := store.ClaimScheduledOutbox(context.Background(), owner, record, a.currentTime(), force)
		result := outboxAttemptResult{err: err}
		if err == nil && claimed {
			record = claimedRecord
			result = attemptOutboxSend(store, owner, claimedRecord, nil, service, manager, hasDrafts, false)
			if result.err != nil {
				record.LastError = result.err.Error()
				record.Updated = a.currentTime().UTC()
				if _, recoverErr := store.RecoverScheduledOutbox(context.Background(), owner, record); recoverErr != nil {
					result.err = errors.Join(result.err, recoverErr)
				}
			}
		}
		a.publish(func() {
			a.finishScheduledOutbox(record, generation, ownerVersion, timer, result, claimed, err != nil && !claimed, force)
		})
	})
}

func (a *App) finishScheduledOutbox(record mail.OutboxMessage, generation, ownerVersion, timer uint64, result outboxAttemptResult, claimed, retry, force bool) {
	owner := normalizedEmail(record.AccountEmail)
	key := scheduledKey(owner, record.ID)
	current := a.scheduledOutboxCurrent(key, generation, ownerVersion, timer)
	if a.scheduledRuntime.running[key] == timer {
		delete(a.scheduledRuntime.running, key)
	}
	if a.scheduledRuntime.timers[key] == timer {
		delete(a.scheduledRuntime.timers, key)
	}
	if !current || a.scheduledQuitting() {
		return
	}
	if ownerGeneration, checked := a.scheduledRuntime.generations[key]; checked {
		delete(a.scheduledRuntime.generations, key)
		if !a.ownerOperationCurrent(owner, ownerGeneration) {
			return
		}
	}
	if owner == a.activeComposeOwner() && a.outboxBusy == record.ID {
		a.outboxBusy = ""
	}
	visible := owner == a.activeComposeOwner()
	if retry {
		a.armScheduledOutboxDelay(record, scheduledRetryDelay, force)
		if force && visible && result.err != nil {
			a.notice = locales.SendFailed(result.err.Error())
		}
	} else if visible && claimed {
		switch {
		case result.err != nil && result.sent:
			a.notice = locales.OutboxCleanupFailed(result.err.Error())
		case result.err != nil:
			a.notice = locales.SendFailed(result.err.Error())
		case result.sent:
			a.notice = locales.SendSucceeded()
			if a.compose != nil && a.compose.outboxID == record.ID && a.composerOwner(a.compose) == owner && !a.compose.sending {
				a.compose = nil
			}
			if outboxRecordKey(a.selectedThread) == record.ID {
				a.clearThread()
			}
		}
	}
	if visible && a.selectedLabel == mail.Outbox {
		a.refreshOutbox()
	}
}

func (a *App) cancelScheduledOutbox() {
	if a.compose != nil || a.accountTransition != nil || a.outboxBusy != "" {
		return
	}
	id := outboxRecordKey(a.selectedThread)
	record, found := a.outboxRecords[id]
	if !found || record.State != mail.OutboxScheduled {
		return
	}
	owner := normalizedEmail(record.AccountEmail)
	if owner == "" || owner != a.activeComposeOwner() {
		return
	}
	key := scheduledKey(owner, id)
	if _, running := a.scheduledRuntime.running[key]; running {
		return
	}
	_, generation := a.ownerService(owner)
	timer := a.invalidateScheduledOutboxTimer(key)
	store, done, ok := a.beginScheduledWork(owner)
	if !ok {
		a.armScheduledOutbox(record)
		return
	}
	a.outboxBusy = id
	a.schedule(func() {
		defer done()
		err := store.CancelScheduledOutbox(context.Background(), owner, record)
		a.publish(func() {
			if a.outboxBusy == id {
				a.outboxBusy = ""
			}
			if !a.ownerOperationCurrent(owner, generation) {
				return
			}
			if err != nil {
				a.notice = locales.ScheduleCancelFailed(err.Error())
				a.armScheduledOutbox(record)
				return
			}
			if a.scheduledRuntime.timers[key] == timer {
				delete(a.scheduledRuntime.timers, key)
			}
			a.openRecoveredDraft(mail.DraftSnapshot{DraftID: record.DraftID, Compose: record.Compose}, owner)
			a.notice = locales.ScheduleCanceled()
			if a.selectedLabel == mail.Outbox {
				a.refreshOutbox()
			}
		})
	})
}
