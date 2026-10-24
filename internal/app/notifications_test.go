package app

import (
	"context"
	"reflect"
	"testing"
	"time"

	"mica/internal/gmail"
	"mica/internal/mail"
)

type notificationHarness struct {
	app           *App
	service       *fakeService
	badges        []int
	notifications []string
	reconcile     func()
	checkpoint    uint64
	saved         uint64
}

func newNotificationHarness() *notificationHarness {
	service := newFakeService()
	service.account.Demo = false
	h := &notificationHarness{service: service}
	h.app = New(service)
	h.app.schedule = func(fn func()) { fn() }
	h.app.restoreAttempted = true
	h.app.after = func(_ time.Duration, fn func()) { h.reconcile = fn }
	h.app.loadCheckpoint = func(string) (uint64, bool, error) {
		return h.checkpoint, h.checkpoint > 0, nil
	}
	h.app.saveCheckpoint = func(_ string, id uint64) error {
		h.saved = id
		return nil
	}
	h.app.SetNotificationHandlers(func(count int) {
		h.badges = append(h.badges, count)
	}, func(id, _, _ string) {
		h.notifications = append(h.notifications, id)
	})
	return h
}

func (h *notificationHarness) syncNow(t *testing.T) {
	t.Helper()
	if h.reconcile == nil {
		t.Fatal("history reconciliation was not scheduled")
	}
	h.reconcile()
}

func (h *notificationHarness) lastBadge(t *testing.T) int {
	t.Helper()
	if len(h.badges) == 0 {
		t.Fatal("badge was not updated")
	}
	return h.badges[len(h.badges)-1]
}

func TestMailNotificationsOnlyForNewActivity(t *testing.T) {
	h := newNotificationHarness()
	first := h.service.threads[0]
	h.app.Start()
	if len(h.notifications) != 0 {
		t.Fatalf("initial unread mail produced notifications: %v", h.notifications)
	}
	if got := h.lastBadge(t); got != 1 {
		t.Fatalf("badge = %d, want 1", got)
	}

	second := mail.ThreadSummary{ID: "thread-2", Subject: "New mail", Senders: "Bob", Updated: first.Updated.Add(time.Minute), Unread: true, LabelIDs: []string{mail.Inbox, mail.Unread}}
	h.service.threads = append(h.service.threads, second)
	h.service.labels[0].Unread = 2
	h.syncNow(t)
	if !reflect.DeepEqual(h.notifications, []string{notificationID("demo@example.com", "thread-2")}) {
		t.Fatalf("notifications = %v, want only thread-2", h.notifications)
	}
	if got := h.lastBadge(t); got != 2 {
		t.Fatalf("badge = %d, want 2", got)
	}

	h.syncNow(t)
	if len(h.notifications) != 1 {
		t.Fatalf("unchanged mail produced another notification: %v", h.notifications)
	}
	h.service.threads = nil
	h.syncNow(t)
	h.service.threads = []mail.ThreadSummary{first}
	h.syncNow(t)
	if len(h.notifications) != 1 {
		t.Fatalf("manual unread change produced another notification: %v", h.notifications)
	}

	first.Updated = first.Updated.Add(2 * time.Minute)
	h.service.threads = []mail.ThreadSummary{first}
	h.syncNow(t)
	if !reflect.DeepEqual(h.notifications, []string{notificationID("demo@example.com", "thread-2"), notificationID("demo@example.com", "thread-1")}) {
		t.Fatalf("new reply did not produce exactly one notification: %v", h.notifications)
	}
}

func TestHistorySyncRefreshesVisibleListAndKeepsSelection(t *testing.T) {
	h := newNotificationHarness()
	h.app.Start()
	h.app.selectedRow = 0
	h.app.selectThread("thread-1")
	cached := h.app.thread
	second := mail.ThreadSummary{ID: "thread-2", Subject: "New mail", Senders: "Bob", Updated: cached.Messages[0].Date.Add(time.Minute), Unread: true, LabelIDs: []string{mail.Inbox, mail.Unread}}
	h.service.threads = append([]mail.ThreadSummary{second}, h.service.threads...)
	h.syncNow(t)
	if len(h.app.threads) != 2 || h.app.threads[0].ID != "thread-2" {
		t.Fatalf("new mail was not inserted into the visible inbox: %+v", h.app.threads)
	}
	if h.app.selectedThread != "thread-1" || h.app.selectedRow != 1 {
		t.Fatalf("selection moved after the list update: thread=%q row=%d", h.app.selectedThread, h.app.selectedRow)
	}
	if got := h.app.threadCache["thread-1"]; !reflect.DeepEqual(got, cached) {
		t.Fatal("background list refresh discarded the opened conversation")
	}
}

func TestHistorySyncAfterRestart(t *testing.T) {
	h := newNotificationHarness()
	first := h.service.threads[0]
	missed := mail.ThreadSummary{ID: "thread-2", Subject: "Missed while closed", Senders: "Bob", Updated: first.Updated.Add(time.Minute), Unread: true, LabelIDs: []string{mail.Inbox, mail.Unread}}
	h.service.threads = append(h.service.threads, missed)
	h.checkpoint = 3
	h.service.watchID = 5
	h.service.historyFunc = func(_ uint64, fn func([]mail.ThreadSummary) error) (uint64, error) {
		if err := fn([]mail.ThreadSummary{missed}); err != nil {
			return 0, err
		}
		return 5, nil
	}
	h.app.Start()
	if !reflect.DeepEqual(h.notifications, []string{notificationID("demo@example.com", "thread-2")}) {
		t.Fatalf("restart notifications = %v, want the missed message only", h.notifications)
	}
	if h.saved != 5 {
		t.Fatalf("checkpoint = %d, want 5", h.saved)
	}
	h.syncNow(t)
	if len(h.notifications) != 1 {
		t.Fatalf("redelivered history produced another notification: %v", h.notifications)
	}
}

