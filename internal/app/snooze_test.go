package app

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"mica/internal/mail"
	"mica/locales"
)

func cloneSnoozeRecord(record mail.SnoozeRecord) mail.SnoozeRecord {
	record.Summary.LabelIDs = slices.Clone(record.Summary.LabelIDs)
	return record
}

func (s *fakeMailStore) SaveSnooze(_ context.Context, email string, record mail.SnoozeRecord) error {
	if s.snoozeErr != nil {
		return s.snoozeErr
	}
	record.AccountEmail = normalizedEmail(email)
	record.WakeAt = record.WakeAt.UTC()
	record.Summary.AccountEmail = record.AccountEmail
	record.Summary.SnoozeAt = record.WakeAt
	if record.Summary.ID == "" {
		record.Summary.ID = record.ThreadID
	}
	s.snoozes[snoozeKey(record.AccountEmail, record.ThreadID)] = cloneSnoozeRecord(record)
	return nil
}

func (s *fakeMailStore) SnoozeRecords(_ context.Context, email string) ([]mail.SnoozeRecord, error) {
	s.snoozeReads++
	if s.snoozeErr != nil {
		return nil, s.snoozeErr
	}
	var records []mail.SnoozeRecord
	for _, record := range s.snoozes {
		if normalizedEmail(record.AccountEmail) == normalizedEmail(email) {
			records = append(records, cloneSnoozeRecord(record))
		}
	}
	slices.SortFunc(records, func(a, b mail.SnoozeRecord) int {
		if a.WakeAt.Equal(b.WakeAt) {
			if a.ThreadID < b.ThreadID {
				return -1
			}
			if a.ThreadID > b.ThreadID {
				return 1
			}
			return 0
		}
		if a.WakeAt.Before(b.WakeAt) {
			return -1
		}
		return 1
	})
	return records, nil
}

func (s *fakeMailStore) SnoozeRecord(_ context.Context, email, threadID string, wakeAt time.Time) (mail.SnoozeRecord, bool, error) {
	if s.snoozeErr != nil {
		return mail.SnoozeRecord{}, false, s.snoozeErr
	}
	record, found := s.snoozes[snoozeKey(email, threadID)]
	if !found || !record.WakeAt.Equal(wakeAt) {
		return mail.SnoozeRecord{}, false, nil
	}
	return cloneSnoozeRecord(record), true, nil
}

func (s *fakeMailStore) DeleteSnooze(_ context.Context, email, threadID string, wakeAt time.Time) error {
	if s.snoozeDeleteErr != nil {
		return s.snoozeDeleteErr
	}
	if s.snoozeErr != nil {
		return s.snoozeErr
	}
	key := snoozeKey(email, threadID)
	record, found := s.snoozes[key]
	if !found || !record.WakeAt.Equal(wakeAt) {
		return errors.New("snooze conflict")
	}
	delete(s.snoozes, key)
	return nil
}

func (s *fakeMailStore) CancelSnooze(_ context.Context, email, threadID string) error {
	if s.snoozeDeleteErr != nil {
		return s.snoozeDeleteErr
	}
	if s.snoozeErr != nil {
		return s.snoozeErr
	}
	delete(s.snoozes, snoozeKey(email, threadID))
	return nil
}

type snoozeService struct {
	*fakeService
	modifyErr error
	onModify  func([]string, []string)
}

func (s *snoozeService) ModifyThread(ctx context.Context, id string, add, remove []string) error {
	if s.onModify != nil {
		s.onModify(add, remove)
	}
	if s.modifyErr != nil {
		return s.modifyErr
	}
	return s.fakeService.ModifyThread(ctx, id, add, remove)
}

func newSnoozeApp(service mail.Service, store *fakeMailStore, after ...func(time.Duration, func())) *App {
	a := New(service)
	a.schedule = func(fn func()) { fn() }
	a.restoreAttempted = true
	a.notificationsStarted = true
	if len(after) > 0 {
		a.after = after[0]
	}
	a.SetMailStore(store)
	a.Start()
	return a
}

