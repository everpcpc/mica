package app

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"

	"mica/internal/gmail"
	"mica/internal/mail"
	"mica/locales"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	watchRenewInterval       = 24 * time.Hour
	historyReconcileInterval = time.Hour
	historyBatchDelay        = 500 * time.Millisecond
)

type pushService interface {
	mail.Service
	EnsurePush(context.Context) error
	Watch(context.Context) (gmail.WatchState, error)
	History(context.Context, uint64, func(gmail.HistoryPage) error) (uint64, error)
	Stream(context.Context, func(gmail.PushNotification) error) error
	Label(context.Context, string) (mail.Label, error)
	ThreadState(context.Context, string) (mail.ThreadSummary, mail.Thread, error)
}

type notificationWorker struct {
	ctx             context.Context
	cancel          context.CancelFunc
	done            chan struct{}
	service         pushService
	version         int
	email           string
	mu              sync.Mutex
	stopping        bool
	wg              sync.WaitGroup
	reauthAttempted bool
}

func (a *App) SetNotificationHandlers(badge func(int), notify func(string, string, string)) {
	a.setBadge = badge
	a.notify = notify
}

func (a *App) startNotifications() {
	if a.accountTransition != nil || a.account.Demo || a.notificationsStarted || a.notification != nil {
		return
	}
	service, ok := a.service.(pushService)
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker := &notificationWorker{
		ctx: ctx, cancel: cancel, done: make(chan struct{}), service: service,
		version: a.serviceVersion, email: normalizedEmail(a.account.Email),
	}
	worker.wg.Add(1)
	a.notification = worker
	a.notificationDone = nil
	a.notificationsStarted = true
	go func() {
		<-worker.ctx.Done()
		worker.wg.Wait()
		close(worker.done)
	}()
	a.schedule(func() {
		defer worker.wg.Done()
		a.runNotifications(worker)
	})
}

func (a *App) stopNotifications() <-chan struct{} {
	worker := a.notification
	a.notification = nil
	a.notificationsStarted = false
	if worker == nil {
		if a.notificationDone != nil {
			done := a.notificationDone
			a.notificationDone = nil
			return done
		}
		done := make(chan struct{})
		close(done)
		return done
	}
	a.notificationDone = worker.done
	worker.mu.Lock()
	worker.stopping = true
	worker.mu.Unlock()
	worker.cancel()
	return worker.done
}

func (a *App) notificationActive(worker *notificationWorker) bool {
	return worker != nil && a.notification == worker && a.serviceVersion == worker.version
}

func (worker *notificationWorker) after(app *App, delay time.Duration, fn func()) {
	if worker.ctx.Err() != nil {
		return
	}
	app.after(delay, func() {
		worker.mu.Lock()
		if worker.stopping || worker.ctx.Err() != nil {
			worker.mu.Unlock()
			return
		}
		worker.wg.Add(1)
		worker.mu.Unlock()
		defer worker.wg.Done()
		fn()
	})
}

func (a *App) runNotifications(worker *notificationWorker) {
	service, ctx := worker.service, worker.ctx
	if err := service.EnsurePush(ctx); err != nil {
		a.handlePushError(worker, err)
		return
	}
	state, err := service.Watch(ctx)
	if err != nil {
		a.handlePushError(worker, err)
		return
	}
	synchronizer := &historySynchronizer{app: a, worker: worker, service: service, ctx: ctx, version: worker.version, email: worker.email, historyID: state.HistoryID}
	checkpoint, ok, err := a.loadCheckpoint(worker.email)
	if err == nil && ok && checkpoint < state.HistoryID {
		a.publish(func() {
			if a.notificationActive(worker) {
				a.unreadSeen = make(map[string]time.Time)
			}
		})
		if err := synchronizer.sync(checkpoint); err != nil {
			a.handlePushError(worker, err)
			return
		}
		a.publish(func() {
			if a.notificationActive(worker) {
				a.unreadSeen = nil
			}
		})
	}
	synchronizer.refreshUnread()
	if err := a.saveCheckpoint(worker.email, synchronizer.current()); err != nil {
		a.handlePushError(worker, err)
		return
	}
	worker.after(a, watchRenewInterval, func() { a.renewWatch(worker) })
	worker.after(a, historyReconcileInterval, func() { a.reconcileHistory(synchronizer) })
	backoff := time.Second
	for {
		err := service.Stream(ctx, func(notification gmail.PushNotification) error {
			if normalizedEmail(notification.EmailAddress) != worker.email {
				return nil
			}
			return synchronizer.trigger()
		})
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return
		}
		if isPushAuthError(err) || isPushConfigError(err) {
			a.handlePushError(worker, err)
			return
		}
		if !sleepContext(ctx, backoff) {
			return
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (a *App) renewWatch(worker *notificationWorker) {
	if worker.ctx.Err() != nil {
		return
	}
	if _, err := worker.service.Watch(worker.ctx); err != nil {
		a.handlePushError(worker, err)
		return
	}
	worker.after(a, watchRenewInterval, func() { a.renewWatch(worker) })
}

func (a *App) reconcileHistory(synchronizer *historySynchronizer) {
	if synchronizer.ctx.Err() != nil {
		return
	}
	if err := synchronizer.sync(synchronizer.current()); err != nil {
		a.handlePushError(synchronizer.worker, err)
		return
	}
	synchronizer.worker.after(a, historyReconcileInterval, func() { a.reconcileHistory(synchronizer) })
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (a *App) handlePushError(worker *notificationWorker, err error) {
	if err == nil {
		return
	}
	a.publish(func() {
		if !a.notificationActive(worker) {
			return
		}
		a.stopNotifications()
		if isPushConfigError(err) {
			a.notice = err.Error()
			return
		}
		if isPushAuthError(err) && !worker.reauthAttempted {
			worker.reauthAttempted = true
			a.reauthorizeGmail()
			return
		}
		a.notice = err.Error()
	})
}

func isPushAuthError(err error) bool {
	var apiErr *gmail.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 401 || apiErr.StatusCode == 403
	}
	code := status.Code(err)
	return code == codes.Unauthenticated || code == codes.PermissionDenied
}

func isPushConfigError(err error) bool {
	var apiErr *gmail.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Reason == "accessNotConfigured" || apiErr.Reason == "serviceDisabled"
	}
	return status.Code(err) == codes.FailedPrecondition
}

type historySynchronizer struct {
	app       *App
	worker    *notificationWorker
	service   pushService
	ctx       context.Context
	version   int
	email     string
	mu        sync.Mutex
	historyID uint64
	pending   bool
}

func (s *historySynchronizer) active() bool {
	if s.worker != nil {
		return s.app.notificationActive(s.worker)
	}
	return s.version == s.app.serviceVersion
}

func (s *historySynchronizer) current() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.historyID
}

