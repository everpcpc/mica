package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	mailmodel "mica/internal/mail"
)

var errComposeAttachmentUnavailable = errors.New("attachments unavailable")

type composeAttachmentSource interface {
	name() string
	read(context.Context, int64) (mailmodel.OutgoingAttachment, error)
}

type localComposeAttachmentSource struct {
	path string
}

func (s localComposeAttachmentSource) name() string {
	return filepath.Base(s.path)
}

func (s localComposeAttachmentSource) read(_ context.Context, limit int64) (mailmodel.OutgoingAttachment, error) {
	return readOutgoingAttachmentLimit(s.path, limit)
}

type messageComposeAttachmentSource struct {
	fetcher    mailmodel.AttachmentFetcher
	messageID  string
	attachment mailmodel.Attachment
}

func (s messageComposeAttachmentSource) name() string {
	return s.attachment.Name
}

func (s messageComposeAttachmentSource) read(ctx context.Context, limit int64) (mailmodel.OutgoingAttachment, error) {
	outgoing := mailmodel.OutgoingAttachment{Name: s.attachment.Name, MIMEType: s.attachment.MIMEType}
	if err := validateOutgoingAttachmentName(outgoing.Name); err != nil {
		return outgoing, err
	}
	if limit < 0 || s.attachment.Size > limit {
		return outgoing, errComposeAttachmentTooLarge
	}
	data := s.attachment.Data
	if data == nil {
		if s.fetcher == nil {
			return outgoing, errComposeAttachmentUnavailable
		}
		var err error
		data, err = s.fetcher.Attachment(ctx, s.messageID, s.attachment.ID)
		if err != nil {
			return outgoing, err
		}
	}
	if int64(len(data)) > limit {
		return outgoing, errComposeAttachmentTooLarge
	}
	outgoing.Data = append([]byte(nil), data...)
	if outgoing.MIMEType == "" {
		outgoing.MIMEType = outgoingMIMEType(outgoing.Name, outgoing.Data)
	}
	return outgoing, nil
}

func validateOutgoingAttachmentName(name string) error {
	if strings.TrimSpace(name) == "" || strings.IndexFunc(name, func(r rune) bool {
		return r < 0x20 || r == 0x7f
	}) >= 0 {
		return errors.New("invalid attachment name")
	}
	return nil
}

func (a *App) addMessageAttachments(message mailmodel.Message) {
	fetcher, _ := a.service.(mailmodel.AttachmentFetcher)
	sources := make([]composeAttachmentSource, len(message.Attachments))
	for i, attachment := range message.Attachments {
		sources[i] = messageComposeAttachmentSource{fetcher: fetcher, messageID: message.ID, attachment: attachment}
	}
	a.addComposeAttachmentSources(sources)
}