func TestSnoozePersistsBeforeRemote(t *testing.T) {
	store := newFakeMailStore()
	service := &snoozeService{fakeService: newFakeService()}
	a := newSnoozeApp(service, store)
	wakeAt := time.Now().Add(24 * time.Hour)
	service.onModify = func(add, remove []string) {
		if len(store.snoozes) != 1 {
			t.Errorf("remote modification happened before snooze persistence: %v", store.snoozes)
		}
		if len(add) != 0 || len(remove) != 1 || remove[0] != mail.Inbox {
			t.Errorf("remote labels = +%v -%v, want only -INBOX", add, remove)
		}
	}
	a.snoozeUntil(a.threads, wakeAt)
	if len(a.snoozeRuntime.records) != 1 || len(store.snoozes) != 1 {
		t.Fatalf("snooze was not retained: runtime=%v store=%v", a.snoozeRuntime.records, store.snoozes)
	}
	for _, record := range store.snoozes {
		if !record.WakeAt.Equal(wakeAt) || !record.Summary.SnoozeAt.Equal(wakeAt) || record.Summary.ID != "thread-1" {
			t.Fatalf("snooze record = %+v, want %v", record, wakeAt)
		}
	}
	if len(a.threads) != 0 {
		t.Fatalf("snoozed conversation remained in Inbox: %+v", a.threads)
	}
}

func TestSnoozeRestoresOfflineViewAndWakes(t *testing.T) {
	store := newFakeMailStore()
	wakeAt := time.Now().Add(-time.Hour)
	if err := store.SaveSnooze(context.Background(), "demo@example.com", mail.SnoozeRecord{
		ThreadID: "thread-1", WakeAt: wakeAt,
		Summary: mail.ThreadSummary{ID: "thread-1", Subject: "Deferred", LabelIDs: []string{mail.Snoozed}},
	}); err != nil {
		t.Fatal(err)
	}
	service := &offlineSearchService{fakeService: newFakeService()}
	var timer func()
	a := newSnoozeApp(service, store, func(_ time.Duration, fn func()) { timer = fn })
	a.selectedLabel = mail.Snoozed
	a.refreshList()
	if len(a.threads) != 1 || !a.threads[0].SnoozeAt.Equal(wakeAt) || a.err != locales.OfflineResults() {
		t.Fatalf("offline Snoozed view = %+v err=%q", a.threads, a.err)
	}
	if timer == nil {
		t.Fatal("restored snooze was not scheduled")
	}
	timer()
	if len(store.snoozes) != 0 || len(a.snoozeRuntime.records) != 0 || len(a.threads) != 0 {
		t.Fatalf("snooze did not complete: store=%v runtime=%v threads=%v", store.snoozes, a.snoozeRuntime.records, a.threads)
	}
	if len(service.add) != 1 || service.add[0] != mail.Inbox || len(service.remove) != 0 {
		t.Fatalf("wake labels = +%v -%v, want only +INBOX", service.add, service.remove)
	}
}

func TestSnoozeFailureRestoresInbox(t *testing.T) {
	store := newFakeMailStore()
	service := &snoozeService{fakeService: newFakeService(), modifyErr: errOffline}
	a := newSnoozeApp(service, store)
	a.snoozeUntil(a.threads, time.Now().Add(time.Hour))
	if len(store.snoozes) != 0 || len(a.snoozeRuntime.records) != 0 || len(a.snoozeRuntime.timers) != 0 {
		t.Fatalf("failed snooze left pending work: store=%v runtime=%v timers=%v", store.snoozes, a.snoozeRuntime.records, a.snoozeRuntime.timers)
	}
	if len(a.threads) != 1 || !containsLabel(a.threads[0].LabelIDs, mail.Inbox) || !a.threads[0].SnoozeAt.IsZero() {
		t.Fatalf("failed snooze did not restore Inbox: %+v", a.threads)
	}
	if a.notice != locales.SnoozeFailed(errOffline.Error()) {
		t.Fatalf("failure notice = %q", a.notice)
	}
}