func (s *historySynchronizer) trigger() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending {
		return nil
	}
	s.pending = true
	run := func() {
		if err := s.sync(s.current()); err != nil {
			if s.worker != nil {
				s.app.handlePushError(s.worker, err)
			} else {
				s.app.publish(func() {
					if s.active() {
						s.app.notice = err.Error()
					}
				})
			}
		}
	}
	if s.worker != nil {
		s.worker.after(s.app, historyBatchDelay, run)
	} else {
		s.app.after(historyBatchDelay, run)
	}
	return nil
}

func (s *historySynchronizer) sync(start uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = false
	if start > s.historyID {
		start = s.historyID
	}
	changed := make(map[string]bool)
	var changedSummaries []mail.ThreadSummary
	var deleted []string
	latest, err := s.service.History(s.ctx, start, func(page gmail.HistoryPage) error {
		for _, id := range page.ChangedThreadIDs {
			if changed[id] {
				continue
			}
			changed[id] = true
			summary, gone, err := s.cacheThread(id)
			if err != nil {
				return err
			}
			if gone {
				deleted = append(deleted, id)
			} else {
				changedSummaries = append(changedSummaries, summary)
			}
		}
		s.app.publish(func() {
			if s.active() {
				s.app.updateUnreadForAccount(s.email, page.Added)
			}
		})
		return nil
	})
	if err != nil {
		var apiErr *gmail.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 {
			return err
		}
		s.app.publish(func() {
			if s.active() {
				s.app.unreadSeen = nil
			}
		})
		s.refreshUnread()
		watch, watchErr := s.service.Watch(s.ctx)
		if watchErr != nil {
			return watchErr
		}
		latest = watch.HistoryID
	}
	if latest > s.historyID {
		s.historyID = latest
	}
	if err := s.app.saveCheckpoint(s.email, s.historyID); err != nil {
		return err
	}
	if len(changedSummaries) == 0 && len(deleted) == 0 {
		return nil
	}
	label, labelErr := s.service.Label(s.ctx, mail.Inbox)
	s.app.publish(func() {
		if !s.active() {
			return
		}
		if labelErr == nil {
			s.app.setLabel(label)
		}
		if a := s.app; a.selectedLabel == mail.Outbox {
			return
		}
		if a := s.app; a.selectedLabel == mail.Draft || a.currentQuery().Search != "" {
			a.refreshList()
		} else {
			a.applyHistoryChanges(changedSummaries, deleted)
		}
	})
	return nil
}

func (s *historySynchronizer) cacheThread(id string) (mail.ThreadSummary, bool, error) {
	summary, thread, err := s.service.ThreadState(s.ctx, id)
	if err != nil {
		var apiErr *gmail.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
			store := s.app.store
			if store == nil || s.email == "" {
				return mail.ThreadSummary{}, true, nil
			}
			version, revision := s.app.nextThreadCacheVersion(s.email, id)
			s.app.cacheState.mu.Lock()
			defer s.app.cacheState.mu.Unlock()
			if version.Load() != revision {
				return mail.ThreadSummary{}, true, nil
			}
			return mail.ThreadSummary{}, true, store.DeleteThread(s.ctx, s.email, id)
		}
		return mail.ThreadSummary{}, false, err
	}
	if store := s.app.store; store != nil && s.email != "" {
		version, revision := s.app.nextThreadCacheVersion(s.email, id)
		s.app.cacheState.mu.Lock()
		defer s.app.cacheState.mu.Unlock()
		if version.Load() != revision {
			return summary, false, nil
		}
		if err := store.SaveThread(s.ctx, s.email, thread); err != nil {
			return mail.ThreadSummary{}, false, err
		}
	}
	return summary, false, nil
}

