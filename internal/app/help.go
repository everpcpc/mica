package app

import (
	"mica/locales"

	"github.com/egoist/mygo/ui"
)

type shortcutItem struct {
	keys  string
	label string
}

func (a *App) helpView(c *ui.Context) {
	groups := []struct {
		title string
		items []shortcutItem
	}{
		{locales.ShortcutNavigation(), []shortcutItem{
			{"j", locales.OlderConversation()},
			{"k", locales.NewerConversation()},
			{"o / Enter", locales.OpenConversation()},
			{"u", locales.BackToList()},
			{"n", locales.NextMessage()},
			{"p", locales.PreviousMessage()},
			{"/", locales.SearchMail()},
		}},
		{locales.ShortcutActions(), []shortcutItem{
			{"s", locales.AddStar()},
			{"e", locales.Archive()},
			{"b", locales.SnoozeAction()},
			{"#", locales.MoveToTrash()},
			{"!", locales.ReportSpam()},
			{"r", locales.Reply()},
			{"a", locales.ReplyAll()},
			{"f", locales.Forward()},
			{"Shift + i", locales.MarkedRead()},
			{"Shift + u", locales.MarkedUnread()},
			{"z", locales.Undo()},
		}},
		{locales.ShortcutJumping(), []shortcutItem{
			{"g + i", locales.GoToInbox()},
			{"g + s", locales.GoToStarred()},
			{"g + t", locales.GoToSent()},
		}},
		{locales.ShortcutCompose(), []shortcutItem{
			{"c", locales.Compose()},
			{"⌘/Ctrl + Enter", locales.Send()},
			{"⌘/Ctrl + Shift + c", locales.ShowCc()},
			{"⌘/Ctrl + Shift + b", locales.ShowBcc()},
			{"⌘/Ctrl + Shift + d", locales.DiscardDraft()},
			{"Shift + /", locales.HelpShortcut()},
		}},
	}
	ui.Modal(c, &a.helpOpen, func() {
		ui.Column(c).Width(560).Gap(14).Children(func() {
			ui.Text(c, locales.ShortcutHelp()).FontSize(20).Bold()
			ui.Text(c, locales.ShortcutHelpIntro()).FontSize(12).TextColor(c.Theme().TextMuted)
			ui.Scroll(c).Height(460).Children(func() {
				ui.Column(c).Gap(14).Children(func() {
					for _, group := range groups {
						ui.Column(c).Gap(5).Children(func() {
							ui.Text(c, group.title).Bold()
							for _, item := range group.items {
								ui.Row(c).Gap(16).Children(func() {
									ui.Text(c, item.keys).Width(170).Shrink(0).Font("monospace").TextColor(c.Theme().Text).Padding(2, 6).Radius(4).Background(c.Theme().Surface).Border(1, c.Theme().Border)
									ui.Text(c, item.label).Grow(1)
								})
							}
						})
					}
				})
			})
			ui.Row(c).Justify(ui.End).Children(func() {
				ui.Button(c, locales.Close()).OnClick(func() { a.helpOpen = false })
			})
		})
	})
}
