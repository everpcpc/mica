package app

import (
	"sort"
	"strings"

	"mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo/ui"
)

type sidebarLabel struct {
	id   string
	icon *ui.SVG
}

type labelColorOption struct {
	name  func() string
	color mail.LabelColor
}

var labelColorOptions = []labelColorOption{
	{name: locales.ColorRed, color: mail.LabelColor{Background: "#fb4c2f", Text: "#ffffff"}},
	{name: locales.ColorOrange, color: mail.LabelColor{Background: "#ffad47", Text: "#000000"}},
	{name: locales.ColorYellow, color: mail.LabelColor{Background: "#fad165", Text: "#000000"}},
	{name: locales.ColorGreen, color: mail.LabelColor{Background: "#16a766", Text: "#ffffff"}},
	{name: locales.ColorTeal, color: mail.LabelColor{Background: "#43d692", Text: "#000000"}},
	{name: locales.ColorBlue, color: mail.LabelColor{Background: "#4a86e8", Text: "#ffffff"}},
	{name: locales.ColorPurple, color: mail.LabelColor{Background: "#a479e2", Text: "#ffffff"}},
	{name: locales.ColorPink, color: mail.LabelColor{Background: "#f691b3", Text: "#000000"}},
	{name: locales.ColorGray, color: mail.LabelColor{Background: "#666666", Text: "#ffffff"}},
}

var mailboxLabels = []sidebarLabel{
	{mail.Inbox, iconInbox},
	{mail.Starred, iconStar},
	{mail.Snoozed, iconSnoozed},
	{mail.Sent, iconSent},
	{mail.Draft, iconCompose},
	{mail.Important, iconImportant},
	{mail.All, iconAll},
	{mail.Spam, iconSpam},
	{mail.Trash, iconTrash},
}

var categoryLabels = []sidebarLabel{
	{mail.CategorySocial, iconLabel},
	{mail.CategoryPromotions, iconLabel},
	{mail.CategoryUpdates, iconLabel},
	{mail.CategoryForums, iconLabel},
	{mail.CategoryPurchases, iconLabel},
	{mail.CategoryReservations, iconLabel},
}

func mailboxQuery(id string) (labelID, search string) {
	switch id {
	case mail.All:
		return "", ""
	case mail.Snoozed:
		return "", "in:snoozed"
	case mail.CategoryPurchases:
		return "", "category:purchases"
	case mail.CategoryReservations:
		return "", "category:reservations"
	default:
		return id, ""
	}
}

func (a *App) labelContextMenu(m *ui.Menu, label mail.Label) {
	m.Submenu(locales.LabelColor(), func(m *ui.Menu) {
		for _, option := range labelColorOptions {
			if m.Item(option.name()).Checked(label.Color == option.color).Chosen() {
				a.updateLabelColor(label, option.color)
			}
		}
	})
	m.Separator()
	if m.Item(locales.DeleteLabel()).Chosen() {
		a.openLabelDelete(label)
	}
}

func assignableLabels(labels []mail.Label) []mail.Label {
	assignable := make([]mail.Label, 0, len(labels))
	for _, label := range labels {
		if label.System || hiddenSystemLabel(label.ID) {
			continue
		}
		assignable = append(assignable, label)
	}
	sort.SliceStable(assignable, func(i, j int) bool {
		left, right := strings.ToLower(assignable[i].Name), strings.ToLower(assignable[j].Name)
		if left == right {
			return assignable[i].Name < assignable[j].Name
		}
		return left < right
	})
	return assignable
}

func visibleSidebarLabels(labels []mail.Label, items []sidebarLabel) []sidebarLabel {
	visible := make([]sidebarLabel, 0, len(items))
	for _, item := range items {
		show := true
		for _, label := range labels {
			if label.ID == item.id {
				show = sidebarLabelShown(label)
				break
			}
		}
		if show {
			visible = append(visible, item)
		}
	}
	return visible
}

func customSidebarLabels(labels []mail.Label) []mail.Label {
	visible := make([]mail.Label, 0, len(labels))
	for _, label := range labels {
		if label.System || hiddenSystemLabel(label.ID) || !sidebarLabelShown(label) {
			continue
		}
		visible = append(visible, label)
	}
	return visible
}

func sidebarLabelShown(label mail.Label) bool {
	return !label.Hide && (!label.ShowIfUnread || label.Unread > 0)
}

func labelUnread(labels []mail.Label, id string) int {
	for _, label := range labels {
		if label.ID == id {
			return label.Unread
		}
	}
	return 0
}

func (a *App) unifiedUnread() int {
	count := labelUnread(a.labels, mail.Inbox)
	for email := range a.accountClients {
		count += labelUnread(a.ownerLabels(email), mail.Inbox)
	}
	return count
}

func hiddenSystemLabel(id string) bool {
	switch id {
	case mail.Inbox, mail.Starred, mail.Snoozed, mail.Sent, mail.Draft, mail.Important, mail.Unified,
		mail.All, mail.Spam, mail.Trash, mail.Unread, "CHAT", "YELLOW_STAR":
		return true
	}
	return strings.HasPrefix(id, "CATEGORY_")
}