func TestHistorySyncPushIDUsesCurrentCheckpoint(t *testing.T) {
	h := newNotificationHarness()
	var starts []uint64
	h.service.historyFunc = func(start uint64, _ func([]mail.ThreadSummary) error) (uint64, error) {
		starts = append(starts, start)
		return 6, nil
	}
	synchronizer := &historySynchronizer{
		app: h.app, service: h.service, ctx: context.Background(),
		version: h.app.serviceVersion, email: "alex@example.com", historyID: 5,
	}
	if err := synchronizer.sync(6); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(starts, []uint64{5}) {
		t.Fatalf("history start IDs = %v, want current checkpoint 5", starts)
	}
}

func TestHistory404RebuildsBaseline(t *testing.T) {
	h := newNotificationHarness()
	h.checkpoint = 3
	h.service.watchID = 5
	h.service.historyFunc = func(uint64, func([]mail.ThreadSummary) error) (uint64, error) {
		return 0, &gmail.APIError{StatusCode: 404}
	}
	h.app.Start()
	if len(h.notifications) != 0 {
		t.Fatalf("history reset notified existing mail: %v", h.notifications)
	}
	if h.saved != 5 {
		t.Fatalf("checkpoint after reset = %d, want 5", h.saved)
	}

	second := mail.ThreadSummary{ID: "thread-2", Subject: "After reset", Senders: "Bob", Updated: time.Now(), Unread: true, LabelIDs: []string{mail.Inbox, mail.Unread}}
	h.service.threads = append(h.service.threads, second)
	h.service.historyFunc = func(_ uint64, fn func([]mail.ThreadSummary) error) (uint64, error) {
		if err := fn([]mail.ThreadSummary{second}); err != nil {
			return 0, err
		}
		return 6, nil
	}
	h.syncNow(t)
	if !reflect.DeepEqual(h.notifications, []string{notificationID("demo@example.com", "thread-2")}) {
		t.Fatalf("notifications after reset = %v, want thread-2", h.notifications)
	}
}

func TestNotificationClickOpensThread(t *testing.T) {
	h := newNotificationHarness()
	h.app.Start()
	h.app.OpenThread(notificationID("demo@example.com", "thread-1"))
	if h.app.selectedThread != "thread-1" || h.app.thread.ID != "thread-1" {
		t.Fatalf("notification did not open the thread: selected=%q loaded=%q", h.app.selectedThread, h.app.thread.ID)
	}
	if h.app.selectedLabel != mail.Inbox || h.app.pendingThread.threadID != "" {
		t.Fatalf("notification left unexpected state: label=%q pending=%q", h.app.selectedLabel, h.app.pendingThread)
	}
}

func TestNotificationClickWaitsForGmailRestore(t *testing.T) {
	h := newNotificationHarness()
	h.app.OpenThread(notificationID("demo@example.com", "thread-1"))
	if h.app.selectedThread != "" {
		t.Fatal("thread opened before Gmail was restored")
	}
	h.app.Start()
	if h.app.selectedThread != "thread-1" || h.app.thread.ID != "thread-1" {
		t.Fatalf("pending notification was not opened after restore: selected=%q loaded=%q", h.app.selectedThread, h.app.thread.ID)
	}
}

func TestNotificationsStopForDemoAndAccountSwitch(t *testing.T) {
	h := newNotificationHarness()
	h.app.Start()
	oldReconcile := h.reconcile
	h.reconcile = nil
	h.app.useService(mail.NewDemo())
	if got := h.lastBadge(t); got != 0 {
		t.Fatalf("badge after account switch = %d, want 0", got)
	}
	if h.reconcile != nil {
		t.Fatal("demo service started notification streaming")
	}
	oldReconcile()
	if len(h.notifications) != 0 {
		t.Fatalf("old account stream produced notifications: %v", h.notifications)
	}
}

func (f *fakeService) EnsurePush(context.Context) error { return nil }

func (f *fakeService) Watch(context.Context) (gmail.WatchState, error) {
	id := f.watchID
	if id == 0 {
		id = 1
	}
	return gmail.WatchState{HistoryID: id, Expiration: time.Now().Add(time.Hour)}, nil
}

func (f *fakeService) History(_ context.Context, start uint64, fn func(gmail.HistoryPage) error) (uint64, error) {
	if f.historyFunc != nil {
		return f.historyFunc(start, func(summaries []mail.ThreadSummary) error {
			return fn(gmail.HistoryPage{Added: summaries})
		})
	}
	if len(f.threads) > 0 {
		changed := make([]string, 0, len(f.threads))
		for _, thread := range f.threads {
			changed = append(changed, thread.ID)
		}
		if err := fn(gmail.HistoryPage{Added: append([]mail.ThreadSummary(nil), f.threads...), ChangedThreadIDs: changed}); err != nil {
			return 0, err
		}
	}
	id := f.watchID
	if id == 0 {
		id = 1
	}
	return id, nil
}

func (f *fakeService) Stream(ctx context.Context, fn func(gmail.PushNotification) error) error {
	if f.stream != nil {
		return f.stream(ctx, fn)
	}
	return context.Canceled
}

func (f *fakeService) Label(_ context.Context, id string) (mail.Label, error) {
	for _, label := range f.labels {
		if label.ID == id {
			return label, nil
		}
	}
	return mail.Label{ID: id}, nil
}
