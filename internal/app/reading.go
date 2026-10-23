package app

import (
	"strings"
	"time"

	"mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo/ui"
)

func (a *App) reader(c *ui.Context) {
	t := c.Theme()
	bar := c.TitleBar()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Row(c).Height(titleBarHeight).Shrink(0).Padding(0, bar.Right+14, 0, 16).Gap(5).AlignItems(ui.Center).DragWindow().Children(func() {
			iconButton(c, locales.Archive(), iconArchive, a.archive).Disabled(a.selectedThread == "")
			iconButton(c, locales.MoveToTrash(), iconTrash, a.trash).Disabled(a.selectedThread == "" || a.selectedLabel == mail.Trash)
			iconButton(c, locales.ReadUnread(), iconMail, a.toggleUnread).Disabled(a.selectedThread == "")
			starLabel := locales.AddStar()
			if summary := a.selectedSummary(); summary != nil && summary.Starred {
				starLabel = locales.RemoveStar()
			}
			star := iconButton(c, starLabel, iconStar, a.toggleStar).Disabled(a.selectedThread == "")
			if summary := a.selectedSummary(); summary != nil && summary.Starred {
				star.TextColor(t.Warning)
			}
			ui.Box(c).Grow(1)
			ui.Button(c, locales.Reply()).Disabled(len(a.thread.Messages) == 0).OnClick(a.openReply)
		})
		ui.Divider(c)
		switch {
		case a.selectedThread == "":
			a.emptyReader(c)
		case a.threadLoading:
			ui.Column(c).Grow(1).Center().Gap(10).Children(func() {
				ui.Spinner(c).Size(22, 22)
				ui.Text(c, locales.OpeningThread()).FontSize(12).TextColor(t.TextMuted)
			})
		case a.threadErr != "":
			ui.Column(c).Grow(1).Center().Padding(28).Gap(10).Children(func() {
				ui.Text(c, locales.ThreadLoadFailed()).FontSize(17).Bold()
				ui.Text(c, a.threadErr).FontSize(12).TextColor(t.TextMuted).TextAlign(ui.Center)
				ui.Button(c, locales.Retry()).OnClick(func() {
					id := a.selectedThread
					a.selectedThread = ""
					a.selectThread(id)
				})
			})
		case len(a.thread.Messages) > 0:
			a.threadContent(c)
		}
	})
}

func (a *App) emptyReader(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Padding(32).Gap(10).Children(func() {
		ui.Icon(c, iconMail).Size(42, 42).TextColor(t.TextMuted)
		ui.Text(c, locales.SelectMessage()).FontSize(19).Bold()
		ui.Text(c, locales.SelectMessageHint()).FontSize(13).TextColor(t.TextMuted)
	})
}

func (a *App) threadContent(c *ui.Context) {
	t := c.Theme()
	thread := a.thread
	ui.Scroll(c.Key("read-" + thread.ID)).TrackScroll(&a.readerScroll).Grow(1).Background(t.Surface).Transition(ui.ElementTransition{
		Enter:    &ui.Motion{Y: 10, Opacity: 0},
		Duration: 240 * time.Millisecond,
	}).Children(func() {
		ui.Row(c).Justify(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MaxWidth(960).Padding(24, 24, 32, 24).Gap(20).Children(func() {
				ui.Column(c).Gap(10).Children(func() {
					ui.Text(c, thread.Subject).FontSize(23).Bold().LineHeight(1.25)
					ui.Row(c).Gap(6).Children(func() {
						if containsLabel(thread.LabelIDs, mail.Important) {
							ui.Icon(c, iconImportant).Size(14, 14).TextColor(t.Warning).Label(locales.Important())
						}
						for _, id := range thread.LabelIDs {
							if id == mail.Inbox || id == mail.Sent {
								ui.Badge(c, a.labelName(id))
							}
						}
						ui.Text(c, locales.MessageCount(len(thread.Messages))).FontSize(11).TextColor(t.TextMuted)
					})
				})
				for i, message := range thread.Messages {
					if i > 0 {
						ui.Divider(c)
					}
					a.messageView(c, i, message)
				}
				ui.Row(c).Gap(10).Children(func() {
					ui.Button(c, locales.Reply()).OnClick(a.openReply)
					ui.Button(c, locales.Forward()).OnClick(a.openForward)
				})
			})
		})
	})
}

