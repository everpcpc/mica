package app

import (
	"html"
	"net/mail"
	"strings"
	"time"

	mailmodel "mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
)

func (a *App) openReplyAll() {
	if len(a.thread.Messages) == 0 {
		return
	}
	message := a.thread.Messages[len(a.thread.Messages)-1]
	a.openCompose(&message)
	a.compose.to, a.compose.cc = replyAllRecipients(message, a.account.Email)
}

func replyAddress(message mailmodel.Message) string {
	if replyTo := strings.TrimSpace(message.ReplyTo); replyTo != "" {
		return replyTo
	}
	return message.From
}

func replyAllRecipients(message mailmodel.Message, own string) (string, string) {
	own = strings.ToLower(strings.TrimSpace(own))
	seen := map[string]bool{}
	var to, cc []string
	add := func(list *[]string, address *mail.Address) {
		email := strings.ToLower(address.Address)
		if email == own || seen[email] {
			return
		}
		seen[email] = true
		*list = append(*list, address.String())
	}
	if from, err := mail.ParseAddress(replyAddress(message)); err == nil {
		add(&to, from)
	}
	for _, list := range []struct {
		value string
		cc    bool
	}{{message.To, false}, {message.Cc, true}} {
		addresses, err := mail.ParseAddressList(list.value)
		if err != nil {
			continue
		}
		for _, address := range addresses {
			if list.cc {
				add(&cc, address)
			} else {
				add(&to, address)
			}
		}
	}
	return strings.Join(to, ", "), strings.Join(cc, ", ")
}

func replyQuoteText(message mailmodel.Message) string {
	body := message.Text
	if body == "" && message.HTML != "" {
		body = richtext.ParseHTML(message.HTML).PlainText()
	}
	return locales.OriginalMessage() + "\n" +
		"From: " + replyAddress(message) + "\n" +
		"Date: " + message.Date.Format(time.RFC1123Z) + "\n" +
		"Subject: " + message.Subject + "\n" +
		"To: " + message.To + "\n\n" + body
}

func replyQuoteHTML(message mailmodel.Message) string {
	quoted := html.EscapeString(replyQuoteText(message))
	quoted = strings.ReplaceAll(quoted, "\n", "<br>")
	return `<blockquote style="margin:12px 0;padding-left:12px;border-left:3px solid #ccc">` + quoted + `</blockquote>`
}

func (a *App) openForward() {
	if len(a.thread.Messages) == 0 {
		return
	}
	message := a.thread.Messages[len(a.thread.Messages)-1]
	subject := strings.TrimSpace(message.Subject)
	if !strings.HasPrefix(strings.ToLower(subject), "fwd:") {
		subject = "Fwd: " + subject
	}
	body := message.Text
	if body == "" && message.HTML != "" {
		body = richtext.ParseHTML(message.HTML).PlainText()
	}
	quote := locales.ForwardedMessage() + "\n" +
		"From: " + message.From + "\n" +
		"Date: " + message.Date.Format(time.RFC1123Z) + "\n" +
		"Subject: " + message.Subject + "\n" +
		"To: " + message.To + "\n\n" + body
	a.compose = &composer{
		open:    true,
		forward: true,
		subject: subject,
		doc:     richtext.NewDocument(richtext.NewText(quote)),
	}
}

func (a *App) composeShortcuts(c *ui.Context) {
	m := a.compose
	if m == nil {
		return
	}
	switch {
	case commandShortcut(c, 0, ui.KeyEnter):
		a.sendCompose()
	case commandShortcut(c, ui.Shift, ui.KeyC):
		m.showCc = true
	case commandShortcut(c, ui.Shift, ui.KeyB):
		m.showBcc = true
	case commandShortcut(c, ui.Shift, ui.KeyD):
		a.compose = nil
	}
}

func commandShortcut(c *ui.Context, mods ui.Modifiers, key ui.Key) bool {
	return c.Shortcut(ui.Cmd|mods, key) || c.Shortcut(ui.Ctrl|mods, key)
}
