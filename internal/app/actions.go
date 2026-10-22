package app

import (
	"context"

	"mica/internal/mail"
	"mica/locales"
)

type undoAction struct {
	id     string
	add    []string
	remove []string
}

func (a *App) recordUndo(id string, add, remove []string) {
	a.undo = &undoAction{id: id, add: add, remove: remove}
}

func (a *App) undoLast() {
	action := a.undo
	if action == nil || a.mutating {
		return
	}
	a.undo = nil
	service, version := a.service, a.serviceVersion
	a.mutating = true
	a.schedule(func() {
		err := service.ModifyThread(context.Background(), action.id, action.add, action.remove)
		a.publish(func() {
			if version != a.serviceVersion {
				return
			}
			a.mutating = false
			if err != nil {
				a.notice = locales.ActionFailed(err.Error())
				return
			}
			a.refresh()
			a.reloadLabels()
			a.notice = locales.ActionUndone()
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
	if summary == nil || a.mutating {
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
