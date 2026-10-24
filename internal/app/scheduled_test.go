package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"mica/internal/mail"
)

func cloneScheduledOutbox(record mail.OutboxMessage) mail.OutboxMessage {
	record.Compose = mail.CloneCompose(record.Compose)
	return record
}

func (s *fakeMailStore) ScheduledOutboxMessages(context.Context) ([]mail.OutboxMessage, error) {
	if s.outboxErr != nil {
		return nil, s.outboxErr
	}
	var records []mail.OutboxMessage
	for _, record := range s.outbox {
		if record.State == mail.OutboxScheduled || record.State == mail.OutboxSending && !record.SendAt.IsZero() {
			records = append(records, cloneScheduledOutbox(record))
		}
	}
	slices.SortFunc(records, func(a, b mail.OutboxMessage) int {
		if a.SendAt.Equal(b.SendAt) {
			return strings.Compare(scheduledKey(a.AccountEmail, a.ID), scheduledKey(b.AccountEmail, b.ID))
		}
		return a.SendAt.Compare(b.SendAt)
	})
	return records, nil
}

func (s *fakeMailStore) ClaimScheduledOutbox(_ context.Context, email string, record mail.OutboxMessage, now time.Time, force bool) (mail.OutboxMessage, bool, error) {
	if s.outboxErr != nil {
		return mail.OutboxMessage{}, false, s.outboxErr
	}
	key := fakeOutboxKey(email, record.ID)
	current, found := s.outbox[key]
	if !found || current.State != mail.OutboxScheduled || current.Attempt != record.Attempt || !current.SendAt.Equal(record.SendAt) || !force && current.SendAt.After(now) {
		return mail.OutboxMessage{}, false, nil
	}
	record.AccountEmail = normalizedEmail(email)
	record.State = mail.OutboxSending
	record.Updated = now
	record.Compose = mail.CloneCompose(record.Compose)
	s.outbox[key] = cloneScheduledOutbox(record)
	return cloneScheduledOutbox(record), true, nil
}

func (s *fakeMailStore) CancelScheduledOutbox(_ context.Context, email string, record mail.OutboxMessage) error {
	if s.outboxErr != nil {
		return s.outboxErr
	}
	key := fakeOutboxKey(email, record.ID)
	current, found := s.outbox[key]
	if !found || current.State != mail.OutboxScheduled || current.Attempt != record.Attempt || !current.SendAt.Equal(record.SendAt) {
		return errors.New("outbox conflict")
	}
	delete(s.outbox, key)
	email = normalizedEmail(email)
	if snapshot, found := s.drafts[email]; found && snapshot.OutboxID == record.ID {
		snapshot.OutboxID = ""
		s.drafts[email] = snapshot
	}
	return nil
}

func (s *fakeMailStore) RecoverScheduledOutbox(_ context.Context, email string, record mail.OutboxMessage) (bool, error) {
	if s.outboxErr != nil {
		return false, s.outboxErr
	}
	key := fakeOutboxKey(email, record.ID)
	current, found := s.outbox[key]
	if !found || current.State != mail.OutboxSending || current.Attempt != record.Attempt || !current.SendAt.Equal(record.SendAt) {
		return false, nil
	}
	record.AccountEmail = normalizedEmail(email)
	record.State = mail.OutboxUncertain
	record.Compose = mail.CloneCompose(record.Compose)
	s.outbox[key] = cloneScheduledOutbox(record)
	return true, nil
}

func scheduledTestTime() time.Time {
	return time.Date(2026, 10, 25, 15, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
}

func newScheduledHarness() (*App, *outboxOrderService, *fakeMailStore) {
	a, service, store := newOutboxOrderHarness()
	now := scheduledTestTime()
	a.now = func() time.Time { return now }
	return a, service, store
}

func restartScheduledHarness(a *App, service *outboxOrderService, store *fakeMailStore, now time.Time) (*App, []func()) {
	restarted := New(service)
	restarted.account = a.account
	restarted.storeAccount = a.storeAccount
	restarted.accountVerified = true
	restarted.restoreAttempted = true
	restarted.notificationsStarted = true
	restarted.now = func() time.Time { return now }
	restarted.SetMailStore(store)
	restarted.schedule = func(fn func()) { fn() }
	var timers []func()
	restarted.after = func(delay time.Duration, fn func()) {
		if delay != draftSaveDelay {
			timers = append(timers, fn)
		}
	}
	restarted.Start()
	return restarted, timers
}

func fireScheduledTimer(t *testing.T, a *App, timer func()) {
	t.Helper()
	records, err := a.outbox.ScheduledOutboxMessages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) > 0 {
		sendAt := records[0].SendAt
		a.now = func() time.Time { return sendAt }
	}
	timer()
}

