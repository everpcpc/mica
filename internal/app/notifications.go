package app

import (
	"context"
	"errors"
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
	EnsurePush(context.Context) error
	Watch(context.Context) (gmail.WatchState, error)
	History(context.Context, uint64, func(gmail.HistoryPage) error) (uint64, error)
	Stream(context.Context, func(uint64) error) error
	Label(context.Context, string) (mail.Label, error)
	ThreadState(context.Context, string) (mail.ThreadSummary, mail.Thread, error)
}

func (a *App) SetNotificationHandlers(badge func(int), notify func(string, string, string)) {
	a.setBadge = badge
	a.notify = notify
}

func (a *App) startNotifications() {
	if a.account.Demo || a.notificationsStarted {
		return
	}
	service, ok := a.service.(pushService)
	if !ok {
		return
	}
	a.notificationsStarted = true
	ctx, cancel := context.WithCancel(context.Background())
	a.notificationCancel = cancel
	version, email := a.serviceVersion, a.account.Email
	a.schedule(func() { a.runNotifications(ctx, service, version, email) })
}

func (a *App) stopNotifications() {
	a.notificationsStarted = false
	if a.notificationCancel != nil {
		a.notificationCancel()
		a.notificationCancel = nil
	}
}

func (a *App) runNotifications(ctx context.Context, service pushService, version int, email string) {
	if err := service.EnsurePush(ctx); err != nil {
		a.handlePushError(err, version)
		return
	}
	state, err := service.Watch(ctx)
	if err != nil {
		a.handlePushError(err, version)
		return
	}
	synchronizer := &historySynchronizer{app: a, service: service, ctx: ctx, version: version, email: email, historyID: state.HistoryID}
	checkpoint, ok, err := a.loadCheckpoint(email)
	if err == nil && ok && checkpoint < state.HistoryID {
		a.publish(func() { a.unreadSeen = make(map[string]time.Time) })
		if err := synchronizer.sync(checkpoint); err != nil {
			a.handlePushError(err, version)
			return
		}
		a.publish(func() { a.unreadSeen = nil })
	}
	a.refreshUnreadVersion(version)
	if err := a.saveCheckpoint(email, synchronizer.current()); err != nil {
		a.handlePushError(err, version)
		return
	}
	a.after(watchRenewInterval, func() { a.renewWatch(ctx, service, version) })
	a.after(historyReconcileInterval, func() { a.reconcileHistory(ctx, synchronizer) })
	backoff := time.Second
	for {
		err := service.Stream(ctx, func(uint64) error {
			return synchronizer.trigger()
		})
		if errors.Is(err, context.Canceled) || ctx.Err() != nil || version != a.serviceVersion {
			return
		}
		if isPushAuthError(err) || isPushConfigError(err) {
			a.handlePushError(err, version)
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

func (a *App) renewWatch(ctx context.Context, service pushService, version int) {
	if ctx.Err() != nil || version != a.serviceVersion {
		return
	}
	if _, err := service.Watch(ctx); err != nil {
		a.handlePushError(err, version)
		return
	}
	a.after(watchRenewInterval, func() { a.renewWatch(ctx, service, version) })
}

func (a *App) reconcileHistory(ctx context.Context, synchronizer *historySynchronizer) {
	if ctx.Err() != nil || synchronizer.version != a.serviceVersion {
		return
	}
	if err := synchronizer.sync(synchronizer.current()); err != nil {
		a.handlePushError(err, synchronizer.version)
		return
	}
	a.after(historyReconcileInterval, func() { a.reconcileHistory(ctx, synchronizer) })
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

func (a *App) handlePushError(err error, version int) {
	if err == nil {
		return
	}
	a.stopNotifications()
	a.publish(func() {
		if version != a.serviceVersion {
			return
		}
		if isPushConfigError(err) {
			a.notice = err.Error()
			return
		}
		if isPushAuthError(err) && !a.pushReauthAttempted {
			a.pushReauthAttempted = true
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
	service   pushService
	ctx       context.Context
	version   int
	email     string
	mu        sync.Mutex
	historyID uint64
	pending   bool
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
	s.app.after(historyBatchDelay, func() {
		if err := s.sync(s.current()); err != nil {
			s.app.handlePushError(err, s.version)
		}
	})
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
			if s.version == s.app.serviceVersion {
				s.app.updateUnread(page.Added)
			}
		})
		return nil
	})
	if err != nil {
		var apiErr *gmail.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 {
			return err
		}
		s.app.publish(func() { s.app.unreadSeen = nil })
		s.app.refreshUnreadVersion(s.version)
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
		if s.version != s.app.serviceVersion {
			return
		}
		if labelErr == nil {
			s.app.setLabel(label)
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
			return mail.ThreadSummary{}, true, store.DeleteThread(s.ctx, s.email, id)
		}
		return mail.ThreadSummary{}, false, err
	}
	if store := s.app.store; store != nil && s.email != "" {
		if err := store.SaveThread(s.ctx, s.email, thread); err != nil {
			return mail.ThreadSummary{}, false, err
		}
	}
	return summary, false, nil
}

func (a *App) refreshUnreadVersion(version int) {
	service := a.service
	threads, err := service.ListThreads(context.Background(), mail.Query{LabelID: mail.Inbox, Search: "is:unread", Limit: 100})
	labels, labelsErr := service.Labels(context.Background())
	a.publish(func() {
		if version != a.serviceVersion {
			return
		}
		if labelsErr == nil {
			a.setLabels(labels)
		}
		if err == nil {
			a.updateUnread(threads)
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
			a.notify(thread.ID, title, thread.Subject)
		}
	}
}

func (a *App) OpenThread(id string) {
	if id == "" {
		return
	}
	a.pendingThread = id
	a.openPendingThread()
}

func (a *App) openPendingThread() {
	if a.pendingThread == "" || a.account.Demo {
		return
	}
	id := a.pendingThread
	a.pendingThread = ""
	if a.selectedLabel != mail.Inbox || a.query != "" {
		a.selectedLabel = mail.Inbox
		a.query = ""
		a.clearThread()
		a.refresh()
	}
	a.selectThread(id)
}
