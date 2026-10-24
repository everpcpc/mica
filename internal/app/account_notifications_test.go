package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"mica/internal/gmail"
	"mica/internal/mail"
)

func TestNotificationIDsIncludeAccount(t *testing.T) {
	first := notificationID(" First@Example.com ", "shared")
	second := notificationID("second@example.com", "shared")
	if first == second {
		t.Fatal("different accounts produced the same notification ID")
	}
	target, ok := parseNotificationID(first)
	if !ok || target.email != "first@example.com" || target.threadID != "shared" {
		t.Fatalf("decoded target = %+v, ok=%v", target, ok)
	}
	for _, id := range []string{"", "shared", "mica-thread-v1:invalid", notificationIDPrefix + "Og::"} {
		if _, ok := parseNotificationID(id); ok {
			t.Fatalf("unscoped notification ID %q was accepted", id)
		}
	}
}

func TestStaleNotificationErrorDoesNotStopCurrentWorker(t *testing.T) {
	a := New(newFakeService())
	oldCtx, oldCancel := context.WithCancel(context.Background())
	defer oldCancel()
	newCtx, newCancel := context.WithCancel(context.Background())
	defer newCancel()
	old := &notificationWorker{ctx: oldCtx, cancel: oldCancel, version: 1}
	current := &notificationWorker{ctx: newCtx, cancel: newCancel, version: 2}
	a.serviceVersion = 2
	a.notification = current
	a.notificationsStarted = true
	a.handlePushError(old, errors.New("late old-account failure"))
	if a.notification != current || !a.notificationsStarted || newCtx.Err() != nil {
		t.Fatalf("stale error stopped the current worker: notification=%p started=%v err=%v", a.notification, a.notificationsStarted, newCtx.Err())
	}
}

func TestStaleHistoryResetPreservesCurrentBaseline(t *testing.T) {
	h := newNotificationHarness()
	old := &notificationWorker{ctx: context.Background(), version: 1, email: "old@example.com"}
	h.app.serviceVersion = 2
	h.app.unreadSeen = map[string]time.Time{"current-thread": time.Now()}
	h.service.historyFunc = func(uint64, func([]mail.ThreadSummary) error) (uint64, error) {
		return 0, &gmail.APIError{StatusCode: 404}
	}
	synchronizer := &historySynchronizer{
		app: h.app, worker: old, service: h.service, ctx: context.Background(),
		version: 1, email: old.email, historyID: 5,
	}
	if err := synchronizer.sync(3); err != nil {
		t.Fatal(err)
	}
	if _, found := h.app.unreadSeen["current-thread"]; !found {
		t.Fatal("stale history reset cleared the current account baseline")
	}
}
