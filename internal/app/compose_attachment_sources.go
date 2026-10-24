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
	store      attachmentStore
	save       attachmentCacheWriter
	begin      attachmentCacheBegin
	message    messageRef
	attachment mailmodel.Attachment
	position   int
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
	store, save := s.store, s.save
	var cacheDone func()
	if s.begin == nil {
		store, save = nil, nil
	} else {
		cacheDone = s.begin()
		if cacheDone == nil {
			store, save = nil, nil
		} else {
			defer cacheDone()
		}
	}
	data := s.attachment.Data
	fromCache := false
	if data == nil && store != nil {
		cached, found, err := store.Attachment(ctx, s.message.AccountEmail, s.message.ThreadID, s.message.MessageID, attachmentStorageID(s.attachment, s.position))
		if err != nil {
			logCacheError(err)
		} else if found {
			data, fromCache = cached, true
		}
	}
	if data == nil && !fromCache {
		if s.fetcher == nil {
			return outgoing, errComposeAttachmentUnavailable
		}
		var err error
		data, err = s.fetcher.Attachment(ctx, s.message.MessageID, s.attachment.ID)
		if err != nil {
			return outgoing, err
		}
	}
	if !fromCache {
		if save != nil {
			save(s.message.ThreadID, s.message.MessageID, attachmentStorageID(s.attachment, s.position), data, cacheDone != nil)
		} else if store != nil {
			if err := store.SaveAttachment(ctx, s.message.AccountEmail, s.message.ThreadID, s.message.MessageID, attachmentStorageID(s.attachment, s.position), data); err != nil {
				logCacheError(err)
			}
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
	owner := a.storedMessageOwner(message)
	service, generation := a.ownerService(owner)
	fetcher, _ := service.(mailmodel.AttachmentFetcher)
	store, _ := a.store.(attachmentStore)
	save := a.attachmentCacheWriter(owner, generation)
	begin := a.attachmentCacheBegin(owner, generation)
	ref := messageRefForAccount(owner, message.ThreadID, message.ID)
	sources := make([]composeAttachmentSource, len(message.Attachments))
	for i, attachment := range message.Attachments {
		sources[i] = messageComposeAttachmentSource{fetcher: fetcher, store: store, save: save, begin: begin, message: ref, attachment: attachment, position: i}
	}
	a.addComposeAttachmentSources(sources, false)
}
