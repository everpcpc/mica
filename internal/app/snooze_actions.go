package app

import (
	"context"
	"errors"
	"sort"
	"time"

	"mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo/ui"
)

type snoozePreset struct {
	name   func() string
	wakeAt time.Time
}

func nextMorning(now time.Time, days int) time.Time {
	date := now.AddDate(0, 0, days)
	return time.Date(date.Year(), date.Month(), date.Day(), 8, 0, 0, 0, date.Location())
}

func snoozePresets(now time.Time) []snoozePreset {
	weekend := (6 - int(now.Weekday()) + 7) % 7
	if weekend == 0 {
		weekend = 7
	}
	monday := (8 - int(now.Weekday())) % 7
	if monday == 0 {
		monday = 7
	}
	return []snoozePreset{
		{name: locales.TomorrowMorning, wakeAt: nextMorning(now, 1)},
		{name: locales.ThisWeekend, wakeAt: nextMorning(now, weekend)},
		{name: locales.NextWeek, wakeAt: nextMorning(now, monday)},
	}
}

func (a *App) snoozeMenu(menu *ui.Menu) {
	summaries := a.actionSummaries()
	if len(summaries) == 0 {
		return
	}
	for _, preset := range snoozePresets(time.Now()) {
		preset := preset
		if menu.Item(preset.name()).Chosen() {
			a.snoozeUntil(summaries, preset.wakeAt)
		}
	}
	for _, summary := range summaries {
		email, threadID := a.snoozeOwner(summary)
		if _, found := a.snoozeRuntime.records[snoozeKey(email, threadID)]; found {
			menu.Separator()
			if menu.Item(locales.Unsnooze()).Chosen() {
				a.unsnooze(summaries)
			}
			return
		}
	}
}

func (a *App) snoozeOwner(summary mail.ThreadSummary) (string, string) {
	ref := parseThreadRef(a.summaryKey(summary))
	email := normalizedEmail(ref.AccountEmail)
	if email == "" {
		email = normalizedEmail(summary.AccountEmail)
	}
	if email == "" {
		email = a.activeComposeOwner()
	}
	return email, ref.ID
}

func (a *App) snoozeSelection(record mail.SnoozeRecord) (string, bool) {
	if !a.compositeThreads && normalizedEmail(record.AccountEmail) != a.activeComposeOwner() {
		return "", false
	}
	return a.summaryKey(record.Summary), true
}

func (a *App) applySnoozeStateForRecord(record mail.SnoozeRecord, add, remove []string, wakeAt time.Time) {
	id, visible := a.snoozeSelection(record)
	if !visible {
		return
	}
	a.applySnoozeState(id, add, remove, wakeAt)
}

func (a *App) applySnoozeState(id string, add, remove []string, wakeAt time.Time) {
	for i := range a.threads {
		if a.summaryKey(a.threads[i]) == id {
			a.threads[i].SnoozeAt = wakeAt
			break
		}
	}
	a.updateThreadLabels(id, add, remove)
	if a.selectedLabel == mail.Snoozed {
		ref := parseThreadRef(id)
		email, threadID := a.snoozeOwner(mail.ThreadSummary{ID: ref.ID, AccountEmail: ref.AccountEmail})
		if record, found := a.snoozeRuntime.records[snoozeKey(email, threadID)]; found {
			a.upsertSnoozeSummary(record.Summary)
		} else {
			a.removeThreadIfHidden(id)
		}
		return
	}
	a.removeThreadIfHidden(id)
}

func (a *App) upsertSnoozeSummary(summary mail.ThreadSummary) {
	id := a.summaryKey(summary)
	found := false
	for i := range a.threads {
		if a.summaryKey(a.threads[i]) != id {
			continue
		}
		a.threads[i].LabelIDs = modifyLabelIDs(a.threads[i].LabelIDs, []string{mail.Snoozed}, []string{mail.Inbox})
		a.threads[i].Unread = containsLabel(a.threads[i].LabelIDs, mail.Unread)
		a.threads[i].Starred = containsLabel(a.threads[i].LabelIDs, mail.Starred)
		a.threads[i].SnoozeAt = summary.SnoozeAt
		found = true
		break
	}
	if !found {
		summary.LabelIDs = modifyLabelIDs(summary.LabelIDs, []string{mail.Snoozed}, []string{mail.Inbox})
		summary.Unread = containsLabel(summary.LabelIDs, mail.Unread)
		summary.Starred = containsLabel(summary.LabelIDs, mail.Starred)
		a.threads = append(a.threads, summary)
	}
	a.threads = mergeSnoozedSummaries(a.threads, nil, false)
	a.syncThreadSelection(false)
	if a.accountVerified || a.compositeThreads {
		a.persistList(a.currentQuery(), a.threads)
	}
}

