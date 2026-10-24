package app

import (
	"context"
	"testing"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

func newOutboxViewHarness(t *testing.T) (*App, *outboxOrderService, *fakeMailStore, mail.OutboxMessage) {
	t.Helper()
	a, service, store := newOutboxOrderHarness()
	service.sendErr = errOffline
	openOutboxTestCompose(a)
	a.sendCompose()
	a.compose = nil
	record := onlyOutboxRecord(t, store)
	a.selectedLabel = mail.Outbox
	a.selectLabel()
	a.selectThread("outbox-" + record.ID)
	return a, service, store, record
}

func TestOutboxRenderedListPreviewAndRetry(t *testing.T) {
	a, service, store, _ := newOutboxViewHarness(t)
	tt := ui.NewTester(a.View, 1280, 800)
	tt.SetLocale("en")
	for _, text := range []string{"Outbox", "Outgoing", "Needs review", "body", "Retry", "Delete"} {
		if !tt.HasText(text) {
			t.Fatalf("Outbox is missing %q: %q", text, tt.Texts())
		}
	}
	if service.listCalls != 0 {
		t.Fatalf("Outbox used Gmail thread listing: %d", service.listCalls)
	}
	a.archive()
	a.openReply()
	a.openForward()
	if len(store.outbox) != 1 || a.compose != nil {
		t.Fatal("ordinary thread actions affected the Outbox")
	}
	service.sendErr = nil
	if err := tt.Click("Retry"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Check before sending again") {
		t.Fatalf("empty Sent result did not show the review dialog: %q", tt.Texts())
	}
	if err := tt.Click("Send again"); err != nil {
		t.Fatal(err)
	}
	if len(store.outbox) != 0 || !tt.HasText("No outgoing mail") {
		t.Fatalf("successful Outbox retry did not clear the view: records=%+v texts=%q", store.outbox, tt.Texts())
	}
}

func TestOutboxDeleteRequiresConfirmation(t *testing.T) {
	a, _, store, _ := newOutboxViewHarness(t)
	tt := ui.NewTester(a.View, 1280, 800)
	tt.SetLocale("en")
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Delete outgoing message?") || !tt.HasText("Delete permanently") {
		t.Fatalf("delete confirmation is missing: %q", tt.Texts())
	}
	if err := tt.Click("Delete permanently"); err != nil {
		t.Fatal(err)
	}
	if len(store.outbox) != 0 || a.outboxDeleteOpen || !tt.HasText("No outgoing mail") {
		t.Fatalf("confirmed deletion did not clear Outbox: records=%+v open=%v texts=%q", store.outbox, a.outboxDeleteOpen, tt.Texts())
	}
	if _, found := store.drafts[a.storeAccount]; found {
		t.Fatal("confirmed deletion retained the linked snapshot")
	}
}

func TestOutboxRecoveredSendingBecomesUncertain(t *testing.T) {
	a, _, store, record := newOutboxViewHarness(t)
	record.State = mail.OutboxSending
	if err := store.SaveOutboxMessage(context.Background(), record.AccountEmail, record); err != nil {
		t.Fatal(err)
	}
	a.refreshOutbox()
	if got := onlyOutboxRecord(t, store); got.State != mail.OutboxUncertain {
		t.Fatalf("stale sending state = %q", got.State)
	}
	tt := ui.NewTester(a.View, 1280, 800)
	tt.SetLocale("en")
	if !tt.HasText("Needs review") {
		t.Fatalf("stale sending state is not visible: %q", tt.Texts())
	}
}

func TestOutboxViewTransitionsDoNotRetainOtherRows(t *testing.T) {
	t.Run("enter with unavailable store", func(t *testing.T) {
		a, _, _ := newOutboxOrderHarness()
		a.threads = []mail.ThreadSummary{{ID: "gmail-thread"}}
		a.selectedLabel = mail.Outbox
		a.outbox = nil
		a.selectLabel()
		if len(a.threads) != 0 || a.err == "" {
			t.Fatalf("unavailable Outbox retained Gmail rows: threads=%+v err=%q", a.threads, a.err)
		}
	})
	t.Run("leave for inbox", func(t *testing.T) {
		a, _, _, record := newOutboxViewHarness(t)
		a.selectedLabel = mail.Inbox
		a.selectLabel()
		for _, thread := range a.threads {
			if a.outboxRecords != nil || thread.ID == "outbox-"+record.ID {
				t.Fatalf("Inbox retained Outbox rows: records=%+v threads=%+v", a.outboxRecords, a.threads)
			}
		}
	})
}

func TestOutboxRetryRequiresVerifiedOwner(t *testing.T) {
	a, service, store, _ := newOutboxViewHarness(t)
	a.accountVerified = false
	a.retryOutbox()
	if service.sendCalls != 1 || len(store.outbox) != 1 || a.notice == "" {
		t.Fatalf("unverified account retried Outbox: calls=%d records=%+v notice=%q", service.sendCalls, store.outbox, a.notice)
	}
}
