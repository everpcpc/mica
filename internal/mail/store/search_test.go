package store

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"mica/internal/mail"
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
	if err := s.SaveAttachment(ctx, "a@example.com", "thread", "message", "oversize", make([]byte, maxAttachmentBytes+1)); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.Attachment(ctx, "a@example.com", "thread", "message", "oversize"); found {
		t.Fatal("oversize attachment was cached")
	}
}
