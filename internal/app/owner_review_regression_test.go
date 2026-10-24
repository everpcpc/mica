package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"mica/internal/mail"
)

func TestLoadThreadRestoresMissingAccount(t *testing.T) {
	h := newOwnerThreadHarness()
	h.b.account = mail.Account{Email: "b@example.com"}
	h.app.removeAccountClient("b@example.com")
	calls := 0
	h.app.accountService = func(context.Context, string) (mail.Service, error) {
		calls++
		return h.b, nil
	}
	h.selectAccount("b@example.com")
	if calls != 1 || h.app.thread.AccountEmail != "b@example.com" || h.app.thread.Messages[0].Text != "b@example.com body" {
		t.Fatalf("missing account was not restored: calls=%d thread=%+v", calls, h.app.thread)
	}
	if h.app.service != h.a || !h.app.ownerVerified("b@example.com") {
		t.Fatal("auxiliary restore changed the active account or verification")
	}
}

func TestAccountRegistryRemoveReaddRejectsOldGeneration(t *testing.T) {
	a := New(newFakeService())
	account := mail.Account{Email: "b@example.com"}
	a.registerAccountClient(newFakeService(), account, nil, true)
	_, oldGeneration := a.ownerService("b@example.com")
	a.removeAccountClient("b@example.com")
	a.registerAccountClient(newFakeService(), account, nil, true)
	_, newGeneration := a.ownerService("b@example.com")
	if newGeneration <= oldGeneration || a.ownerOperationCurrent("b@example.com", oldGeneration) {
		t.Fatalf("remove/re-add accepted old generation: old=%d new=%d", oldGeneration, newGeneration)
	}
	if !a.ownerOperationCurrent("b@example.com", newGeneration) {
		t.Fatal("new account generation was not accepted")
	}
}

func TestLoadThreadCacheGuardRejectsChangedActiveAccount(t *testing.T) {
	h := newOwnerThreadHarness()
	h.store.threads[cacheThreadKey{email: "a@example.com", id: "shared"}] = mail.Thread{
		ID: "shared", Messages: []mail.Message{{ID: "message-shared", ThreadID: "shared", Text: "old A"}},
	}
	var jobs, updates []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	h.app.update = func(fn func()) { updates = append(updates, fn) }
	h.app.compositeThreads = false
	h.app.threads = []mail.ThreadSummary{{ID: "shared"}}
	h.app.selectThread("shared")
	h.app.storeAccount = "b@example.com"
	jobs[0]()
	for _, update := range updates {
		update()
	}
	if h.app.thread.ID != "" {
		t.Fatalf("changed active account accepted an old cache snapshot: %+v", h.app.thread)
	}
}

func TestKeyboardOpenSelectedUsesOwnerKey(t *testing.T) {
	h := newOwnerThreadHarness()
	h.app.threads = []mail.ThreadSummary{
		{ID: "shared", AccountEmail: "a@example.com"},
		{ID: "shared", AccountEmail: "b@example.com"},
	}
	h.app.selectedRow = 1
	h.app.openSelected()
	if h.app.selectedThread != threadRefForAccount("b@example.com", "shared").key() || h.app.thread.Messages[0].Text != "b@example.com body" {
		t.Fatalf("keyboard opened the wrong account: selected=%q thread=%+v", h.app.selectedThread, h.app.thread)
	}
}

func TestPersistListClearsMissingOwners(t *testing.T) {
	h := newOwnerThreadHarness()
	query := mail.Query{LabelID: mail.Inbox, Limit: 25}
	h.store.lists[cacheListKey{email: "a@example.com", query: query}] = []mail.ThreadSummary{{ID: "old-a"}}
	h.store.lists[cacheListKey{email: "b@example.com", query: query}] = []mail.ThreadSummary{{ID: "old-b"}}
	h.app.persistList(query, []mail.ThreadSummary{{ID: "new-a", AccountEmail: "a@example.com"}})
	for _, tc := range []struct {
		email string
		want  int
	}{
		{email: "a@example.com", want: 1},
		{email: "b@example.com", want: 0},
	} {
		got, found, err := h.store.ThreadList(context.Background(), tc.email, query)
		if err != nil || !found || len(got) != tc.want {
			t.Fatalf("list for %s = %+v, found=%v, err=%v; want %d", tc.email, got, found, err, tc.want)
		}
	}
	h.app.persistList(query, nil)
	got, found, err := h.store.ThreadList(context.Background(), "a@example.com", query)
	if err != nil || !found || len(got) != 0 {
		t.Fatalf("empty list was not persisted: %+v, found=%v, err=%v", got, found, err)
	}
}

