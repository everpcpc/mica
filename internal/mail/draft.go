package mail

import (
	"context"
	"errors"
	"fmt"
	stdmail "net/mail"
	"strings"
)

type DraftRecord struct {
	ID      string
	Message Message
}

type DraftPage struct {
	Drafts        []DraftRecord
	NextPageToken string
}

type DraftSnapshot struct {
	DraftID          string
	Compose          Compose
	AttachmentErrors []int
}

type DraftManager interface {
	Drafts(context.Context, Query) (DraftPage, error)
	Draft(context.Context, string) (DraftRecord, error)
	CreateDraft(context.Context, Compose) (DraftRecord, error)
	UpdateDraft(context.Context, string, Compose) (DraftRecord, error)
	DeleteDraft(context.Context, string) error
	SendDraft(context.Context, string, Compose) error
}

func CloneCompose(compose Compose) Compose {
	compose.Attachments = append([]OutgoingAttachment(nil), compose.Attachments...)
	for i := range compose.Attachments {
		compose.Attachments[i].Data = append([]byte(nil), compose.Attachments[i].Data...)
	}
	return compose
}

func (d DraftRecord) Clone() DraftRecord {
	d.Message.LabelIDs = append([]string(nil), d.Message.LabelIDs...)
	d.Message.Attachments = append([]Attachment(nil), d.Message.Attachments...)
	for i := range d.Message.Attachments {
		d.Message.Attachments[i].Data = append([]byte(nil), d.Message.Attachments[i].Data...)
	}
	return d
}

func ValidateComposeForSend(compose Compose) error {
	if strings.TrimSpace(compose.To) == "" && strings.TrimSpace(compose.Cc) == "" && strings.TrimSpace(compose.Bcc) == "" {
		return errors.New("mail: at least one recipient is required")
	}
	for _, recipients := range []string{compose.To, compose.Cc, compose.Bcc} {
		if strings.TrimSpace(recipients) == "" {
			continue
		}
		if _, err := stdmail.ParseAddressList(recipients); err != nil {
			return fmt.Errorf("mail: invalid recipients: %w", err)
		}
	}
	if strings.TrimSpace(compose.Text) == "" && strings.TrimSpace(compose.HTML) == "" {
		return errors.New("mail: message body is required")
	}
	return nil
}
