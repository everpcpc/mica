package app

import (
	"context"

	"mica/internal/mail"
	"mica/locales"
)

type threadMutation int

const (
	mutationModify threadMutation = iota
	mutationTrash
	mutationUntrash
)

type undoItem struct {
	id      string
	kind    threadMutation
	add     []string
	remove  []string
	summary mail.ThreadSummary
}

type undoAction struct {
	items []undoItem
}

type mutationJob struct {
	service         mail.Service
	runFunc         func() error
	id              string
	owner           string
	kind            threadMutation
	add             []string
	remove          []string
	deleteLabel     bool
	labelColor      *mail.LabelColor
	readStatus      bool
	readQuery       mail.Query
	undo            *undoAction
	failure         func(string) string
	done            func(error)
	sequence        uint64
	viewVersion     int
	ownerGeneration uint64
}

func (j mutationJob) run() error {
	if j.runFunc != nil {
		return j.runFunc()
	}
	if j.deleteLabel {
		return j.service.(mail.LabelManager).DeleteLabel(context.Background(), j.id)
	}
	if j.labelColor != nil {
		return j.service.(mail.LabelManager).UpdateLabelColor(context.Background(), j.id, *j.labelColor)
	}
	id := parseThreadRef(j.id).ID
	switch j.kind {
	case mutationTrash:
		return j.service.TrashThread(context.Background(), id)
	case mutationUntrash:
		return j.service.UntrashThread(context.Background(), id)
	}
	return j.service.ModifyThread(context.Background(), id, j.add, j.remove)
}

func (a *App) recordUndo(id string, add, remove []string) {
	a.recordUndoKind(id, mutationModify, add, remove)
}

func (a *App) recordUndoKind(id string, kind threadMutation, add, remove []string) {
	a.undo = &undoAction{items: []undoItem{{id: id, kind: kind, add: add, remove: remove}}}
}

func (a *App) captureUndoSummary(id string) {
	if a.undo == nil {
		return
	}
	for i := len(a.undo.items) - 1; i >= 0; i-- {
		if a.undo.items[i].id != id {
			continue
		}
		for j := range a.threads {
			if a.summaryKey(a.threads[j]) == id {
				a.undo.items[i].summary = a.threads[j]
				a.undo.items[i].summary.LabelIDs = append([]string(nil), a.undo.items[i].summary.LabelIDs...)
				return
			}
		}
	}
}

func (a *App) canMutateThread(id string) bool {
	if a.serviceForThreadKey(id) != nil {
		return true
	}
	a.notice = locales.ActionFailed(errAccountClientUnavailable.Error())
	return false
}

func (a *App) restoreUndo(action *undoAction) {
	search := a.currentQuery().Search != ""
	if a.compositeThreads && a.unified != nil && !search {
		a.restoreUnifiedUndo(action)
		return
	}
	summaries := make([]mail.ThreadSummary, 0, len(action.items))
	for _, item := range action.items {
		a.updateThreadLabels(item.id, item.add, item.remove)
		if item.summary.ID == "" {
			continue
		}
		summary := item.summary
		summary.LabelIDs = modifyLabelIDs(summary.LabelIDs, item.add, item.remove)
		summary.Unread = containsLabel(summary.LabelIDs, mail.Unread)
		summary.Starred = containsLabel(summary.LabelIDs, mail.Starred)
		if search {
			found := false
			for i := range a.threads {
				if a.summaryKey(a.threads[i]) == item.id {
					found = true
					break
				}
			}
			if !found {
				a.mutationRefresh = true
			}
			continue
		}
		summaries = append(summaries, summary)
	}
	if !search && len(summaries) > 0 {
		a.applyHistoryChanges(summaries, nil)
	}
}

func (a *App) undoLast() {
	if a.accountTransition != nil {
		return
	}
	action := a.undo
	if action == nil {
		return
	}
	a.undo = nil
	a.restoreUndo(action)
	a.notice = locales.ActionUndone()
	for _, item := range action.items {
		a.enqueueMutation(mutationJob{
			service: a.serviceForThreadKey(item.id),
			id:      item.id,
			kind:    item.kind,
			add:     item.add,
			remove:  item.remove,
			failure: locales.ActionFailed,
		})
	}
}