func enqueueScheduledTestRecord(store *fakeMailStore, owner, id string, sendAt time.Time) mail.OutboxMessage {
	record := mail.OutboxMessage{
		ID: id, AccountEmail: owner, State: mail.OutboxScheduled, Attempt: 1,
		Created: sendAt.Add(-time.Hour), Updated: sendAt.Add(-time.Hour), SendAt: sendAt.UTC(),
		Compose: mail.Compose{To: "to@example.com", Subject: "Scheduled", Text: "body", MessageID: "<" + id + "@example.com>"},
	}
	if err := store.EnqueueOutbox(context.Background(), owner, record, mail.DraftSnapshot{}); err != nil {
		panic(err)
	}
	return record
}

func TestScheduledComposePersistsWithoutSending(t *testing.T) {
	a, service, store := newScheduledHarness()
	m := openOutboxTestCompose(a)
	sendAt := a.currentTime().Add(2 * time.Hour)
	a.scheduleCompose(sendAt)
	if a.compose != nil || m.outboxAttempted || service.sendCalls != 0 || service.sendDraftCalls != 0 {
		t.Fatalf("schedule sent or discarded input: compose=%p attempted=%v calls=%d/%d", a.compose, m.outboxAttempted, service.sendCalls, service.sendDraftCalls)
	}
	record := onlyOutboxRecord(t, store)
	if record.State != mail.OutboxScheduled || record.Attempt != 1 || !record.SendAt.Equal(sendAt) || record.SendAt.Location() != time.UTC {
		t.Fatalf("scheduled record = %+v", record)
	}
	if len(record.Compose.Attachments) != 1 || string(record.Compose.Attachments[0].Data) != string(m.attachments[0].attachment.Data) {
		t.Fatalf("scheduled attachments = %+v", record.Compose.Attachments)
	}
	if snapshot := store.drafts[a.storeAccount]; snapshot.OutboxID != record.ID || snapshot.Compose.MessageID != record.Compose.MessageID {
		t.Fatalf("scheduled snapshot = %+v", snapshot)
	}
}

func TestScheduledComposeWaitsForDraftSave(t *testing.T) {
	a, service, store := newScheduledHarness()
	m := openOutboxTestCompose(a)
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.markComposerEdited(m)
	a.pumpDraftSave(m)
	a.scheduleCompose(a.currentTime().Add(time.Hour))
	if len(store.outbox) != 0 || service.sendCalls != 0 || m.pendingAction != composerActionSchedule {
		t.Fatalf("schedule bypassed draft save: records=%v calls=%d action=%v", store.outbox, service.sendCalls, m.pendingAction)
	}
	for len(jobs) > 0 {
		job := jobs[0]
		jobs = jobs[1:]
		job()
	}
	record := onlyOutboxRecord(t, store)
	if record.State != mail.OutboxScheduled || record.DraftID == "" || service.sendCalls != 0 || service.sendDraftCalls != 0 {
		t.Fatalf("saved schedule = %+v calls=%d/%d", record, service.sendCalls, service.sendDraftCalls)
	}
}

func TestScheduledComposeRejectsAttemptedMessage(t *testing.T) {
	a, service, store := newScheduledHarness()
	service.sendErr = errOffline
	m := openOutboxTestCompose(a)
	a.sendCompose()
	before := onlyOutboxRecord(t, store)
	a.scheduleCompose(a.currentTime().Add(time.Hour))
	after := onlyOutboxRecord(t, store)
	if a.compose != m || m.err == "" || before.ID != after.ID || after.State != mail.OutboxUncertain || service.sendCalls != 1 {
		t.Fatalf("attempted message bypassed review: compose=%p record=%+v calls=%d", a.compose, after, service.sendCalls)
	}
}