func TestSnoozeRescheduleUsesLatestTimer(t *testing.T) {
	store := newFakeMailStore()
	service := &snoozeService{fakeService: newFakeService()}
	var jobs []func()
	a := newSnoozeApp(service, store, func(_ time.Duration, fn func()) { jobs = append(jobs, fn) })
	a.snoozeUntil(a.threads, time.Now().Add(time.Hour))
	var record mail.SnoozeRecord
	for _, current := range a.snoozeRuntime.records {
		record = current
	}
	a.snoozeUntil([]mail.ThreadSummary{record.Summary}, time.Now().Add(2*time.Hour))
	if len(jobs) != 2 {
		t.Fatalf("scheduled jobs = %d, want old and replacement", len(jobs))
	}
	beforeStale := len(service.modified)
	jobs[0]()
	if len(store.snoozes) != 1 || len(service.modified) != beforeStale {
		t.Fatalf("stale timer fired: store=%v modifications=%v", store.snoozes, service.modified)
	}
	jobs[1]()
	if len(store.snoozes) != 0 || len(service.modified) != beforeStale+1 {
		t.Fatalf("replacement timer did not complete exactly once: store=%v modifications=%v", store.snoozes, service.modified)
	}
	if service.add[0] != mail.Inbox {
		t.Fatalf("replacement wake labels = +%v -%v", service.add, service.remove)
	}
}

func TestSnoozeQuitDoesNotWrite(t *testing.T) {
	store := newFakeMailStore()
	service := &snoozeService{fakeService: newFakeService()}
	a := newSnoozeApp(service, store)
	a.snoozeUntil(a.threads, time.Now().Add(time.Hour))
	beforeQuit := len(service.modified)
	a.WaitForCacheWrites()
	for _, record := range store.snoozes {
		a.wakeSnooze(record)
	}
	reads := store.snoozeReads
	a.restoreSnoozes()
	if len(service.modified) != beforeQuit || len(store.snoozes) != 1 || store.snoozeReads != reads {
		t.Fatalf("quit allowed snooze work: modifications=%v store=%v reads=%d", service.modified, store.snoozes, store.snoozeReads)
	}
	cache := a.snoozeCache("demo@example.com")
	if cache == nil {
		t.Fatal("snooze cache was not configured")
	}
	if err := cache(func(snoozeStore) error { return nil }); !errors.Is(err, errAccountClientUnavailable) {
		t.Fatalf("cache operation after quit = %v", err)
	}
}

func TestSnoozePresets(t *testing.T) {
	zone := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, 10, 23, 15, 0, 0, 0, zone)
	presets := snoozePresets(now)
	if len(presets) != 3 {
		t.Fatalf("presets = %v", presets)
	}
	for i, want := range []time.Weekday{time.Saturday, time.Saturday, time.Monday} {
		if got := presets[i].wakeAt; got.Weekday() != want || got.Hour() != 8 || got.Location() != zone {
			t.Errorf("preset %d = %v, want %v at 08:00 in %v", i, got, want, zone)
		}
	}
}

func TestSnoozeStoreFailurePreventsRemote(t *testing.T) {
	store := newFakeMailStore()
	store.snoozeErr = errOffline
	service := &snoozeService{fakeService: newFakeService()}
	a := newSnoozeApp(service, store)
	before := len(service.modified)
	a.snoozeUntil(a.threads, time.Now().Add(time.Hour))
	if len(service.modified) != before {
		t.Fatal("remote modification ran after snooze persistence failed")
	}
	if len(store.snoozes) != 0 || len(a.threads) != 1 || !containsLabel(a.threads[0].LabelIDs, mail.Inbox) {
		t.Fatalf("failed persistence changed local state: store=%v threads=%v", store.snoozes, a.threads)
	}
}