func (a *App) mutationsPending() bool {
	return a.mutationRunning || len(a.mutationQueue) > 0
}

func (a *App) waitForMutations(fn func()) {
	if !a.mutationsPending() {
		fn()
		return
	}
	a.mutationWaiters = append(a.mutationWaiters, fn)
}

func (a *App) enqueueMutation(job mutationJob) {
	if job.service == nil && job.runFunc == nil {
		if job.done != nil {
			job.done(errAccountClientUnavailable)
		}
		if job.failure != nil {
			a.notice = job.failure(errAccountClientUnavailable.Error())
		} else {
			a.notice = locales.ActionFailed(errAccountClientUnavailable.Error())
		}
		return
	}
	if a.accountTransition != nil {
		if job.done != nil {
			job.done(errAccountTransition)
		}
		a.notice = locales.AccountSwitchPending()
		return
	}
	a.mutationSequence++
	job.sequence = a.mutationSequence
	job.viewVersion = a.serviceVersion
	if job.owner != "" {
		_, job.ownerGeneration = a.ownerService(job.owner)
	} else {
		job.ownerGeneration = a.generationForThreadKey(job.id)
	}
	a.mutationQueue = append(a.mutationQueue, job)
	a.pumpMutations()
}

func (a *App) pumpMutations() {
	if a.mutationRunning {
		return
	}
	if len(a.mutationQueue) == 0 {
		if a.mutationRefresh {
			a.mutationRefresh = false
			a.refresh()
		}
		if a.mutationLabels {
			a.mutationLabels = false
			a.reloadLabels()
		}
		waiters := a.mutationWaiters
		a.mutationWaiters = nil
		for _, waiter := range waiters {
			waiter()
		}
		return
	}
	job := a.mutationQueue[0]
	a.mutationRunning = true
	a.schedule(func() {
		err := job.run()
		a.publish(func() {
			if len(a.mutationQueue) == 0 || a.mutationQueue[0].sequence != job.sequence {
				return
			}
			if job.viewVersion != a.serviceVersion || job.ownerGeneration != a.generationForThreadKey(job.id) {
				a.mutationQueue = a.mutationQueue[1:]
				a.mutationRunning = false
				if job.done != nil {
					job.done(errAccountClientUnavailable)
				}
				a.pumpMutations()
				return
			}
			a.mutationQueue = a.mutationQueue[1:]
			a.mutationRunning = false
			a.mutationLabels = true
			if err != nil {
				if job.undo != nil && a.undo == job.undo {
					a.undo = nil
				}
				if job.readStatus {
					a.updateThreadLabels(job.id, []string{mail.Unread}, nil)
					a.restoreUnread(job.id, job.readQuery)
				} else {
					a.mutationRefresh = true
				}
				if job.failure != nil {
					a.notice = job.failure(err.Error())
				}
			}
			if job.done != nil {
				job.done(err)
			}
			a.pumpMutations()
		})
	})
}

func (a *App) closeReader() {
	a.threadCall++
	a.selectedThread = ""
	a.thread = mail.Thread{}
	a.threadErr = ""
	a.threadLoading = false
	a.focusedMessage = -1
	a.messageScroll = -1
}

func (a *App) recoveryActionLabels() (string, string) {
	trashLabel, spamLabel := locales.MoveToTrash(), locales.ReportSpam()
	summaries := a.actionSummaries()
	if len(summaries) == 0 {
		return trashLabel, spamLabel
	}
	if containsLabel(summaries[0].LabelIDs, mail.Trash) {
		trashLabel = locales.Untrash()
	}
	if containsLabel(summaries[0].LabelIDs, mail.Spam) {
		spamLabel = locales.NotSpam()
	}
	return trashLabel, spamLabel
}

func (a *App) toggleSpam() {
	summaries := a.actionSummaries()
	if len(summaries) == 0 {
		return
	}
	if containsLabel(summaries[0].LabelIDs, mail.Spam) {
		a.notSpam()
		return
	}
	a.reportSpam()
}

