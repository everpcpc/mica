package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"mica/internal/mail"
)

var errOutboxUnavailable = errors.New("mail: outbox is unavailable")
var errOutboxReviewRequired = errors.New("mail: check Sent before retrying")

type outboxStore interface {
	EnqueueOutbox(context.Context, string, mail.OutboxMessage, mail.DraftSnapshot) error
	SaveOutboxMessage(context.Context, string, mail.OutboxMessage) error
	OutboxMessage(context.Context, string, string) (mail.OutboxMessage, bool, error)
	OutboxMessages(context.Context, string) ([]mail.OutboxMessage, error)
	DeleteOutboxMessage(context.Context, string, string, uint64) error
}

type outboxAttemptResult struct {
	sent bool
	err  error
}

func ensureOutboxIdentity(m *composer, owner string) error {
	if m.outboxID == "" {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return fmt.Errorf("mail: create outbox ID: %w", err)
		}
		m.outboxID = hex.EncodeToString(random[:])
	}
	if m.messageID == "" {
		_, domain, ok := strings.Cut(owner, "@")
		if !ok || domain == "" {
			domain = "mica.example"
		}
		m.messageID = fmt.Sprintf("<%s@%s>", m.outboxID, domain)
	}
	return nil
}

func outboxRecordForCompose(outboxID, owner, draftID string, compose mail.Compose, previous mail.OutboxMessage, found bool) mail.OutboxMessage {
	now := time.Now()
	record := mail.OutboxMessage{
		ID: outboxID, AccountEmail: normalizedEmail(owner), DraftID: draftID, Compose: compose,
		State: mail.OutboxSending, Attempt: 1, Created: now, Updated: now,
	}
	if found {
		record.Created = previous.Created
		record.Attempt = previous.Attempt + 1
	}
	return record
}

func attemptOutboxSend(store outboxStore, owner string, record mail.OutboxMessage, snapshot *mail.DraftSnapshot, service mail.Service, manager mail.DraftManager, hasDrafts bool) outboxAttemptResult {
	if service == nil || record.Attempt == 0 || record.Compose.MessageID == "" {
		return outboxAttemptResult{err: errOutboxUnavailable}
	}
	if record.Attempt > 1 {
		finder, ok := service.(mail.SentMessageFinder)
		if !ok {
			return outboxAttemptResult{err: errOutboxReviewRequired}
		}
		found, err := finder.SentMessageExists(context.Background(), record.Compose.MessageID)
		if err != nil {
			return outboxAttemptResult{err: err}
		}
		if found {
			return outboxAttemptResult{sent: true, err: store.DeleteOutboxMessage(context.Background(), owner, record.ID, record.Attempt-1)}
		}
	}
	if record.DraftID != "" && !hasDrafts {
		return outboxAttemptResult{err: errors.New("mail: drafts are unavailable")}
	}
	record.State = mail.OutboxSending
	record.LastError = ""
	record.Updated = time.Now()
	var err error
	if snapshot != nil {
		snapshot.DraftID = record.DraftID
		snapshot.OutboxID = record.ID
		snapshot.Compose = mail.CloneCompose(record.Compose)
		err = store.EnqueueOutbox(context.Background(), owner, record, *snapshot)
	} else {
		err = store.SaveOutboxMessage(context.Background(), owner, record)
	}
	if err != nil {
		return outboxAttemptResult{err: err}
	}
	if hasDrafts && record.DraftID != "" {
		err = manager.SendDraft(context.Background(), record.DraftID, record.Compose)
	} else {
		err = service.Send(context.Background(), record.Compose)
	}
	if err == nil {
		return outboxAttemptResult{sent: true, err: store.DeleteOutboxMessage(context.Background(), owner, record.ID, record.Attempt)}
	}
	record.State = mail.OutboxUncertain
	record.LastError = err.Error()
	record.Updated = time.Now()
	if saveErr := store.SaveOutboxMessage(context.Background(), owner, record); saveErr != nil {
		return outboxAttemptResult{err: errors.Join(err, saveErr)}
	}
	return outboxAttemptResult{err: err}
}
