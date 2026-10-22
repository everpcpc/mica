package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"mica/internal/mail"
)

type mailStore interface {
	LastAccount(context.Context) (mail.Account, bool, error)
	SaveAccount(context.Context, mail.Account) error
	Labels(context.Context, string) ([]mail.Label, bool, error)
	SaveLabels(context.Context, string, []mail.Label) error
	ThreadList(context.Context, string, mail.Query) ([]mail.ThreadSummary, bool, error)
	SaveThreadList(context.Context, string, mail.Query, []mail.ThreadSummary) error
	Thread(context.Context, string, string) (mail.Thread, bool, error)
	SaveThread(context.Context, string, mail.Thread) error
	DeleteThread(context.Context, string, string) error
}

var _ mailStore = (*Store)(nil)

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return s
}

func requireSnapshot[T any](t *testing.T, got T, found bool, err error, want T) {
	t.Helper()
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if !found {
		t.Fatal("snapshot not found")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %#v, want %#v", got, want)
	}
}

func requireMissing(t *testing.T, found bool, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("read missing snapshot: %v", err)
	}
	if found {
		t.Fatal("missing snapshot reported as found")
	}
}

func TestOpenConfiguresDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "mail.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	s := openTestStore(t, path)

	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("database directory mode = %o, want 700", got)
	}

	rows, err := s.db.Query(context.Background(), "PRAGMA journal_mode")
	if err != nil {
		t.Fatal(err)
	}
	if got := rows.Values[0][0]; got != "wal" {
		t.Fatalf("journal_mode = %v, want wal", got)
	}
	rows, err = s.db.Query(context.Background(), "PRAGMA foreign_keys")
	if err != nil {
		t.Fatal(err)
	}
	if got := rows.Values[0][0]; got != int64(1) {
		t.Fatalf("foreign_keys = %v, want 1", got)
	}
	rows, err = s.db.Query(context.Background(), "PRAGMA user_version")
	if err != nil {
		t.Fatal(err)
	}
	if got := rows.Values[0][0]; got != int64(len(migrations)) {
		t.Fatalf("user_version = %v, want %d", got, len(migrations))
	}
}

func TestOpenRejectsNewerDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mail.db")
	s := openTestStore(t, path)
	if _, err := s.db.Execute(context.Background(), "PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(context.Background(), path)
	if reopened != nil {
		reopened.Close()
	}
	if !errors.Is(err, ErrNewerDatabase) {
		t.Fatalf("Open() error = %v, want ErrNewerDatabase", err)
	}
}

func TestRoundTripPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	s := openTestStore(t, path)
	account := mail.Account{
		Name:                "Example User",
		Email:               "user@example.com",
		Demo:                true,
		PhotoURL:            "https://example.com/photo.jpg",
		ProfileUnauthorized: true,
	}
	labels := []mail.Label{
		{ID: "INBOX", Name: "Inbox", Messages: 12, Unread: 3, System: true},
		{ID: "custom", Name: "Receipts", Messages: 4, Unread: 1, Hide: true, ShowIfUnread: true},
	}
	query := mail.Query{LabelID: mail.Inbox, Search: "invoice", Limit: 25}
	summaries := []mail.ThreadSummary{{
		ID:           "thread-1",
		Subject:      "Invoice",
		Snippet:      "Your receipt",
		Senders:      "Billing",
		Updated:      time.Date(2026, 10, 23, 9, 8, 7, 123456789, time.UTC),
		MessageCount: 2,
		LabelIDs:     []string{mail.Inbox, mail.Important},
		Unread:       true,
		Starred:      true,
	}}
	thread := mail.Thread{
		ID:       "thread-1",
		Subject:  "Invoice",
		LabelIDs: []string{mail.Inbox},
		Messages: []mail.Message{{
			ID:         "message-1",
			ThreadID:   "thread-1",
			From:       "billing@example.com",
			To:         "user@example.com",
			Cc:         "archive@example.com",
			Subject:    "Invoice",
			MessageID:  "message-id@example.com",
			References: "reference@example.com",
			Date:       time.Date(2026, 10, 22, 18, 17, 16, 987654321, time.UTC),
			Text:       "Plain text",
			HTML:       "<p>HTML</p>",
			LabelIDs:   []string{mail.Inbox},
			Attachments: []mail.Attachment{{
				ID:        "attachment-1",
				Name:      "invoice.pdf",
				MIMEType:  "application/pdf",
				Size:      12345,
				ContentID: "content-id",
			}},
		}},
	}
	if err := s.SaveAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveLabels(ctx, account.Email, labels); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveThreadList(ctx, account.Email, query, summaries); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveThread(ctx, account.Email, thread); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = openTestStore(t, path)
	gotAccount, found, err := s.LastAccount(ctx)
	requireSnapshot(t, gotAccount, found, err, account)
	gotLabels, found, err := s.Labels(ctx, account.Email)
	requireSnapshot(t, gotLabels, found, err, labels)
	gotSummaries, found, err := s.ThreadList(ctx, account.Email, query)
	requireSnapshot(t, gotSummaries, found, err, summaries)
	gotThread, found, err := s.Thread(ctx, account.Email, thread.ID)
	requireSnapshot(t, gotThread, found, err, thread)
}

