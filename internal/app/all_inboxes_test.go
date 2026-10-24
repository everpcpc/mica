package app

import (
	"context"
	"errors"
	"testing"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

type nonPushMailService struct {
	mail.Service
	unified *unifiedFakeService
}

func (s nonPushMailService) ListThreadPage(ctx context.Context, query mail.Query) (mail.ThreadPage, error) {
	return s.unified.ListThreadPage(ctx, query)
}

func newAllInboxesHarness(t *testing.T) *unifiedHarness {
	t.Helper()
	h := newUnifiedHarness()
	h.app.service = nonPushMailService{Service: h.services["a@example.com"], unified: h.services["a@example.com"]}
	h.app.accountService = func(_ context.Context, email string) (mail.Service, error) {
		return nonPushMailService{Service: h.services[email], unified: h.services[email]}, nil
	}
	for _, account := range []mail.Account{
		{Name: "Personal", Email: "a@example.com"},
		{Name: "Work", Email: "b@example.com"},
	} {
		if err := h.store.SaveAccount(context.Background(), account); err != nil {
			t.Fatal(err)
		}
		h.setPages(account.Email, mail.ThreadPage{Threads: []mail.ThreadSummary{
			unifiedInboxSummary(account.Email[:1]+"-1", account.Email, 10),
		}})
	}
	h.app.refreshUnified()
	return h
}

func TestAllInboxesUsesAppleStyleGrouping(t *testing.T) {
	h := newAllInboxesHarness(t)
	tt := ui.NewTester(h.app.View, 1280, 800)
	tt.SetLocale("en")
	if !tt.HasText("All Inboxes") || !tt.HasText("Personal") || !tt.HasText("Work") {
		t.Fatalf("All Inboxes accounts are missing: %q", tt.Texts())
	}
	if tt.HasText("Unified Inbox") {
		t.Fatal("old Unified Inbox label is still visible")
	}
	parent, parentFound := tt.Find("All Inboxes")
	child, childFound := tt.Find("Work")
	if !parentFound || !childFound || child.X < parent.X {
		t.Fatalf("account row is not indented under All Inboxes: parent=%+v child=%+v", parent, child)
	}
}

func TestAllInboxesCollapseAndExpand(t *testing.T) {
	h := newAllInboxesHarness(t)
	tt := ui.NewTester(h.app.View, 1280, 800)
	tt.SetLocale("en")
	if err := tt.Click("Show or hide accounts"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("Personal") || tt.HasText("Work") {
		t.Fatal("collapsed account list is still visible")
	}
	if !tt.HasText("All Inboxes") {
		t.Fatal("collapsing hid the All Inboxes entry")
	}
	if err := tt.Click("Show or hide accounts"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Personal") || !tt.HasText("Work") {
		t.Fatal("expanding did not restore account rows")
	}
}

func TestAccountInboxSwitchesActiveAccount(t *testing.T) {
	h := newAllInboxesHarness(t)
	h.app.saveActiveAccount = func(string) error { return nil }
	tt := ui.NewTester(h.app.View, 1280, 800)
	tt.SetLocale("en")
	if err := tt.Click("Work"); err != nil {
		t.Fatal(err)
	}
	service, ok := h.app.service.(nonPushMailService)
	if h.app.storeAccount != "b@example.com" || !ok || service.Service != h.services["b@example.com"] {
		t.Fatalf("account child did not switch the active account: store=%q service=%p", h.app.storeAccount, h.app.service)
	}
	if h.app.selectedLabel != "account-inbox-b@example.com" {
		t.Fatalf("account child selection was lost after activation: %q", h.app.selectedLabel)
	}
	if h.app.compositeThreads || h.app.currentQuery().LabelID != mail.Inbox || !h.app.inboxView() {
		t.Fatalf("account child did not use ordinary Inbox behavior: label=%q query=%+v", h.app.selectedLabel, h.app.currentQuery())
	}
	tt.Frame()
	if !tt.HasText("Work") || !tt.HasText("All Inboxes") {
		t.Fatal("account activation removed the All Inboxes group")
	}
}

func TestAccountInboxRemovalReturnsToInbox(t *testing.T) {
	h := newAllInboxesHarness(t)
	h.app.selectedLabel = "account-inbox-b@example.com"
	h.app.recordUndo("b-1", nil, []string{mail.Inbox})
	for _, email := range []string{"a@example.com", "b@example.com"} {
		delete(h.store.lists, cacheListKey{email: email, query: h.query})
	}
	calls := len(h.services["a@example.com"].listCalls)
	h.app.refreshUnifiedAfterAccountRemoval("b@example.com")
	if h.app.selectedLabel != mail.Inbox || h.app.currentQuery().LabelID != mail.Inbox || h.app.compositeThreads || h.app.unified != nil || h.app.undo != nil {
		t.Fatalf("removed account child did not fully leave Unified: label=%q composite=%v unified=%p undo=%p", h.app.selectedLabel, h.app.compositeThreads, h.app.unified, h.app.undo)
	}
	if len(h.services["a@example.com"].listCalls) != calls+1 || len(h.app.threads) != 1 || h.app.threads[0].ID != "a-1" {
		t.Fatalf("fallback did not refresh the active account: calls=%+v threads=%+v", h.services["a@example.com"].listCalls, h.app.threads)
	}
	cached, found := h.store.lists[cacheListKey{email: "a@example.com", query: h.query}]
	_, removedAccountCached := h.store.lists[cacheListKey{email: "b@example.com", query: h.query}]
	if !found || len(cached) != 1 || cached[0].ID != "a-1" || removedAccountCached {
		t.Fatalf("fallback persisted the wrong view: cached=%+v found=%v removed=%v", cached, found, removedAccountCached)
	}
	h.app.selectThread("a-1")
	if h.app.selectedThread != "a-1" {
		t.Fatalf("ordinary Inbox retained a composite selection key: %q", h.app.selectedThread)
	}
}

func TestAccountInboxSwitchFailureRestoresCurrentAccount(t *testing.T) {
	h := newAllInboxesHarness(t)
	h.app.saveActiveAccount = func(string) error { return errors.New("keychain unavailable") }
	tt := ui.NewTester(h.app.View, 1280, 800)
	tt.SetLocale("en")
	if err := tt.Click("Work"); err != nil {
		t.Fatal(err)
	}
	if h.app.storeAccount != "a@example.com" {
		t.Fatalf("failed activation changed the current account: %q", h.app.storeAccount)
	}
	if h.app.selectedLabel != mail.Inbox || h.app.compositeThreads || h.app.currentQuery().LabelID != mail.Inbox {
		t.Fatalf("failed activation left a mismatched selection: label=%q composite=%v", h.app.selectedLabel, h.app.compositeThreads)
	}
	if len(h.app.threads) != 1 || h.app.threads[0].AccountEmail != "a@example.com" {
		t.Fatalf("failed activation did not restore the current account's Inbox: %+v", h.app.threads)
	}
}

func runAllInboxesCallbacks(t *testing.T, jobs, updates *[]func()) {
	t.Helper()
	for steps := 0; len(*jobs)+len(*updates) > 0; steps++ {
		if steps >= 60 {
			t.Fatal("account callbacks did not finish")
		}
		if len(*updates) > 0 {
			update := (*updates)[0]
			*updates = (*updates)[1:]
			update()
			continue
		}
		job := (*jobs)[0]
		*jobs = (*jobs)[1:]
		job()
	}
}

func TestAccountInboxTransitionClearsMergedViewAndKeepsRequest(t *testing.T) {
	h := newAllInboxesHarness(t)
	h.app.saveActiveAccount = func(string) error { return nil }
	h.app.recordUndo("a-1", nil, []string{mail.Inbox})
	var jobs, updates []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	h.app.update = func(fn func()) { updates = append(updates, fn) }
	tt := ui.NewTester(h.app.View, 1280, 800)
	tt.SetLocale("en")
	if err := tt.Click("Work"); err != nil {
		t.Fatal(err)
	}
	if h.app.compositeThreads || h.app.unified != nil || len(h.app.threads) != 0 || !h.app.loading || h.app.undo != nil {
		t.Fatalf("pending activation retained the merged view: composite=%v unified=%p threads=%d loading=%v undo=%p", h.app.compositeThreads, h.app.unified, len(h.app.threads), h.app.loading, h.app.undo)
	}
	jobsBefore := len(jobs)
	if err := tt.Click("All Inboxes"); err != nil {
		t.Fatal(err)
	}
	if h.app.selectedLabel != "account-inbox-b@example.com" || len(jobs) != jobsBefore {
		t.Fatalf("an intermediate selection replaced the requested account: label=%q jobs=%d -> %d", h.app.selectedLabel, jobsBefore, len(jobs))
	}
	runAllInboxesCallbacks(t, &jobs, &updates)
	if h.app.accountTransition != nil || h.app.storeAccount != "b@example.com" || h.app.selectedLabel != "account-inbox-b@example.com" {
		t.Fatalf("request was not preserved through activation: transition=%p account=%q label=%q", h.app.accountTransition, h.app.storeAccount, h.app.selectedLabel)
	}
}

func TestAccountInboxRestoreFailureRestoresCurrentAccount(t *testing.T) {
	h := newAllInboxesHarness(t)
	h.app.removeAccountClient("b@example.com")
	h.app.accountService = func(context.Context, string) (mail.Service, error) { return nil, errOffline }
	var jobs, updates []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	h.app.update = func(fn func()) { updates = append(updates, fn) }
	tt := ui.NewTester(h.app.View, 1280, 800)
	tt.SetLocale("en")
	if err := tt.Click("Work"); err != nil {
		t.Fatal(err)
	}
	runAllInboxesCallbacks(t, &jobs, &updates)
	if h.app.storeAccount != "a@example.com" || h.app.selectedLabel != mail.Inbox || h.app.compositeThreads || h.app.unified != nil {
		t.Fatalf("restore failure left stale navigation: account=%q label=%q composite=%v unified=%p", h.app.storeAccount, h.app.selectedLabel, h.app.compositeThreads, h.app.unified)
	}
	if len(h.app.threads) != 1 || h.app.threads[0].AccountEmail != "a@example.com" {
		t.Fatalf("restore failure did not restore the current Inbox: %+v", h.app.threads)
	}
}

func TestAccountInboxAuthenticationSuccessKeepsRequest(t *testing.T) {
	h := newAllInboxesHarness(t)
	h.app.saveActiveAccount = func(string) error { return nil }
	var jobs []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	tt := ui.NewTester(h.app.View, 1280, 800)
	tt.SetLocale("en")
	if err := tt.Click("Work"); err != nil {
		t.Fatal(err)
	}
	h.app.accountTransition = nil
	h.app.authCancel = func() {}
	if err := tt.Click("All Inboxes"); err != nil {
		t.Fatal(err)
	}
	if h.app.selectedLabel != "account-inbox-b@example.com" {
		t.Fatalf("authentication changed the requested child: %q", h.app.selectedLabel)
	}
	h.app.schedule = func(fn func()) { fn() }
	h.app.transitionToService(nonPushMailService{Service: h.services["b@example.com"], unified: h.services["b@example.com"]}, "b@example.com", false, "")
	if h.app.storeAccount != "b@example.com" || h.app.selectedLabel != "account-inbox-b@example.com" {
		t.Fatalf("authentication success lost the requested child: account=%q label=%q", h.app.storeAccount, h.app.selectedLabel)
	}
}

func TestAccountInboxAuthenticationCancelRestoresCurrentAccount(t *testing.T) {
	h := newAllInboxesHarness(t)
	var jobs []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	tt := ui.NewTester(h.app.View, 1280, 800)
	tt.SetLocale("en")
	if err := tt.Click("Work"); err != nil {
		t.Fatal(err)
	}
	h.app.accountTransition = nil
	h.app.authCancel = func() {}
	h.app.schedule = func(fn func()) { fn() }
	h.app.accountAuthenticationFailed(nil, "b@example.com", context.Canceled)
	if h.app.selectedLabel != mail.Inbox || h.app.compositeThreads || h.app.unified != nil {
		t.Fatalf("authentication cancellation left stale navigation: label=%q composite=%v unified=%p", h.app.selectedLabel, h.app.compositeThreads, h.app.unified)
	}
	if len(h.app.threads) != 1 || h.app.threads[0].AccountEmail != "a@example.com" {
		t.Fatalf("authentication cancellation did not restore the current Inbox: %+v", h.app.threads)
	}
}

func TestAllInboxesKeyboardNavigation(t *testing.T) {
	h := newAllInboxesHarness(t)
	h.app.saveActiveAccount = func(string) error { return nil }
	tt := ui.NewTester(h.app.View, 1280, 800)
	tt.SetLocale("en")
	if err := tt.Click("All Inboxes"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyDown)
	if h.app.selectedLabel != "account-inbox-a@example.com" || h.app.compositeThreads {
		t.Fatalf("Down did not select the first account child: %q", h.app.selectedLabel)
	}
	tt.Key(0, ui.KeyDown)
	if h.app.storeAccount != "b@example.com" || h.app.selectedLabel != "account-inbox-b@example.com" {
		t.Fatalf("Down did not activate the next account: account=%q label=%q", h.app.storeAccount, h.app.selectedLabel)
	}
	if err := tt.Click("Show or hide accounts"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyEnter)
	if !h.app.unifiedExpanded || !tt.HasText("Work") {
		t.Fatal("Enter did not expand the account group")
	}
	if err := tt.Click("Show or hide accounts"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("All Inboxes"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyDown)
	if h.app.selectedLabel != mail.Inbox {
		t.Fatalf("Down selected a hidden account child: %q", h.app.selectedLabel)
	}
}
