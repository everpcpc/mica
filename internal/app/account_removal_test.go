package app

import (
	"context"
	"testing"

	"mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo/ui"
)

func TestRemoveAccountWithLocalDraftChangesNothing(t *testing.T) {
	h := newAccountTransitionHarness()
	a := h.app
	a.settings = &settingsState{open: true}
	if err := h.store.SaveDraftSnapshot(context.Background(), h.old.account.Email, mail.DraftSnapshot{Compose: mail.Compose{Text: "local only"}}); err != nil {
		t.Fatal(err)
	}
	var removedCredentials, deletedCheckpoints []string
	a.removeCredentials = func(_ context.Context, email string) error {
		removedCredentials = append(removedCredentials, email)
		return nil
	}
	a.deleteCheckpoint = func(email string) error {
		deletedCheckpoints = append(deletedCheckpoints, email)
		return nil
	}
	a.removeAccount(h.old.account.Email)
	h.runJob(t)
	h.runUpdate(t)
	if len(removedCredentials) != 0 || len(deletedCheckpoints) != 0 {
		t.Fatalf("draft removal deleted credentials=%v checkpoints=%v", removedCredentials, deletedCheckpoints)
	}
	if _, found, err := h.store.Account(context.Background(), h.old.account.Email); err != nil || !found {
		t.Fatalf("account with a local draft was removed: found=%v err=%v", found, err)
	}
	if a.service != h.old || a.accountTransition != nil || a.settings.err == "" || a.settings.busy {
		t.Fatalf("draft guard left unexpected state: service=%p transition=%p settings=%+v", a.service, a.accountTransition, a.settings)
	}
	tt := ui.NewTester(a.View, 1280, 800)
	if !tt.HasText(locales.AccountDraftRemaining(h.old.account.Email)) {
		t.Fatalf("local draft warning is not rendered: %q", tt.Texts())
	}
}

func TestAccountRemovalChecksLegacyBeforeFormalCredentials(t *testing.T) {
	h := newAccountTransitionHarness()
	a := h.app
	a.settings = &settingsState{open: true}
	var calls []string
	a.verifyLegacy = func(_ context.Context, email string) (bool, error) {
		calls = append(calls, "verify:"+email)
		return true, nil
	}
	a.removeLegacy = func(_ context.Context, email string) error {
		calls = append(calls, "legacy:"+email)
		return nil
	}
	a.removeCredentials = func(_ context.Context, email string) error {
		calls = append(calls, "formal:"+email)
		return nil
	}
	a.deleteCheckpoint = func(string) error { return nil }
	a.removeAccount(h.target.account.Email)
	h.runJob(t)
	h.runUpdate(t)
	want := []string{"verify:" + h.target.account.Email, "legacy:" + h.target.account.Email, "formal:" + h.target.account.Email}
	if len(calls) != 3 || calls[0] != want[0] || calls[1] != want[1] || calls[2] != want[2] {
		t.Fatalf("credential cleanup calls = %v, want %v", calls, want)
	}
}

