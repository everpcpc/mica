package app

import (
	"context"
	"sort"
	"strings"
	"time"

	"mica/internal/mail"
	"mica/locales"
)

type snoozeStore interface {
	SaveSnooze(context.Context, string, mail.SnoozeRecord) error
	SnoozeRecords(context.Context, string) ([]mail.SnoozeRecord, error)
	SnoozeRecord(context.Context, string, string, time.Time) (mail.SnoozeRecord, bool, error)
	DeleteSnooze(context.Context, string, string, time.Time) error
	CancelSnooze(context.Context, string, string) error
}

type snoozeRuntime struct {
	records    map[string]mail.SnoozeRecord
	versions   map[string]uint64
	changes    uint64
	generation uint64
	load       uint64
	timers     map[string]uint64
	timerSeq   uint64
}

type snoozeCacheOperation func(func(snoozeStore) error) error

func snoozeKey(email, threadID string) string {
	return normalizedEmail(email) + "\x00" + threadID
}

func (a *App) removeSnoozesForAccount(email string) {
	prefix := normalizedEmail(email) + "\x00"
	for key := range a.snoozeRuntime.records {
		if strings.HasPrefix(key, prefix) {
			delete(a.snoozeRuntime.records, key)
		}
	}
	for key := range a.snoozeRuntime.timers {
		if strings.HasPrefix(key, prefix) {
			delete(a.snoozeRuntime.timers, key)
		}
	}
	for key := range a.snoozeRuntime.versions {
		if strings.HasPrefix(key, prefix) {
			delete(a.snoozeRuntime.versions, key)
		}
	}
}

func (a *App) currentSnoozedSummaries() []mail.ThreadSummary {
	var summaries []mail.ThreadSummary
	for _, summary := range a.threads {
		if containsLabel(summary.LabelIDs, mail.Snoozed) {
			summaries = append(summaries, summary)
		}
	}
	return cloneThreadSummaries(summaries)
}

func mergeSnoozedSummaries(summaries []mail.ThreadSummary, records []mail.SnoozeRecord, recordsLoaded bool) []mail.ThreadSummary {
	scheduled := make(map[string]bool, len(records))
	for _, record := range records {
		scheduled[record.ThreadID] = !record.Restoring
	}
	result := make([]mail.ThreadSummary, 0, len(summaries)+len(records))
	indices := make(map[string]int, len(summaries)+len(records))
	for _, summary := range summaries {
		if !containsLabel(summary.LabelIDs, mail.Snoozed) && !scheduled[summary.ID] {
			continue
		}
		if recordsLoaded && !summary.SnoozeAt.IsZero() && !scheduled[summary.ID] {
			continue
		}
		if _, found := indices[summary.ID]; found {
			continue
		}
		indices[summary.ID] = len(result)
		result = append(result, summary)
	}
	for _, record := range records {
		if record.Restoring {
			continue
		}
		summary := record.Summary
		summary.ID = record.ThreadID
		summary.AccountEmail = record.AccountEmail
		summary.SnoozeAt = record.WakeAt
		if index, found := indices[summary.ID]; found {
			current := result[index]
			current.SnoozeAt = record.WakeAt
			current.LabelIDs = modifyLabelIDs(current.LabelIDs, []string{mail.Snoozed}, []string{mail.Inbox})
			current.Unread = containsLabel(current.LabelIDs, mail.Unread)
			current.Starred = containsLabel(current.LabelIDs, mail.Starred)
			if current.AccountEmail == "" {
				current.AccountEmail = summary.AccountEmail
			}
			result[index] = current
		} else {
			indices[summary.ID] = len(result)
			result = append(result, summary)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		left, right := result[i], result[j]
		if left.SnoozeAt.IsZero() != right.SnoozeAt.IsZero() {
			return right.SnoozeAt.IsZero()
		}
		if left.SnoozeAt.IsZero() {
			if left.Updated.Equal(right.Updated) {
				return left.ID < right.ID
			}
			return left.Updated.After(right.Updated)
		}
		if left.SnoozeAt.Equal(right.SnoozeAt) {
			return left.ID < right.ID
		}
		return left.SnoozeAt.Before(right.SnoozeAt)
	})
	return result
}

func (a *App) resetSnoozeRuntime() {
	a.snoozeRuntime.generation++
	a.snoozeRuntime.load++
	a.snoozeRuntime.records = make(map[string]mail.SnoozeRecord)
	a.snoozeRuntime.versions = make(map[string]uint64)
	a.snoozeRuntime.timers = make(map[string]uint64)
}

func (a *App) snoozeVersion(key string) uint64 {
	a.snoozeRuntime.changes++
	if a.snoozeRuntime.versions == nil {
		a.snoozeRuntime.versions = make(map[string]uint64)
	}
	a.snoozeRuntime.versions[key] = a.snoozeRuntime.changes
	return a.snoozeRuntime.changes
}

func (a *App) snoozeCache(email string) snoozeCacheOperation {
	email = normalizedEmail(email)
	store := a.snooze
	if store == nil || email == "" {
		return nil
	}
	client := a.accountClients[email]
	version := a.serviceVersion
	_, generation := a.ownerService(email)
	return func(operation func(snoozeStore) error) error {
		a.cacheState.mu.Lock()
		defer a.cacheState.mu.Unlock()
		if a.cacheState.quitting || a.cacheState.attachmentWritesBlocked || version != a.cacheState.version || client != nil && client.generation.Load() != generation {
			return errAccountClientUnavailable
		}
		a.cacheState.writes.Add(1)
		defer a.cacheState.writes.Done()
		return operation(store)
	}
}

type snoozeRestore struct {
	email      string
	cache      snoozeCacheOperation
	generation uint64
	load       uint64
}

