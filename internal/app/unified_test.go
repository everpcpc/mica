package app

import (
	"testing"
	"time"

	"mica/internal/mail"
)

func unifiedSummary(id string, email string, updated int64) mail.ThreadSummary {
	return mail.ThreadSummary{ID: id, AccountEmail: email, Updated: time.Unix(updated, 0)}
}

func TestMergeUnifiedAccountsUsesIndependentCursors(t *testing.T) {
	a := newUnifiedAccountStream("a@example.com", []mail.ThreadSummary{unifiedSummary("a1", "a@example.com", 10), unifiedSummary("a2", "a@example.com", 7), unifiedSummary("a3", "a@example.com", 3)})
	b := newUnifiedAccountStream("b@example.com", []mail.ThreadSummary{unifiedSummary("b1", "b@example.com", 9), unifiedSummary("b2", "b@example.com", 8), unifiedSummary("b3", "b@example.com", 2)})
	c := newUnifiedAccountStream("c@example.com", []mail.ThreadSummary{unifiedSummary("c1", "c@example.com", 6), unifiedSummary("c2", "c@example.com", 5), unifiedSummary("c3", "c@example.com", 1)})
	streams := []*unifiedAccountStream{a, b, c}
	first := mergeUnifiedAccounts(streams, 5)
	if got := []string{first[0].ID, first[1].ID, first[2].ID, first[3].ID, first[4].ID}; !equalStrings(got, []string{"a1", "b1", "b2", "a2", "c1"}) {
		t.Fatalf("first merge = %v", got)
	}
	second := mergeUnifiedAccounts(streams, 4)
	if got := []string{second[0].ID, second[1].ID, second[2].ID, second[3].ID}; !equalStrings(got, []string{"c2", "a3", "b3", "c3"}) {
		t.Fatalf("second merge = %v", got)
	}
	if unifiedHasMore(streams) {
		t.Fatal("merge reported more after all streams were exhausted")
	}
}

func TestUnifiedPageOverlapDoesNotDuplicateOutput(t *testing.T) {
	stream := newUnifiedAccountStream("a@example.com", []mail.ThreadSummary{unifiedSummary("a1", "a@example.com", 10), unifiedSummary("a2", "a@example.com", 8)})
	streams := []*unifiedAccountStream{stream}
	if got := mergeUnifiedAccounts(streams, 2); len(got) != 2 {
		t.Fatalf("initial merge = %v", got)
	}
	stream.appendPage(mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedSummary("a2", "a@example.com", 8), unifiedSummary("a3", "a@example.com", 9), unifiedSummary("a4", "a@example.com", 4)}})
	got := mergeUnifiedAccounts(streams, 3)
	if len(got) != 2 || got[0].ID != "a3" || got[1].ID != "a4" {
		t.Fatalf("overlap merge = %+v", got)
	}
}

func TestUnifiedTieBreaksAreDeterministic(t *testing.T) {
	streams := []*unifiedAccountStream{
		newUnifiedAccountStream("b@example.com", []mail.ThreadSummary{unifiedSummary("b", "b@example.com", 5)}),
		newUnifiedAccountStream("a@example.com", []mail.ThreadSummary{unifiedSummary("a", "a@example.com", 5)}),
	}
	got := mergeUnifiedAccounts(streams, 2)
	if got[0].AccountEmail != "a@example.com" || got[1].AccountEmail != "b@example.com" {
		t.Fatalf("tie order = %+v", got)
	}
}

func TestUnifiedEmptyPageWithTokenHasMore(t *testing.T) {
	stream := newUnifiedAccountStream("a@example.com", nil)
	stream.nextPageToken = "next"
	if !unifiedHasMore([]*unifiedAccountStream{stream}) {
		t.Fatal("empty page with a token was treated as exhausted")
	}
	stream.nextPageToken = ""
	if unifiedHasMore([]*unifiedAccountStream{stream}) {
		t.Fatal("empty exhausted stream has more")
	}
}

func TestUnifiedErrorIsNotExhaustion(t *testing.T) {
	stream := newUnifiedAccountStream("a@example.com", nil)
	stream.err = errOffline
	if !unifiedHasMore([]*unifiedAccountStream{stream}) {
		t.Fatal("failed account was treated as exhausted")
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
