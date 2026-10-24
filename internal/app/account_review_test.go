package app

import (
	"errors"
	"testing"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

func TestCancelledRestoreDoesNotReplaceServiceOrBlockTransition(t *testing.T) {
	h := newAccountTransitionHarness()
	a := h.app
	version, auth := a.serviceVersion, a.authID
	transition := &accountTransition{sourceVersion: version, email: h.target.account.Email, service: h.target}
	a.accountTransition = transition
	a.cancelPendingAuthentication()
	a.finishRestoreGmail(version, auth, h.target, h.target.account.Email, nil)
	if a.service != h.old || a.accountTransition != transition {
		t.Fatalf("cancelled restore changed state: service=%p transition=%p", a.service, a.accountTransition)
	}
}

func TestDelayedAccountDoesNotRestartWorkerDuringTransition(t *testing.T) {
	h := newAccountTransitionHarness()
	a := h.app
	a.notificationsStarted = false
	a.Start()
	a.transitionToService(h.target, h.target.account.Email, false, "")
	h.runJobAt(t, 1)
	h.runUpdate(t)
	if a.notification != nil || a.notificationsStarted {
		t.Fatalf("delayed account callback restarted notifications: worker=%p started=%v", a.notification, a.notificationsStarted)
	}
	h.finishTransition(t)
}

func TestAccountTransitionWaitsForLabelCreation(t *testing.T) {
	h := newAccountTransitionHarness()
	a := h.app
	a.labelDialog = &labelDialogState{open: true, name: "Receipts"}
	a.createLabel()
	a.transitionToService(h.target, h.target.account.Email, false, "")
	h.runJobAt(t, 1)
	h.runUpdate(t)
	if a.service != h.old || len(h.old.createdLabels) != 0 {
		t.Fatalf("label creation was bypassed: service=%p labels=%v", a.service, h.old.createdLabels)
	}
	h.runJobAt(t, 0)
	h.runUpdate(t)
	if len(h.old.createdLabels) != 1 || h.old.createdLabels[0] != "Receipts" {
		t.Fatalf("created labels = %v", h.old.createdLabels)
	}
	h.finishTransition(t)
	if a.service != h.target || a.labelDialog != nil {
		t.Fatalf("label creation did not finish before activation: service=%p dialog=%p", a.service, a.labelDialog)
	}
}

func TestAccountTransitionClosesComposerAndMutationEntrances(t *testing.T) {
	a := newTestApp(newFakeService())
	a.accountTransition = &accountTransition{}
	message := newFakeService().thread.Messages[0]
	a.openReplyAllMessage(message)
	if a.compose != nil {
		t.Fatal("Reply all opened a composer during account transition")
	}
	a.openForwardMessage(message)
	if a.compose != nil {
		t.Fatal("Forward opened a composer during account transition")
	}
	a.openDraftMessage("draft-1", message)
	if a.compose != nil {
		t.Fatal("Draft opened a composer during account transition")
	}
	called := false
	var doneErr error
	a.enqueueMutation(mutationJob{
		runFunc: func() error {
			called = true
			return nil
		},
		done: func(err error) { doneErr = err },
	})
	if called || !errors.Is(doneErr, errAccountTransition) || len(a.mutationQueue) != 0 {
		t.Fatalf("transition accepted a mutation: called=%v err=%v queue=%d", called, doneErr, len(a.mutationQueue))
	}
	if a.notice == "" || a.actionSummaries() != nil {
		t.Fatal("mutation actions were not closed during transition")
	}
}

func TestSettingsSwitchButtonShowsBusyAndSwitches(t *testing.T) {
	h := newAccountTransitionHarness()
	a := h.app
	a.settings = &settingsState{open: true, accounts: []mail.Account{h.old.account, h.target.account}}
	tt := ui.NewTester(a.View, 1280, 800)
	if !tt.HasText("Reauthorize") {
		t.Fatal("active account has no reauthorization action")
	}
	if err := tt.Click("Switch"); err != nil {
		t.Fatal(err)
	}
	if !a.settings.busy || a.accountTransition == nil {
		t.Fatalf("Switch did not start a busy transition: settings=%+v transition=%p", a.settings, a.accountTransition)
	}
	tt.Frame()
	if !tt.HasText("Working…") {
		t.Fatalf("Switch busy state is not rendered: %q", tt.Texts())
	}
	h.finishTransition(t)
	tt.Frame()
	if a.service != h.target || a.settings.busy {
		t.Fatalf("Switch did not finish: service=%p busy=%v", a.service, a.settings.busy)
	}
}

func TestAuthenticationFailureClearsPendingNotification(t *testing.T) {
	a := New(newFakeService())
	a.pendingThread = notificationTarget{email: "target@example.com", threadID: "shared"}
	a.accountAuthenticationFailed(nil, "target@example.com", errors.New("cancelled"))
	if a.pendingThread.threadID != "" {
		t.Fatalf("failed authentication retained pending target: %+v", a.pendingThread)
	}
}
