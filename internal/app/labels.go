package app

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo/ui"
)

type sidebarLabel struct {
	id   string
	icon *ui.SVG
}

type labelColorOption struct {
	name   func() string
	swatch string
	color  mail.LabelColor
}

var labelColorOptions = []labelColorOption{
	{name: locales.ColorRed, swatch: "🔴", color: mail.LabelColor{Background: "#fb4c2f", Text: "#ffffff"}},
	{name: locales.ColorOrange, swatch: "🟠", color: mail.LabelColor{Background: "#ffad47", Text: "#000000"}},
	{name: locales.ColorYellow, swatch: "🟡", color: mail.LabelColor{Background: "#fad165", Text: "#000000"}},
	{name: locales.ColorGreen, swatch: "🟢", color: mail.LabelColor{Background: "#16a766", Text: "#ffffff"}},
	{name: locales.ColorTeal, swatch: "🩵", color: mail.LabelColor{Background: "#43d692", Text: "#000000"}},
	{name: locales.ColorBlue, swatch: "🔵", color: mail.LabelColor{Background: "#4a86e8", Text: "#ffffff"}},
	{name: locales.ColorPurple, swatch: "🟣", color: mail.LabelColor{Background: "#a479e2", Text: "#ffffff"}},
	{name: locales.ColorPink, swatch: "🩷", color: mail.LabelColor{Background: "#f691b3", Text: "#000000"}},
	{name: locales.ColorGray, swatch: "⚫", color: mail.LabelColor{Background: "#666666", Text: "#ffffff"}},
}

// The empty slot keeps the label aligned while Image draws the swatch in its own colors.
var labelColorSlot = icon("")
var labelColorIcons sync.Map

func labelColorIcon(background string) *ui.SVG {
	if background == "" {
		return iconLabel
	}
	if cached, ok := labelColorIcons.Load(background); ok {
		return cached.(*ui.SVG)
	}
	color := ui.Hex(background)
	glyph := ui.MustParseSVG([]byte(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><circle cx="8" cy="8" r="5" fill="#%02x%02x%02x"/></svg>`, color.R, color.G, color.B)))
	actual, _ := labelColorIcons.LoadOrStore(background, glyph)
	return actual.(*ui.SVG)
}

var mailboxLabels = []sidebarLabel{
	{mail.Inbox, iconInbox},
	{mail.Starred, iconStar},
	{mail.Snoozed, iconSnoozed},
	{mail.Sent, iconSent},
	{mail.Draft, iconCompose},
	{mail.Outbox, iconSent},
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

const accountInboxPrefix = "account-inbox-"

func accountInboxLabel(id string) string {
	if !strings.HasPrefix(id, accountInboxPrefix) {
		return ""
	}
	return normalizedEmail(strings.TrimPrefix(id, accountInboxPrefix))
}

func (a *App) accountInboxAvailable(email string) bool {
	email = normalizedEmail(email)
	for _, account := range a.unifiedAccounts {
		if normalizedEmail(account.Email) == email {
			return true
		}
	}
	service, _ := a.ownerService(email)
	return service != nil
}

func mailboxQuery(id string) (labelID, search string) {
	if accountInboxLabel(id) != "" {
		return mail.Inbox, ""
	}
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
			if m.Item(option.swatch + " " + option.name()).Checked(label.Color == option.color).Chosen() {
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

func (a *App) threadLabels(owner string, ids []string, omit string) []mail.Label {
	labels := make([]mail.Label, 0, len(ids))
	for _, id := range ids {
		if id != omit && (id == mail.Inbox || id == mail.Sent) {
			labels = append(labels, mail.Label{ID: id, Name: a.labelName(id)})
		}
	}
	for _, label := range assignableLabels(a.ownerLabels(owner)) {
		if label.ID != omit && containsLabel(ids, label.ID) {
			labels = append(labels, label)
		}
	}
	return labels
}

func threadLabel(c *ui.Context, label mail.Label) ui.Element {
	t := c.Theme()
	background, foreground := t.Text.Alpha(0.08), t.TextMuted
	if label.Color.Background != "" && label.Color.Text != "" {
		background, foreground = ui.Hex(label.Color.Background), ui.Hex(label.Color.Text)
	}
	return ui.Box(c).Padding(2, 7).Radius(999).Background(background).Shrink(0).Children(func() {
		// One shared offset balances glyph ink without giving label names different baselines.
		ui.Text(c, label.Name).FontSize(11).TextColor(foreground).SingleLine().Top(-0.75)
	})
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
	case mail.Inbox, mail.Starred, mail.Snoozed, mail.Sent, mail.Draft, mail.Outbox, mail.Important, mail.Unified,
		mail.All, mail.Spam, mail.Trash, mail.Unread, "CHAT", "YELLOW_STAR":
		return true
	}
	return strings.HasPrefix(id, "CATEGORY_")
}