func TestScheduledTimerSendsOnlyOnce(t *testing.T) {
	a, service, store := newScheduledHarness()
	var timers []func()
	a.after = func(_ time.Duration, fn func()) { timers = append(timers, fn) }
	openOutboxTestCompose(a)
	a.scheduleCompose(a.currentTime().Add(time.Hour))
	record := onlyOutboxRecord(t, store)
	checked := 0
	service.sentMessageExistsFunc = func(string) (bool, error) {
		checked++
		return false, nil
	}
	if len(timers) != 1 {
		t.Fatalf("scheduled timers = %d, want one", len(timers))
	}
	fireScheduledTimer(t, a, timers[0])
	fireScheduledTimer(t, a, timers[0])
	if service.sendCalls != 1 || checked != 0 || len(store.outbox) != 0 || service.sent.MessageID != record.Compose.MessageID {
		t.Fatalf("duplicate timer sent: calls=%d checks=%d records=%v sent=%+v", service.sendCalls, checked, store.outbox, service.sent)
	}
}

func TestScheduledRestartSendsOverdue(t *testing.T) {
	a, service, store := newScheduledHarness()
	openOutboxTestCompose(a)
	a.scheduleCompose(a.currentTime().Add(2 * time.Hour))
	restarted, timers := restartScheduledHarness(a, service, store, a.currentTime().Add(3*time.Hour))
	if restarted.compose != nil {
		t.Fatal("restart opened a second send path for a scheduled message")
	}
	if len(timers) != 1 {
		t.Fatalf("restored timers = %d, want one", len(timers))
	}
	fireScheduledTimer(t, a, timers[0])
	if service.sendCalls != 1 || len(store.outbox) != 0 || restarted.compose != nil {
		t.Fatalf("restart did not finish scheduled send: calls=%d records=%v compose=%+v", service.sendCalls, store.outbox, restarted.compose)
	}
}

func TestScheduledUncertainDoesNotRetry(t *testing.T) {
	a, service, store := newScheduledHarness()
	var timers []func()
	a.after = func(_ time.Duration, fn func()) { timers = append(timers, fn) }
	service.sendErr = errOffline
	openOutboxTestCompose(a)
	a.scheduleCompose(a.currentTime().Add(time.Hour))
	fireScheduledTimer(t, a, timers[0])
	if record := onlyOutboxRecord(t, store); record.State != mail.OutboxUncertain {
		t.Fatalf("failed scheduled send = %+v", record)
	}
	fireScheduledTimer(t, a, timers[0])
	if service.sendCalls != 1 || len(timers) != 1 {
		t.Fatalf("uncertain send was retried: calls=%d timers=%d", service.sendCalls, len(timers))
	}
	_, restored := restartScheduledHarness(a, service, store, a.currentTime().Add(2*time.Hour))
	if service.sendCalls != 1 || len(restored) != 0 {
		t.Fatalf("restart retried uncertain send: calls=%d timers=%d", service.sendCalls, len(restored))
	}
}

func TestScheduledRecoveredSendingRequiresReview(t *testing.T) {
	a, service, store := newScheduledHarness()
	openOutboxTestCompose(a)
	a.scheduleCompose(a.currentTime().Add(time.Hour))
	record := onlyOutboxRecord(t, store)
	record.State = mail.OutboxSending
	if err := store.SaveOutboxMessage(context.Background(), record.AccountEmail, record); err != nil {
		t.Fatal(err)
	}
	_, timers := restartScheduledHarness(a, service, store, a.currentTime().Add(2*time.Hour))
	if record := onlyOutboxRecord(t, store); record.State != mail.OutboxUncertain {
		t.Fatalf("recovered sending state = %q", record.State)
	}
	if service.sendCalls != 0 || len(timers) != 0 {
		t.Fatalf("recovery sent without review: calls=%d timers=%d", service.sendCalls, len(timers))
	}
}

