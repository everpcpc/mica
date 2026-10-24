package app

import (
	"strings"
	"time"

	"mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
)

func (a *App) reader(c *ui.Context) {
	t := c.Theme()
	bar := c.TitleBar()
	ui.Box(c).Fill().Background(t.Background).Children(func() {
		ui.Column(c).Fill().Children(func() {
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
		ui.Toolbar(c, func() {
			iconButton(c, locales.Archive(), iconArchive, a.archive).Disabled(a.selectedThread == "" || a.selectedLabel == mail.Draft)
			iconButton(c, locales.MoveToTrash(), iconTrash, a.trash).Disabled(a.selectedThread == "" || a.selectedLabel == mail.Trash || a.selectedLabel == mail.Draft)
			iconButton(c, locales.ReadUnread(), iconMail, a.toggleUnread).Disabled(a.selectedThread == "" || a.selectedLabel == mail.Draft)
			ui.Button(c, "").Label(locales.EditLabels()).Tooltip(locales.EditLabels()).Disabled(a.selectedLabel == mail.Draft).Padding(5, 6).Children(func() {
				ui.Icon(c, iconLabel).Size(16, 16)
			}).Menu(a.labelMenu)
			starLabel := locales.AddStar()
			if summary := a.selectedSummary(); summary != nil && summary.Starred {
				starLabel = locales.RemoveStar()
			}
			star := iconButton(c, starLabel, iconStar, a.toggleStar).Disabled(a.selectedThread == "" || a.selectedLabel == mail.Draft)
			if summary := a.selectedSummary(); summary != nil && summary.Starred {
				star.TextColor(t.Warning)
			}
			ui.Spacer(c)
			ui.Button(c, locales.Reply()).Disabled(len(a.thread.Messages) == 0 || a.selectedLabel == mail.Draft).OnClick(a.openReply)
		}).Absolute().Top(6).Left(12).Right(bar.Right+12).Height(40).Padding(4, 6).Radius(20).Material(glass.Glass{Tint: t.Background}).Shadow(0, 2, 10, 0, ui.RGBA(0, 0, 0, 0.06)).DragWindow()
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

func (a *App) labelMenu(m *ui.Menu) {
	labels := assignableLabels(a.labels)
	summary := a.selectedSummary()
	if len(labels) == 0 {
		m.Item(locales.NoCustomLabels()).Disabled(true)
	}
	for _, label := range labels {
		checked := summary != nil && containsLabel(summary.LabelIDs, label.ID)
		if m.Item(label.Name).Checked(checked).Disabled(summary == nil).Chosen() {
			a.toggleThreadLabel(label)
		}
	}
}

func (a *App) threadContent(c *ui.Context) {
	t := c.Theme()
	thread := a.thread
	ui.Scroll(c.Key("read-"+thread.ID)).TrackScroll(&a.readerScroll).Grow(1).Background(t.Background).Padding(titleBarHeight, 0, 0, 0).ScrollbarInsets(titleBarHeight, 0, 0).Transition(ui.ElementTransition{
		Enter:    &ui.Motion{Y: 10, Opacity: 0},
		Duration: 240 * time.Millisecond,
	}).Children(func() {
		ui.Row(c).Justify(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MaxWidth(960).Padding(24, 24, 32, 24).Gap(18).Children(func() {
				ui.Column(c).Gap(8).Children(func() {
					ui.Text(c, thread.Subject).FontSize(22).LineHeight(1.3)
					ui.Row(c).Gap(6).Children(func() {
						if containsLabel(thread.LabelIDs, mail.Important) {
							ui.Icon(c, iconImportant).Size(14, 14).TextColor(t.Warning).Label(locales.Important())
						}
						for _, id := range thread.LabelIDs {
							if id == mail.Inbox || id == mail.Sent {
								ui.Badge(c, a.labelName(id))
							}
						}
						for _, label := range assignableLabels(a.labels) {
							if containsLabel(thread.LabelIDs, label.ID) {
								ui.Badge(c, label.Name)
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
					if a.selectedLabel == mail.Draft {
						threadAction(c, locales.ContinueDraft(), iconCompose, a.openSelectedDraft)
					} else {
						threadAction(c, locales.Reply(), iconReply, a.openReply)
						threadAction(c, locales.ReplyAll(), iconReplyAll, a.openReplyAll)
						threadAction(c, locales.Forward(), iconForward, a.openForward)
					}
				})
			})
		})
	})
}

func threadAction(c *ui.Context, label string, glyph *ui.SVG, action func()) {
	t := c.Theme()
	ui.Button(c, "").Label(label).Padding(7, 12).Radius(16).Material(glass.Glass{Tint: t.Background.Alpha(0.82), Interactive: true}).Border(1, t.Border).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.04)).Children(func() {
		ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, glyph).Size(15, 15)
			ui.Text(c, label)
		})
	}).OnClick(action)
}

func (a *App) messageView(c *ui.Context, index int, message mail.Message) {
	t := c.Theme()
	container := ui.Column(c.Key(message.ID)).Gap(12).Transition(ui.ElementTransition{Animation: &ui.Snappy})
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
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, addressName(message.From)).Bold().SingleLine().MinWidth(0)
					if containsLabel(message.LabelIDs, mail.Important) {
						ui.Icon(c, iconImportant).Size(13, 13).TextColor(t.Warning).Label(locales.Important())
					}
					if email := addressEmail(message.From); email != "" {
						ui.Text(c, "<"+email+">").FontSize(12).TextColor(t.TextMuted).SingleLine().Grow(2).MinWidth(0)
					}
				})
				if message.To != "" {
					ui.Text(c, locales.SendTo(message.To)).FontSize(12).TextColor(t.TextMuted).SingleLine()
				}
			})
			ui.Text(c, fullTime(c, message.Date)).FontSize(11).TextColor(t.TextMuted).Shrink(0)
			iconButton(c, locales.CollapseMessage(), iconChevronDown, func() { a.setMessageExpanded(message.ID, false) })
		})
		if message.HTML != "" && a.emailDocument(message).hasContent {
			a.emailView(c, message)
		} else if message.Text != "" {
			ui.Text(c, message.Text).FontSize(14).LineHeight(1.55).Selectable().Padding(20, 24)
		} else {
			ui.Text(c, locales.NoBody()).FontSize(13).TextColor(t.TextMuted)
		}
		for i, attachment := range message.Attachments {
			a.attachmentView(c, message, attachment, i)
		}
		if a.selectedLabel != mail.Draft {
			a.expandedMessageActions(c, message)
		}
	})
}

