package app

import (
	"context"
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
	calls := len(h.services["a@example.com"].listCalls)
	h.app.refreshUnifiedAfterAccountRemoval("b@example.com")
	if h.app.selectedLabel != mail.Inbox || h.app.currentQuery().LabelID != mail.Inbox {
		t.Fatalf("removed account child did not return to Inbox: %q", h.app.selectedLabel)
	}
	if len(h.services["a@example.com"].listCalls) != calls+1 || len(h.app.threads) != 1 || h.app.threads[0].ID != "a-1" {
		t.Fatalf("fallback did not refresh the active account: calls=%+v threads=%+v", h.services["a@example.com"].listCalls, h.app.threads)
	}
}
