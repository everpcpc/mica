package app

import (
	"context"

	"mica/internal/mail"
	"mica/locales"
)

type undoAction struct {
	id      string
	add     []string
	remove  []string
	summary mail.ThreadSummary
}

type mutationJob struct {
	service    mail.Service
	id         string
	add        []string
	remove     []string
	trash      bool
	readStatus bool
	undo       *undoAction
	failure    func(string) string
}

func (j mutationJob) run() error {
	if j.trash {
		return j.service.TrashThread(context.Background(), j.id)
	}
	return j.service.ModifyThread(context.Background(), j.id, j.add, j.remove)
}

func (a *App) recordUndo(id string, add, remove []string) {
	a.undo = &undoAction{id: id, add: add, remove: remove}
}

func (a *App) captureUndoSummary(id string) {
	if a.undo == nil || a.undo.id != id {
		return
	}
	for i := range a.threads {
		if a.threads[i].ID == id {
			a.undo.summary = a.threads[i]
			a.undo.summary.LabelIDs = append([]string(nil), a.undo.summary.LabelIDs...)
			return
		}
	}
}

func (a *App) restoreUndo(action *undoAction) {
	a.updateThreadLabels(action.id, action.add, action.remove)
	if action.summary.ID == "" {
		return
	}
	summary := action.summary
	summary.LabelIDs = modifyLabelIDs(summary.LabelIDs, action.add, action.remove)
	summary.Unread = containsLabel(summary.LabelIDs, mail.Unread)
	summary.Starred = containsLabel(summary.LabelIDs, mail.Starred)
	if a.currentQuery().Search != "" {
		for i := range a.threads {
			if a.threads[i].ID == action.id {
				return
			}
		}
		a.mutationRefresh = true
		return
	}
	a.applyHistoryChanges([]mail.ThreadSummary{summary}, nil)
}

func (a *App) undoLast() {
	action := a.undo
	if action == nil {
		return
	}
	a.undo = nil
	a.restoreUndo(action)
	a.notice = locales.ActionUndone()
	a.enqueueMutation(mutationJob{
		service: a.service,
		id:      action.id,
		add:     action.add,
		remove:  action.remove,
		failure: locales.ActionFailed,
	})
}

func (a *App) mutationsPending() bool {
	return a.mutationRunning || len(a.mutationQueue) > 0
}

func (a *App) enqueueMutation(job mutationJob) {
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
		return
	}
	job := a.mutationQueue[0]
	version := a.serviceVersion
	a.mutationRunning = true
	a.schedule(func() {
		err := job.run()
		a.publish(func() {
			if version != a.serviceVersion {
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
				} else {
					a.mutationRefresh = true
				}
				if job.failure != nil {
					a.notice = job.failure(err.Error())
				}
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

func (a *App) reportSpam() {
	summary := a.selectedSummary()
	if summary == nil {
		return
	}
	a.recordUndo(summary.ID, []string{mail.Inbox}, []string{mail.Spam})
	a.mutate(summary.ID, []string{mail.Spam}, []string{mail.Inbox}, true, locales.SpamReported)
}

func (a *App) setUnread(unread bool) {
	summary := a.selectedSummary()
	if summary == nil || summary.Unread == unread {
		return
	}
	a.toggleUnread()
}