func TestRemoveInactiveAccountPreservesCurrentAccount(t *testing.T) {
	h := newAccountTransitionHarness()
	a := h.app
	a.settings = &settingsState{open: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := &notificationWorker{ctx: ctx, cancel: cancel}
	a.notification = worker
	a.notificationsStarted = true
	var removedCredentials []string
	a.removeCredentials = func(_ context.Context, email string) error {
		removedCredentials = append(removedCredentials, email)
		return nil
	}
	a.deleteCheckpoint = func(string) error { return nil }
	a.deleteActiveAccount = func() error {
		t.Fatal("inactive removal deleted the active marker")
		return nil
	}
	a.removeAccount(h.target.account.Email)
	h.runJob(t)
	h.runUpdate(t)
	if a.service != h.old || a.notification != worker || ctx.Err() != nil {
		t.Fatalf("inactive removal disturbed the active account: service=%p worker=%p err=%v", a.service, a.notification, ctx.Err())
	}
	if len(removedCredentials) != 1 || removedCredentials[0] != h.target.account.Email {
		t.Fatalf("removed credentials = %v", removedCredentials)
	}
	if _, found, _ := h.store.Account(context.Background(), h.target.account.Email); found {
		t.Fatal("inactive account was not removed")
	}
	last, found, err := h.store.LastAccount(context.Background())
	if err != nil || !found || last.Email != h.old.account.Email {
		t.Fatalf("last account after inactive removal = %+v, found=%v, err=%v", last, found, err)
	}
}

func TestRemoveInactiveAccountAfterSettingsClose(t *testing.T) {
	h := newAccountTransitionHarness()
	a := h.app
	a.settings = &settingsState{open: true}
	a.removeCredentials = func(context.Context, string) error { return nil }
	a.deleteCheckpoint = func(string) error { return nil }
	a.accountClients[h.target.account.Email] = &accountClient{service: h.target, account: h.target.account}
	a.accountCount = 2
	a.unifiedAccounts = []mail.Account{h.old.account, h.target.account}
	a.selectedLabel = mail.Unified
	a.compositeThreads = true
	a.removeAccount(h.target.account.Email)
	h.runJob(t)
	a.settings = nil
	h.runUpdate(t)
	for steps := 0; len(h.jobs)+len(h.updates) > 0 && steps < 30; steps++ {
		if len(h.updates) > 0 {
			h.runUpdate(t)
		} else {
			h.runJob(t)
		}
	}
	if _, found := a.accountClients[h.target.account.Email]; found {
		t.Fatal("closed Settings left the removed account client registered")
	}
	if a.accountCount != 1 || len(a.unifiedAccounts) != 1 || a.selectedLabel != mail.Inbox || a.compositeThreads {
		t.Fatalf("closed Settings left stale navigation: count=%d accounts=%+v label=%q composite=%v", a.accountCount, a.unifiedAccounts, a.selectedLabel, a.compositeThreads)
	}
}

func TestRemoveActiveAccountActivatesNextAccount(t *testing.T) {
	h := newAccountTransitionHarness()
	a := h.app
	a.settings = &settingsState{open: true}
	var removedCredentials, deletedCheckpoints []string
	a.removeCredentials = func(_ context.Context, email string) error {
		removedCredentials = append(removedCredentials, email)
		return nil
	}
	a.deleteCheckpoint = func(email string) error {
		deletedCheckpoints = append(deletedCheckpoints, email)
		return nil
	}
	cancelled := false
	a.authCancel = func() { cancelled = true }
	a.removeAccount(h.old.account.Email)
	h.runJob(t)
	h.runUpdate(t)
	if !cancelled || a.authCancel != nil {
		t.Fatalf("active removal did not cancel pending authentication: cancelled=%v", cancelled)
	}
	h.finishTransition(t)
	if a.service != h.target || h.active != h.target.account.Email {
		t.Fatalf("active removal activated service=%p account=%q", a.service, h.active)
	}
	if len(removedCredentials) != 1 || removedCredentials[0] != h.old.account.Email {
		t.Fatalf("removed credentials = %v", removedCredentials)
	}
	if len(deletedCheckpoints) != 1 || deletedCheckpoints[0] != h.old.account.Email {
		t.Fatalf("deleted checkpoints = %v", deletedCheckpoints)
	}
	if _, found, _ := h.store.Account(context.Background(), h.old.account.Email); found {
		t.Fatal("active account was not removed from the catalog")
	}
}

func TestSettingsAccountManagementAndRemoveConfirmation(t *testing.T) {
	h := newAccountTransitionHarness()
	a := h.app
	a.settings = &settingsState{open: true, accounts: []mail.Account{h.old.account, h.target.account}}
	tt := ui.NewTester(a.View, 1280, 800)
	for _, text := range []string{"Add account", "Switch", "old@example.com", "target@example.com"} {
		if !tt.HasText(text) {
			t.Fatalf("account settings are missing %q: %q", text, tt.Texts())
		}
	}
	a.settings.accounts = []mail.Account{h.old.account}
	tt.Frame()
	if err := tt.Click("Remove…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Remove account") || !tt.HasText("Remove old@example.com from Mica? This deletes its sign-in credentials and local mail cache on this Mac. Mail on the Gmail server is not affected.") {
		t.Fatalf("remove confirmation is incomplete: %q", tt.Texts())
	}
}
