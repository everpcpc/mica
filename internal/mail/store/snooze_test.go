package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"mica/internal/mail"
)

func TestSnoozeRecordRestartAndAccountIsolation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	s := openTestStore(t, path)
	wakeAt := time.Date(2026, 10, 26, 8, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	record := mail.SnoozeRecord{
		AccountEmail: "A@EXAMPLE.COM", ThreadID: "t1", WakeAt: wakeAt,
		Summary: mail.ThreadSummary{ID: "t1", Subject: "First", LabelIDs: []string{mail.Snoozed}},
	}
	if err := s.SaveSnooze(ctx, record.AccountEmail, record); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, path)
	records, err := s.SnoozeRecords(ctx, "a@example.com")
	if err != nil || len(records) != 1 {
		t.Fatalf("snooze records = %+v, %v", records, err)
	}
	got := records[0]
	if got.AccountEmail != "a@example.com" || !got.WakeAt.Equal(wakeAt) || got.Summary.Subject != "First" || !got.Summary.SnoozeAt.Equal(wakeAt) {
		t.Fatalf("restored snooze = %+v", got)
	}
	if _, found, err := s.SnoozeRecord(ctx, "a@example.com", "t1", wakeAt.Add(time.Second)); err != nil || found {
		t.Fatalf("stale wake time matched: found=%v err=%v", found, err)
	}
	if _, found, err := s.SnoozeRecord(ctx, "a@example.com", "t1", wakeAt); err != nil || !found {
		t.Fatalf("current wake time did not match: found=%v err=%v", found, err)
	}
	if err := s.DeleteSnooze(ctx, "a@example.com", "t1", wakeAt.Add(time.Second)); !errors.Is(err, ErrSnoozeConflict) {
		t.Fatalf("stale deletion error = %v", err)
	}
	if err := s.SaveSnooze(ctx, "b@example.com", mail.SnoozeRecord{ThreadID: "t1", WakeAt: wakeAt, Summary: mail.ThreadSummary{Subject: "Second"}}); err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"a@example.com", "b@example.com"} {
		if err := s.SaveAccount(ctx, mail.Account{Email: email}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteAccount(ctx, "a@example.com"); err != nil {
		t.Fatal(err)
	}
	records, err = s.SnoozeRecords(ctx, "a@example.com")
	if err != nil || len(records) != 0 {
		t.Fatalf("deleted account snoozes = %+v, %v", records, err)
	}
	records, err = s.SnoozeRecords(ctx, "b@example.com")
	if err != nil || len(records) != 1 || records[0].Summary.Subject != "Second" {
		t.Fatalf("other account snoozes = %+v, %v", records, err)
	}
	if err := s.CancelSnooze(ctx, "b@example.com", "t1"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.SnoozeRecord(ctx, "b@example.com", "t1", wakeAt); found {
		t.Fatal("cancelled snooze remains")
	}
}

func TestSnoozeMigrationAndWakeDeletion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	s := openTestStore(t, path)
	if _, err := s.db.Execute(ctx, "DROP TABLE IF EXISTS snoozed_threads"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Execute(ctx, "PRAGMA user_version = 7"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, path)
	wakeAt := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	if err := s.SaveSnooze(ctx, "a@example.com", mail.SnoozeRecord{ThreadID: "t1", WakeAt: wakeAt}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSnooze(ctx, "a@example.com", "t1", wakeAt); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.SnoozeRecord(ctx, "a@example.com", "t1", wakeAt); found {
		t.Fatal("completed snooze remains")
	}
}