func TestSaveAccountMarksLastAccount(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	first := mail.Account{Name: "First", Email: "first@example.com"}
	second := mail.Account{Name: "Second", Email: "second@example.com"}
	if err := s.SaveAccount(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAccount(ctx, second); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.LastAccount(ctx)
	requireSnapshot(t, got, found, err, second)

	first.Name = "Updated First"
	if err := s.SaveAccount(ctx, first); err != nil {
		t.Fatal(err)
	}
	got, found, err = s.LastAccount(ctx)
	requireSnapshot(t, got, found, err, first)
}

func TestAccountAndQueryIsolation(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	firstEmail := "first@example.com"
	secondEmail := "second@example.com"
	firstLabels := []mail.Label{{ID: "first-label"}}
	secondLabels := []mail.Label{{ID: "second-label"}}
	if err := s.SaveLabels(ctx, firstEmail, firstLabels); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveLabels(ctx, secondEmail, secondLabels); err != nil {
		t.Fatal(err)
	}

	firstQuery := mail.Query{LabelID: mail.Inbox, Search: "same", Limit: 10}
	secondQuery := mail.Query{LabelID: mail.Inbox, Search: "same", Limit: 20}
	firstList := []mail.ThreadSummary{{ID: "first-query"}}
	secondList := []mail.ThreadSummary{{ID: "second-query"}}
	otherAccountList := []mail.ThreadSummary{{ID: "other-account"}}
	if err := s.SaveThreadList(ctx, firstEmail, firstQuery, firstList); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveThreadList(ctx, firstEmail, secondQuery, secondList); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveThreadList(ctx, secondEmail, firstQuery, otherAccountList); err != nil {
		t.Fatal(err)
	}

	gotLabels, found, err := s.Labels(ctx, firstEmail)
	requireSnapshot(t, gotLabels, found, err, firstLabels)
	gotLabels, found, err = s.Labels(ctx, secondEmail)
	requireSnapshot(t, gotLabels, found, err, secondLabels)
	gotList, found, err := s.ThreadList(ctx, firstEmail, firstQuery)
	requireSnapshot(t, gotList, found, err, firstList)
	gotList, found, err = s.ThreadList(ctx, firstEmail, secondQuery)
	requireSnapshot(t, gotList, found, err, secondList)
	gotList, found, err = s.ThreadList(ctx, secondEmail, firstQuery)
	requireSnapshot(t, gotList, found, err, otherAccountList)
}

func TestEmptyListsAreFound(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	email := "user@example.com"
	labels := []mail.Label{}
	summaries := []mail.ThreadSummary{}
	query := mail.Query{LabelID: mail.Inbox}
	if err := s.SaveLabels(ctx, email, labels); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveThreadList(ctx, email, query, summaries); err != nil {
		t.Fatal(err)
	}
	gotLabels, found, err := s.Labels(ctx, email)
	requireSnapshot(t, gotLabels, found, err, labels)
	gotSummaries, found, err := s.ThreadList(ctx, email, query)
	requireSnapshot(t, gotSummaries, found, err, summaries)
}

func TestSaveListsReplaceSnapshots(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	email := "user@example.com"
	query := mail.Query{LabelID: mail.Inbox}
	if err := s.SaveLabels(ctx, email, []mail.Label{{ID: "old"}, {ID: "stale"}}); err != nil {
		t.Fatal(err)
	}
	labels := []mail.Label{{ID: "replacement"}}
	if err := s.SaveLabels(ctx, email, labels); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveThreadList(ctx, email, query, []mail.ThreadSummary{{ID: "old"}, {ID: "stale"}}); err != nil {
		t.Fatal(err)
	}
	summaries := []mail.ThreadSummary{{ID: "replacement"}}
	if err := s.SaveThreadList(ctx, email, query, summaries); err != nil {
		t.Fatal(err)
	}

	gotLabels, found, err := s.Labels(ctx, email)
	requireSnapshot(t, gotLabels, found, err, labels)
	gotSummaries, found, err := s.ThreadList(ctx, email, query)
	requireSnapshot(t, gotSummaries, found, err, summaries)
}

func TestDeleteThreadIsAccountScoped(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	firstEmail := "first@example.com"
	secondEmail := "second@example.com"
	firstThread := mail.Thread{ID: "shared", Subject: "First"}
	secondThread := mail.Thread{ID: "shared", Subject: "Second"}
	if err := s.SaveThread(ctx, firstEmail, firstThread); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveThread(ctx, secondEmail, secondThread); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.Thread(ctx, firstEmail, firstThread.ID)
	requireSnapshot(t, got, found, err, firstThread)
	got, found, err = s.Thread(ctx, secondEmail, secondThread.ID)
	requireSnapshot(t, got, found, err, secondThread)

	if err := s.DeleteThread(ctx, firstEmail, firstThread.ID); err != nil {
		t.Fatal(err)
	}
	_, found, err = s.Thread(ctx, firstEmail, firstThread.ID)
	requireMissing(t, found, err)
	got, found, err = s.Thread(ctx, secondEmail, secondThread.ID)
	requireSnapshot(t, got, found, err, secondThread)
	if err := s.DeleteThread(ctx, firstEmail, firstThread.ID); err != nil {
		t.Fatalf("deleting a missing thread: %v", err)
	}
}

func TestPreferencesPersistAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	s := openTestStore(t, path)
	if err := s.SavePreference(ctx, "sidebar-width", "240"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, path)
	value, found, err := s.Preference(ctx, "sidebar-width")
	if err != nil || !found || value != "240" {
		t.Fatalf("preference = %q, found=%v, err=%v, want 240", value, found, err)
	}
	_, found, err = s.Preference(ctx, "missing")
	requireMissing(t, found, err)
}

func TestMissingSnapshots(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	_, found, err := s.LastAccount(ctx)
	requireMissing(t, found, err)
	_, found, err = s.Labels(ctx, "missing@example.com")
	requireMissing(t, found, err)
	_, found, err = s.ThreadList(ctx, "missing@example.com", mail.Query{LabelID: mail.Inbox})
	requireMissing(t, found, err)
	_, found, err = s.Thread(ctx, "missing@example.com", "missing")
	requireMissing(t, found, err)
}