func TestScheduledCancelRestoresCompleteDraft(t *testing.T) {
	a, service, store := newScheduledHarness()
	var timers []func()
	a.after = func(_ time.Duration, fn func()) { timers = append(timers, fn) }
	openOutboxTestCompose(a)
	a.scheduleCompose(a.currentTime().Add(time.Hour))
	record := onlyOutboxRecord(t, store)
	a.selectedLabel = mail.Outbox
	a.selectLabel()
	a.selectThread("outbox-" + record.ID)
	a.cancelScheduledOutbox()
	if len(store.outbox) != 0 || a.compose == nil || a.compose.outboxID != "" || a.compose.outboxAttempted {
		t.Fatalf("cancel did not detach recovery: records=%v compose=%+v", store.outbox, a.compose)
	}
	if a.compose.doc.Text().PlainText() != "body" || len(a.compose.attachments) != 1 || len(a.compose.attachments[0].attachment.Data) == 0 {
		t.Fatalf("cancel lost compose content: %+v", a.compose)
	}
	if snapshot := store.drafts[a.storeAccount]; snapshot.OutboxID != "" || snapshot.Compose.MessageID != record.Compose.MessageID {
		t.Fatalf("cancel snapshot = %+v", snapshot)
	}
	fireScheduledTimer(t, a, timers[0])
	if service.sendCalls != 0 {
		t.Fatalf("cancelled timer sent: %d", service.sendCalls)
	}
}

func TestScheduledSendNowUsesExistingDraft(t *testing.T) {
	a, service, store := newScheduledHarness()
	draft, err := service.CreateDraft(context.Background(), mail.Compose{To: "to@example.com", Text: "saved"})
	if err != nil {
		t.Fatal(err)
	}
	a.openDraftMessage(draft.ID, draft.Message)
	a.scheduleCompose(a.currentTime().Add(24 * time.Hour))
	record := onlyOutboxRecord(t, store)
	a.selectedLabel = mail.Outbox
	a.selectLabel()
	a.selectThread("outbox-" + record.ID)
	a.retryOutbox()
	if service.sendDraftCalls != 1 || service.sendCalls != 0 || len(store.outbox) != 0 {
		t.Fatalf("Send now used the wrong endpoint: draft=%d message=%d records=%v", service.sendDraftCalls, service.sendCalls, store.outbox)
	}
}

func TestScheduledSendNowRetriesClaimFailure(t *testing.T) {
	a, service, store := newScheduledHarness()
	var timers []func()
	a.after = func(_ time.Duration, fn func()) { timers = append(timers, fn) }
	openOutboxTestCompose(a)
	a.scheduleCompose(a.currentTime().Add(time.Hour))
	record := onlyOutboxRecord(t, store)
	a.selectedLabel = mail.Outbox
	a.selectLabel()
	a.selectThread("outbox-" + record.ID)
	store.outboxErr = errOffline
	a.retryOutbox()
	store.outboxErr = nil
	if len(timers) != 2 {
		t.Fatalf("claim failure timers = %d, want original and retry", len(timers))
	}
	timers[len(timers)-1]()
	if service.sendCalls != 1 || len(store.outbox) != 0 {
		t.Fatalf("Send now retry lost force before the scheduled time: calls=%d records=%v", service.sendCalls, store.outbox)
	}
}

func TestOutboxUncertainSnapshotRestoresComposer(t *testing.T) {
	a, service, store := newScheduledHarness()
	service.sendErr = errOffline
	openOutboxTestCompose(a)
	a.sendCompose()
	restarted, timers := restartScheduledHarness(a, service, store, a.currentTime().Add(time.Hour))
	if restarted.compose == nil || !restarted.compose.outboxAttempted {
		t.Fatalf("ordinary failed Outbox did not restore review composer: %+v", restarted.compose)
	}
	if service.sendCalls != 1 || len(timers) != 0 {
		t.Fatalf("ordinary Outbox restored an automatic send: calls=%d timers=%d", service.sendCalls, len(timers))
	}
}