func (a *App) reportSpam() {
	summaries := a.actionSummaries()
	targets := make([]mail.ThreadSummary, 0, len(summaries))
	for _, summary := range summaries {
		if !containsLabel(summary.LabelIDs, mail.Spam) {
			targets = append(targets, summary)
		}
	}
	if len(targets) == 0 {
		return
	}
	if len(targets) == 1 {
		summary := targets[0]
		a.recordUndo(a.summaryKey(summary), []string{mail.Inbox}, []string{mail.Spam})
		a.mutate(a.summaryKey(summary), []string{mail.Spam}, []string{mail.Inbox}, true, locales.SpamReported)
		return
	}
	a.mutateThreads(targets, batchMutation{
		add: []string{mail.Spam}, remove: []string{mail.Inbox}, inverseAdd: []string{mail.Inbox}, inverseRemove: []string{mail.Spam}, removal: batchRemoveAll,
		message: locales.ConversationsSpamReported, failure: locales.ActionFailed,
	})
}

func (a *App) notSpam() {
	summaries := a.actionSummaries()
	targets := make([]mail.ThreadSummary, 0, len(summaries))
	for _, summary := range summaries {
		if containsLabel(summary.LabelIDs, mail.Spam) {
			targets = append(targets, summary)
		}
	}
	if len(targets) == 0 {
		return
	}
	if len(targets) == 1 {
		summary := targets[0]
		id := a.summaryKey(summary)
		if !a.canMutateThread(id) {
			return
		}
		a.recordUndo(id, []string{mail.Spam}, []string{mail.Inbox})
		a.updateThreadLabels(id, []string{mail.Inbox}, []string{mail.Spam})
		a.captureUndoSummary(id)
		a.removeThreadIfHidden(id)
		a.notice = locales.NotSpamMarked()
		a.enqueueMutation(mutationJob{
			service: a.serviceForThreadKey(id),
			id:      id,
			add:     []string{mail.Inbox},
			remove:  []string{mail.Spam},
			undo:    a.undo,
			failure: locales.ActionFailed,
		})
		return
	}
	a.mutateThreads(targets, batchMutation{
		add: []string{mail.Inbox}, remove: []string{mail.Spam}, inverseAdd: []string{mail.Spam}, inverseRemove: []string{mail.Inbox}, removal: batchRemoveHidden,
		message: locales.ConversationsNotSpamMarked, failure: locales.ActionFailed,
	})
}

func (a *App) toggleThreadLabel(label mail.Label) {
	if a.accountTransition != nil {
		return
	}
	summary := a.selectedSummary()
	if summary == nil {
		return
	}
	added := !containsLabel(summary.LabelIDs, label.ID)
	add, remove := []string{label.ID}, []string(nil)
	if !added {
		add, remove = remove, add
	}
	a.recordUndo(a.summaryKey(*summary), remove, add)
	a.mutate(a.summaryKey(*summary), add, remove, !added && a.selectedLabel == label.ID, func() string {
		if added {
			return locales.LabelAdded(label.Name)
		}
		return locales.LabelRemoved(label.Name)
	})
}

func (a *App) setUnread(unread bool) {
	summaries := a.actionSummaries()
	targets := make([]mail.ThreadSummary, 0, len(summaries))
	for _, summary := range summaries {
		if summary.Unread != unread {
			targets = append(targets, summary)
		}
	}
	if len(targets) == 0 {
		return
	}
	add, remove := []string{mail.Unread}, []string(nil)
	if !unread {
		add, remove = remove, add
	}
	if len(targets) == 1 {
		summary := targets[0]
		a.recordUndo(a.summaryKey(summary), remove, add)
		a.mutate(a.summaryKey(summary), add, remove, false, func() string {
			if unread {
				return locales.MarkedUnread()
			}
			return locales.MarkedRead()
		})
		return
	}
	message := locales.ConversationsRead
	if unread {
		message = locales.ConversationsUnread
	}
	a.mutateThreads(targets, batchMutation{
		add: add, remove: remove, inverseAdd: remove, inverseRemove: add,
		message: message, failure: locales.ActionFailed,
	})
}
