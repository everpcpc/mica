package app

import (
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

func (a *App) shortcuts(c *ui.Context) {
	if c.Shortcut(ui.Shift, ui.KeySlash) {
		a.helpOpen = true
		return
	}
	if a.chord != "" {
		if time.Since(a.chordAt) > time.Second {
			a.chord = ""
		} else {
			switch {
			case c.Shortcut(0, ui.KeyI):
				a.chord = ""
				a.goLabel(mail.Inbox)
			case c.Shortcut(0, ui.KeyS):
				a.chord = ""
				a.goLabel(mail.Starred)
			case c.Shortcut(0, ui.KeyT):
				a.chord = ""
				a.goLabel(mail.Sent)
			case c.Shortcut(0, ui.KeyG):
				a.chordAt = time.Now()
			}
			return
		}
	}
	if c.Shortcut(0, ui.KeyG) {
		a.chord = "g"
		a.chordAt = time.Now()
		return
	}
	switch {
	case c.Shortcut(0, ui.KeyC):
		a.openCompose(nil)
	case c.Shortcut(0, ui.KeySlash):
		a.search.Focus(c)
	case c.Shortcut(0, ui.KeyJ):
		a.moveSelection(1)
	case c.Shortcut(0, ui.KeyK):
		a.moveSelection(-1)
	case c.Shortcut(0, ui.KeyO), c.Shortcut(0, ui.KeyEnter):
		a.openSelected()
	case c.Shortcut(0, ui.KeyU):
		a.closeReader()
	case c.Shortcut(0, ui.KeyE):
		a.archive()
	case c.Shortcut(0, ui.KeyS):
		a.toggleStar()
	case c.Shortcut(ui.Shift, ui.Key3):
		a.trash()
	case c.Shortcut(ui.Shift, ui.Key1):
		a.reportSpam()
	case c.Shortcut(0, ui.KeyR):
		a.openReply()
	case c.Shortcut(0, ui.KeyA):
		a.openReplyAll()
	case c.Shortcut(0, ui.KeyF):
		a.openForward()
	case c.Shortcut(ui.Shift, ui.KeyI):
		a.setUnread(false)
	case c.Shortcut(ui.Shift, ui.KeyU):
		a.setUnread(true)
	case c.Shortcut(0, ui.KeyZ):
		a.undoLast()
	case c.Shortcut(0, ui.KeyN):
		a.moveMessage(1)
	case c.Shortcut(0, ui.KeyP):
		a.moveMessage(-1)
	}
}

func (a *App) goLabel(id string) {
	if a.selectedLabel == id {
		return
	}
	a.selectedLabel = id
	a.selectLabel()
}

func (a *App) moveSelection(delta int) {
	if len(a.threads) == 0 {
		return
	}
	row := a.selectedRow
	if row < 0 {
		if delta > 0 {
			row = 0
		} else {
			row = len(a.threads) - 1
		}
	} else {
		row = max(0, min(len(a.threads)-1, row+delta))
	}
	a.selectedRow = row
	a.list.ScrollIntoView(row)
	a.selectThread(a.threads[row].ID)
}

func (a *App) openSelected() {
	if a.selectedRow >= 0 && a.selectedRow < len(a.threads) {
		a.selectThread(a.threads[a.selectedRow].ID)
	}
}

func (a *App) moveMessage(delta int) {
	if len(a.thread.Messages) == 0 {
		return
	}
	a.focusedMessage = max(0, min(len(a.thread.Messages)-1, a.focusedMessage+delta))
	a.messageScroll = a.focusedMessage
}