func (a *App) prepareSnoozeRestore(email string) (snoozeRestore, bool) {
	email = normalizedEmail(email)
	cache := a.snoozeCache(email)
	if cache == nil || a.cacheState.quitting {
		return snoozeRestore{}, false
	}
	a.snoozeRuntime.load++
	return snoozeRestore{email: email, cache: cache, generation: a.snoozeRuntime.generation, load: a.snoozeRuntime.load}, true
}

func (a *App) restoreSnoozes() {
	restore, ok := a.prepareSnoozeRestore(a.activeComposeOwner())
	if !ok {
		return
	}
	a.schedule(func() { a.runSnoozeRestore(restore) })
}

func (a *App) runSnoozeRestore(restore snoozeRestore) {
	var records []mail.SnoozeRecord
	err := restore.cache(func(store snoozeStore) error {
		var err error
		records, err = store.SnoozeRecords(context.Background(), restore.email)
		return err
	})
	if err != nil {
		logCacheError(err)
		return
	}
	a.publish(func() {
		if restore.generation != a.snoozeRuntime.generation || restore.load != a.snoozeRuntime.load || restore.email != a.activeComposeOwner() || a.cacheState.quitting {
			return
		}
		for _, record := range records {
			key := snoozeKey(record.AccountEmail, record.ThreadID)
			if a.snoozeRuntime.versions[key] != 0 {
				continue
			}
			a.setSnoozeRecord(record)
			if record.Restoring {
				a.wakeSnooze(record)
				continue
			}
			a.armSnooze(record)
		}
	})
}

func (a *App) armSnooze(record mail.SnoozeRecord) {
	a.armSnoozeDelay(record, time.Until(record.WakeAt))
}

func (a *App) invalidateSnoozeTimer(key string) uint64 {
	a.snoozeRuntime.timerSeq++
	if a.snoozeRuntime.timers == nil {
		a.snoozeRuntime.timers = make(map[string]uint64)
	}
	a.snoozeRuntime.timers[key] = a.snoozeRuntime.timerSeq
	return a.snoozeRuntime.timerSeq
}

func (a *App) armSnoozeDelay(record mail.SnoozeRecord, delay time.Duration) {
	if a.snooze == nil || a.cacheState.quitting {
		return
	}
	key := snoozeKey(record.AccountEmail, record.ThreadID)
	timer := a.invalidateSnoozeTimer(key)
	generation := a.snoozeRuntime.generation
	if delay < 0 {
		delay = 0
	}
	a.after(delay, func() {
		a.publish(func() {
			if generation != a.snoozeRuntime.generation || a.snoozeRuntime.timers[key] != timer || a.cacheState.quitting {
				return
			}
			a.wakeSnooze(record)
		})
	})
}

func (a *App) retrySnooze(record mail.SnoozeRecord) {
	key := snoozeKey(record.AccountEmail, record.ThreadID)
	if current, found := a.snoozeRuntime.records[key]; found && current.WakeAt.Equal(record.WakeAt) {
		a.armSnoozeDelay(record, time.Minute)
	}
}

func (a *App) wakeSnooze(record mail.SnoozeRecord) {
	if a.cacheState.quitting {
		return
	}
	email := normalizedEmail(record.AccountEmail)
	record.Summary.ID = record.ThreadID
	record.Summary.AccountEmail = email
	key := snoozeKey(email, record.ThreadID)
	id, visible := a.snoozeSelection(record)
	service, _ := a.ownerService(email)
	cache := a.snoozeCache(email)
	if service == nil || cache == nil {
		return
	}
	version := a.snoozeVersion(key)
	generation := a.snoozeRuntime.generation
	var restored, applied, stale bool
	a.enqueueMutation(mutationJob{
		service: service,
		owner:   email,
		id:      id,
		runFunc: func() error {
			var found bool
			if err := cache(func(store snoozeStore) error {
				var err error
				_, found, err = store.SnoozeRecord(context.Background(), email, record.ThreadID, record.WakeAt)
				return err
			}); err != nil {
				return err
			}
			if !found {
				stale = true
				return nil
			}
			record.Restoring = true
			if err := cache(func(store snoozeStore) error {
				return store.SaveSnooze(context.Background(), email, record)
			}); err != nil {
				return err
			}
			if err := service.ModifyThread(context.Background(), record.ThreadID, []string{mail.Inbox}, nil); err != nil {
				return err
			}
			restored = true
			if err := cache(func(store snoozeStore) error {
				return store.DeleteSnooze(context.Background(), email, record.ThreadID, record.WakeAt)
			}); err != nil {
				return err
			}
			applied = true
			return nil
		},
		failure: a.snoozeFailureNotice(record),
		done: func(err error) {
			if generation != a.snoozeRuntime.generation || a.snoozeRuntime.versions[key] != version || a.cacheState.quitting {
				return
			}
			if stale {
				if current, found := a.snoozeRuntime.records[key]; found && current.WakeAt.Equal(record.WakeAt) {
					delete(a.snoozeRuntime.records, key)
					delete(a.snoozeRuntime.timers, key)
				}
				return
			}
			if applied || restored {
				if applied {
					delete(a.snoozeRuntime.records, key)
					delete(a.snoozeRuntime.timers, key)
				} else {
					a.retrySnooze(record)
				}
				a.applySnoozeStateForRecord(record, []string{mail.Inbox}, []string{mail.Snoozed}, time.Time{})
				a.restoreSnoozeInbox(record)
			}
			if err != nil && !restored {
				a.retrySnooze(record)
			}
			if applied && visible {
				a.notice = locales.SnoozeRestored()
			}
		},
	})
}
