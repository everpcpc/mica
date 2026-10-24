package app

import (
	"context"
	"testing"

	"mica/internal/mail"
)

type ownerThreadHarness struct {
	app   *App
	a     *fakeService
	b     *fakeService
	store *fakeMailStore
}

func newOwnerThreadHarness() *ownerThreadHarness {
	a := newFakeService()
	b := newFakeService()
	h := &ownerThreadHarness{a: a, b: b}
	app := New(a)
	app.storeAccount = "a@example.com"
	app.account = mail.Account{Email: "a@example.com"}
	app.accountVerified = true
	app.compositeThreads = true
	app.restoreAttempted = true
	app.schedule = func(fn func()) { fn() }
	app.registerAccountClient(b, mail.Account{Email: "b@example.com"}, nil, true)
	h.store = newFakeMailStore()
	app.SetMailStore(h.store)
	for _, service := range []*fakeService{a, b} {
		service.threadFunc = func(id string) (mail.Thread, error) {
			owner := ""
			if service == b {
				owner = "b@example.com"
			} else {
				owner = "a@example.com"
			}
			return mail.Thread{ID: id, Messages: []mail.Message{{
				ID: "message-shared", ThreadID: id, Subject: owner, Text: owner + " body",
			}}}, nil
		}
	}
	h.app = app
	return h
}

func (h *ownerThreadHarness) selectAccount(email string) {
	h.app.threads = []mail.ThreadSummary{
		{ID: "shared", AccountEmail: "a@example.com"},
		{ID: "shared", AccountEmail: "b@example.com"},
	}
	h.app.selectThread(threadRefForAccount(email, "shared").key())
}

func TestOwnerThreadReadAndMutationIsolation(t *testing.T) {
	h := newOwnerThreadHarness()
	h.selectAccount("b@example.com")
	if h.app.thread.AccountEmail != "b@example.com" || len(h.app.thread.Messages) != 1 || h.app.thread.Messages[0].Text != "b@example.com body" {
		t.Fatalf("B thread was not loaded by B: %+v", h.app.thread)
	}
	if h.app.thread.Messages[0].AccountEmail != "b@example.com" {
		t.Fatal("message owner was not backfilled")
	}
	if got := h.store.threads[cacheThreadKey{email: "b@example.com", id: "shared"}]; got.AccountEmail != "b@example.com" {
		t.Fatalf("B cache write used the wrong account: %+v", got)
	}
	h.selectAccount("a@example.com")
	if h.app.thread.Messages[0].Text != "a@example.com body" {
		t.Fatalf("A thread was not loaded by A: %+v", h.app.thread)
	}
	h.selectAccount("b@example.com")
	if h.app.thread.Messages[0].Text != "b@example.com body" || len(h.app.threadCache) != 2 {
		t.Fatalf("A-B-A cache was confused: thread=%+v cache=%d", h.app.thread, len(h.app.threadCache))
	}
	h.app.toggleStar()
	if len(h.a.modified) != 0 || len(h.b.modified) != 1 || h.b.modified[0] != "shared" {
		t.Fatalf("star mutation used the wrong service: A=%v B=%v", h.a.modified, h.b.modified)
	}
	h.app.undoLast()
	if len(h.a.modified) != 0 || len(h.b.modified) != 2 {
		t.Fatalf("undo used the wrong service: A=%v B=%v", h.a.modified, h.b.modified)
	}
}

func TestOwnerReplacementRejectsStaleThreadResult(t *testing.T) {
	h := newOwnerThreadHarness()
	var jobs, updates []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	h.app.update = func(fn func()) { updates = append(updates, fn) }
	h.selectAccount("b@example.com")
	if len(jobs) != 1 {
		t.Fatalf("thread jobs = %d, want 1", len(jobs))
	}
	replacement := newFakeService()
	h.app.registerAccountClient(replacement, mail.Account{Email: "b@example.com"}, nil, true)
	jobs[0]()
	for _, update := range updates {
		update()
	}
	if h.app.thread.ID != "" || h.app.selectedThread != threadRefForAccount("b@example.com", "shared").key() {
		t.Fatalf("stale B result was installed: thread=%+v selected=%q", h.app.thread, h.app.selectedThread)
	}
	if len(h.store.threads) != 0 {
		t.Fatal("stale B result was persisted")
	}
}