func (a *App) expandedMessageActions(c *ui.Context, message mail.Message) {
	ui.Row(c).Gap(2).Justify(ui.End).Children(func() {
		iconButton(c, locales.Reply(), iconReply, func() { a.openReplyMessage(message) })
		iconButton(c, locales.ReplyAll(), iconReplyAll, func() { a.openReplyAllMessage(message) })
		iconButton(c, locales.Forward(), iconForward, func() { a.openForwardMessage(message) })
	})
}

func (a *App) messageActionsMenu(c *ui.Context, message mail.Message) {
	ui.Button(c, "").Label(locales.MessageActions()).Tooltip(locales.MessageActions()).Padding(5, 6).Children(func() {
		ui.Icon(c, iconMore).Size(16, 16)
	}).Menu(func(menu *ui.Menu) {
		if menu.Item(locales.Reply()).Chosen() {
			a.openReplyMessage(message)
		}
		if menu.Item(locales.ReplyAll()).Chosen() {
			a.openReplyAllMessage(message)
		}
		if menu.Item(locales.Forward()).Chosen() {
			a.openForwardMessage(message)
		}
	})
}

func (a *App) collapsedMessageView(c *ui.Context, message mail.Message) {
	t := c.Theme()
	ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
		ui.Button(c, "").Label(locales.ExpandMessage()).Grow(1).MinWidth(0).Padding(8, 10).Radius(8).Background(t.Background).OnClick(func() {
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
		if a.selectedLabel != mail.Draft {
			a.messageActionsMenu(c, message)
		}
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