func (a *App) restoreSnoozeInbox(record mail.SnoozeRecord) {
	id, visible := a.snoozeSelection(record)
	if !visible || a.selectedLabel == mail.Snoozed {
		return
	}
	summary := record.Summary
	summary.ID = record.ThreadID
	summary.AccountEmail = record.AccountEmail
	summary.LabelIDs = modifyLabelIDs(summary.LabelIDs, []string{mail.Inbox}, []string{mail.Snoozed})
	summary.Unread = containsLabel(summary.LabelIDs, mail.Unread)
	summary.Starred = containsLabel(summary.LabelIDs, mail.Starred)
	summary.SnoozeAt = time.Time{}
	query := a.currentQuery()
	if query.Search == "" && historyListContains(query, summary) {
		found := false
		for i := range a.threads {
			if a.summaryKey(a.threads[i]) == id {
				a.threads[i].LabelIDs = summary.LabelIDs
				a.threads[i].Unread = summary.Unread
				a.threads[i].Starred = summary.Starred
				a.threads[i].SnoozeAt = time.Time{}
				found = true
				break
			}
		}
		if !found {
			a.threads = append(a.threads, summary)
		}
		sort.SliceStable(a.threads, func(i, j int) bool {
			if a.threads[i].Updated.Equal(a.threads[j].Updated) {
				return a.threads[i].ID < a.threads[j].ID
			}
			return a.threads[i].Updated.After(a.threads[j].Updated)
		})
		a.syncThreadSelection(false)
		if a.accountVerified || a.compositeThreads {
			a.persistList(query, a.threads)
		}
	}
	a.refresh()
}

func (a *App) snoozeTomorrow() {
	a.snoozeUntil(a.actionSummaries(), nextMorning(time.Now(), 1))
}

func (a *App) snoozeUntil(summaries []mail.ThreadSummary, wakeAt time.Time) {
	if a.cacheState.quitting {
		return
	}
	for _, summary := range summaries {
		a.snoozeOne(summary, wakeAt)
	}
}

func (a *App) snoozeOne(summary mail.ThreadSummary, wakeAt time.Time) bool {
	if a.selectedLabel == mail.Draft || a.selectedLabel == mail.Outbox || a.cacheState.quitting {
		return false
	}
	id := a.summaryKey(summary)
	email, threadID := a.snoozeOwner(summary)
	cache := a.snoozeCache(email)
	if cache == nil || !a.canMutateThread(id) {
		if cache == nil {
			a.notice = locales.ActionFailed(errAccountClientUnavailable.Error())
		}
		return false
	}
	key := snoozeKey(email, threadID)
	previous, hadPrevious := a.snoozeRuntime.records[key]
	service, _ := a.ownerService(email)
	version := a.snoozeVersion(key)
	generation := a.snoozeRuntime.generation
	record := mail.SnoozeRecord{AccountEmail: email, ThreadID: threadID, WakeAt: wakeAt.UTC(), Summary: summary}
	record.Summary.ID = threadID
	record.Summary.AccountEmail = email
	record.Summary.LabelIDs = modifyLabelIDs(summary.LabelIDs, []string{mail.Snoozed}, []string{mail.Inbox})
	record.Summary.Unread = containsLabel(record.Summary.LabelIDs, mail.Unread)
	record.Summary.Starred = containsLabel(record.Summary.LabelIDs, mail.Starred)
	record.Summary.SnoozeAt = record.WakeAt
	original := summary
	a.undo = nil
	a.setSnoozeRecord(record)
	a.applySnoozeState(id, []string{mail.Snoozed}, []string{mail.Inbox}, record.WakeAt)
	a.invalidateSnoozeTimer(key)
	a.notice = locales.SnoozeUntil(snoozeTime(record.WakeAt))
	a.enqueueMutation(mutationJob{
		service: service,
		owner:   email,
		id:      id,
		runFunc: func() error {
			if err := cache(func(store snoozeStore) error {
				return store.SaveSnooze(context.Background(), email, record)
			}); err != nil {
				return err
			}
			if err := service.ModifyThread(context.Background(), threadID, nil, []string{mail.Inbox}); err != nil {
				if hadPrevious {
					return errors.Join(err, cache(func(store snoozeStore) error {
						return store.SaveSnooze(context.Background(), email, previous)
					}))
				}
				return errors.Join(err, cache(func(store snoozeStore) error {
					return store.CancelSnooze(context.Background(), email, threadID)
				}))
			}
			return nil
		},
		failure: locales.ActionFailed,
		done: func(err error) {
			if generation != a.snoozeRuntime.generation || a.snoozeRuntime.versions[key] != version || a.cacheState.quitting {
				return
			}
			if err != nil {
				if hadPrevious {
					a.setSnoozeRecord(previous)
					a.applySnoozeStateForRecord(previous, previous.Summary.LabelIDs, []string{mail.Snoozed}, previous.Summary.SnoozeAt)
					a.armSnooze(previous)
				} else {
					delete(a.snoozeRuntime.records, key)
					originalRecord := mail.SnoozeRecord{AccountEmail: email, ThreadID: threadID, Summary: original}
					a.applySnoozeStateForRecord(originalRecord, original.LabelIDs, []string{mail.Snoozed}, original.SnoozeAt)
					delete(a.snoozeRuntime.timers, key)
				}
				a.notice = locales.SnoozeFailed(err.Error())
				return
			}
			a.setSnoozeRecord(record)
			a.applySnoozeStateForRecord(record, []string{mail.Snoozed}, []string{mail.Inbox}, record.WakeAt)
			a.armSnooze(record)
			a.notice = locales.SnoozeUntil(snoozeTime(record.WakeAt))
		},
	})
	return true
}