func TestMutationCallbackRejectsReplacedOwner(t *testing.T) {
	h := newOwnerThreadHarness()
	var jobs, updates []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	h.app.update = func(fn func()) { updates = append(updates, fn) }
	h.app.threads = []mail.ThreadSummary{{ID: "shared", AccountEmail: "b@example.com", LabelIDs: []string{mail.Inbox}}}
	var doneErr error
	h.app.enqueueMutation(mutationJob{
		id:      threadRefForAccount("b@example.com", "shared").key(),
		runFunc: func() error { return errors.New("old failure") },
		done:    func(err error) { doneErr = err },
	})
	h.app.registerAccountClient(newFakeService(), mail.Account{Email: "b@example.com"}, nil, true)
	jobs[0]()
	for _, update := range updates {
		update()
	}
	if len(h.app.mutationQueue) != 0 || h.app.mutationRunning {
		t.Fatalf("stale mutation was not dequeued: queue=%d running=%v", len(h.app.mutationQueue), h.app.mutationRunning)
	}
	if h.app.mutationRefresh || h.app.mutationLabels || h.app.notice != "" {
		t.Fatalf("stale mutation changed view state: refresh=%v labels=%v notice=%q", h.app.mutationRefresh, h.app.mutationLabels, h.app.notice)
	}
	if !errors.Is(doneErr, errAccountClientUnavailable) {
		t.Fatalf("stale mutation completion error = %v", doneErr)
	}
	if len(h.app.threads[0].LabelIDs) != 1 || h.app.threads[0].LabelIDs[0] != mail.Inbox {
		t.Fatalf("stale failure changed labels: %+v", h.app.threads[0].LabelIDs)
	}
}

func TestRemoveAccountWaitsForPendingDraftWrite(t *testing.T) {
	h := newAccountTransitionHarness()
	a := h.app
	a.settings = &settingsState{open: true}
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.writeDraftSnapshot(h.old.account.Email, &mail.DraftSnapshot{Compose: mail.Compose{Text: "local only"}})
	if len(jobs) != 1 {
		t.Fatalf("draft jobs = %d, want 1", len(jobs))
	}
	removedCredentials := false
	a.removeCredentials = func(context.Context, string) error {
		removedCredentials = true
		return nil
	}
	a.deleteCheckpoint = func(string) error { return nil }
	a.removeAccount(h.old.account.Email)
	if len(jobs) != 2 {
		t.Fatalf("removal jobs = %d, want 2", len(jobs))
	}
	finished := make(chan struct{})
	go func() {
		jobs[1]()
		close(finished)
	}()
	select {
	case <-finished:
		t.Fatal("removal completed before the pending draft write")
	case <-time.After(50 * time.Millisecond):
	}
	jobs[0]()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("removal did not finish after the draft write")
	}
	h.runUpdate(t)
	if removedCredentials || a.settings.err == "" || a.accountTransition != nil {
		t.Fatalf("pending draft was not preserved: removed=%v settings=%+v transition=%p", removedCredentials, a.settings, a.accountTransition)
	}
}

func TestDemoActivationAdvancesGeneration(t *testing.T) {
	a := New(newFakeService())
	a.storeAccount = "a@example.com"
	a.nextAccountGeneration(a.storeAccount)
	_, oldGeneration := a.ownerService(a.storeAccount)
	a.schedule = func(fn func()) { fn() }
	a.restoreAttempted = true
	a.useService(mail.NewDemo())
	_, newGeneration := a.ownerService("")
	if newGeneration <= oldGeneration {
		t.Fatalf("Demo did not advance generation: old=%d new=%d", oldGeneration, newGeneration)
	}
}

func TestEnsureSuccessDoesNotReopenStaleThread(t *testing.T) {
	h := newOwnerThreadHarness()
	h.b.account = mail.Account{Email: "b@example.com"}
	h.app.removeAccountClient("b@example.com")
	var jobs, updates []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	h.app.update = func(fn func()) { updates = append(updates, fn) }
	h.app.accountService = func(context.Context, string) (mail.Service, error) { return h.b, nil }
	h.selectAccount("b@example.com")
	h.app.threadCall++
	h.app.selectedThread = "other"
	h.app.thread = mail.Thread{ID: "other"}
	jobs[0]()
	for _, update := range updates {
		update()
	}
	if h.app.selectedThread != "other" || h.app.thread.ID != "other" {
		t.Fatalf("stale restore reopened the old thread: selected=%q thread=%+v", h.app.selectedThread, h.app.thread)
	}
}