func TestSnoozeRestoreDoesNotOverrideLocalChange(t *testing.T) {
	store := newFakeMailStore()
	oldWake := time.Now().Add(time.Hour)
	if err := store.SaveSnooze(context.Background(), "demo@example.com", mail.SnoozeRecord{ThreadID: "thread-1", WakeAt: oldWake, Summary: mail.ThreadSummary{ID: "thread-1", Subject: "Old"}}); err != nil {
		t.Fatal(err)
	}
	service := &snoozeService{fakeService: newFakeService()}
	a := newSnoozeApp(service, store)
	var jobs, updates []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.update = func(fn func()) { updates = append(updates, fn) }
	a.restoreSnoozes()
	newWake := oldWake.Add(time.Hour)
	a.snoozeOne(mail.ThreadSummary{ID: "thread-1", AccountEmail: "demo@example.com", LabelIDs: []string{mail.Inbox}}, newWake)
	if len(jobs) != 2 || len(updates) != 0 {
		t.Fatalf("unexpected restore setup: jobs=%d updates=%d", len(jobs), len(updates))
	}
	jobs[0]()
	jobs[1]()
	if len(updates) != 2 {
		t.Fatalf("restore updates = %d, want restore and mutation completion", len(updates))
	}
	updates[1]()
	updates[0]()
	record := a.snoozeRuntime.records[snoozeKey("demo@example.com", "thread-1")]
	if !record.WakeAt.Equal(newWake) {
		t.Fatalf("stale restore replaced the new snooze: %+v", record)
	}
}

func TestSnoozeUnifiedOwnerAndRemoval(t *testing.T) {
	store := newFakeMailStore()
	serviceA := &snoozeService{fakeService: newFakeService()}
	serviceB := &snoozeService{fakeService: newFakeService()}
	a := newOwnerResourceApp(serviceA)
	a.schedule = func(fn func()) { fn() }
	a.SetMailStore(store)
	a.registerAccountClient(serviceB, mail.Account{Email: "b@example.com"}, nil, true)
	a.compositeThreads = true
	a.threads = []mail.ThreadSummary{{ID: "shared", AccountEmail: "b@example.com", LabelIDs: []string{mail.Inbox}}}
	a.snoozeUntil(a.threads, time.Now().Add(time.Hour))
	if len(serviceA.modified) != 0 || len(serviceB.modified) != 1 {
		t.Fatalf("snooze routed to the wrong owner: A=%v B=%v", serviceA.modified, serviceB.modified)
	}
	record := store.snoozes[snoozeKey("b@example.com", "shared")]
	a.compositeThreads = false
	a.wakeSnooze(record)
	if len(store.snoozes) != 0 || len(serviceB.modified) != 2 {
		t.Fatalf("unified wake did not use owner B: store=%v B=%v", store.snoozes, serviceB.modified)
	}
	if err := store.SaveSnooze(context.Background(), record.AccountEmail, record); err != nil {
		t.Fatal(err)
	}
	a.setSnoozeRecord(record)
	a.removeAccountClient("b@example.com")
	a.wakeSnooze(record)
	if len(store.snoozes) != 1 || len(serviceB.modified) != 2 {
		t.Fatalf("removed owner was modified or deleted: store=%v B=%v", store.snoozes, serviceB.modified)
	}
}

func TestSnoozeUnsnoozeFailureRestoresRecord(t *testing.T) {
	store := newFakeMailStore()
	service := &snoozeService{fakeService: newFakeService()}
	a := newSnoozeApp(service, store)
	a.snoozeUntil(a.threads, time.Now().Add(time.Hour))
	a.selectedLabel = mail.Snoozed
	a.refreshList()
	service.modifyErr = errOffline
	a.unsnooze(a.threads)
	if len(store.snoozes) != 1 || len(a.snoozeRuntime.records) != 1 || len(a.snoozeRuntime.timers) != 1 {
		t.Fatalf("failed Unsnooze lost pending state: store=%v runtime=%v timers=%v", store.snoozes, a.snoozeRuntime.records, a.snoozeRuntime.timers)
	}
	if len(a.threads) != 1 || !containsLabel(a.threads[0].LabelIDs, mail.Snoozed) {
		t.Fatalf("failed Unsnooze did not restore Snoozed view: %+v", a.threads)
	}
	if a.notice != locales.SnoozeFailed(errOffline.Error()) {
		t.Fatalf("Unsnooze failure notice = %q", a.notice)
	}
}

