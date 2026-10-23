package app

import (
	"fmt"
	"time"

	"mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo/ui"
)

const (
	titleBarHeight       = 52
	noticeDuration       = 4 * time.Second
	actionNoticeDuration = 8 * time.Second
)

func (a *App) View(c *ui.Context) {
	theme := *c.Theme()
	if theme.Dark && !c.Preferences().HighContrast {
		theme.TextMuted = ui.Hex("#b9b9c2")
	}
	c.SetTheme(&theme)
	if c.Vibrancy() {
		c.Root().Background(ui.Transparent)
	}
	if a.compose == nil && (a.settings == nil || !a.settings.open) && !a.helpOpen && (a.labelDialog == nil || !a.labelDialog.open) {
		a.shortcuts(c)
	}
	sidebarSplit := ui.Split(c, &a.sidebarWidth, func() {
		a.sidebar(c)
	}, func() {
		listSplit := ui.Split(c, &a.listWidth, func() {
			a.threadList(c)
		}, func() {
			a.reader(c)
		}).Fill()
		if listSplit.Changed() {
			a.savePaneWidths()
		}
	}).Fill()
	if sidebarSplit.Changed() {
		a.savePaneWidths()
	}
	if a.compose != nil && a.compose.open {
		a.composeView(c)
	}
	if a.settings != nil && a.settings.open {
		a.settingsView(c)
	}
	if a.helpOpen {
		a.helpView(c)
	}
	if a.labelDialog != nil && a.labelDialog.open {
		a.labelDialogView(c)
	}
	a.showNotice(c)
}

func (a *App) showNotice(c *ui.Context) {
	if a.expiredNoticeID != "" {
		c.CloseToast(a.expiredNoticeID)
		a.expiredNoticeID = ""
	}
	if a.notice != "" {
		undo := a.undo
		if a.noticeToastID != "" {
			c.CloseToast(a.noticeToastID)
		}
		a.undo = undo
		a.noticeID++
		id := fmt.Sprintf("mica-notice-%d", a.noticeID)
		duration := noticeDuration
		toast := ui.Toast{ID: id, Title: a.notice, Timeout: -1}
		if undo != nil {
			duration = actionNoticeDuration
			toast.Action = locales.Undo()
			toast.OnAction = a.undoLast
		}
		toast.OnClose = func() {
			a.noticeToastID = ""
			a.undo = nil
		}
		c.AddToast(toast)
		a.noticeToastID = id
		a.after(duration, func() {
			a.publish(func() { a.expiredNoticeID = id })
		})
		a.notice = ""
	}
}

func (a *App) sidebar(c *ui.Context) {
	t := c.Theme()
	bar := c.TitleBar()
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Height(titleBarHeight).Shrink(0).Padding(0, 12, 0, bar.Left+12).AlignItems(ui.Center).Justify(ui.End).DragWindow().Children(func() {
			ui.PrimaryButton(c, "").Label(locales.Compose()).Tooltip(locales.ComposeTooltip()).Padding(5, 7).Children(func() {
				ui.Icon(c, iconCompose).Size(16, 16)
			}).OnClick(func() { a.openCompose(nil) })
		})
		ui.Row(c).Padding(8, 14, 14, 14).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Avatar(c, a.account.Name, a.accountPhoto)
			ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, a.account.Name).Bold().SingleLine()
					if a.account.Demo {
						ui.Badge(c, locales.Demo())
					}
				})
				ui.Text(c, a.account.Email).FontSize(11).TextColor(t.TextMuted).SingleLine()
			})
		})
		mailboxes := ui.Sidebar(c, &a.selectedLabel, func() {
			ui.SidebarSection(c, locales.Mailboxes(), nil, func() {
				for _, mailbox := range visibleSidebarLabels(a.labels, mailboxLabels) {
					item := ui.SidebarItem(c.Key(mailbox.id), mailbox.id, mailbox.icon, a.labelName(mailbox.id))
					if unread := labelUnread(a.labels, mailbox.id); unread > 0 {
						item.Children(func() { ui.Badge(c, fmt.Sprint(unread)) })
					}
				}
			})
			if categories := visibleSidebarLabels(a.labels, categoryLabels); len(categories) > 0 {
				ui.SidebarSection(c, locales.Categories(), nil, func() {
					for _, category := range categories {
						ui.SidebarItem(c.Key(category.id), category.id, category.icon, a.labelName(category.id))
					}
				})
			}
			if labels := customSidebarLabels(a.labels); len(labels) > 0 {
				ui.SidebarSection(c, locales.Labels(), nil, func() {
					for _, label := range labels {
						item := ui.SidebarItem(c.Key(label.ID), label.ID, iconLabel, label.Name)
						if label.Unread > 0 {
							item.Children(func() { ui.Badge(c, fmt.Sprint(label.Unread)) })
						}
					}
				})
			}
		}).Grow(1).Label(locales.SidebarLabels())
		if mailboxes.Changed() {
			a.selectLabel()
		}
	})
}