func TestOwnerCacheReadUsesSnapshotAccount(t *testing.T) {
	h := newOwnerThreadHarness()
	h.b.threadFunc = func(string) (mail.Thread, error) { return mail.Thread{}, errOffline }
	h.store.threads[cacheThreadKey{email: "b@example.com", id: "shared"}] = mail.Thread{
		ID: "shared", Messages: []mail.Message{{ID: "message-shared", ThreadID: "shared", Text: "cached B"}},
	}
	h.selectAccount("b@example.com")
	if h.app.thread.AccountEmail != "b@example.com" || h.app.thread.Messages[0].AccountEmail != "b@example.com" || h.app.thread.Messages[0].Text != "cached B" {
		t.Fatalf("cached B owner was not restored: %+v", h.app.thread)
	}
}

func TestOwnerCacheWriteStopsAfterReplacement(t *testing.T) {
	h := newOwnerThreadHarness()
	var jobs []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	h.app.persistThread(mail.Thread{ID: "shared", AccountEmail: "b@example.com"})
	if len(jobs) != 1 {
		t.Fatalf("cache jobs = %d, want 1", len(jobs))
	}
	h.app.registerAccountClient(newFakeService(), mail.Account{Email: "b@example.com"}, nil, true)
	jobs[0]()
	if len(h.store.threads) != 0 {
		t.Fatal("replaced account accepted an old cache write")
	}
}

func TestOwnerThreadRefsDoNotChangeLegacySelection(t *testing.T) {
	h := newOwnerThreadHarness()
	h.app.compositeThreads = false
	h.app.threads = []mail.ThreadSummary{{ID: "shared"}}
	h.app.selectThread("shared")
	if h.app.selectedThread != "shared" {
		t.Fatalf("legacy selection changed to %q", h.app.selectedThread)
	}
	if h.app.thread.Messages[0].Text != "a@example.com body" {
		t.Fatalf("legacy thread was not restored: %+v", h.app.thread)
	}
}

func TestOwnerCacheReadDoesNotUseActiveAccount(t *testing.T) {
	h := newOwnerThreadHarness()
	h.b.threadFunc = func(string) (mail.Thread, error) { return mail.Thread{}, errOffline }
	h.store.threads[cacheThreadKey{email: "a@example.com", id: "shared"}] = mail.Thread{
		ID: "shared", Messages: []mail.Message{{ID: "message-shared", ThreadID: "shared", Text: "wrong A"}},
	}
	h.store.threads[cacheThreadKey{email: "b@example.com", id: "shared"}] = mail.Thread{
		ID: "shared", Messages: []mail.Message{{ID: "message-shared", ThreadID: "shared", Text: "right B"}},
	}
	h.selectAccount("b@example.com")
	if h.app.thread.Messages[0].Text != "right B" {
		t.Fatalf("B cache read used A: %+v", h.app.thread)
	}
}

func TestOwnerThreadMutationUsesRawGmailID(t *testing.T) {
	h := newOwnerThreadHarness()
	h.selectAccount("b@example.com")
	h.app.trash()
	if h.b.trashed != "shared" || h.a.trashed != "" {
		t.Fatalf("trash used the wrong account or composite ID: A=%q B=%q", h.a.trashed, h.b.trashed)
	}
	h.app.undoLast()
	if h.b.untrashed != "shared" || h.a.untrashed != "" {
		t.Fatalf("untrash used the wrong account or composite ID: A=%q B=%q", h.a.untrashed, h.b.untrashed)
	}
}

func TestOwnerCacheThreadHelperPreservesSnapshot(t *testing.T) {
	h := newOwnerThreadHarness()
	h.app.persistThread(mail.Thread{ID: "shared", AccountEmail: "b@example.com", Messages: []mail.Message{{ID: "message-shared", AccountEmail: "b@example.com"}}})
	got, found, err := h.store.Thread(context.Background(), "b@example.com", "shared")
	if err != nil || !found || got.AccountEmail != "b@example.com" || got.Messages[0].AccountEmail != "b@example.com" {
		t.Fatalf("persisted thread = %+v, found=%v, err=%v", got, found, err)
	}
}
