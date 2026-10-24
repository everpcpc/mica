package app

import (
	"context"
	"errors"
	"testing"

	"mica/internal/mail"
)

type accountTransitionHarness struct {
	app     *App
	old     *fakeService
	target  *fakeService
	store   *fakeMailStore
	jobs    []func()
	updates []func()
	active  string
}

func newAccountTransitionHarness() *accountTransitionHarness {
	old := newFakeService()
	old.account = mail.Account{Name: "Old", Email: "old@example.com"}
	target := newFakeService()
	target.account = mail.Account{Name: "Target", Email: "target@example.com"}
	h := &accountTransitionHarness{old: old, target: target}
	a := New(old)
	a.account = old.account
	a.storeAccount = old.account.Email
	a.accountVerified = true
	a.restoreAttempted = true
	a.notificationsStarted = true
	a.schedule = func(fn func()) { h.jobs = append(h.jobs, fn) }
	a.update = func(fn func()) { h.updates = append(h.updates, fn) }
	h.store = newFakeMailStore()
	if err := h.store.SaveAccount(context.Background(), old.account); err != nil {
		panic(err)
	}
	if err := h.store.SaveAccount(context.Background(), target.account); err != nil {
		panic(err)
	}
	if err := h.store.SetLastAccount(context.Background(), old.account.Email); err != nil {
		panic(err)
	}
	a.SetMailStore(h.store)
	a.removeLegacy = func(context.Context, string) error { return nil }
	a.verifyLegacy = func(context.Context, string) (bool, error) { return false, nil }
	a.saveActiveAccount = func(email string) error {
		h.active = email
		return nil
	}
	a.accountService = func(context.Context, string) (mail.Service, error) {
		return target, nil
	}
	h.app = a
	return h
}

func (h *accountTransitionHarness) runJob(t *testing.T) {
	t.Helper()
	if len(h.jobs) == 0 {
		t.Fatal("no scheduled job")
	}
	job := h.jobs[0]
	h.jobs = h.jobs[1:]
	job()
}

func (h *accountTransitionHarness) runJobAt(t *testing.T, index int) {
	t.Helper()
	if index >= len(h.jobs) {
		t.Fatalf("job index %d, have %d", index, len(h.jobs))
	}
	job := h.jobs[index]
	h.jobs = append(h.jobs[:index], h.jobs[index+1:]...)
	job()
}

func (h *accountTransitionHarness) runUpdate(t *testing.T) {
	t.Helper()
	if len(h.updates) == 0 {
		t.Fatal("no published update")
	}
	update := h.updates[0]
	h.updates = h.updates[1:]
	update()
}

func (h *accountTransitionHarness) finishTransition(t *testing.T) {
	t.Helper()
	for steps := 0; h.app.accountTransition != nil && steps < 20; steps++ {
		if len(h.updates) > 0 {
			h.runUpdate(t)
		} else if len(h.jobs) > 0 {
			h.runJob(t)
		} else {
			t.Fatal("account transition is waiting without scheduled work")
		}
	}
	if h.app.accountTransition != nil {
		t.Fatal("account transition did not finish")
	}
}

func TestAccountTransitionWaitsForMutationFIFO(t *testing.T) {
	h := newAccountTransitionHarness()
	a := h.app
	a.enqueueMutation(mutationJob{service: h.old, id: "old-thread", add: []string{mail.Starred}})
	a.transitionToService(h.target, h.target.account.Email, false, "")
	if len(h.jobs) != 2 {
		t.Fatalf("scheduled jobs = %d, want mutation and notification drain", len(h.jobs))
	}
	h.runJobAt(t, 1)
	h.runUpdate(t)
	if a.service != h.old || !a.mutationRunning {
		t.Fatalf("account changed before the mutation completed: service=%p running=%v", a.service, a.mutationRunning)
	}
	h.runJobAt(t, 0)
	if a.service != h.old {
		t.Fatal("mutation completion activated the account before its UI callback")
	}
	h.runUpdate(t)
	if len(h.old.modified) != 1 || h.old.modified[0] != "old-thread" {
		t.Fatalf("old-account mutations = %v", h.old.modified)
	}
	h.finishTransition(t)
	if a.service != h.target || a.mutationsPending() {
		t.Fatalf("transition finished with the wrong service or pending mutations: service=%p pending=%v", a.service, a.mutationsPending())
	}
	if h.active != h.target.account.Email {
		t.Fatalf("active account = %q", h.active)
	}
}

