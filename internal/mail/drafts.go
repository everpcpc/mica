package mail

import (
	"context"
	"errors"
	"fmt"
	stdmail "net/mail"
	"sort"
	"strconv"
	"strings"
)

func (d *Demo) Drafts(_ context.Context, query Query) (DraftPage, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	limit := query.Limit
	if limit <= 0 {
		limit = demoThreadLimit
	}
	offset := 0
	if query.PageToken != "" {
		parsed, err := strconv.Atoi(query.PageToken)
		if err != nil || parsed < 0 {
			return DraftPage{}, errors.New("mail: invalid page token")
		}
		offset = parsed
	}
	drafts := make([]DraftRecord, 0, len(d.drafts))
	for _, draft := range d.drafts {
		if draftMatchesSearch(draft, query.Search) {
			drafts = append(drafts, draft.Clone())
		}
	}
	sort.Slice(drafts, func(i, j int) bool {
		if drafts[i].Message.Date.Equal(drafts[j].Message.Date) {
			return drafts[i].ID < drafts[j].ID
		}
		return drafts[i].Message.Date.After(drafts[j].Message.Date)
	})
	offset = min(offset, len(drafts))
	end := len(drafts)
	if limit < end-offset {
		end = offset + limit
	}
	page := DraftPage{Drafts: drafts[offset:end]}
	if end < len(drafts) {
		page.NextPageToken = strconv.Itoa(end)
	}
	return page, nil
}

func (d *Demo) Draft(_ context.Context, id string) (DraftRecord, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	draft, ok := d.drafts[id]
	if !ok {
		return DraftRecord{}, errors.New("mail: draft not found")
	}
	return draft.Clone(), nil
}

func (d *Demo) CreateDraft(_ context.Context, compose Compose) (DraftRecord, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.nextID++
	draftID := fmt.Sprintf("demo-draft-%d", d.nextID)
	d.nextID++
	messageID := fmt.Sprintf("demo-message-%d", d.nextID)
	draft := DraftRecord{ID: draftID, Message: d.draftMessage(messageID, compose)}
	d.replaceDraftAttachments(&draft.Message, compose.Attachments)
	d.drafts[draftID] = draft
	return draft.Clone(), nil
}

func (d *Demo) UpdateDraft(_ context.Context, id string, compose Compose) (DraftRecord, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	draft, ok := d.drafts[id]
	if !ok {
		return DraftRecord{}, errors.New("mail: draft not found")
	}
	message := draft.Message
	message.ThreadID = compose.ThreadID
	message.To = strings.TrimSpace(compose.To)
	message.Cc = strings.TrimSpace(compose.Cc)
	message.Bcc = strings.TrimSpace(compose.Bcc)
	message.Subject = strings.TrimSpace(compose.Subject)
	message.InReplyTo = strings.TrimSpace(compose.InReplyTo)
	message.References = strings.TrimSpace(compose.References)
	message.Date = d.now()
	message.Text = compose.Text
	message.HTML = compose.HTML
	d.replaceDraftAttachments(&message, compose.Attachments)
	draft.Message = message
	d.drafts[id] = draft
	return draft.Clone(), nil
}

func (d *Demo) DeleteDraft(_ context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	draft, ok := d.drafts[id]
	if !ok {
		return errors.New("mail: draft not found")
	}
	d.deleteDraftAttachments(draft.Message)
	delete(d.drafts, id)
	return nil
}

func (d *Demo) SendDraft(ctx context.Context, id string, compose Compose) error {
	if err := ValidateComposeForSend(compose); err != nil {
		return err
	}
	if _, err := d.UpdateDraft(ctx, id, compose); err != nil {
		return err
	}
	if err := d.Send(ctx, compose); err != nil {
		return err
	}
	return d.DeleteDraft(ctx, id)
}

func (d *Demo) draftMessage(id string, compose Compose) Message {
	return Message{
		ID:         id,
		ThreadID:   compose.ThreadID,
		From:       (&stdmail.Address{Name: d.account.Name, Address: d.account.Email}).String(),
		To:         strings.TrimSpace(compose.To),
		Cc:         strings.TrimSpace(compose.Cc),
		Bcc:        strings.TrimSpace(compose.Bcc),
		Subject:    strings.TrimSpace(compose.Subject),
		MessageID:  fmt.Sprintf("<%s@mica.example>", id),
		InReplyTo:  strings.TrimSpace(compose.InReplyTo),
		References: strings.TrimSpace(compose.References),
		Date:       d.now(),
		Text:       compose.Text,
		HTML:       compose.HTML,
		LabelIDs:   []string{Draft},
	}
}

func (d *Demo) replaceDraftAttachments(message *Message, attachments []OutgoingAttachment) {
	d.deleteDraftAttachments(*message)
	message.Attachments = nil
	for i, attachment := range attachments {
		metadata := Attachment{
			ID:       fmt.Sprintf("%s-attachment-%d", message.ID, i+1),
			Name:     attachment.Name,
			MIMEType: attachment.MIMEType,
			Size:     int64(len(attachment.Data)),
		}
		message.Attachments = append(message.Attachments, metadata)
		d.attachmentData[message.ID+"\x00"+metadata.ID] = append([]byte(nil), attachment.Data...)
	}
}

func (d *Demo) deleteDraftAttachments(message Message) {
	for _, attachment := range message.Attachments {
		delete(d.attachmentData, message.ID+"\x00"+attachment.ID)
	}
}

func draftMatchesSearch(draft DraftRecord, search string) bool {
	search = strings.ToLower(strings.TrimSpace(search))
	if search == "" {
		return true
	}
	content := strings.ToLower(draft.Message.Subject + "\n" + draft.Message.Text + "\n" + draft.Message.HTML)
	return strings.Contains(content, search)
}
