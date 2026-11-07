package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"mica/internal/gmail"
	"mica/internal/mail"
)

func newNotificationWorkerForTest(t *testing.T, h *notificationHarness) *notificationWorker {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	worker := &notificationWorker{
		ctx: ctx, cancel: cancel, service: h.service, version: h.app.serviceVersion,
		email: normalizedEmail(h.service.account.Email),
		sleep: func(ctx context.Context, _ time.Duration) bool { return ctx.Err() == nil },
	}
	h.app.notification = worker
	h.app.notificationsStarted = true
	return worker
}

func TestNotificationStartupRetriesTransientFailures(t *testing.T) {
	for _, test := range []struct {
		name  string
		fail  func(*fakeService, *int)
		calls func(*fakeService, *int) int
	}{
		{
			name: "ensure push",
			fail: func(service *fakeService, calls *int) {
				service.ensurePushFunc = func() error {
					*calls++
					if *calls == 1 {
						return errors.New("temporary network failure")
					}
					return nil
				}
			},
			calls: func(_ *fakeService, calls *int) int { return *calls },
		},
		{
			name: "watch",
			fail: func(service *fakeService, calls *int) {
				service.watchFunc = func() (gmail.WatchState, error) {
					*calls++
					if *calls == 1 {
						return gmail.WatchState{}, errors.New("temporary network failure")
					}
					return gmail.WatchState{HistoryID: 1}, nil
				}
			},
			calls: func(_ *fakeService, calls *int) int { return *calls },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newNotificationHarness()
			h.app.after = func(time.Duration, func()) {}
			worker := newNotificationWorkerForTest(t, h)
			calls := 0
			test.fail(h.service, &calls)
			streamCalls := 0
			h.service.stream = func(context.Context, func(gmail.PushNotification) error) error {
				streamCalls++
				worker.cancel()
				return nil
			}
			worker.sleep = func(ctx context.Context, _ time.Duration) bool { return ctx.Err() == nil }
			h.app.runNotifications(worker)
			if got := test.calls(h.service, &calls); got != 2 {
				t.Fatalf("setup calls = %d, want 2", got)
			}
			if streamCalls != 1 {
				t.Fatalf("stream calls = %d, want 1 after setup recovery", streamCalls)
			}
			if h.app.notification != worker || !h.app.notificationsStarted {
				t.Fatal("temporary setup failure stopped the notification worker")
			}
		})
	}
}

func TestNotificationStatusTracksReconnectAndRecovery(t *testing.T) {
	h := newNotificationHarness()
	worker := newNotificationWorkerForTest(t, h)
	calls := 0
	h.service.ensurePushFunc = func() error {
		calls++
		if calls == 1 {
			return errors.New("temporary network failure")
		}
		return nil
	}
	var statuses []notificationStatus
	h.app.update = func(fn func()) {
		fn()
		if len(statuses) == 0 || statuses[len(statuses)-1] != h.app.notificationStatus {
			statuses = append(statuses, h.app.notificationStatus)
		}
	}
	h.service.stream = func(context.Context, func(gmail.PushNotification) error) error {
		worker.cancel()
		return context.Canceled
	}
	h.app.runNotifications(worker)
	if !reflect.DeepEqual(statuses, []notificationStatus{notificationReconnecting, notificationConnected}) {
		t.Fatalf("notification statuses = %v, want reconnecting then connected", statuses)
	}
	if h.app.notificationErr != nil {
		t.Fatalf("recovered notification retained error: %v", h.app.notificationErr)
	}
}

func TestNotificationReconnectWatchesAndCatchesUp(t *testing.T) {
	h := newNotificationHarness()
	h.app.after = func(time.Duration, func()) {}
	worker := newNotificationWorkerForTest(t, h)
	watchCalls := 0
	h.service.watchFunc = func() (gmail.WatchState, error) {
		watchCalls++
		return gmail.WatchState{HistoryID: uint64(watchCalls)}, nil
	}
	h.app.loadCheckpoint = func(string) (uint64, bool, error) { return 1, true, nil }
	var historyStarts []uint64
	h.service.historyFunc = func(start uint64, _ func([]mail.ThreadSummary) error) (uint64, error) {
		historyStarts = append(historyStarts, start)
		return 2, nil
	}
	streamCalls := 0
	h.service.stream = func(_ context.Context, fn func(gmail.PushNotification) error) error {
		streamCalls++
		if streamCalls == 1 {
			if err := fn(gmail.PushNotification{EmailAddress: h.service.account.Email, HistoryID: 2}); err != nil {
				return err
			}
			return errors.New("stream disconnected")
		}
		worker.cancel()
		return context.Canceled
	}
	h.app.runNotifications(worker)
	if streamCalls != 2 {
		t.Fatalf("stream calls = %d, want 2", streamCalls)
	}
	if watchCalls != 2 {
		t.Fatalf("watch calls = %d, want a new watch after reconnect", watchCalls)
	}
	if !reflect.DeepEqual(historyStarts, []uint64{1}) {
		t.Fatalf("history starts = %v, want catch-up from checkpoint 1", historyStarts)
	}
	if h.saved != 2 {
		t.Fatalf("saved checkpoint = %d, want 2", h.saved)
	}
}