func TestAccountTransitionFailureKeepsCurrentService(t *testing.T) {
	h := newAccountTransitionHarness()
	h.app.account.Demo = true
	h.app.saveActiveAccount = func(string) error { return errors.New("keychain unavailable") }
	h.app.transitionToService(h.target, h.target.account.Email, false, "")
	h.finishTransition(t)
	if h.app.service != h.old {
		t.Fatal("failed activation replaced the current service")
	}
	last, found, err := h.store.LastAccount(context.Background())
	if err != nil || !found || last.Email != h.old.account.Email {
		t.Fatalf("last account = %+v, found=%v, err=%v", last, found, err)
	}
	if h.app.accountTransition != nil {
		t.Fatal("failed activation left the transition busy")
	}
}

func TestAccountTransitionRejectsOpenComposer(t *testing.T) {
	h := newAccountTransitionHarness()
	composer := &composer{open: true}
	h.app.compose = composer
	h.app.transitionToService(h.target, h.target.account.Email, false, "")
	if h.app.service != h.old || h.app.compose != composer || h.app.accountTransition != nil {
		t.Fatalf("composer was not preserved: service=%p compose=%p transition=%p", h.app.service, h.app.compose, h.app.accountTransition)
	}
}

func TestCancelPendingAuthenticationPreservesWorkingService(t *testing.T) {
	service := &deactivatingService{fakeService: newFakeService()}
	a := New(service)
	a.cancelPendingAuthentication()
	if service.deactivated {
		t.Fatal("cancelling a pending login deactivated the working service")
	}
	a.cancelAuthentication()
	if !service.deactivated {
		t.Fatal("service replacement did not deactivate the old service")
	}
}

func TestNotificationClickSwitchesAccountBeforeOpening(t *testing.T) {
	h := newAccountTransitionHarness()
	var oldCalls, targetCalls []string
	h.old.threadFunc = func(id string) (mail.Thread, error) {
		oldCalls = append(oldCalls, id)
		return mail.Thread{ID: id}, nil
	}
	h.target.threadFunc = func(id string) (mail.Thread, error) {
		targetCalls = append(targetCalls, id)
		return mail.Thread{ID: id}, nil
	}
	h.app.OpenThread(notificationID(h.target.account.Email, "shared-thread"))
	if h.app.selectedThread != "" || len(oldCalls) != 0 {
		t.Fatalf("notification opened in the current account: selected=%q calls=%v", h.app.selectedThread, oldCalls)
	}
	h.finishTransition(t)
	if h.app.service != h.target || h.app.selectedThread != "shared-thread" {
		t.Fatalf("notification routed to service=%p thread=%q", h.app.service, h.app.selectedThread)
	}
	h.runJobAt(t, 2)
	if len(oldCalls) != 0 || len(targetCalls) != 1 || targetCalls[0] != "shared-thread" {
		t.Fatalf("thread requests: old=%v target=%v", oldCalls, targetCalls)
	}
}

func TestNotificationClickFromDemoRestoresAccount(t *testing.T) {
	h := newAccountTransitionHarness()
	h.app.account = mail.Account{Name: "Mica", Demo: true}
	h.app.storeAccount = ""
	h.app.OpenThread(notificationID(h.target.account.Email, "shared-thread"))
	if h.app.selectedThread != "" {
		t.Fatal("notification opened in Demo")
	}
	h.finishTransition(t)
	if h.app.service != h.target || h.app.selectedThread != "shared-thread" {
		t.Fatalf("Demo notification routed to service=%p thread=%q", h.app.service, h.app.selectedThread)
	}
}
