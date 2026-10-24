package app

import (
	"context"
	"fmt"
	"strings"

	"mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo/ui"
)

func outboxRecordKey(id string) string {
	return strings.TrimPrefix(id, "outbox-")
}

func outboxMessage(record mail.OutboxMessage) mail.Message {
	id := "outbox-" + record.ID
	message := mail.Message{
		ID: id, ThreadID: record.Compose.ThreadID, AccountEmail: record.AccountEmail,
		From: record.AccountEmail, To: record.Compose.To, Cc: record.Compose.Cc, Bcc: record.Compose.Bcc,
		Subject: record.Compose.Subject, MessageID: record.Compose.MessageID,
		InReplyTo: record.Compose.InReplyTo, References: record.Compose.References,
		Date: record.Updated, Text: record.Compose.Text, HTML: record.Compose.HTML,
		LabelIDs: []string{mail.Outbox},
	}
	for i, attachment := range record.Compose.Attachments {
		message.Attachments = append(message.Attachments, mail.Attachment{
			ID: fmt.Sprintf("%s-attachment-%d", id, i+1), Name: attachment.Name, MIMEType: attachment.MIMEType,
			Size: int64(len(attachment.Data)), Data: append([]byte(nil), attachment.Data...),
		})
	}
	return message
}

func outboxRecordSummary(record mail.OutboxMessage) mail.ThreadSummary {
	message := outboxMessage(record)
	summary := draftSummary(mail.DraftRecord{ID: message.ID, Message: message})
	summary.Senders = record.Compose.To
	if state := outboxStateLabel(record.State); state != "" {
		summary.Snippet = strings.TrimSpace(state + " · " + summary.Snippet)
	}
	return summary
}

func outboxStateLabel(state mail.OutboxState) string {
	switch state {
	case mail.OutboxSending:
		return locales.OutboxSending()
	case mail.OutboxUncertain:
		return locales.OutboxUncertain()
	default:
		return ""
	}
}

func outboxThread(record mail.OutboxMessage) mail.Thread {
	summary := outboxRecordSummary(record)
	return mail.Thread{
		ID: summary.ID, AccountEmail: record.AccountEmail, Subject: summary.Subject,
		LabelIDs: []string{mail.Outbox}, Messages: []mail.Message{outboxMessage(record)},
	}
}

func (a *App) refreshOutbox() {
	store := a.outbox
	if store == nil {
		a.loading = false
		a.err = errOutboxUnavailable.Error()
		return
	}
	a.request++
	request := a.request
	a.nextPageToken = ""
	a.loadingMore = false
	a.loading = true
	a.err = ""
	owner := a.storeAccount
	if owner == "" {
		owner = normalizedEmail(a.account.Email)
	}
	version, busy := a.serviceVersion, a.outboxBusy
	a.schedule(func() {
		records, err := store.OutboxMessages(context.Background(), owner)
		for i := range records {
			if records[i].State == mail.OutboxSending && records[i].ID != busy {
				records[i].State = mail.OutboxUncertain
				if saveErr := store.SaveOutboxMessage(context.Background(), owner, records[i]); saveErr != nil {
					logCacheError(saveErr)
				}
			}
		}
		a.publish(func() {
			if version != a.serviceVersion || request != a.request || a.selectedLabel != mail.Outbox {
				return
			}
			a.loading = false
			if err != nil {
				a.err = err.Error()
				return
			}
			a.outboxRecords = make(map[string]mail.OutboxMessage, len(records))
			summaries := make([]mail.ThreadSummary, 0, len(records))
			query := strings.ToLower(strings.TrimSpace(a.query))
			for _, record := range records {
				a.outboxRecords[record.ID] = record
				if query != "" && !strings.Contains(strings.ToLower(record.Compose.Subject+" "+record.Compose.To+" "+record.Compose.Cc+" "+record.Compose.Bcc+" "+record.Compose.Text), query) {
					continue
				}
				summaries = append(summaries, outboxRecordSummary(record))
			}
			a.applyThreadList(summaries, true)
			a.startupSelectionDone = true
			if id := outboxRecordKey(a.selectedThread); id != "" {
				if record, found := a.outboxRecords[id]; found {
					a.thread = outboxThread(record)
				} else {
					a.clearThread()
				}
			}
		})
	})
}

func (a *App) outboxDeleteView(c *ui.Context) {
	ui.Modal(c, &a.outboxDeleteOpen, func() {
		ui.Column(c).Width(400).Padding(20).Gap(16).Children(func() {
			ui.Text(c, locales.OutboxDeleteQuestion()).FontSize(17).Bold()
			ui.Text(c, locales.OutboxDeleteConfirmation()).LineHeight(1.4)
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				ui.Button(c, locales.Cancel()).OnClick(func() { a.outboxDeleteOpen = false })
				ui.Button(c, locales.OutboxDeleteConfirm()).TextColor(c.Theme().Danger).Disabled(a.outboxBusy != "").OnClick(a.deleteOutbox)
			})
		})
	})
}