func TestSnoozeKeepsCurrentSummaryMetadata(t *testing.T) {
	store := newFakeMailStore()
	service := &snoozeService{fakeService: newFakeService()}
	a := newSnoozeApp(service, store)
	a.snoozeUntil(a.threads, time.Now().Add(time.Hour))
	var record mail.SnoozeRecord
	for _, current := range store.snoozes {
		record = current
	}
	latest := record.Summary
	latest.Unread = false
	latest.Starred = true
	latest.Snippet = "Latest reply"
	latest.LabelIDs = []string{mail.Starred, mail.Snoozed}
	service.threads = []mail.ThreadSummary{latest}
	a.selectedLabel = mail.Snoozed
	a.refreshList()
	if len(a.threads) != 1 || a.threads[0].Unread || !a.threads[0].Starred || a.threads[0].Snippet != "Latest reply" {
		t.Fatalf("stale snooze metadata replaced the current summary: %+v", a.threads)
	}
}

func TestSnoozeWakeRestoresInbox(t *testing.T) {
	store := newFakeMailStore()
	service := &snoozeService{fakeService: newFakeService()}
	var timer func()
	a := newSnoozeApp(service, store, func(_ time.Duration, fn func()) { timer = fn })
	a.snoozeUntil(a.threads, time.Now().Add(-time.Hour))
	if timer == nil {
		t.Fatal("snooze was not scheduled")
	}
	timer()
	if len(store.snoozes) != 0 || len(a.snoozeRuntime.records) != 0 {
		t.Fatalf("wake did not clean up: store=%v runtime=%v", store.snoozes, a.snoozeRuntime.records)
	}
	if len(a.threads) != 1 || !containsLabel(a.threads[0].LabelIDs, mail.Inbox) || !a.threads[0].SnoozeAt.IsZero() {
		t.Fatalf("wake did not restore the current Inbox: %+v", a.threads)
	}
}

func TestSnoozeCleanupFailureRetries(t *testing.T) {
	store := newFakeMailStore()
	service := &snoozeService{fakeService: newFakeService()}
	var jobs []func()
	a := newSnoozeApp(service, store, func(_ time.Duration, fn func()) { jobs = append(jobs, fn) })
	a.snoozeUntil(a.threads, time.Now().Add(-time.Hour))
	var record mail.SnoozeRecord
	for _, current := range store.snoozes {
		record = current
	}
	store.snoozeDeleteErr = errOffline
	a.wakeSnooze(record)
	if len(a.snoozeRuntime.records) != 1 || len(a.snoozeRuntime.timers) != 1 || len(store.snoozes) != 1 {
		t.Fatalf("cleanup failure discarded pending cleanup: runtime=%v timers=%v store=%v", a.snoozeRuntime.records, a.snoozeRuntime.timers, store.snoozes)
	}
	store.snoozeDeleteErr = nil
	if len(jobs) == 0 {
		t.Fatal("cleanup failure did not schedule a retry")
	}
	jobs[len(jobs)-1]()
	if len(a.snoozeRuntime.records) != 0 || len(a.snoozeRuntime.timers) != 0 || len(store.snoozes) != 0 {
		t.Fatalf("cleanup retry did not finish: runtime=%v timers=%v store=%v", a.snoozeRuntime.records, a.snoozeRuntime.timers, store.snoozes)
	}
	if len(a.threads) != 1 || !containsLabel(a.threads[0].LabelIDs, mail.Inbox) {
		t.Fatalf("cleanup retry lost the restored Inbox row: %+v", a.threads)
	}
}

