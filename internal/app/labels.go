package app

import (
	"sort"
	"strings"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

type sidebarLabel struct {
	id   string
	icon *ui.SVG
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

func hiddenSystemLabel(id string) bool {
	switch id {
	case mail.Inbox, mail.Starred, mail.Snoozed, mail.Sent, mail.Draft, mail.Important,
		mail.All, mail.Spam, mail.Trash, mail.Unread, "CHAT", "YELLOW_STAR":
		return true
	}
	return strings.HasPrefix(id, "CATEGORY_")
}
