package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"mica/internal/mail"
)

func outboxFixture(email, id string, attempt uint64) mail.OutboxMessage {
	updated := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC).Add(time.Duration(attempt) * time.Second)
	return mail.OutboxMessage{
		ID: id, AccountEmail: email, DraftID: "draft-" + id, State: mail.OutboxSending, Attempt: attempt,
		Created: updated, Updated: updated,
		Compose: mail.Compose{
			To: "to@example.com", Subject: "Outgoing", Text: "body", MessageID: "<" + id + "@example.com>",
			Attachments: []mail.OutgoingAttachment{{Name: "payload.bin", MIMEType: "application/octet-stream", Data: []byte{0, 1, '\r', '\n', 255}}},
		},
	}
}

func TestOutboxRoundTripPreservesAttachmentsAndSnapshot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	s := openTestStore(t, path)
	record := outboxFixture("A@EXAMPLE.COM", "outbox-a", 1)
	record.AccountEmail = normalizeAccountEmail(record.AccountEmail)
	if err := s.EnqueueOutbox(ctx, "A@EXAMPLE.COM", record, mail.DraftSnapshot{}); err != nil {
		t.Fatal(err)
	}
	other := outboxFixture("b@example.com", "outbox-b", 1)
	if err := s.EnqueueOutbox(ctx, other.AccountEmail, other, mail.DraftSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, path)
	got, found, err := s.OutboxMessage(ctx, "a@example.com", record.ID)
	if err != nil || !found || !reflect.DeepEqual(got, record) {
		t.Fatalf("outbox round trip: found=%v err=%v\ngot=%+v\nwant=%+v", found, err, got, record)
	}
	if !reflect.DeepEqual(got.Compose.Attachments[0].Data, record.Compose.Attachments[0].Data) {
		t.Fatalf("attachment bytes = %v", got.Compose.Attachments[0].Data)
	}
	snapshot, found, err := s.DraftSnapshot(ctx, "a@example.com")
	if err != nil || !found || snapshot.OutboxID != record.ID || snapshot.Compose.MessageID != record.Compose.MessageID {
		t.Fatalf("linked snapshot = %+v found=%v err=%v", snapshot, found, err)
	}
	for _, email := range []string{"a@example.com", "b@example.com"} {
		messages, err := s.OutboxMessages(ctx, email)
		if err != nil || len(messages) != 1 || messages[0].AccountEmail != email {
			t.Fatalf("outbox account %q = %+v err=%v", email, messages, err)
		}
	}
}

func TestOutboxRejectsStaleAttemptAtomically(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	record := outboxFixture("a@example.com", "outbox-a", 1)
	if err := s.EnqueueOutbox(ctx, record.AccountEmail, record, mail.DraftSnapshot{}); err != nil {
		t.Fatal(err)
	}
	newer := record
	newer.Attempt = 2
	newer.Compose.Text = "newer"
	if err := s.EnqueueOutbox(ctx, newer.AccountEmail, newer, mail.DraftSnapshot{}); err != nil {
		t.Fatal(err)
	}
	stale := record
	stale.State = mail.OutboxUncertain
	stale.Compose.Text = "stale"
	if err := s.EnqueueOutbox(ctx, stale.AccountEmail, stale, mail.DraftSnapshot{}); !errors.Is(err, ErrOutboxConflict) {
		t.Fatalf("stale enqueue error = %v", err)
	}
	got, _, _ := s.OutboxMessage(ctx, record.AccountEmail, record.ID)
	if got.Attempt != 2 || got.Compose.Text != "newer" {
		t.Fatalf("stale attempt replaced current record: %+v", got)
	}
	snapshot, _, _ := s.DraftSnapshot(ctx, record.AccountEmail)
	if snapshot.Compose.Text != "newer" {
		t.Fatalf("conflicting enqueue changed the snapshot: %+v", snapshot)
	}
	if err := s.DeleteOutboxMessage(ctx, record.AccountEmail, record.ID, 1); !errors.Is(err, ErrOutboxConflict) {
		t.Fatalf("stale delete error = %v", err)
	}
	if err := s.DeleteOutboxMessage(ctx, record.AccountEmail, record.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.OutboxMessage(ctx, record.AccountEmail, record.ID); found {
		t.Fatal("current attempt did not delete the record")
	}
}

func TestCompleteOutboxRemovesSnapshotAndRejectsStaleAttempt(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	record := outboxFixture("a@example.com", "outbox-a", 1)
	if err := s.EnqueueOutbox(ctx, record.AccountEmail, record, mail.DraftSnapshot{}); err != nil {
		t.Fatal(err)
	}
	newer := record
	newer.Attempt = 2
	if err := s.EnqueueOutbox(ctx, newer.AccountEmail, newer, mail.DraftSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteOutbox(ctx, record.AccountEmail, record.ID, 1); !errors.Is(err, ErrOutboxConflict) {
		t.Fatalf("stale completion error = %v", err)
	}
	if _, found, _ := s.DraftSnapshot(ctx, record.AccountEmail); !found {
		t.Fatal("stale completion removed the linked snapshot")
	}
	if err := s.CompleteOutbox(ctx, record.AccountEmail, record.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.OutboxMessage(ctx, record.AccountEmail, record.ID); found {
		t.Fatal("completion retained the Outbox row")
	}
	if _, found, _ := s.DraftSnapshot(ctx, record.AccountEmail); found {
		t.Fatal("completion retained the linked snapshot")
	}
	if err := s.CompleteOutbox(ctx, record.AccountEmail, record.ID, 2); err != nil {
		t.Fatalf("missing completion was not idempotent: %v", err)
	}
}

func TestDeleteAccountRemovesOutbox(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	account := mail.Account{Email: "a@example.com"}
	if err := s.SaveAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	record := outboxFixture(account.Email, "outbox-a", 1)
	if err := s.EnqueueOutbox(ctx, account.Email, record, mail.DraftSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, account.Email); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.OutboxMessage(ctx, account.Email, record.ID); found {
		t.Fatal("account deletion retained the outbox record")
	}
}

func TestOutboxMigrationFromDraftSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	s := openTestStore(t, path)
	for _, query := range []string{
		"DROP TRIGGER IF EXISTS outbox_attempt_guard",
		"DROP TABLE IF EXISTS outbox_messages",
		"PRAGMA user_version = 3",
	} {
		if _, err := s.db.Execute(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, path)
	record := outboxFixture("a@example.com", "outbox-a", 1)
	if err := s.EnqueueOutbox(ctx, record.AccountEmail, record, mail.DraftSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.OutboxMessage(ctx, record.AccountEmail, record.ID); !found {
		t.Fatal("migrated outbox table is unavailable")
	}
}
