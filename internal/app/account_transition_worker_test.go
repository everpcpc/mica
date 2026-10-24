package app

import (
	"context"
	"testing"
)

func TestAccountTransitionWaitsForNotificationWorker(t *testing.T) {
	h := newAccountTransitionHarness()
	a := h.app
	ctx, cancel := context.WithCancel(context.Background())
	worker := &notificationWorker{
		ctx: ctx, cancel: cancel, done: make(chan struct{}), service: h.old,
		version: a.serviceVersion, email: h.old.account.Email,
	}
	a.notification = worker
	a.notificationsStarted = true
	a.transitionToService(h.target, h.target.account.Email, false, "")
	if a.service != h.old || len(h.jobs) != 1 {
		t.Fatalf("transition bypassed the notification drain: service=%p jobs=%d", a.service, len(h.jobs))
	}
	if ctx.Err() == nil {
		t.Fatal("old notification worker was not cancelled")
	}
	close(worker.done)
	h.finishTransition(t)
	if a.service != h.target {
		t.Fatal("transition did not activate after the notification worker exited")
	}
}
