package app

import (
	"fmt"
	"time"

	"mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/richtext"
)

func (a *App) reader(c *ui.Context) {
	t := c.Theme()
	bar := c.TitleBar()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Row(c).Height(titleBarHeight).Shrink(0).Padding(0, bar.Right+14, 0, 16).Gap(5).AlignItems(ui.Center).DragWindow().Children(func() {
			iconButton(c, locales.Archive(), iconArchive, a.archive).Disabled(a.selectedThread == "" || a.mutating)
			iconButton(c, locales.MoveToTrash(), iconTrash, a.trash).Disabled(a.selectedThread == "" || a.mutating || a.selectedLabel == mail.Trash)
			iconButton(c, locales.ReadUnread(), iconMail, a.toggleUnread).Disabled(a.selectedThread == "" || a.mutating)
			starLabel := locales.AddStar()
			if summary := a.selectedSummary(); summary != nil && summary.Starred {
				starLabel = locales.RemoveStar()
			}
			star := iconButton(c, starLabel, iconStar, a.toggleStar).Disabled(a.selectedThread == "" || a.mutating)
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
	ui.Scroll(c.Key("read-" + thread.ID)).Grow(1).Transition(ui.ElementTransition{
		Enter:    &ui.Motion{Y: 6},
		Duration: 180 * time.Millisecond,
	}).Children(func() {
		ui.Column(c).Padding(24, 28, 32, 28).Gap(20).MaxWidth(780).Children(func() {
			ui.Column(c).Gap(10).Children(func() {
				ui.Text(c, thread.Subject).FontSize(23).Bold().LineHeight(1.25)
				ui.Row(c).Gap(6).Children(func() {
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
			ui.Row(c).Padding(4, 0, 0, 0).Children(func() {
				ui.PrimaryButton(c, locales.Reply()).OnClick(a.openReply)
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
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Avatar(c, addressName(message.From), nil)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, addressName(message.From)).Bold().SingleLine()
				ui.Text(c, addressEmail(message.From)).FontSize(11).TextColor(t.TextMuted).SingleLine()
			})
			ui.Text(c, fullTime(c, message.Date)).FontSize(11).TextColor(t.TextMuted)
		})
		if message.To != "" {
			ui.Text(c, locales.SendTo(message.To)).FontSize(11).TextColor(t.TextMuted)
		}
		body, rich := a.messageBody(message)
		if rich {
			markdown.View(c.Key("html-"+message.ID), body, &a.markdown).FontSize(14).LineHeight(1.55)
		} else if body != "" {
			ui.Text(c, body).FontSize(14).LineHeight(1.55).Selectable()
		} else {
			ui.Text(c, locales.NoBody()).FontSize(13).TextColor(t.TextMuted)
		}
		for _, attachment := range message.Attachments {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconLabel).Size(14, 14).TextColor(t.TextMuted)
				ui.Text(c, attachment.Name).FontSize(12)
				ui.Text(c, fmt.Sprintf("%d KB", max(attachment.Size/1024, 1))).FontSize(11).TextColor(t.TextMuted)
			})
		}
	})
}

func (a *App) messageBody(message mail.Message) (string, bool) {
	if message.HTML == "" {
		return message.Text, false
	}
	if a.bodies == nil {
		a.bodies = make(map[string]string)
	}
	body, ok := a.bodies[message.ID]
	if !ok {
		body = richtext.ParseHTML(prepareMessageHTML(message)).Markdown()
		a.bodies[message.ID] = body
	}
	if body == "" {
		return message.Text, false
	}
	return body, true
}