func (a *App) setSnoozeRecord(record mail.SnoozeRecord) {
	if a.snoozeRuntime.records == nil {
		a.snoozeRuntime.records = make(map[string]mail.SnoozeRecord)
	}
	a.snoozeRuntime.records[snoozeKey(record.AccountEmail, record.ThreadID)] = record
}

func (a *App) unsnooze(summaries []mail.ThreadSummary) {
	if a.cacheState.quitting {
		return
	}
	for _, summary := range summaries {
		email, threadID := a.snoozeOwner(summary)
		record, found := a.snoozeRuntime.records[snoozeKey(email, threadID)]
		if found {
			a.unsnoozeOne(summary, record)
		}
	}
}

func (a *App) unsnoozeOne(summary mail.ThreadSummary, record mail.SnoozeRecord) bool {
	if a.cacheState.quitting {
		return false
	}
	id := a.summaryKey(summary)
	email, threadID := a.snoozeOwner(summary)
	cache := a.snoozeCache(email)
	if cache == nil || !a.canMutateThread(id) {
		return false
	}
	key := snoozeKey(email, threadID)
	service, _ := a.ownerService(email)
	version := a.snoozeVersion(key)
	generation := a.snoozeRuntime.generation
	a.invalidateSnoozeTimer(key)
	delete(a.snoozeRuntime.records, key)
	a.applySnoozeState(id, []string{mail.Inbox}, []string{mail.Snoozed}, time.Time{})
	restored := false
	a.enqueueMutation(mutationJob{
		service: service,
		owner:   email,
		id:      id,
		runFunc: func() error {
			if err := service.ModifyThread(context.Background(), threadID, []string{mail.Inbox}, nil); err != nil {
				return err
			}
			restored = true
			return cache(func(store snoozeStore) error {
				return store.CancelSnooze(context.Background(), email, threadID)
			})
		},
		failure: locales.ActionFailed,
		done: func(err error) {
			if generation != a.snoozeRuntime.generation || a.snoozeRuntime.versions[key] != version || a.cacheState.quitting {
				return
			}
			if err != nil && !restored {
				a.setSnoozeRecord(record)
				a.applySnoozeStateForRecord(record, record.Summary.LabelIDs, []string{mail.Snoozed}, record.Summary.SnoozeAt)
				a.armSnooze(record)
				a.notice = locales.SnoozeFailed(err.Error())
				return
			}
			if err != nil {
				a.setSnoozeRecord(record)
				a.retrySnooze(record)
			} else {
				delete(a.snoozeRuntime.records, key)
				delete(a.snoozeRuntime.timers, key)
			}
			a.applySnoozeStateForRecord(record, []string{mail.Inbox}, []string{mail.Snoozed}, time.Time{})
			a.restoreSnoozeInbox(record)
			if err == nil {
				a.notice = locales.SnoozeRestored()
			}
		},
	})
	return true
}
