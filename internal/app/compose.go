package app

import (
	"net/mail"
	"strings"

	mailmodel "mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
)

type composer struct {
	open              bool
	accountEmail      string
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
	draftID           string
	outboxID          string
	outboxAttempted   bool
	outboxReviewed    bool
	messageID         string
	revision          uint64
	savedRevision     uint64
	saveState         draftSaveState
	draftSaving       bool
	pendingAction     composerPendingAction
	generation        uint64
	recoveryAccount   string
	sending           bool
	err               string
}

func (a *App) activeComposeOwner() string {
	if email := normalizedEmail(a.storeAccount); email != "" {
		return email
	}
	return normalizedEmail(a.account.Email)
}

func (a *App) storedMessageOwner(message mailmodel.Message) string {
	if email := normalizedEmail(message.AccountEmail); email != "" {
		return email
	}
	return a.activeComposeOwner()
}

func (a *App) composerOwner(m *composer) string {
	if m != nil {
		if email := normalizedEmail(m.accountEmail); email != "" {
			return email
		}
	}
	return normalizedEmail(a.storeAccount)
}

func (a *App) composeOwnerVerified(email string) bool {
	if normalizedEmail(email) == "" {
		return a.accountVerified
	}
	return a.ownerVerified(email)
}

func (a *App) composeOwnerIsActive(email string) bool {
	email = normalizedEmail(email)
	return email == "" || email == normalizedEmail(a.storeAccount) || email == normalizedEmail(a.account.Email)
}

func (a *App) ownerOperationCurrent(email string, generation uint64) bool {
	service, current := a.ownerService(email)
	return service != nil && current == generation
}

func (a *App) openCompose(reply *mailmodel.Message) {
	if a.accountTransition != nil {
		return
	}
	m := &composer{open: true, accountEmail: a.activeComposeOwner(), generation: 1, doc: richtext.NewDocument(richtext.NewText(""))}
	if reply != nil {
		if owner := a.storedMessageOwner(*reply); owner != "" {
			m.accountEmail = owner
		}
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
	if a.selectedLabel == mailmodel.Draft || a.selectedLabel == mailmodel.Outbox || len(a.thread.Messages) == 0 {
		return
	}
	a.openReplyMessage(a.thread.Messages[len(a.thread.Messages)-1])
}

func (a *App) openReplyMessage(message mailmodel.Message) {
	if a.selectedLabel == mailmodel.Outbox {
		return
	}
	a.openCompose(&message)
}

func (a *App) composeView(c *ui.Context) {
	m := a.compose
	t := c.Theme()
	attachmentsLoading, attachmentsFailed := m.composeAttachmentsState()
	busy := m.sending || m.pendingAction != composerActionNone
	owner := m.accountEmail
	if owner == "" {
		owner = a.activeComposeOwner()
	}
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
				ui.Text(c, owner).FontSize(11).TextColor(t.TextMuted)
			})
			sectionPanel(c).Children(func() {
				a.composeField(c, m, "to", locales.To(), &m.to, "name@example.com")
				if m.showCc || m.cc != "" {
					a.composeField(c, m, "cc", locales.Cc(), &m.cc, locales.Optional())
				}
				if m.showBcc || m.bcc != "" {
					a.composeField(c, m, "bcc", locales.Bcc(), &m.bcc, locales.Optional())
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
				a.composeField(c, m, "subject", locales.Subject(), &m.subject, locales.SubjectPlaceholder())
			})
			sectionPanel(c).Children(func() {
				ui.Row(c).Gap(4).Padding(2).Radius(8).Background(t.Surface).Children(func() {
					formatButton(c, "B", locales.BoldTooltip(), func() { m.doc.ToggleBold(); a.markComposerEdited(m) }, busy).Bold()
					formatButton(c, "I", locales.ItalicTooltip(), func() { m.doc.ToggleItalic(); a.markComposerEdited(m) }, busy).Italic()
					formatButton(c, "U", locales.UnderlineTooltip(), func() { m.doc.ToggleUnderline(); a.markComposerEdited(m) }, busy).Underline()
					formatButton(c, "•", locales.BulletsTooltip(), func() { m.doc.ToggleList(richtext.Bullets); a.markComposerEdited(m) }, busy)
				})
				editor := richtext.Editor(c.Key("compose-body"), m.doc, richtext.EditorOptions{
					Label:       locales.Body(),
					Placeholder: locales.BodyPlaceholder(),
				}).Height(250).Disabled(busy)
				if editor.Changed() {
					a.markComposerEdited(m)
				}
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
				if status := a.composeSaveStatus(m); status != "" {
					color := t.TextMuted
					if m.saveState == draftSaveFailed {
						color = t.Danger
					}
					ui.Text(c, status).FontSize(11).TextColor(color)
				}
				ui.Text(c, locales.SendShortcut()).FontSize(11).TextColor(t.TextMuted).Grow(1)
				ui.Button(c, locales.Cancel()).Disabled(busy).OnClick(a.closeCompose)
				ui.Button(c, locales.DiscardDraft()).Disabled(busy).OnClick(a.discardCompose)
				primaryAction(c, locales.Send()).Disabled(busy || attachmentsLoading || attachmentsFailed).OnClick(a.sendCompose)
			})
		})
		if len(dropped) > 0 {
			a.addComposeAttachments(dropped)
		}
	}).Dismissed()
	if dismissed {
		m.open = true
		a.closeCompose()
	}
}

func (a *App) composeField(c *ui.Context, m *composer, key, label string, value *string, placeholder string) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).Width(52).Shrink(0)
		field := ui.TextInput(c.Key(key), value).Label(label).Placeholder(placeholder).Grow(1).Disabled(m.sending || m.pendingAction != composerActionNone)
		if field.Changed() {
			a.markComposerEdited(m)
		}
	})
}

func formatButton(c *ui.Context, label, tip string, action func(), disabled ...bool) ui.Element {
	inactive := len(disabled) > 0 && disabled[0]
	return ui.Button(c, label).KeepFocus().Tooltip(tip).MinWidth(30).Disabled(inactive).OnClick(action)
}

func (a *App) sendCompose() {
	a.sendComposeWithDraft()
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
