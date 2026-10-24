package store

import (
	"context"
	"path/filepath"
	"testing"

	"mica/internal/mail"
)

func TestDraftSnapshotMigrationRestartAndAccountIsolation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	var s *Store
	t.Cleanup(func() {
		if s != nil {
			_ = s.Close()
		}
	})
	var err error
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	account := mail.Account{Email: "first@example.com"}
	if err := s.SaveAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLastAccount(ctx, account.Email); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Execute(ctx, "DROP TABLE draft_snapshots"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Execute(ctx, "PRAGMA user_version = 2"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = nil

	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.LastAccount(ctx); err != nil || !found {
		t.Fatalf("existing account was lost during migration: found=%v err=%v", found, err)
	}
	first := mail.DraftSnapshot{DraftID: "draft-first", Compose: mail.Compose{
		To: "to@example.com", Bcc: "bcc@example.com", Text: "Unsynced", ThreadID: "thread-1", InReplyTo: "<selected@example.com>", References: "<root@example.com>",
		Attachments: []mail.OutgoingAttachment{{Name: "payload.bin", MIMEType: "application/octet-stream", Data: []byte{0, 1, '\r', '\n', 255}}},
	}}
	second := mail.DraftSnapshot{DraftID: "draft-second", Compose: mail.Compose{Text: "Other account"}}
	if err := s.SaveDraftSnapshot(ctx, account.Email, first); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDraftSnapshot(ctx, "second@example.com", second); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = nil

	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := s.DraftSnapshot(ctx, account.Email)
	requireSnapshot(t, got, found, err, first)
	got.Compose.Attachments[0].Data[0] = 'X'
	again, found, err := s.DraftSnapshot(ctx, account.Email)
	if err != nil || !found || again.Compose.Attachments[0].Data[0] != 0 {
		t.Fatalf("snapshot data was aliased: %+v, found=%v err=%v", again, found, err)
	}
	if err := s.DeleteDraftSnapshot(ctx, account.Email); err != nil {
		t.Fatal(err)
	}
	_, found, err = s.DraftSnapshot(ctx, account.Email)
	requireMissing(t, found, err)
	other, found, err := s.DraftSnapshot(ctx, "second@example.com")
	requireSnapshot(t, other, found, err, second)
}