func (a *App) threadList(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		title := a.labelName(a.selectedLabel)
		if a.query != "" {
			title = locales.SearchResults()
		}
		ui.Row(c).Height(titleBarHeight).Shrink(0).Padding(0, 12, 0, 18).Gap(8).AlignItems(ui.Center).DragWindow().Children(func() {
			ui.Text(c, title).FontSize(16).Bold().Grow(1).SingleLine()
			filterLabel := locales.FilterUnread()
			if a.unreadOnly {
				filterLabel = locales.ShowAllMail()
			}
			filter := ui.Toggle(c.Key("unread-filter"), &a.unreadOnly, "").Label(filterLabel).Tooltip(filterLabel).Padding(5, 6).Children(func() {
				ui.Icon(c, iconFilter).Size(16, 16)
			})
			if filter.Changed() {
				a.syncThreadSelection(false)
			}
			iconButton(c, locales.Sync(), iconRefresh, a.refresh).Disabled(a.loading)
		})
		ui.Column(c).Padding(0, 12, 10, 12).Gap(8).Children(func() {
			search := ui.SearchField(c.Key("search"), &a.query).Bind(&a.search).Label(locales.SearchMail()).Placeholder(locales.SearchGmail())
			if search.Submitted() || (search.Changed() && a.query == "") {
				a.refresh()
			}
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.errorText()).FontSize(11).TextColor(t.TextMuted).Grow(1)
				if a.loading || a.loadingMore {
					ui.Spinner(c).Size(12, 12)
				}
			})
		})
		ui.Divider(c)
		threads := a.visibleThreads()
		if len(threads) == 0 {
			a.emptyList(c)
			if a.unreadOnly {
				a.loadMoreThreads()
			}
			return
		}
		a.list.Key = func(i int) any { return threads[i].ID }
		a.list.Label = func(i int) string {
			thread := threads[i]
			return thread.Senders + ", " + thread.Subject + ", " + thread.Snippet
		}
		list := ui.List(c.Key("threads"), &a.list, len(threads)).Grow(1).Rows(func(row ui.ListRow) {
			a.threadRow(row, &threads[row.Index])
		})
		if list.Changed() && a.selectedRow >= 0 && a.selectedRow < len(threads) {
			a.selectThread(threads[a.selectedRow].ID)
		}
		if a.list.AtEnd() {
			a.loadMoreThreads()
		}
	})
}

func (a *App) threadRow(row ui.ListRow, thread *mail.ThreadSummary) {
	c := row.Context
	t := c.Theme()
	primary, secondary, accent := t.Text, t.TextMuted, t.Accent
	if row.Selected() {
		primary, secondary, accent = t.AccentText, t.AccentText.Alpha(0.9), t.AccentText
	}
	ui.Column(c).Padding(10, 12).Gap(4).Children(func() {
		ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
			if thread.Unread {
				ui.Box(c).Size(6, 6).Radius(3).Background(accent)
			} else {
				ui.Box(c).Size(6, 6)
			}
			senders := ui.Text(c, thread.Senders).Grow(1).MinWidth(0).SingleLine().TextColor(primary)
			if thread.Unread {
				senders.Bold()
			}
			ui.Text(c, shortTime(c, thread.Updated)).FontSize(11).TextColor(secondary)
		})
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			subject := ui.Text(c, thread.Subject).Grow(1).MinWidth(0).SingleLine().TextColor(primary)
			if thread.Unread {
				subject.Bold()
			}
			if containsLabel(thread.LabelIDs, mail.Important) {
				importantColor := t.Warning
				if row.Selected() {
					importantColor = accent
				}
				ui.Icon(c, iconImportant).Size(13, 13).TextColor(importantColor).Label(locales.Important())
			}
			if thread.Starred {
				starColor := t.Warning
				if row.Selected() {
					starColor = accent
				}
				ui.Icon(c, iconStar).Size(13, 13).TextColor(starColor)
			}
		})
		ui.Text(c, thread.Snippet).FontSize(12).LineHeight(1.35).TextColor(secondary).MaxLines(2)
		if thread.MessageCount > 1 {
			ui.Text(c, locales.MessageCount(thread.MessageCount)).FontSize(10).TextColor(secondary)
		}
	})
}

func (a *App) emptyList(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Padding(24).Gap(8).Children(func() {
		ui.Icon(c, iconMail).Size(30, 30).TextColor(t.TextMuted)
		if a.err != "" {
			ui.Text(c, locales.LoadMailFailed()).Bold()
			ui.Text(c, a.err).FontSize(12).TextColor(t.TextMuted).TextAlign(ui.Center)
			ui.Button(c, locales.Retry()).OnClick(a.refresh)
		} else if !a.loading {
			if a.unreadOnly && len(a.threads) > 0 {
				ui.Text(c, locales.EmptyUnread()).Bold()
				ui.Text(c, locales.EmptyUnreadHint()).FontSize(12).TextColor(t.TextMuted).TextAlign(ui.Center)
			} else {
				ui.Text(c, locales.EmptyMailbox()).Bold()
				ui.Text(c, locales.EmptyMailboxHint()).FontSize(12).TextColor(t.TextMuted).TextAlign(ui.Center)
			}
		}
	})
}

func iconButton(c *ui.Context, label string, svg *ui.SVG, action func()) ui.Element {
	return ui.Button(c, "").Label(label).Tooltip(label).Padding(5, 6).Children(func() {
		ui.Icon(c, svg).Size(16, 16)
	}).OnClick(action)
}