func TestSnoozeUnsnoozeRestoresInbox(t *testing.T) {
	store := newFakeMailStore()
	service := &snoozeService{fakeService: newFakeService()}
	a := newSnoozeApp(service, store)
	a.snoozeUntil(a.threads, time.Now().Add(time.Hour))
	var record mail.SnoozeRecord
	for _, current := range store.snoozes {
		record = current
	}
	a.unsnooze([]mail.ThreadSummary{record.Summary})
	if len(store.snoozes) != 0 || len(a.threads) != 1 || !containsLabel(a.threads[0].LabelIDs, mail.Inbox) {
		t.Fatalf("Unsnooze did not restore Inbox: store=%v threads=%v", store.snoozes, a.threads)
	}
}

func TestSnoozeStaleOfflineSnapshotRemoved(t *testing.T) {
	store := newFakeMailStore()
	wakeAt := time.Now().Add(-time.Hour)
	query := mail.Query{Search: "in:snoozed", Limit: threadPageSize}
	store.lists[cacheListKey{email: "demo@example.com", query: query}] = []mail.ThreadSummary{{
		ID: "thread-1", Subject: "Completed", SnoozeAt: wakeAt, LabelIDs: []string{mail.Snoozed},
	}}
	a := newSnoozeApp(&offlineSearchService{fakeService: newFakeService()}, store)
	a.selectedLabel = mail.Snoozed
	a.refreshList()
	if len(a.threads) != 0 {
		t.Fatalf("completed snooze returned from an offline snapshot: %+v", a.threads)
	}
}

func TestSnoozeRefreshConcurrentAccountRegistration(t *testing.T) {
	store := newFakeMailStore()
	service := &snoozeService{fakeService: newFakeService()}
	a := newSnoozeApp(service, store)
	a.selectedLabel = mail.Snoozed
	var jobs sync.WaitGroup
	a.schedule = func(fn func()) {
		jobs.Add(1)
		go func() {
			defer jobs.Done()
			fn()
		}()
	}
	var updatesMu sync.Mutex
	var updates []func()
	a.update = func(fn func()) {
		updatesMu.Lock()
		updates = append(updates, fn)
		updatesMu.Unlock()
	}
	for i := 0; i < 40; i++ {
		a.refreshList()
		a.registerAccountClient(&snoozeService{fakeService: newFakeService()}, mail.Account{Email: "b@example.com"}, nil, true)
	}
	jobs.Wait()
	for _, update := range updates {
		update()
	}
}

func TestSnoozeZeroWakeSortsLast(t *testing.T) {
	now := time.Now()
	got := mergeSnoozedSummaries(
		[]mail.ThreadSummary{{ID: "native", Updated: now, LabelIDs: []string{mail.Snoozed}}},
		[]mail.SnoozeRecord{{ThreadID: "scheduled", WakeAt: now.Add(time.Hour), Summary: mail.ThreadSummary{ID: "scheduled", LabelIDs: []string{mail.Snoozed}}}},
		true,
	)
	if len(got) != 2 || got[0].ID != "scheduled" || got[1].ID != "native" {
		t.Fatalf("native snooze was not ordered after local schedules: %+v", got)
	}
}

func TestSnoozeLocalOnlyKeepsCurrentMetadata(t *testing.T) {
	store := newFakeMailStore()
	service := &snoozeService{fakeService: newFakeService()}
	a := newSnoozeApp(service, store)
	a.snoozeUntil(a.threads, time.Now().Add(time.Hour))
	a.selectedLabel = mail.Snoozed
	a.refreshList()
	a.selectThread("thread-1")
	key := snoozeKey("demo@example.com", "thread-1")
	stale := store.snoozes[key]
	stale.Summary.Unread = true
	stale.Summary.LabelIDs = append(stale.Summary.LabelIDs, mail.Unread)
	store.snoozes[key] = stale
	a.setUnread(false)
	store.lists = make(map[cacheListKey][]mail.ThreadSummary)
	service.threads = nil
	a.refreshList()
	if len(a.threads) != 1 || a.threads[0].Unread {
		t.Fatalf("local-only snooze restored stale unread metadata: %+v", a.threads)
	}
}