func (a *App) messageView(c *ui.Context, index int, message mail.Message) {
	t := c.Theme()
	container := ui.Column(c.Key(message.ID)).Gap(14)
	if a.messageScroll == index {
		container.ScrollIntoView()
		a.messageScroll = -1
	}
	container.Children(func() {
		if !a.messageExpanded(index, message) {
			a.collapsedMessageView(c, message)
			return
		}
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Avatar(c, addressName(message.From), nil)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
					ui.Text(c, addressName(message.From)).Bold().SingleLine()
					if containsLabel(message.LabelIDs, mail.Important) {
						ui.Icon(c, iconImportant).Size(13, 13).TextColor(t.Warning).Label(locales.Important())
					}
				})
				ui.Text(c, addressEmail(message.From)).FontSize(11).TextColor(t.TextMuted).SingleLine()
			})
			ui.Text(c, fullTime(c, message.Date)).FontSize(11).TextColor(t.TextMuted)
			iconButton(c, locales.CollapseMessage(), iconChevronDown, func() { a.setMessageExpanded(message.ID, false) })
		})
		if message.To != "" {
			ui.Text(c, locales.SendTo(message.To)).FontSize(11).TextColor(t.TextMuted)
		}
		if message.HTML != "" && a.emailDocument(message).hasContent {
			a.emailView(c, message)
		} else if message.Text != "" {
			ui.Text(c, message.Text).FontSize(14).LineHeight(1.55).Selectable().Padding(20, 24)
		} else {
			ui.Text(c, locales.NoBody()).FontSize(13).TextColor(t.TextMuted)
		}
		for _, attachment := range message.Attachments {
			a.attachmentView(c, message, attachment)
		}
	})
}

func (a *App) collapsedMessageView(c *ui.Context, message mail.Message) {
	t := c.Theme()
	ui.Button(c, "").Label(locales.ExpandMessage()).WidthPercent(100).Padding(8, 10).Radius(8).Background(t.Background).OnClick(func() {
		a.setMessageExpanded(message.ID, true)
	}).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Avatar(c, addressName(message.From), nil)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, addressName(message.From)).Bold().SingleLine()
				ui.Text(c, a.messagePreview(message)).FontSize(11).TextColor(t.TextMuted).SingleLine()
			})
			ui.Text(c, fullTime(c, message.Date)).FontSize(11).TextColor(t.TextMuted)
			ui.Icon(c, iconChevronRight).Size(12, 12).TextColor(t.TextMuted)
		})
	})
}

func (a *App) messageExpanded(index int, message mail.Message) bool {
	if expanded, ok := a.expandedMessages[message.ID]; ok {
		return expanded
	}
	return index == len(a.thread.Messages)-1 || containsLabel(message.LabelIDs, mail.Unread)
}

func (a *App) setMessageExpanded(id string, expanded bool) {
	if a.expandedMessages == nil {
		a.expandedMessages = make(map[string]bool)
	}
	a.expandedMessages[id] = expanded
	if !expanded {
		a.closeEmailView(id)
	}
}

func (a *App) messagePreview(message mail.Message) string {
	preview := strings.TrimSpace(message.Text)
	if preview == "" {
		preview = a.emailDocument(message).preview
	}
	preview = strings.Join(strings.Fields(preview), " ")
	if preview == "" {
		return locales.NoBody()
	}
	const limit = 120
	runes := []rune(preview)
	if len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return preview
}
