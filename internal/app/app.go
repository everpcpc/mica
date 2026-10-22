package app

import (
	"context"
	"strings"
	"time"

	"mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

type App struct {
	service mail.Service
	account mail.Account
	labels  []mail.Label
	threads []mail.ThreadSummary
	thread  mail.Thread

	selectedLabel  string
	selectedThread string
	selectedRow    int
	sidebarWidth   float32
	listWidth      float32
	query          string

	loading       bool
	threadLoading bool
	mutating      bool
	err           string
	threadErr     string
	notice        string

	list             ui.ListState
	search           ui.Handle
	markdown         markdown.Options
	bodies           map[string]string
	compose          *composer
	settings         *settingsState
	appearance       string
	helpOpen         bool
	chord            string
	chordAt          time.Time
	focusedMessage   int
	messageScroll    int
	undo             *undoAction
	request          int
	threadCall       int
	serviceVersion   int
	restoreAttempted bool
	update           func(func())
	schedule         func(func())
}

func New(service mail.Service) *App {
	a := &App{
		service:        service,
		account:        mail.Account{Name: "Mica", Demo: true},
		selectedLabel:  mail.Inbox,
		selectedRow:    -1,
		sidebarWidth:   204,
		listWidth:      342,
		appearance:     "system",
		focusedMessage: -1,
		messageScroll:  -1,
		schedule:       func(fn func()) { go fn() },
	}
	a.list.Selected = &a.selectedRow
	a.list.Key = func(i int) any { return a.threads[i].ID }
	a.list.Label = func(i int) string {
		t := a.threads[i]
		return t.Senders + ", " + t.Subject + ", " + t.Snippet
	}
	return a
}

func (a *App) SetUpdate(update func(func())) {
	a.update = update
}

func (a *App) Start() {
	a.refresh()
	service, version := a.service, a.serviceVersion
	a.schedule(func() {
		account, accountErr := service.Account(context.Background())
		labels, labelsErr := service.Labels(context.Background())
		a.publish(func() {
			if version != a.serviceVersion {
				return
			}
			if accountErr == nil {
				a.account = account
			}
			if labelsErr == nil {
				a.labels = labels
			}
			if accountErr != nil {
				a.notice = accountErr.Error()
			} else if labelsErr != nil {
				a.notice = labelsErr.Error()
			}
		})
	})
	a.restoreGmail()
}

func (a *App) publish(fn func()) {
	if a.update != nil {
		a.update(fn)
		return
	}
	fn()
}

func (a *App) refresh() {
	a.request++
	request := a.request
	a.loading = true
	a.err = ""
	service, version := a.service, a.serviceVersion
	search := strings.TrimSpace(a.query)
	labelID := a.selectedLabel
	if search != "" {
		labelID = ""
	}
	query := mail.Query{LabelID: labelID, Search: search, Limit: 100}
	a.schedule(func() {
		threads, err := service.ListThreads(context.Background(), query)
		a.publish(func() {
			if version != a.serviceVersion || request != a.request {
				return
			}
			a.loading = false
			if err != nil {
				a.err = err.Error()
				return
			}
			a.threads = threads
			if a.selectedRow >= len(threads) {
				a.selectedRow = -1
			}
			if a.selectedThread != "" && !hasThread(threads, a.selectedThread) {
				a.clearThread()
			}
		})
	})
}

func (a *App) selectLabel() {
	a.query = ""
	a.clearThread()
	a.refresh()
}

func (a *App) clearThread() {
	a.threadCall++
	a.selectedRow = -1
	a.selectedThread = ""
	a.thread = mail.Thread{}
	a.threadErr = ""
	a.threadLoading = false
}

func (a *App) selectThread(id string) {
	if id == "" || id == a.selectedThread {
		return
	}
	a.threadCall++
	call := a.threadCall
	a.selectedThread = id
	a.thread = mail.Thread{}
	a.threadErr = ""
	a.threadLoading = true
	service, version := a.service, a.serviceVersion
	a.schedule(func() {
		thread, err := service.Thread(context.Background(), id)
		a.publish(func() {
			if version != a.serviceVersion || call != a.threadCall {
				return
			}
			a.threadLoading = false
			if err != nil {
				a.threadErr = err.Error()
				return
			}
			a.thread = thread
			a.focusedMessage = len(thread.Messages) - 1
			a.messageScroll = -1
			for i := range a.threads {
				if a.threads[i].ID == id && a.threads[i].Unread {
					a.threads[i].Unread = false
					a.markRead(id)
					break
				}
			}
		})
	})
}

func (a *App) selectedSummary() *mail.ThreadSummary {
	for i := range a.threads {
		if a.threads[i].ID == a.selectedThread {
			return &a.threads[i]
		}
	}
	return nil
}

func (a *App) markRead(id string) {
	service, version := a.service, a.serviceVersion
	a.schedule(func() {
		err := service.ModifyThread(context.Background(), id, nil, []string{mail.Unread})
		a.publish(func() {
			if version != a.serviceVersion {
				return
			}
			if err != nil {
				a.notice = locales.ReadStatusFailed(err.Error())
				return
			}
			a.reloadLabels()
		})
	})
}

func (a *App) reloadLabels() {
	service, version := a.service, a.serviceVersion
	a.schedule(func() {
		labels, err := service.Labels(context.Background())
		a.publish(func() {
			if version == a.serviceVersion && err == nil {
				a.labels = labels
			}
		})
	})
}

func (a *App) toggleStar() {
	summary := a.selectedSummary()
	if summary == nil || a.mutating {
		return
	}
	starred := !summary.Starred
	summary.Starred = starred
	add, remove := []string{mail.Starred}, []string(nil)
	if !starred {
		add, remove = remove, add
	}
	a.recordUndo(summary.ID, remove, add)
	a.mutate(summary.ID, add, remove, false, func() string {
		if starred {
			return locales.StarAdded()
		}
		return locales.StarRemoved()
	})
}

func (a *App) toggleUnread() {
	summary := a.selectedSummary()
	if summary == nil || a.mutating {
		return
	}
	unread := !summary.Unread
	summary.Unread = unread
	add, remove := []string{mail.Unread}, []string(nil)
	if !unread {
		add, remove = remove, add
	}
	a.recordUndo(summary.ID, remove, add)
	a.mutate(summary.ID, add, remove, false, func() string {
		if unread {
			return locales.MarkedUnread()
		}
		return locales.MarkedRead()
	})
}

func (a *App) archive() {
	summary := a.selectedSummary()
	if summary == nil || a.mutating {
		return
	}
	a.recordUndo(summary.ID, []string{mail.Inbox}, nil)
	a.mutate(summary.ID, nil, []string{mail.Inbox}, a.selectedLabel == mail.Inbox, locales.Archived)
}

func (a *App) trash() {
	summary := a.selectedSummary()
	if summary == nil || a.mutating || a.selectedLabel == mail.Trash {
		return
	}
	a.mutating = true
	id := summary.ID
	a.recordUndo(id, nil, []string{mail.Trash})
	service, version := a.service, a.serviceVersion
	a.schedule(func() {
		err := service.TrashThread(context.Background(), id)
		a.publish(func() {
			if version != a.serviceVersion {
				return
			}
			a.mutating = false
			if err != nil {
				if a.undo != nil && a.undo.id == id {
					a.undo = nil
				}
				a.notice = locales.TrashFailed(err.Error())
				return
			}
			a.removeThread(id)
			a.reloadLabels()
			a.notice = locales.Trashed()
		})
	})
}

func (a *App) mutate(id string, add, remove []string, removeFromList bool, message func() string) {
	a.mutating = true
	service, version := a.service, a.serviceVersion
	a.schedule(func() {
		err := service.ModifyThread(context.Background(), id, add, remove)
		a.publish(func() {
			if version != a.serviceVersion {
				return
			}
			a.mutating = false
			if err != nil {
				if a.undo != nil && a.undo.id == id {
					a.undo = nil
				}
				a.notice = locales.ActionFailed(err.Error())
				a.refresh()
				return
			}
			if removeFromList {
				a.removeThread(id)
			}
			a.reloadLabels()
			a.notice = message()
		})
	})
}

func (a *App) removeThread(id string) {
	for i := range a.threads {
		if a.threads[i].ID == id {
			a.threads = append(a.threads[:i], a.threads[i+1:]...)
			break
		}
	}
	if a.selectedThread == id {
		a.clearThread()
	}
}

func (a *App) labelName(id string) string {
	switch id {
	case mail.Inbox:
		return locales.Inbox()
	case mail.Starred:
		return locales.Starred()
	case mail.Sent:
		return locales.Sent()
	case mail.Spam:
		return locales.Spam()
	case mail.Trash:
		return locales.Trash()
	}
	for _, label := range a.labels {
		if label.ID == id {
			return label.Name
		}
	}
	return id
}

func hasThread(threads []mail.ThreadSummary, id string) bool {
	for _, thread := range threads {
		if thread.ID == id {
			return true
		}
	}
	return false
}

func (a *App) errorText() string {
	if a.err != "" {
		return a.err
	}
	if a.loading {
		return locales.LoadingMail()
	}
	return locales.ConversationCount(len(a.threads))
}
