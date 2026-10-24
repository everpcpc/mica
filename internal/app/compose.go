package app

import (
	"context"
	"net/mail"
	"strings"

	mailmodel "mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
)

type composer struct {
	open              bool
	forward           bool
	showCc            bool
	showBcc           bool
	to                string
	cc                string
	bcc               string
	subject           string
	doc               *richtext.Document
	quote             *mailmodel.Message
	quoteOpen         bool
	threadID          string
	inReplyTo         string
	references        string
	attachments       []composerAttachment
	nextAttachmentID  uint64
	readingAttachment bool
	sending           bool
	err               string
}

func (a *App) openCompose(reply *mailmodel.Message) {
	m := &composer{open: true, doc: richtext.NewDocument(richtext.NewText(""))}
	if reply != nil {
		m.to = replyAddress(*reply)
		m.subject = replySubject(reply.Subject)
		m.quote = reply
		m.threadID = reply.ThreadID
		m.inReplyTo = reply.MessageID
		m.references = strings.TrimSpace(reply.References + " " + reply.MessageID)
	}
	a.compose = m
}

func (a *App) openReply() {
	if len(a.thread.Messages) == 0 {
		return
	}
	a.openCompose(&a.thread.Messages[len(a.thread.Messages)-1])
}

func (a *App) composeView(c *ui.Context) {
	m := a.compose
	t := c.Theme()
	attachmentsLoading, attachmentsFailed := m.composeAttachmentsState()
	dismissed := ui.Modal(c, &m.open, func() {
		a.composeShortcuts(c)
		content := ui.Column(c).Width(640).Gap(12)
		dropped := content.DroppedFiles()
		content.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				title := locales.NewMessage()
				if m.forward {
					title = locales.ForwardThread()
				} else if m.threadID != "" {
					title = locales.ReplyThread()
				}
				ui.Text(c, title).FontSize(18).Bold().Grow(1)
				ui.Text(c, a.account.Email).FontSize(11).TextColor(t.TextMuted)
			})
			sectionPanel(c).Children(func() {
				composeField(c, "to", locales.To(), &m.to, "name@example.com")
				if m.showCc || m.cc != "" {
					composeField(c, "cc", locales.Cc(), &m.cc, locales.Optional())
				}
				if m.showBcc || m.bcc != "" {
					composeField(c, "bcc", locales.Bcc(), &m.bcc, locales.Optional())
				}
				if !m.showCc || !m.showBcc {
					ui.Row(c).Gap(8).Children(func() {
						if !m.showCc {
							ui.Button(c, locales.Cc()).Padding(2, 7).OnClick(func() { m.showCc = true })
						}
						if !m.showBcc {
							ui.Button(c, locales.Bcc()).Padding(2, 7).OnClick(func() { m.showBcc = true })
						}
					})
				}
				composeField(c, "subject", locales.Subject(), &m.subject, locales.SubjectPlaceholder())
			})
			sectionPanel(c).Children(func() {
				ui.Row(c).Gap(4).Padding(2).Radius(8).Background(t.Surface).Children(func() {
					formatButton(c, "B", locales.BoldTooltip(), m.doc.ToggleBold).Bold()
					formatButton(c, "I", locales.ItalicTooltip(), m.doc.ToggleItalic).Italic()
					formatButton(c, "U", locales.UnderlineTooltip(), m.doc.ToggleUnderline).Underline()
					formatButton(c, "•", locales.BulletsTooltip(), func() { m.doc.ToggleList(richtext.Bullets) })
				})
				richtext.Editor(c.Key("compose-body"), m.doc, richtext.EditorOptions{
					Label:       locales.Body(),
					Placeholder: locales.BodyPlaceholder(),
				}).Height(250)
				a.composeAttachmentsView(c, m)
				if m.quote != nil {
					ui.Collapsible(c, locales.OriginalMessage(), &m.quoteOpen, func() {
						ui.Text(c, replyQuoteText(*m.quote)).FontSize(12).TextColor(t.TextMuted).MaxLines(8).Selectable()
					})
				}
			})
			if m.err != "" {
				ui.Text(c, m.err).FontSize(12).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.SendShortcut()).FontSize(11).TextColor(t.TextMuted).Grow(1)
				ui.Button(c, locales.Cancel()).Disabled(m.sending).OnClick(func() { a.compose = nil })
				primaryAction(c, locales.Send()).Disabled(m.sending || attachmentsLoading || attachmentsFailed).OnClick(a.sendCompose)
			})
		})
		if len(dropped) > 0 {
			a.addComposeAttachments(dropped)
		}
	}).Dismissed()
	if dismissed && m.sending {
		m.open = true
	}
	if !m.open {
		a.compose = nil
	}
}

func composeField(c *ui.Context, key, label string, value *string, placeholder string) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).Width(52).Shrink(0)
		ui.TextInput(c.Key(key), value).Label(label).Placeholder(placeholder).Grow(1)
	})
}

func formatButton(c *ui.Context, label, tip string, action func()) ui.Element {
	return ui.Button(c, label).KeepFocus().Tooltip(tip).MinWidth(30).OnClick(action)
}

func (a *App) sendCompose() {
	m := a.compose
	if m == nil || m.sending {
		return
	}
	attachmentsLoading, attachmentsFailed := m.composeAttachmentsState()
	if attachmentsLoading {
		m.err = locales.AttachmentsReading()
		return
	}
	if attachmentsFailed {
		m.err = locales.AttachmentsReadFailed()
		return
	}
	if err := validateRecipients(m.to, m.cc, m.bcc); err != nil {
		m.err = err.Error()
		return
	}
	if strings.TrimSpace(m.doc.Text().PlainText()) == "" {
		m.err = locales.BodyRequired()
		return
	}
	m.err = ""
	m.sending = true
	text := m.doc.Text().PlainText()
	htmlContent := m.doc.Text().HTML()
	if m.quote != nil {
		text += "\n\n" + replyQuoteText(*m.quote)
		htmlContent += replyQuoteHTML(*m.quote)
	}
	out := mailmodel.Compose{
		To:          m.to,
		Cc:          m.cc,
		Bcc:         m.bcc,
		Subject:     m.subject,
		Text:        text,
		HTML:        htmlContent,
		ThreadID:    m.threadID,
		InReplyTo:   m.inReplyTo,
		References:  m.references,
		Attachments: m.outgoingAttachments(),
	}
	service, version := a.service, a.serviceVersion
	a.schedule(func() {
		err := service.Send(context.Background(), out)
		a.publish(func() {
			if version != a.serviceVersion {
				return
			}
			if err != nil {
				m.sending = false
				m.err = locales.SendFailed(err.Error())
				return
			}
			if a.compose == m {
				a.compose = nil
			}
			a.notice = locales.SendSucceeded()
			a.refresh()
		})
	})
}

func validateRecipients(lists ...string) error {
	for _, list := range lists {
		if strings.TrimSpace(list) == "" {
			continue
		}
		if _, err := mail.ParseAddressList(list); err != nil {
			return err
		}
	}
	if strings.TrimSpace(lists[0]) == "" {
		return &recipientError{locales.RecipientRequired()}
	}
	return nil
}

type recipientError struct{ message string }

func (e *recipientError) Error() string { return e.message }