func TestMutationJobUsesOwnerGeneration(t *testing.T) {
	service := &snoozeService{fakeService: newFakeService()}
	a := newOwnerResourceApp(service)
	a.schedule = func(fn func()) { fn() }
	a.compositeThreads = false
	a.registerAccountClient(service, mail.Account{Email: "b@example.com"}, nil, true)
	var completed error
	a.enqueueMutation(mutationJob{
		service: service,
		owner:   "b@example.com",
		id:      "",
		done:    func(err error) { completed = err },
	})
	if completed != nil {
		t.Fatalf("owner-scoped mutation completed as stale: %v", completed)
	}
}

func TestSnoozeInvisibleOwnerNotice(t *testing.T) {
	store := newFakeMailStore()
	serviceA := &snoozeService{fakeService: newFakeService()}
	serviceB := &snoozeService{fakeService: newFakeService()}
	a := newOwnerResourceApp(serviceA)
	a.schedule = func(fn func()) { fn() }
	a.SetMailStore(store)
	a.registerAccountClient(serviceB, mail.Account{Email: "b@example.com"}, nil, true)
	a.compositeThreads = true
	a.threads = []mail.ThreadSummary{{ID: "shared", AccountEmail: "b@example.com", LabelIDs: []string{mail.Inbox}}}
	a.snoozeUntil(a.threads, time.Now().Add(time.Hour))
	a.compositeThreads = false
	a.notice = ""
	record := store.snoozes[snoozeKey("b@example.com", "shared")]
	a.wakeSnooze(record)
	if a.notice != "" {
		t.Fatalf("invisible owner changed the active account notice: %q", a.notice)
	}
}

func TestSnoozeUnifiedWakeRestoresStream(t *testing.T) {
	h := newUnifiedHarness()
	summary := unifiedSummary("shared", "b@example.com", time.Now().Unix())
	summary.LabelIDs = []string{mail.Inbox}
	h.setPages("a@example.com", mail.ThreadPage{})
	h.setPages("b@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{summary}})
	h.app.refreshUnified()
	h.app.snoozeUntil([]mail.ThreadSummary{summary}, time.Now().Add(time.Hour))
	key := summaryKey(summary)
	if !h.app.unified.removed[key] {
		t.Fatal("snooze did not create the Unified removal tombstone")
	}
	record := h.store.snoozes[snoozeKey("b@example.com", "shared")]
	h.setPages("b@example.com", mail.ThreadPage{})
	h.app.wakeSnooze(record)
	if h.app.unified.removed[key] {
		t.Fatal("wake left the Unified removal tombstone")
	}
	for _, summary := range h.app.threads {
		if summaryKey(summary) == key && containsLabel(summary.LabelIDs, mail.Inbox) {
			return
		}
	}
	t.Fatalf("Unified wake did not restore the stream row: %+v", h.app.threads)
}

func TestSnoozeRestoringResume(t *testing.T) {
	store := newFakeMailStore()
	service := &snoozeService{fakeService: newFakeService()}
	a := newSnoozeApp(service, store)
	a.snoozeUntil(a.threads, time.Now().Add(24*time.Hour))
	var record mail.SnoozeRecord
	for _, current := range store.snoozes {
		record = current
	}
	store.snoozeDeleteErr = errOffline
	a.unsnooze([]mail.ThreadSummary{record.Summary})
	record = store.snoozes[snoozeKey("demo@example.com", "thread-1")]
	if !record.Restoring {
		t.Fatal("failed cleanup did not persist Restoring")
	}
	store.snoozeDeleteErr = nil
	restarted := newSnoozeApp(service, store)
	if len(store.snoozes) != 0 || len(restarted.snoozeRuntime.records) != 0 {
		t.Fatalf("restart did not finish pending cleanup: store=%v runtime=%v", store.snoozes, restarted.snoozeRuntime.records)
	}
	if len(restarted.threads) != 1 || !containsLabel(restarted.threads[0].LabelIDs, mail.Inbox) {
		t.Fatalf("restart restored Snoozed instead of Inbox: %+v", restarted.threads)
	}
}