func (s *historySynchronizer) refreshUnread() {
	threads, err := s.service.ListThreads(s.ctx, mail.Query{LabelID: mail.Inbox, Search: "is:unread", Limit: 100})
	labels, labelsErr := s.service.Labels(s.ctx)
	s.app.publish(func() {
		if !s.active() {
			return
		}
		if labelsErr == nil {
			s.app.setLabels(labels)
		}
		if err == nil {
			s.app.updateUnreadForAccount(s.email, threads)
		}
	})
}

func (a *App) setLabels(labels []mail.Label) {
	a.labels = labels
	a.updateBadge()
}

func (a *App) setLabel(label mail.Label) {
	for i := range a.labels {
		if a.labels[i].ID == label.ID {
			a.labels[i] = label
			a.updateBadge()
			if a.accountVerified {
				a.persistLabels(a.labels)
			}
			return
		}
	}
	a.labels = append(a.labels, label)
	a.updateBadge()
	if a.accountVerified {
		a.persistLabels(a.labels)
	}
}

func (a *App) updateBadge() {
	if a.setBadge == nil {
		return
	}
	count := 0
	if !a.account.Demo {
		count = labelUnread(a.labels, mail.Inbox)
	}
	a.setBadge(count)
}

func (a *App) updateUnread(threads []mail.ThreadSummary) {
	email := a.storeAccount
	if email == "" {
		email = a.account.Email
	}
	a.updateUnreadForAccount(normalizedEmail(email), threads)
}

func (a *App) updateUnreadForAccount(email string, threads []mail.ThreadSummary) {
	if a.unreadSeen == nil {
		a.unreadSeen = make(map[string]time.Time)
		for _, thread := range threads {
			if thread.Unread && containsLabel(thread.LabelIDs, mail.Inbox) {
				a.unreadSeen[thread.ID] = thread.Updated
			}
		}
		return
	}
	for _, thread := range threads {
		if !thread.Unread || !containsLabel(thread.LabelIDs, mail.Inbox) {
			continue
		}
		updated, seen := a.unreadSeen[thread.ID]
		if seen && !thread.Updated.After(updated) {
			continue
		}
		a.unreadSeen[thread.ID] = thread.Updated
		if a.notify != nil {
			title := thread.Senders
			if title == "" {
				title = locales.NewMessage()
			}
			a.notify(notificationID(email, thread.ID), title, thread.Subject)
		}
	}
}

type notificationTarget struct {
	email    string
	threadID string
}

const notificationIDPrefix = "mica-thread-v1:"

func notificationID(email, threadID string) string {
	return notificationIDPrefix + base64.RawURLEncoding.EncodeToString([]byte(normalizedEmail(email))) + ":" + base64.RawURLEncoding.EncodeToString([]byte(threadID))
}

func parseNotificationID(id string) (notificationTarget, bool) {
	encoded, found := strings.CutPrefix(id, notificationIDPrefix)
	if !found {
		return notificationTarget{}, false
	}
	emailPart, threadPart, found := strings.Cut(encoded, ":")
	if !found {
		return notificationTarget{}, false
	}
	email, err := base64.RawURLEncoding.DecodeString(emailPart)
	if err != nil {
		return notificationTarget{}, false
	}
	threadID, err := base64.RawURLEncoding.DecodeString(threadPart)
	if err != nil || len(email) == 0 || len(threadID) == 0 {
		return notificationTarget{}, false
	}
	return notificationTarget{email: normalizedEmail(string(email)), threadID: string(threadID)}, true
}

func (a *App) OpenThread(id string) {
	target, ok := parseNotificationID(id)
	if !ok {
		return
	}
	a.pendingThread = target
	a.openPendingThread()
}

func (a *App) openPendingThread() {
	target := a.pendingThread
	if target.threadID == "" {
		return
	}
	if a.account.Demo {
		if a.store != nil {
			a.switchAccount(target.email)
		}
		return
	}
	email := normalizedEmail(a.storeAccount)
	if email == "" {
		email = normalizedEmail(a.account.Email)
	}
	if email == "" {
		return
	}
	if target.email != email {
		a.switchAccount(target.email)
		return
	}
	a.pendingThread = notificationTarget{}
	if a.selectedLabel != mail.Inbox || a.query != "" {
		a.selectedLabel = mail.Inbox
		a.query = ""
		a.clearThread()
		a.refresh()
	}
	a.selectThread(target.threadID)
}
