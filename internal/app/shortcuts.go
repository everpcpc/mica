package app

import (
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

type shortcutBinding struct {
	name  string
	key   ui.Key
	mods  ui.Modifiers
	email bool
}

var applicationShortcuts = []shortcutBinding{
	{name: "help", key: ui.KeySlash, mods: ui.Shift, email: true},
	{name: "compose", key: ui.KeyC, email: true},
	{name: "search", key: ui.KeySlash},
	{name: "next", key: ui.KeyJ, email: true},
	{name: "previous", key: ui.KeyK, email: true},
	{name: "open", key: ui.KeyO, email: true},
	{name: "open", key: ui.KeyEnter, email: true},
	{name: "close", key: ui.KeyU, email: true},
	{name: "archive", key: ui.KeyE, email: true},
	{name: "snooze", key: ui.KeyB, email: true},
	{name: "star", key: ui.KeyS, email: true},
	{name: "trash", key: ui.Key3, mods: ui.Shift, email: true},
	{name: "spam", key: ui.Key1, mods: ui.Shift, email: true},
	{name: "reply", key: ui.KeyR, email: true},
	{name: "replyAll", key: ui.KeyA, email: true},
	{name: "forward", key: ui.KeyF, email: true},
	{name: "read", key: ui.KeyI, mods: ui.Shift, email: true},
	{name: "unread", key: ui.KeyU, mods: ui.Shift, email: true},
	{name: "undo", key: ui.KeyZ, email: true},
	{name: "nextMessage", key: ui.KeyN, email: true},
	{name: "previousMessage", key: ui.KeyP, email: true},
	{name: "go", key: ui.KeyG, email: true},
	{name: "inbox", key: ui.KeyI, email: true},
	{name: "sent", key: ui.KeyT, email: true},
}

func (a *App) SetShortcutMenuHandler(handler func(bool, string, string)) {
	a.shortcutMenu = handler
	a.shortcutMenuDisabled = a.shortcutsDisabled()
	a.shortcutMenuTrash, a.shortcutMenuSpam = a.recoveryActionLabels()
	if handler != nil {
		handler(a.shortcutMenuDisabled, a.shortcutMenuTrash, a.shortcutMenuSpam)
	}
}

func (a *App) shortcutsDisabled() bool {
	return a.searchFocused || a.compose != nil || (a.settings != nil && a.settings.open) || a.helpOpen || (a.labelDialog != nil && a.labelDialog.open) || (a.filterDialog != nil && a.filterDialog.open) || a.outboxDeleteOpen || a.outboxReviewOpen
}

func (a *App) syncShortcutMenu() {
	disabled := a.shortcutsDisabled()
	trashLabel, spamLabel := a.recoveryActionLabels()
	if disabled == a.shortcutMenuDisabled && trashLabel == a.shortcutMenuTrash && spamLabel == a.shortcutMenuSpam {
		return
	}
	a.shortcutMenuDisabled = disabled
	a.shortcutMenuTrash, a.shortcutMenuSpam = trashLabel, spamLabel
	if a.shortcutMenu != nil {
		a.shortcutMenu(disabled, trashLabel, spamLabel)
	}
}

func (a *App) PerformShortcut(key ui.Key, mods ui.Modifiers) {
	if a.shortcutsDisabled() {
		return
	}
	a.performShortcut(key, mods)
}

func (a *App) shortcuts(c *ui.Context) {
	if a.searchFocused {
		return
	}
	for _, binding := range applicationShortcuts {
		if !c.Shortcut(binding.mods, binding.key) {
			continue
		}
		if binding.name == "search" {
			a.search.Focus(c)
		} else {
			a.performShortcut(binding.key, binding.mods)
		}
		return
	}
}

func (a *App) performShortcut(key ui.Key, mods ui.Modifiers) {
	if key == ui.KeySlash && mods == ui.Shift {
		a.helpOpen = true
		return
	}
	if a.chord != "" {
		if time.Since(a.chordAt) > time.Second {
			a.chord = ""
		} else {
			switch {
			case mods == 0 && key == ui.KeyI:
				a.chord = ""
				a.goLabel(mail.Inbox)
			case mods == 0 && key == ui.KeyS:
				a.chord = ""
				a.goLabel(mail.Starred)
			case mods == 0 && key == ui.KeyT:
				a.chord = ""
				a.goLabel(mail.Sent)
			case mods == 0 && key == ui.KeyG:
				a.chordAt = time.Now()
			}
			return
		}
	}
	if mods == 0 && key == ui.KeyG {
		a.chord = "g"
		a.chordAt = time.Now()
		return
	}
	switch {
	case mods == 0 && key == ui.KeyC:
		a.openCompose(nil)
	case mods == 0 && key == ui.KeyJ:
		a.moveSelection(1)
	case mods == 0 && key == ui.KeyK:
		a.moveSelection(-1)
	case mods == 0 && (key == ui.KeyO || key == ui.KeyEnter):
		a.openSelected()
	case mods == 0 && key == ui.KeyU:
		a.closeReader()
	case mods == 0 && key == ui.KeyE:
		a.archive()
	case mods == 0 && key == ui.KeyB:
		a.snoozeTomorrow()
	case mods == 0 && key == ui.KeyS:
		a.toggleStar()
	case mods == ui.Shift && key == ui.Key3:
		a.toggleTrash()
	case mods == ui.Shift && key == ui.Key1:
		a.toggleSpam()
	case mods == 0 && key == ui.KeyR:
		a.openReply()
	case mods == 0 && key == ui.KeyA:
		a.openReplyAll()
	case mods == 0 && key == ui.KeyF:
		a.openForward()
	case mods == ui.Shift && key == ui.KeyI:
		a.setUnread(false)
	case mods == ui.Shift && key == ui.KeyU:
		a.setUnread(true)
	case mods == 0 && key == ui.KeyZ:
		a.undoLast()
	case mods == 0 && key == ui.KeyN:
		a.moveMessage(1)
	case mods == 0 && key == ui.KeyP:
		a.moveMessage(-1)
	}
}

func emailShortcutBinding(name string) (shortcutBinding, bool) {
	for _, binding := range applicationShortcuts {
		if binding.email && binding.name == name {
			return binding, true
		}
	}
	return shortcutBinding{}, false
}

func (a *App) goLabel(id string) {
	if a.selectedLabel == id {
		return
	}
	a.selectedLabel = id
	a.selectLabel()
}

func (a *App) moveSelection(delta int) {
	threads := a.visibleThreads()
	if len(threads) == 0 {
		return
	}
	row := a.selectedRow
	if row < 0 {
		if delta > 0 {
			row = 0
		} else {
			row = len(threads) - 1
		}
	} else {
		row = max(0, min(len(threads)-1, row+delta))
	}
	a.selectedRow = row
	a.list.ScrollIntoView(row)
	a.selectThread(a.summaryKey(threads[row]))
}

func (a *App) openSelected() {
	threads := a.visibleThreads()
	if a.selectedRow >= 0 && a.selectedRow < len(threads) {
		a.selectThread(a.summaryKey(threads[a.selectedRow]))
	}
}

func (a *App) moveMessage(delta int) {
	if len(a.thread.Messages) == 0 {
		return
	}
	a.focusedMessage = max(0, min(len(a.thread.Messages)-1, a.focusedMessage+delta))
	a.messageScroll = a.focusedMessage
}
