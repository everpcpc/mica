package app

import (
	"errors"
	"net/url"
	"strings"

	"mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo/ui/richtext"
)

var errMailtoInvalid = errors.New("mail: invalid mailto link")

func parseMailto(raw string) (mail.Compose, error) {
	var compose mail.Compose
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "mailto") {
		return compose, errMailtoInvalid
	}
	address := u.Opaque
	if address == "" && u.User != nil && u.Host != "" {
		address = u.User.String() + "@" + u.Host
	} else if address == "" {
		address = u.Host
	}
	if address == "" {
		address = strings.TrimPrefix(u.Path, "/")
	}
	address, err = url.PathUnescape(address)
	if err != nil || mailtoHeaderInvalid(address) {
		return compose, errMailtoInvalid
	}
	compose.To = address
	for _, part := range strings.Split(u.RawQuery, "&") {
		key, value, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		value, err = url.PathUnescape(value)
		if err != nil {
			return compose, errMailtoInvalid
		}
		switch strings.ToLower(key) {
		case "to":
			compose.To = mailtoAppendRecipients(compose.To, value)
		case "cc":
			compose.Cc = mailtoAppendRecipients(compose.Cc, value)
		case "bcc":
			compose.Bcc = mailtoAppendRecipients(compose.Bcc, value)
		case "subject":
			if compose.Subject == "" {
				compose.Subject = value
			}
		case "body":
			if compose.Text == "" {
				compose.Text = value
			}
		}
	}
	if mailtoHeaderInvalid(compose.To) || mailtoHeaderInvalid(compose.Cc) || mailtoHeaderInvalid(compose.Bcc) || mailtoHeaderInvalid(compose.Subject) {
		return compose, errMailtoInvalid
	}
	if compose.To == "" && compose.Cc == "" && compose.Bcc == "" {
		return compose, nil
	}
	if err := validateRecipients(compose.To, compose.Cc, compose.Bcc); err != nil {
		return compose, errMailtoInvalid
	}
	return compose, nil
}

func mailtoHeaderInvalid(value string) bool {
	return strings.ContainsAny(value, "\r\n\x00")
}

func mailtoAppendRecipients(current, value string) string {
	if strings.TrimSpace(value) == "" {
		return current
	}
	if current == "" {
		return value
	}
	return current + ", " + value
}

func (a *App) OpenMailto(raw string) error {
	if _, err := parseMailto(raw); err != nil {
		a.notice = locales.MailtoInvalid()
		return err
	}
	if a.compose != nil || a.pendingMailto != "" {
		a.notice = locales.MailtoDraftExists()
		return nil
	}
	a.pendingMailto = raw
	a.openPendingMailto()
	return nil
}

func (a *App) openPendingMailto() {
	if a.pendingMailto == "" || !a.restoreAttempted || a.authCancel != nil || a.accountTransition != nil || a.startupPending || a.draftRestorePending {
		return
	}
	raw := a.pendingMailto
	a.pendingMailto = ""
	compose, err := parseMailto(raw)
	if err != nil {
		a.notice = locales.MailtoInvalid()
		return
	}
	if a.compose != nil {
		a.notice = locales.MailtoDraftExists()
		return
	}
	a.openCompose(nil)
	m := a.compose
	m.to, m.cc, m.bcc, m.subject = compose.To, compose.Cc, compose.Bcc, compose.Subject
	m.doc.SetText(richtext.NewText(compose.Text))
	a.markComposerEdited(m)
}

func (a *App) defaultMailAppRegistered() bool {
	return a.defaultMailApp
}

func (a *App) setDefaultMailApp() {
	s := a.settings
	if s == nil {
		return
	}
	if a.registerURLScheme == nil {
		s.err = locales.DefaultMailAppFailed("unavailable")
		return
	}
	a.schedule(func() {
		err := a.registerURLScheme("mailto")
		a.publish(func() {
			if err == nil {
				a.defaultMailApp = true
			}
			if a.settings != s {
				return
			}
			if err != nil {
				s.err = locales.DefaultMailAppFailed(err.Error())
				return
			}
			s.err = ""
		})
	})
}
