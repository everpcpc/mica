package app

import (
	"net/mail"
	"strings"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

func shortTime(c *ui.Context, t time.Time) string {
	now := time.Now()
	locale := c.Region().FormatLocale(c.Locale())
	if t.YearDay() == now.YearDay() && t.Year() == now.Year() {
		return i18n.FormatDate(locale, t, i18n.DateOptions{Time: i18n.ShortStyle})
	}
	if t.Year() == now.Year() {
		return i18n.FormatDate(locale, t, i18n.DateOptions{Skeleton: "MMMd"})
	}
	return i18n.FormatDate(locale, t, i18n.DateOptions{Date: i18n.MediumStyle})
}

func fullTime(c *ui.Context, t time.Time) string {
	locale := c.Region().FormatLocale(c.Locale())
	return i18n.FormatDate(locale, t, i18n.DateOptions{Date: i18n.MediumStyle, Time: i18n.ShortStyle})
}

func snoozeTime(t time.Time) string {
	return i18n.FormatDate(i18n.Locale(), t.Local(), i18n.DateOptions{Date: i18n.MediumStyle, Time: i18n.ShortStyle})
}

func addressName(s string) string {
	address, err := mail.ParseAddress(s)
	if err != nil {
		return s
	}
	if address.Name != "" {
		return address.Name
	}
	return address.Address
}

func addressEmail(s string) string {
	address, err := mail.ParseAddress(s)
	if err != nil {
		return s
	}
	return address.Address
}

func replySubject(subject string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(subject)), "re:") {
		return subject
	}
	return "Re: " + subject
}
