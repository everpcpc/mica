package app

import (
	"testing"
	"time"

	"mica/internal/mail"
)

func TestNotificationCancellationFinishesDelayedWork(t *testing.T) {
	h := newNotificationHarness()
	h.app.after = func(time.Duration, func()) {}
	h.app.Start()
	done := h.app.stopNotifications()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("notification worker did not finish with delayed callbacks pending")
	}
}

func TestNotificationCancellationWaitsForRunningDelayedWork(t *testing.T) {
	h := newNotificationHarness()
	started := make(chan struct{})
	release := make(chan struct{})
	h.service.historyFunc = func(uint64, func([]mail.ThreadSummary) error) (uint64, error) {
		close(started)
		<-release
		return 6, nil
	}
	h.app.Start()
	reconcile := h.reconcile
	go reconcile()
	<-started
	done := h.app.stopNotifications()
	select {
	case <-done:
		t.Fatal("notification worker finished while a delayed callback was running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("notification worker did not finish after the callback returned")
	}
}