func TestScheduledInactiveOwnerDoesNotNotify(t *testing.T) {
	store := newFakeMailStore()
	serviceA := &outboxOrderService{draftTestService: newDraftTestService(), store: store, owner: "a@example.com"}
	serviceB := &outboxOrderService{draftTestService: newDraftTestService(), store: store, owner: "b@example.com"}
	a := newOwnerResourceApp(serviceA)
	a.schedule = func(fn func()) { fn() }
	a.SetMailStore(store)
	a.registerAccountClient(serviceB, mail.Account{Email: "b@example.com"}, nil, true)
	var timers []func()
	a.after = func(_ time.Duration, fn func()) { timers = append(timers, fn) }
	record := enqueueScheduledTestRecord(store, "b@example.com", "scheduled-b", a.currentTime().Add(time.Hour))
	a.restoreScheduledOutbox()
	if len(timers) != 1 {
		t.Fatalf("owner timers = %d, want one", len(timers))
	}
	fireScheduledTimer(t, a, timers[0])
	if serviceA.sendCalls != 0 || serviceB.sendCalls != 1 || a.notice != "" {
		t.Fatalf("inactive owner sent incorrectly: A=%d B=%d notice=%q", serviceA.sendCalls, serviceB.sendCalls, a.notice)
	}
	if _, found := store.outbox[fakeOutboxKey(record.AccountEmail, record.ID)]; found {
		t.Fatal("inactive owner record was not completed")
	}
}

func TestScheduledAccountRemovalStopsTimer(t *testing.T) {
	store := newFakeMailStore()
	serviceA := &outboxOrderService{draftTestService: newDraftTestService(), store: store, owner: "a@example.com"}
	serviceB := &outboxOrderService{draftTestService: newDraftTestService(), store: store, owner: "b@example.com"}
	a := newOwnerResourceApp(serviceA)
	a.schedule = func(fn func()) { fn() }
	a.SetMailStore(store)
	a.registerAccountClient(serviceB, mail.Account{Email: "b@example.com"}, nil, true)
	var timers []func()
	a.after = func(_ time.Duration, fn func()) { timers = append(timers, fn) }
	enqueueScheduledTestRecord(store, "b@example.com", "scheduled-b", a.currentTime().Add(time.Hour))
	a.restoreScheduledOutbox()
	a.removeAccountClient("b@example.com")
	fireScheduledTimer(t, a, timers[0])
	if serviceB.sendCalls != 0 || len(store.outbox) != 1 {
		t.Fatalf("removed owner sent: calls=%d records=%v", serviceB.sendCalls, store.outbox)
	}
}

func TestScheduledQuitRejectsNewWork(t *testing.T) {
	a, service, store := newScheduledHarness()
	var timers []func()
	a.after = func(_ time.Duration, fn func()) { timers = append(timers, fn) }
	openOutboxTestCompose(a)
	a.scheduleCompose(a.currentTime().Add(time.Hour))
	a.WaitForCacheWrites()
	fireScheduledTimer(t, a, timers[0])
	a.restoreScheduledOutbox()
	if service.sendCalls != 0 || len(store.outbox) != 1 {
		t.Fatalf("quit allowed scheduled work: calls=%d records=%v", service.sendCalls, store.outbox)
	}
}

type blockingScheduledService struct {
	*outboxOrderService
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingScheduledService) Send(ctx context.Context, compose mail.Compose) error {
	s.once.Do(func() { close(s.started) })
	<-s.release
	return s.outboxOrderService.Send(ctx, compose)
}

func TestScheduledQuitWaitsForAdmittedSend(t *testing.T) {
	a, service, store := newScheduledHarness()
	var timers []func()
	a.after = func(_ time.Duration, fn func()) { timers = append(timers, fn) }
	openOutboxTestCompose(a)
	a.scheduleCompose(a.currentTime().Add(time.Hour))
	blocking := &blockingScheduledService{outboxOrderService: service, started: make(chan struct{}), release: make(chan struct{})}
	a.service = blocking
	a.schedule = func(fn func()) { go fn() }
	fireScheduledTimer(t, a, timers[0])
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("scheduled send did not start")
	}
	drained := make(chan struct{})
	go func() {
		a.WaitForCacheWrites()
		close(drained)
	}()
	select {
	case <-drained:
		t.Fatal("cache drain returned before the scheduled send finished")
	case <-time.After(20 * time.Millisecond):
	}
	close(blocking.release)
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("cache drain did not finish after the scheduled send")
	}
	if service.sendCalls != 1 || len(store.outbox) != 0 {
		t.Fatalf("admitted send did not complete: calls=%d records=%v", service.sendCalls, store.outbox)
	}
}