func TestNotificationReconnectNeverRegressesCheckpoint(t *testing.T) {
	synchronizer := &historySynchronizer{historyID: 9}
	synchronizer.advance(5)
	if got := synchronizer.current(); got != 9 {
		t.Fatalf("checkpoint = %d after older watch, want 9", got)
	}
	synchronizer.advance(11)
	if got := synchronizer.current(); got != 11 {
		t.Fatalf("checkpoint = %d after newer watch, want 11", got)
	}
}

func TestWatchRenewalRetriesTransientFailure(t *testing.T) {
	h := newNotificationHarness()
	worker := newNotificationWorkerForTest(t, h)
	watchCalls := 0
	h.service.watchFunc = func() (gmail.WatchState, error) {
		watchCalls++
		if watchCalls == 1 {
			return gmail.WatchState{}, errors.New("temporary network failure")
		}
		return gmail.WatchState{HistoryID: 1}, nil
	}
	type scheduled struct {
		delay time.Duration
		fn    func()
	}
	var callbacks []scheduled
	h.app.after = func(delay time.Duration, fn func()) {
		callbacks = append(callbacks, scheduled{delay: delay, fn: fn})
	}
	h.app.renewWatch(worker)
	if len(callbacks) != 1 || callbacks[0].delay >= watchRenewInterval {
		t.Fatalf("renewal failure callbacks = %+v, want an immediate retry", callbacks)
	}
	callbacks[0].fn()
	if watchCalls != 2 {
		t.Fatalf("watch calls = %d, want 2", watchCalls)
	}
	if len(callbacks) != 2 || callbacks[1].delay != watchRenewInterval {
		t.Fatalf("renewal success callbacks = %+v, want the next daily renewal", callbacks)
	}
	if h.app.notification != worker || worker.ctx.Err() != nil {
		t.Fatal("temporary renewal failure stopped the notification worker")
	}
}

func TestHistoryReconciliationRetriesTransientFailure(t *testing.T) {
	h := newNotificationHarness()
	worker := newNotificationWorkerForTest(t, h)
	historyCalls := 0
	h.service.historyFunc = func(uint64, func([]mail.ThreadSummary) error) (uint64, error) {
		historyCalls++
		if historyCalls == 1 {
			return 0, errors.New("temporary network failure")
		}
		return 1, nil
	}
	var delays []time.Duration
	var callbacks []func()
	h.app.after = func(delay time.Duration, fn func()) {
		delays = append(delays, delay)
		callbacks = append(callbacks, fn)
	}
	synchronizer := &historySynchronizer{
		app: h.app, worker: worker, service: h.service, ctx: worker.ctx,
		version: worker.version, email: worker.email, historyID: 1,
	}
	h.app.reconcileHistory(synchronizer)
	if len(callbacks) != 1 || delays[0] >= historyReconcileInterval {
		t.Fatalf("reconciliation failure delays = %v, want an immediate retry", delays)
	}
	callbacks[0]()
	if historyCalls != 2 {
		t.Fatalf("history calls = %d, want 2", historyCalls)
	}
	if len(delays) != 2 || delays[1] != historyReconcileInterval {
		t.Fatalf("reconciliation success delays = %v, want the next hourly reconciliation", delays)
	}
	if h.app.notification != worker || worker.ctx.Err() != nil {
		t.Fatal("temporary history failure stopped the notification worker")
	}
}

func TestPushHistoryFailureRetriesWithoutDisconnect(t *testing.T) {
	h := newNotificationHarness()
	worker := newNotificationWorkerForTest(t, h)
	historyCalls := 0
	h.service.historyFunc = func(uint64, func([]mail.ThreadSummary) error) (uint64, error) {
		historyCalls++
		if historyCalls == 1 {
			return 0, errors.New("temporary network failure")
		}
		return 1, nil
	}
	var callbacks []func()
	h.app.after = func(_ time.Duration, fn func()) { callbacks = append(callbacks, fn) }
	synchronizer := &historySynchronizer{
		app: h.app, worker: worker, service: h.service, ctx: worker.ctx,
		version: worker.version, email: worker.email, historyID: 1,
	}
	if err := synchronizer.trigger(); err != nil {
		t.Fatal(err)
	}
	if len(callbacks) != 1 {
		t.Fatalf("push callbacks = %d, want 1", len(callbacks))
	}
	callbacks[0]()
	if len(callbacks) != 2 {
		t.Fatalf("callbacks after failure = %d, want a retry", len(callbacks))
	}
	callbacks[1]()
	if historyCalls != 2 {
		t.Fatalf("history calls = %d, want 2", historyCalls)
	}
	if h.app.notification != worker || worker.ctx.Err() != nil {
		t.Fatal("temporary push history failure stopped the notification worker")
	}
}

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
