package app

import (
	"fmt"

	"mica/locales"

	"github.com/egoist/mygo/ui"
)

const titleBarHeight = 52

func (a *App) View(c *ui.Context) {
	theme := *c.Theme()
	if theme.Dark && !c.Preferences().HighContrast {
		theme.TextMuted = ui.Hex("#b9b9c2")
	}
	c.SetTheme(&theme)
	if c.Vibrancy() {
		c.Root().Background(ui.Transparent)
	}
	if a.compose == nil && (a.settings == nil || !a.settings.open) && !a.helpOpen {
		a.shortcuts(c)
	}
	ui.Split(c, &a.sidebarWidth, func() {
		a.sidebar(c)
	}, func() {
		ui.Split(c, &a.listWidth, func() {
			a.threadList(c)
		}, func() {
			a.reader(c)
		}).Fill()
	}).Fill()
	if a.compose != nil && a.compose.open {
		a.composeView(c)
	}
	if a.settings != nil && a.settings.open {
		a.settingsView(c)
	}
	if a.helpOpen {
		a.helpView(c)
	}
	if a.notice != "" {
		if a.undo != nil {
			c.ToastAction(a.notice, locales.Undo(), a.undoLast)
		} else {
			c.Toast(a.notice)
		}
		a.notice = ""
	}
}

func (a *App) sidebar(c *ui.Context) {
	t := c.Theme()
	bar := c.TitleBar()
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Height(titleBarHeight).Shrink(0).Padding(0, 12, 0, bar.Left+12).Gap(8).AlignItems(ui.Center).DragWindow().Children(func() {
			ui.Icon(c, iconMail).Size(19, 19).TextColor(t.Accent)
			ui.Text(c, "Mica").FontSize(16).Bold().Grow(1)
			ui.PrimaryButton(c, "").Label(locales.Compose()).Tooltip(locales.ComposeTooltip()).Padding(5, 7).Children(func() {
				ui.Icon(c, iconCompose).Size(16, 16)
			}).OnClick(func() { a.openCompose(nil) })
		})
		ui.Row(c).Padding(8, 14, 14, 14).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Avatar(c, a.account.Name, nil)
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
		ui.Divider(c)
		ui.Row(c).Padding(8, 10).Children(func() {
			ui.Button(c, "").Label(locales.Settings()).Children(func() {
				ui.Icon(c, iconSettings).Size(15, 15)
				ui.Text(c, locales.Settings())
			}).OnClick(a.openSettings)
		})
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
			iconButton(c, locales.Sync(), iconRefresh, a.refresh).Disabled(a.loading)
		})
		ui.Column(c).Padding(0, 12, 10, 12).Gap(8).Children(func() {
			search := ui.SearchField(c.Key("search"), &a.query).Bind(&a.search).Label(locales.SearchMail()).Placeholder(locales.SearchGmail())
			if search.Submitted() || (search.Changed() && a.query == "") {
				a.refresh()
			}
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.errorText()).FontSize(11).TextColor(t.TextMuted).Grow(1)
				if a.loading {
					ui.Spinner(c).Size(12, 12)
				}
			})
		})
		ui.Divider(c)
		if len(a.threads) == 0 {
			a.emptyList(c)
			return
		}
		list := ui.List(c.Key("threads"), &a.list, len(a.threads)).Grow(1).Rows(func(row ui.ListRow) {
			a.threadRow(row)
		})
		if list.Changed() && a.selectedRow >= 0 && a.selectedRow < len(a.threads) {
			a.selectThread(a.threads[a.selectedRow].ID)
		}
	})
}

func (a *App) threadRow(row ui.ListRow) {
	c := row.Context
	t := c.Theme()
	thread := &a.threads[row.Index]
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
			ui.Text(c, locales.EmptyMailbox()).Bold()
			ui.Text(c, locales.EmptyMailboxHint()).FontSize(12).TextColor(t.TextMuted)
		}
	})
}

func iconButton(c *ui.Context, label string, svg *ui.SVG, action func()) ui.Element {
	return ui.Button(c, "").Label(label).Tooltip(label).Padding(5, 6).Children(func() {
		ui.Icon(c, svg).Size(16, 16)
	}).OnClick(action)
}
