package store

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo/plugins/sqlite"
)

func TestSearchThreadsUsesCachedBodiesAndDeduplicates(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	first := mail.ThreadSummary{ID: "t1", Subject: "Old launch", Snippet: "summary", Senders: "Alice", Updated: now, LabelIDs: []string{mail.Inbox}}
	second := first
	second.Subject = "New launch"
	second.Updated = now.Add(time.Hour)
	third := mail.ThreadSummary{ID: "t2", Subject: "Other", Snippet: "other", Senders: "Bob", Updated: now.Add(2 * time.Hour), LabelIDs: []string{mail.Starred, mail.Unread}, Unread: true, Starred: true}
	for i, summaries := range [][]mail.ThreadSummary{{first, third}, {second}} {
		if err := s.SaveThreadList(ctx, "a@example.com", mail.Query{Search: string(rune('a' + i)), Limit: 25}, summaries); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveThreadList(ctx, "b@example.com", mail.Query{Limit: 25}, []mail.ThreadSummary{first}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveThread(ctx, "a@example.com", mail.Thread{ID: "t1", Subject: "New launch", LabelIDs: []string{mail.Inbox}, Messages: []mail.Message{{
		ID: "m1", From: "Alice", Subject: "New launch", Text: "hidden-body-token", Date: now,
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveThread(ctx, "a@example.com", mail.Thread{ID: "t3", Subject: "Standalone", LabelIDs: []string{mail.Snoozed}, Messages: []mail.Message{{
		ID: "m3", From: "Carol", Subject: "Standalone", Text: "standalone-body", Date: now.Add(4 * time.Hour),
	}}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		query mail.Query
		id    string
	}{
		{name: "body", query: mail.Query{Search: "hidden-body-token", Limit: 25}, id: "t1"},
		{name: "detail only", query: mail.Query{Search: "standalone-body", Limit: 25}, id: "t3"},
		{name: "label", query: mail.Query{LabelID: mail.Starred, Limit: 25}, id: "t2"},
		{name: "snoozed", query: mail.Query{Search: "in:snoozed", Limit: 25}, id: "t3"},
		{name: "unread", query: mail.Query{Search: "is:unread", Limit: 25}, id: "t2"},
		{name: "latest first", query: mail.Query{Limit: 1}, id: "t3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.SearchThreads(ctx, "a@example.com", tc.query)
			if err != nil || len(got) != 1 || got[0].ID != tc.id {
				t.Fatalf("SearchThreads(%+v) = %+v, %v", tc.query, got, err)
			}
		})
	}
	got, err := s.SearchThreads(ctx, "a@example.com", mail.Query{Search: "launch", Limit: 25})
	if err != nil || len(got) != 1 || got[0].Subject != "New launch" {
		t.Fatalf("duplicate summaries were not coalesced: %+v, %v", got, err)
	}
}

func TestAttachmentRoundTripAndAccountCleanup(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	s := openTestStore(t, path)
	data := []byte{0, 1, '\r', '\n', 255}
	if err := s.SaveAttachment(ctx, "A@EXAMPLE.COM", "thread", "message", "attachment", data); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, path)
	got, found, err := s.Attachment(ctx, "a@example.com", "thread", "message", "attachment")
	if err != nil || !found || string(got) != string(data) {
		t.Fatalf("attachment = %v found=%v err=%v", got, found, err)
	}
	if _, found, _ := s.Attachment(ctx, "a@example.com", "other", "message", "attachment"); found {
		t.Fatal("attachment matched the wrong thread")
	}
	if err := s.SaveAccount(ctx, mail.Account{Email: "a@example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, "a@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.Attachment(ctx, "a@example.com", "thread", "message", "attachment"); found {
		t.Fatal("account deletion retained attachment bytes")
	}
}

func TestAttachmentCacheEvictsOldestAndSkipsOversize(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	for i, id := range []string{"oldest", "middle", "newest", "fourth"} {
		if err := s.SaveAttachment(ctx, "a@example.com", "thread", "message", id, bytes.Repeat([]byte{byte(i + 1)}, 30<<20)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveAttachment(ctx, "a@example.com", "thread", "message", "extra", bytes.Repeat([]byte{4}, 16<<20)); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.Attachment(ctx, "a@example.com", "thread", "message", "oldest"); found {
		t.Fatal("oldest attachment was not evicted")
	}
	for _, id := range []string{"middle", "newest", "fourth", "extra"} {
		if _, found, _ := s.Attachment(ctx, "a@example.com", "thread", "message", id); !found {
			t.Fatalf("recent attachment %q was evicted", id)
		}
	}
	if err := s.SaveAttachment(ctx, "a@example.com", "thread", "message", "oversize", make([]byte, (32<<20)+1)); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.Attachment(ctx, "a@example.com", "thread", "message", "oversize"); found {
		t.Fatal("oversize attachment was cached")
	}
}

func TestSearchThreadsUsesLatestCacheState(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	old := mail.ThreadSummary{ID: "t1", Subject: "Old", Updated: now, LabelIDs: []string{mail.Starred}, Starred: true}
	current := old
	current.Subject = "Current"
	current.LabelIDs = []string{mail.Inbox}
	current.Starred = false
	if err := s.SaveThreadList(ctx, "a@example.com", mail.Query{Search: "a"}, []mail.ThreadSummary{old}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveThreadList(ctx, "a@example.com", mail.Query{Search: "z"}, []mail.ThreadSummary{current}); err != nil {
		t.Fatal(err)
	}
	t.Run("lists", func(t *testing.T) {
		got, err := s.SearchThreads(ctx, "a@example.com", mail.Query{LabelID: mail.Starred})
		if err != nil || len(got) != 0 {
			t.Fatalf("stale starred summary returned: %+v, %v", got, err)
		}
		got, err = s.SearchThreads(ctx, "a@example.com", mail.Query{})
		if err != nil || len(got) != 1 || got[0].Subject != "Current" {
			t.Fatalf("latest list snapshot was not selected: %+v, %v", got, err)
		}
	})
	if err := s.SaveThread(ctx, "a@example.com", mail.Thread{ID: "t1", Subject: "Detail", LabelIDs: []string{mail.Inbox}, Messages: []mail.Message{{
		ID: "m1", From: "Alice", Text: "Current body", Date: now,
	}}}); err != nil {
		t.Fatal(err)
	}
	t.Run("detail", func(t *testing.T) {
		got, err := s.SearchThreads(ctx, "a@example.com", mail.Query{LabelID: mail.Starred})
		if err != nil || len(got) != 0 {
			t.Fatalf("detail and stale summary labels were merged: %+v, %v", got, err)
		}
		got, err = s.SearchThreads(ctx, "a@example.com", mail.Query{})
		if err != nil || len(got) != 1 || got[0].Subject != "Detail" || got[0].Starred {
			t.Fatalf("latest detail was not selected: %+v, %v", got, err)
		}
	})
}

func TestSearchThreadsUnreadQueryNormalization(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	summaries := []mail.ThreadSummary{
		{ID: "unread", LabelIDs: []string{mail.Unread}, Unread: true},
		{ID: "read", LabelIDs: []string{mail.Inbox}},
	}
	if err := s.SaveThreadList(ctx, "a@example.com", mail.Query{}, summaries); err != nil {
		t.Fatal(err)
	}
	got, err := s.SearchThreads(ctx, "a@example.com", mail.Query{Search: " IS:UNREAD "})
	if err != nil || len(got) != 1 || got[0].ID != "unread" {
		t.Fatalf("normalized unread query = %+v, %v", got, err)
	}
}

func TestSearchThreadsMigratesLegacySnapshots(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	db, err := sqlite.Open(ctx, path, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, migration := range migrations[:4] {
		statements := make([]sqlite.Statement, 0, len(migration)+1)
		for _, sql := range migration {
			statements = append(statements, sqlite.Statement{SQL: sql})
		}
		statements = append(statements, sqlite.Statement{SQL: fmt.Sprintf("PRAGMA user_version = %d", i+1)})
		if _, err := db.Transaction(ctx, statements); err != nil {
			t.Fatal(err)
		}
	}
	legacy := `[{"ID":"legacy","Subject":"Legacy","LabelIDs":["INBOX"]}]`
	if _, err := db.Execute(ctx, "INSERT INTO thread_lists(account_email, query_key, data) VALUES(?, ?, ?)", "User@Example.com", "legacy", legacy); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s := openTestStore(t, path)
	got, err := s.SearchThreads(ctx, "user@example.com", mail.Query{Search: "legacy"})
	if err != nil || len(got) != 1 || got[0].ID != "legacy" {
		t.Fatalf("legacy cache search = %+v, %v", got, err)
	}
}

func TestSearchThreadsSnapshotStorageTypes(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	for _, tc := range []struct {
		name  string
		value any
	}{
		{name: "TEXT JSON", value: `[{"ID":"text","Subject":"Text","LabelIDs":["INBOX"]}]`},
		{name: "invalid type", value: 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.db.Execute(ctx, "DELETE FROM thread_lists"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Execute(ctx, "INSERT INTO thread_lists(account_email, query_key, data) VALUES(?, ?, ?)", "a@example.com", tc.name, tc.value); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("search panicked for %s: %v", tc.name, recovered)
				}
			}()
			got, err := s.SearchThreads(ctx, "a@example.com", mail.Query{})
			if tc.name == "invalid type" {
				if err == nil {
					t.Fatalf("invalid snapshot type returned %+v", got)
				}
				return
			}
			if err != nil || len(got) != 1 || got[0].ID != "text" {
				t.Fatalf("TEXT snapshot search = %+v, %v", got, err)
			}
		})
	}
}

func TestAttachmentEvictionRollsBackFailedInsert(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	fixtures := []struct {
		id   string
		data []byte
	}{
		{id: "oldest", data: bytes.Repeat([]byte{1}, 30<<20)},
		{id: "second", data: bytes.Repeat([]byte{2}, 32<<20)},
		{id: "third", data: bytes.Repeat([]byte{3}, 32<<20)},
		{id: "fourth", data: bytes.Repeat([]byte{4}, 32<<20)},
	}
	for _, fixture := range fixtures {
		if err := s.SaveAttachment(ctx, "a@example.com", "thread", "message", fixture.id, fixture.data); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Execute(ctx, `CREATE TRIGGER reject_attachment BEFORE INSERT ON attachments
		WHEN NEW.attachment_id = 'reject' BEGIN SELECT RAISE(ABORT, 'reject attachment'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAttachment(ctx, "a@example.com", "thread", "message", "reject", bytes.Repeat([]byte{5}, 16<<20)); err == nil {
		t.Fatal("failing attachment insert succeeded")
	}
	for _, fixture := range fixtures {
		requireAttachmentBytes(t, s, "a@example.com", fixture.id, fixture.data)
	}
}

func TestAttachmentCacheIsConcurrentAndBounded(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	start := make(chan struct{})
	errs := make(chan error, 6)
	var writers sync.WaitGroup
	for i := range 6 {
		writers.Go(func() {
			<-start
			errs <- s.SaveAttachment(ctx, "a@example.com", "thread", "message", fmt.Sprintf("attachment-%d", i), bytes.Repeat([]byte{byte(i + 1)}, 30<<20))
		})
	}
	close(start)
	writers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.db.Query(ctx, "SELECT COUNT(*), COALESCE(SUM(length(data)), 0) FROM attachments WHERE account_email = ?", "a@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Values) != 1 || len(rows.Values[0]) != 2 || rows.Values[0][0] != int64(4) || rows.Values[0][1] != int64(120<<20) {
		t.Fatalf("concurrent attachment cache = %v, want four attachments totaling 120 MiB", rows.Values)
	}
}

func TestAttachmentCacheBoundariesAndAccountIsolation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	s := openTestStore(t, path)
	first := bytes.Repeat([]byte{1}, 30<<20)
	second := bytes.Repeat([]byte{2}, 30<<20)
	other := bytes.Repeat([]byte{3}, 32<<20)
	exact := bytes.Repeat([]byte{4}, 32<<20)
	additional := bytes.Repeat([]byte{5}, 32<<20)
	last := bytes.Repeat([]byte{6}, 16<<20)
	if err := s.SaveAttachment(ctx, "a@example.com", "thread", "message", "first", first); err != nil {
		t.Fatal(err)
	}
	for id, data := range map[string][]byte{"second": second, "exact": exact, "additional": additional} {
		if err := s.SaveAttachment(ctx, "a@example.com", "thread", "message", id, data); err != nil {
			t.Fatal(err)
		}
	}
	otherAttachments := map[string][]byte{
		"other":        other,
		"other-second": bytes.Repeat([]byte{7}, 32<<20),
		"other-third":  bytes.Repeat([]byte{8}, 32<<20),
		"other-fourth": bytes.Repeat([]byte{9}, 32<<20),
	}
	for id, data := range otherAttachments {
		if err := s.SaveAttachment(ctx, "b@example.com", "thread", "message", id, data); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveAttachment(ctx, "a@example.com", "thread", "message", "last", last); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.Attachment(ctx, "a@example.com", "thread", "message", "first"); found {
		t.Fatal("per-account quota was not enforced")
	}
	for id, want := range map[string][]byte{"second": second, "exact": exact, "additional": additional, "last": last} {
		requireAttachmentBytes(t, s, "a@example.com", id, want)
	}
	for id, want := range otherAttachments {
		requireAttachmentBytes(t, s, "b@example.com", id, want)
	}
	replacement := []byte{9, 8, 7}
	if err := s.SaveAttachment(ctx, "a@example.com", "thread", "message", "exact", replacement); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, path)
	requireAttachmentBytes(t, s, "a@example.com", "exact", replacement)
	for id, want := range otherAttachments {
		requireAttachmentBytes(t, s, "b@example.com", id, want)
	}
	for _, email := range []string{"a@example.com", "b@example.com"} {
		if err := s.SaveAccount(ctx, mail.Account{Email: email}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteAccount(ctx, "a@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.Attachment(ctx, "a@example.com", "thread", "message", "exact"); found {
		t.Fatal("deleted account retained attachment bytes")
	}
	for id, want := range otherAttachments {
		requireAttachmentBytes(t, s, "b@example.com", id, want)
	}
}

func requireAttachmentBytes(t *testing.T, s *Store, email, id string, want []byte) {
	t.Helper()
	got, found, err := s.Attachment(context.Background(), email, "thread", "message", id)
	if err != nil || !found || !bytes.Equal(got, want) {
		rows, queryErr := s.db.Query(context.Background(), "SELECT account_email, attachment_id, length(data), updated FROM attachments ORDER BY account_email, attachment_id")
		t.Logf("cache rows=%v query err=%v", rows.Values, queryErr)
		t.Fatalf("attachment %s/%s: found=%v bytes match=%v err=%v", email, id, found, bytes.Equal(got, want), err)
	}
}

func TestCacheRevisionSurvivesRestartAndConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	s := openTestStore(t, path)
	if err := s.SaveThreadList(ctx, "a@example.com", mail.Query{}, []mail.ThreadSummary{{ID: "t1", Subject: "one"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveThread(ctx, "a@example.com", mail.Thread{ID: "t1", Subject: "two"}); err != nil {
		t.Fatal(err)
	}
	requireCacheRevision(t, s, 2)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, path)
	if err := s.SaveThread(ctx, "a@example.com", mail.Thread{ID: "t1", Subject: "three"}); err != nil {
		t.Fatal(err)
	}
	requireCacheRevision(t, s, 3)
	var writers sync.WaitGroup
	errs := make(chan error, 4)
	for i := range 4 {
		writers.Go(func() {
			errs <- s.SaveThread(ctx, "a@example.com", mail.Thread{ID: fmt.Sprintf("concurrent-%d", i)})
		})
	}
	writers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	requireCacheRevision(t, s, 7)
	rows, err := s.db.Query(ctx, "SELECT COUNT(DISTINCT updated) FROM threads WHERE thread_id LIKE 'concurrent-%'")
	if err != nil || len(rows.Values) != 1 || rows.Values[0][0] != int64(4) {
		t.Fatalf("concurrent revisions = %v, %v", rows.Values, err)
	}
	got, err := s.SearchThreads(ctx, "a@example.com", mail.Query{Search: "three"})
	if err != nil || len(got) != 1 || got[0].Subject != "three" {
		t.Fatalf("latest thread after restart = %+v, %v", got, err)
	}
}

func requireCacheRevision(t *testing.T, s *Store, want int64) {
	t.Helper()
	rows, err := s.db.Query(context.Background(), "SELECT value FROM cache_revision WHERE id = 1")
	if err != nil || len(rows.Values) != 1 || rows.Values[0][0] != want {
		t.Fatalf("cache revision = %v, want %d, err=%v", rows.Values, want, err)
	}
}

func TestSearchThreadsLegacyDetailPrecedenceAndDeterministicLists(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	for _, item := range []struct{ key, data string }{
		{key: "a", data: `[{"ID":"t1","Subject":"first","LabelIDs":["INBOX"]}]`},
		{key: "z", data: `[{"ID":"t1","Subject":"second","LabelIDs":["INBOX"]}]`},
	} {
		if _, err := s.db.Execute(ctx, "INSERT INTO thread_lists(account_email, query_key, data, updated) VALUES(?, ?, ?, 0)", "a@example.com", item.key, item.data); err != nil {
			t.Fatal(err)
		}
	}
	// Revision-zero lists have no recoverable write order; the query key only makes the fallback deterministic.
	got, err := s.SearchThreads(ctx, "a@example.com", mail.Query{})
	if err != nil || len(got) != 1 || got[0].Subject != "second" {
		t.Fatalf("deterministic legacy fallback = %+v, %v", got, err)
	}
	detail := `{"ID":"t1","Subject":"detail","LabelIDs":["STARRED"],"Messages":[]}`
	if _, err := s.db.Execute(ctx, "INSERT INTO threads(account_email, thread_id, data, updated) VALUES(?, ?, ?, 0)", "a@example.com", "t1", detail); err != nil {
		t.Fatal(err)
	}
	got, err = s.SearchThreads(ctx, "a@example.com", mail.Query{LabelID: mail.Starred})
	if err != nil || len(got) != 1 || got[0].Subject != "detail" {
		t.Fatalf("legacy detail did not take precedence = %+v, %v", got, err)
	}
}
