package app

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"mica/internal/gmail"
	"mica/internal/mail"
	mailstore "mica/internal/mail/store"
)

var errOffline = errors.New("offline")

type offlineMailService struct{}

func (offlineMailService) Account(context.Context) (mail.Account, error) {
	return mail.Account{}, errOffline
}
func (offlineMailService) Labels(context.Context) ([]mail.Label, error) {
	return nil, errOffline
}
func (offlineMailService) ListThreads(context.Context, mail.Query) ([]mail.ThreadSummary, error) {
	return nil, errOffline
}
func (offlineMailService) Thread(context.Context, string) (mail.Thread, error) {
	return mail.Thread{}, errOffline
}
func (offlineMailService) ModifyThread(context.Context, string, []string, []string) error {
	return errOffline
}
func (offlineMailService) TrashThread(context.Context, string) error   { return errOffline }
func (offlineMailService) UntrashThread(context.Context, string) error { return errOffline }
func (offlineMailService) Send(context.Context, mail.Compose) error    { return errOffline }

func TestSQLiteCacheRestoresMailOffline(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	var db *mailstore.Store
	t.Cleanup(func() {
		if db != nil {
			_ = db.Close()
		}
	})
	var err error
	db, err = mailstore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	service := newFakeService()
	service.account = mail.Account{Name: "Cached User", Email: "cached@example.com"}
	a := New(service)
	a.schedule = func(fn func()) { fn() }
	a.restoreAttempted = true
	a.notificationsStarted = true
	a.SetMailStore(db)
	a.Start()
	a.selectThread("thread-1")
	a.toggleStar()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = nil

	db, err = mailstore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(offlineMailService{})
	restarted.schedule = func(fn func()) { fn() }
	restarted.restoreAttempted = true
	restarted.SetMailStore(db)
	restarted.storeAccount = service.account.Email
	restarted.Start()
	if restarted.account.Email != service.account.Email {
		t.Fatalf("restored account = %q, want %q", restarted.account.Email, service.account.Email)
	}
	if len(restarted.labels) == 0 || len(restarted.threads) != 1 {
		t.Fatalf("offline metadata was not restored: labels=%v threads=%v", restarted.labels, restarted.threads)
	}
	if !restarted.threads[0].Starred || restarted.threads[0].Unread {
		t.Fatalf("offline summary did not preserve actions: %+v", restarted.threads[0])
	}
	restarted.selectThread("thread-1")
	if restarted.thread.ID != "thread-1" || len(restarted.thread.Messages) != 1 {
		t.Fatalf("offline thread was not restored: %+v", restarted.thread)
	}
	if restarted.threadErr != "" {
		t.Fatalf("offline revalidation hid the cached thread: %q", restarted.threadErr)
	}
	if !containsLabel(restarted.thread.LabelIDs, mail.Starred) {
		t.Fatalf("offline thread labels = %v, want STARRED", restarted.thread.LabelIDs)
	}
}

type cacheListKey struct {
	email string
	query mail.Query
}

type cacheThreadKey struct {
	email string
	id    string
}

type fakeMailStore struct {
	account          mail.Account
	accountFound     bool
	labels           map[string][]mail.Label
	lists            map[cacheListKey][]mail.ThreadSummary
	threads          map[cacheThreadKey]mail.Thread
	preferences      map[string]string
	preferenceWrites int
	saveThreadErr    error
}

func newFakeMailStore() *fakeMailStore {
	return &fakeMailStore{
		labels:      make(map[string][]mail.Label),
		lists:       make(map[cacheListKey][]mail.ThreadSummary),
		threads:     make(map[cacheThreadKey]mail.Thread),
		preferences: make(map[string]string),
	}
}

func (s *fakeMailStore) Preference(_ context.Context, key string) (string, bool, error) {
	value, found := s.preferences[key]
	return value, found, nil
}

func (s *fakeMailStore) SavePreference(_ context.Context, key, value string) error {
	s.preferences[key] = value
	s.preferenceWrites++
	return nil
}

func (s *fakeMailStore) LastAccount(context.Context) (mail.Account, bool, error) {
	return s.account, s.accountFound, nil
}
func (s *fakeMailStore) SaveAccount(_ context.Context, account mail.Account) error {
	s.account, s.accountFound = account, true
	return nil
}
func (s *fakeMailStore) Labels(_ context.Context, email string) ([]mail.Label, bool, error) {
	labels, found := s.labels[email]
	return slices.Clone(labels), found, nil
}
func (s *fakeMailStore) SaveLabels(_ context.Context, email string, labels []mail.Label) error {
	s.labels[email] = slices.Clone(labels)
	return nil
}
func (s *fakeMailStore) ThreadList(_ context.Context, email string, query mail.Query) ([]mail.ThreadSummary, bool, error) {
	threads, found := s.lists[cacheListKey{email: email, query: query}]
	return cloneThreadSummaries(threads), found, nil
}
func (s *fakeMailStore) SaveThreadList(_ context.Context, email string, query mail.Query, threads []mail.ThreadSummary) error {
	s.lists[cacheListKey{email: email, query: query}] = cloneThreadSummaries(threads)
	return nil
}
func (s *fakeMailStore) Thread(_ context.Context, email, id string) (mail.Thread, bool, error) {
	thread, found := s.threads[cacheThreadKey{email: email, id: id}]
	return cloneMailThread(thread), found, nil
}
func (s *fakeMailStore) SaveThread(_ context.Context, email string, thread mail.Thread) error {
	if s.saveThreadErr != nil {
		return s.saveThreadErr
	}
	s.threads[cacheThreadKey{email: email, id: thread.ID}] = cloneMailThread(thread)
	return nil
}
func (s *fakeMailStore) DeleteThread(_ context.Context, email, id string) error {
	delete(s.threads, cacheThreadKey{email: email, id: id})
	return nil
}

func TestStartupSelectsCachedThreadAndKeepsItAfterRefresh(t *testing.T) {
	store := newFakeMailStore()
	account := mail.Account{Name: "Cached User", Email: "cached@example.com"}
	store.account, store.accountFound = account, true
	cachedSummary := mail.ThreadSummary{ID: "thread-1", Subject: "Cached", Updated: time.Unix(100, 0), LabelIDs: []string{mail.Inbox}}
	newSummary := mail.ThreadSummary{ID: "thread-2", Subject: "New", Updated: time.Unix(200, 0), LabelIDs: []string{mail.Inbox}}
	query := mail.Query{LabelID: mail.Inbox, Limit: 25}
	store.lists[cacheListKey{email: account.Email, query: query}] = []mail.ThreadSummary{cachedSummary}
	cachedThread := mail.Thread{
		ID: "thread-1", Subject: "Cached",
		Messages: []mail.Message{{ID: "cached-message", ThreadID: "thread-1", Text: "Cached body"}},
	}
	store.threads[cacheThreadKey{email: account.Email, id: "thread-1"}] = cachedThread
	service := newFakeService()
	service.account = account
	service.threads = []mail.ThreadSummary{newSummary, cachedSummary}
	service.thread = mail.Thread{
		ID: "thread-1", Subject: "Fresh",
		Messages: []mail.Message{{ID: "fresh-message", ThreadID: "thread-1", Text: "Fresh body"}},
	}
	a := New(service)
	a.restoreAttempted = true
	a.storeAccount = account.Email
	a.SetMailStore(store)
	var jobs, updates []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.update = func(fn func()) { updates = append(updates, fn) }
	a.Start()
	if len(jobs) != 2 {
		t.Fatalf("scheduled jobs = %d, want list and account jobs", len(jobs))
	}
	jobs[0]()
	if len(updates) != 2 {
		t.Fatalf("published list updates = %d, want cached and network updates", len(updates))
	}
	updates[0]()
	if a.selectedThread != "thread-1" || a.selectedRow != 0 {
		t.Fatalf("cached selection = %q at row %d, want thread-1 at row 0", a.selectedThread, a.selectedRow)
	}
	if len(jobs) != 3 {
		t.Fatalf("scheduled jobs after cached selection = %d, want a thread load", len(jobs))
	}
	jobs[2]()
	if len(updates) != 4 {
		t.Fatalf("published updates = %d, want cached and network thread updates", len(updates))
	}
	updates[2]()
	if a.thread.ID != "thread-1" || len(a.thread.Messages) != 1 || a.thread.Messages[0].ID != "cached-message" {
		t.Fatalf("cached detail was not restored: %+v", a.thread)
	}
	updates[1]()
	if len(a.threads) != 2 || a.threads[0].ID != "thread-2" {
		t.Fatalf("new mail was not inserted above the cached conversation: %+v", a.threads)
	}
	if a.selectedThread != "thread-1" || a.selectedRow != 1 {
		t.Fatalf("selection after refresh = %q at row %d, want thread-1 at row 1", a.selectedThread, a.selectedRow)
	}
	if a.thread.ID != "thread-1" || a.thread.Messages[0].ID != "cached-message" {
		t.Fatalf("list refresh changed the open detail: %+v", a.thread)
	}
	updates[3]()
	if a.selectedThread != "thread-1" || a.selectedRow != 1 || a.thread.ID != "thread-1" || a.thread.Messages[0].ID != "fresh-message" {
		t.Fatalf("detail revalidation changed the selection: selected=%q row=%d detail=%+v", a.selectedThread, a.selectedRow, a.thread)
	}
}

func TestCachedListCannotOverwriteFreshNetworkResult(t *testing.T) {
	store := newFakeMailStore()
	account := mail.Account{Name: "Cached User", Email: "cached@example.com"}
	store.account, store.accountFound = account, true
	query := mail.Query{LabelID: mail.Inbox, Limit: 100}
	store.lists[cacheListKey{email: account.Email, query: query}] = []mail.ThreadSummary{{ID: "thread-1", Subject: "Stale"}}
	service := newFakeService()
	service.account = account
	service.threads = []mail.ThreadSummary{{ID: "thread-1", Subject: "Fresh"}}
	a := New(service)
	a.restoreAttempted = true
	a.storeAccount = account.Email
	a.SetMailStore(store)
	var jobs, updates []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.update = func(fn func()) { updates = append(updates, fn) }
	a.Start()
	if len(jobs) != 2 {
		t.Fatalf("scheduled jobs = %d, want list and account jobs", len(jobs))
	}
	jobs[0]()
	if len(updates) != 2 {
		t.Fatalf("published updates = %d, want cached and network updates", len(updates))
	}
	updates[1]()
	updates[0]()
	if len(a.threads) != 1 || a.threads[0].Subject != "Fresh" {
		t.Fatalf("stale cache replaced the network list: %+v", a.threads)
	}
}

func TestCachedThreadRefreshKeepsScrollAndFreshContent(t *testing.T) {
	for _, test := range []struct {
		name  string
		order []int
	}{
		{name: "cache then network", order: []int{0, 1}},
		{name: "late cache", order: []int{1, 0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newFakeMailStore()
			email := "cached@example.com"
			store.threads[cacheThreadKey{email: email, id: "thread-1"}] = mail.Thread{
				ID: "thread-1", Subject: "Stale", Messages: []mail.Message{{ID: "stale", Text: "Stale"}},
			}
			service := newFakeService()
			service.thread = mail.Thread{ID: "thread-1", Subject: "Fresh", Messages: []mail.Message{{ID: "fresh", Text: "Fresh"}}}
			a := New(service)
			a.storeAccount = email
			a.SetMailStore(store)
			var jobs, updates []func()
			a.schedule = func(fn func()) { jobs = append(jobs, fn) }
			a.update = func(fn func()) { updates = append(updates, fn) }
			a.selectThread("thread-1")
			if len(jobs) != 1 {
				t.Fatalf("scheduled jobs = %d, want 1", len(jobs))
			}
			jobs[0]()
			if len(updates) != 2 {
				t.Fatalf("published updates = %d, want cached and network updates", len(updates))
			}
			for step, index := range test.order {
				updates[index]()
				if step == 0 {
					a.readerScroll.Y = 42
				}
			}
			if a.thread.Subject != "Fresh" || a.thread.Messages[0].ID != "fresh" {
				t.Fatalf("fresh thread was not retained: %+v", a.thread)
			}
			if a.readerScroll.Y != 42 {
				t.Fatalf("reader scroll = %v, want 42", a.readerScroll.Y)
			}
		})
	}
}

type failingModifyService struct {
	*fakeService
}

func (s *failingModifyService) ModifyThread(context.Context, string, []string, []string) error {
	return errOffline
}

func TestFailedActionDoesNotPersistOptimisticState(t *testing.T) {
	service := &failingModifyService{fakeService: newFakeService()}
	service.account = mail.Account{Name: "Cached User", Email: "cached@example.com"}
	store := newFakeMailStore()
	a := New(service)
	a.schedule = func(fn func()) { fn() }
	a.restoreAttempted = true
	a.notificationsStarted = true
	a.SetMailStore(store)
	a.Start()
	a.selectThread("thread-1")
	a.toggleStar()
	cached, found, err := store.ThreadList(context.Background(), service.account.Email, a.currentQuery())
	if err != nil || !found || len(cached) != 1 {
		t.Fatalf("cached list = %+v, found=%v, err=%v", cached, found, err)
	}
	if cached[0].Starred {
		t.Fatal("failed star action was persisted")
	}
}

func TestCacheWriteFromOldServiceIsDropped(t *testing.T) {
	store := newFakeMailStore()
	a := New(newFakeService())
	a.restoreAttempted = true
	a.SetMailStore(store)
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.persistAccount(mail.Account{Email: "old@example.com"})
	if len(jobs) != 1 {
		t.Fatalf("scheduled writes = %d, want 1", len(jobs))
	}
	a.useService(mail.NewDemo())
	jobs[0]()
	if store.accountFound {
		t.Fatal("old service updated the last account after an account switch")
	}
}

func TestPaneWidthsRestoreAndSave(t *testing.T) {
	store := newFakeMailStore()
	store.preferences[sidebarWidthPreference] = "240"
	store.preferences[listWidthPreference] = "400"
	a := New(newFakeService())
	a.SetMailStore(store)
	a.RestorePaneWidths()
	if a.sidebarWidth != 240 || a.listWidth != 400 {
		t.Fatalf("restored widths = %v, %v, want 240 and 400", a.sidebarWidth, a.listWidth)
	}
	var delayed []func()
	a.after = func(duration time.Duration, fn func()) {
		if duration != paneWidthSaveDelay {
			t.Fatalf("save delay = %v, want %v", duration, paneWidthSaveDelay)
		}
		delayed = append(delayed, fn)
	}
	a.sidebarWidth, a.listWidth = 250, 410
	a.savePaneWidths()
	a.sidebarWidth, a.listWidth = 260, 420
	a.savePaneWidths()
	if len(delayed) != 2 {
		t.Fatalf("scheduled saves = %d, want 2", len(delayed))
	}
	for _, fn := range delayed {
		fn()
	}
	if store.preferenceWrites != 2 {
		t.Fatalf("preference writes = %d, want only the final width pair", store.preferenceWrites)
	}
	if store.preferences[sidebarWidthPreference] != "260" || store.preferences[listWidthPreference] != "420" {
		t.Fatalf("saved widths = %q, %q", store.preferences[sidebarWidthPreference], store.preferences[listWidthPreference])
	}
}

func TestPaneWidthsRejectInvalidValues(t *testing.T) {
	store := newFakeMailStore()
	store.preferences[sidebarWidthPreference] = "80"
	store.preferences[listWidthPreference] = "invalid"
	a := New(newFakeService())
	a.SetMailStore(store)
	a.RestorePaneWidths()
	if a.sidebarWidth != 204 || a.listWidth != 342 {
		t.Fatalf("invalid widths replaced defaults: %v, %v", a.sidebarWidth, a.listWidth)
	}
}

type historyPageService struct {
	*fakeService
	history    func(uint64, func(gmail.HistoryPage) error) (uint64, error)
	listCalls  int
	labelCalls int
}

func (s *historyPageService) History(_ context.Context, start uint64, fn func(gmail.HistoryPage) error) (uint64, error) {
	return s.history(start, fn)
}

func (s *historyPageService) ListThreads(ctx context.Context, query mail.Query) ([]mail.ThreadSummary, error) {
	s.listCalls++
	return s.fakeService.ListThreads(ctx, query)
}

func (s *historyPageService) Label(ctx context.Context, id string) (mail.Label, error) {
	s.labelCalls++
	return s.fakeService.Label(ctx, id)
}

func newHistoryCacheHarness() (*notificationHarness, *fakeMailStore, *historyPageService) {
	h := newNotificationHarness()
	h.app.startupSelectionDone = true
	store := newFakeMailStore()
	h.app.store = store
	h.app.storeAccount = h.service.account.Email
	h.app.accountVerified = true
	service := &historyPageService{fakeService: h.service}
	h.app.service = service
	return h, store, service
}

func TestHistorySyncSkipsEmptyRefresh(t *testing.T) {
	h, _, service := newHistoryCacheHarness()
	service.history = func(_ uint64, fn func(gmail.HistoryPage) error) (uint64, error) {
		if err := fn(gmail.HistoryPage{}); err != nil {
			return 0, err
		}
		return 6, nil
	}
	synchronizer := &historySynchronizer{app: h.app, service: service, ctx: context.Background(), email: h.service.account.Email, historyID: 5}
	if err := synchronizer.sync(5); err != nil {
		t.Fatal(err)
	}
	if service.listCalls != 0 || service.labelCalls != 0 {
		t.Fatalf("empty history refreshed data: list=%d label=%d", service.listCalls, service.labelCalls)
	}
}

func TestHistorySyncUpdatesOnlyChangedThread(t *testing.T) {
	h, _, service := newHistoryCacheHarness()
	first := h.service.threads[0]
	second := mail.ThreadSummary{ID: "thread-2", Subject: "New mail", Senders: "Bob", Updated: first.Updated.Add(time.Minute), LabelIDs: []string{mail.Inbox, mail.Unread}, Unread: true}
	h.service.threads = append([]mail.ThreadSummary{second}, h.service.threads...)
	h.app.threads = append([]mail.ThreadSummary(nil), h.service.threads...)
	service.history = func(_ uint64, fn func(gmail.HistoryPage) error) (uint64, error) {
		if err := fn(gmail.HistoryPage{ChangedThreadIDs: []string{"thread-2"}}); err != nil {
			return 0, err
		}
		return 6, nil
	}
	synchronizer := &historySynchronizer{app: h.app, service: service, ctx: context.Background(), email: h.service.account.Email, historyID: 5}
	if err := synchronizer.sync(5); err != nil {
		t.Fatal(err)
	}
	if service.listCalls != 0 || service.labelCalls != 1 {
		t.Fatalf("incremental update performed a full refresh: list=%d label=%d", service.listCalls, service.labelCalls)
	}
	if len(h.app.threads) != 2 || h.app.threads[0].ID != "thread-2" {
		t.Fatalf("updated list = %+v, want new thread first", h.app.threads)
	}
}

func TestHistorySyncSearchRefreshesList(t *testing.T) {
	h, _, service := newHistoryCacheHarness()
	h.app.query = "invoice"
	service.history = func(_ uint64, fn func(gmail.HistoryPage) error) (uint64, error) {
		if err := fn(gmail.HistoryPage{ChangedThreadIDs: []string{"thread-1"}}); err != nil {
			return 0, err
		}
		return 6, nil
	}
	synchronizer := &historySynchronizer{app: h.app, service: service, ctx: context.Background(), email: h.service.account.Email, historyID: 5}
	if err := synchronizer.sync(5); err != nil {
		t.Fatal(err)
	}
	if service.listCalls != 1 {
		t.Fatalf("search list requests = %d, want one server refresh", service.listCalls)
	}
}

func TestHistoryTriggerCoalescesPushNotifications(t *testing.T) {
	h, _, service := newHistoryCacheHarness()
	var delayed []func()
	h.app.after = func(_ time.Duration, fn func()) { delayed = append(delayed, fn) }
	historyCalls := 0
	service.history = func(_ uint64, fn func(gmail.HistoryPage) error) (uint64, error) {
		historyCalls++
		if err := fn(gmail.HistoryPage{ChangedThreadIDs: []string{"thread-1"}}); err != nil {
			return 0, err
		}
		return 6, nil
	}
	synchronizer := &historySynchronizer{app: h.app, service: service, ctx: context.Background(), email: h.service.account.Email, historyID: 5}
	for range 3 {
		if err := synchronizer.trigger(); err != nil {
			t.Fatal(err)
		}
	}
	if len(delayed) != 1 {
		t.Fatalf("scheduled syncs = %d, want one coalesced sync", len(delayed))
	}
	delayed[0]()
	if historyCalls != 1 {
		t.Fatalf("history calls = %d, want one after three notifications", historyCalls)
	}
}

func TestHistorySyncPersistsCompleteChangedThreads(t *testing.T) {
	h, store, service := newHistoryCacheHarness()
	var fetched []string
	h.service.threadFunc = func(id string) (mail.Thread, error) {
		fetched = append(fetched, id)
		return mail.Thread{ID: id, Subject: "Synced " + id, Messages: []mail.Message{{ID: "message-" + id}}}, nil
	}
	service.history = func(_ uint64, fn func(gmail.HistoryPage) error) (uint64, error) {
		if err := fn(gmail.HistoryPage{ChangedThreadIDs: []string{"thread-1", "thread-2", "thread-1"}}); err != nil {
			return 0, err
		}
		return 6, nil
	}
	synchronizer := &historySynchronizer{app: h.app, service: service, ctx: context.Background(), email: h.service.account.Email, historyID: 5}
	if err := synchronizer.sync(6); err != nil {
		t.Fatal(err)
	}
	if want := []string{"thread-1", "thread-2"}; !reflect.DeepEqual(fetched, want) {
		t.Fatalf("fetched threads = %v, want %v", fetched, want)
	}
	for _, id := range []string{"thread-1", "thread-2"} {
		thread, found, err := store.Thread(context.Background(), h.service.account.Email, id)
		if err != nil || !found || thread.Subject != "Synced "+id || len(thread.Messages) != 1 {
			t.Fatalf("cached %s = %+v, found=%v, err=%v", id, thread, found, err)
		}
	}
	if h.saved != 6 {
		t.Fatalf("checkpoint = %d, want 6", h.saved)
	}
}

func TestHistorySyncCacheFailureKeepsCheckpoint(t *testing.T) {
	h, store, service := newHistoryCacheHarness()
	store.saveThreadErr = errors.New("database full")
	service.history = func(_ uint64, fn func(gmail.HistoryPage) error) (uint64, error) {
		if err := fn(gmail.HistoryPage{ChangedThreadIDs: []string{"thread-1"}}); err != nil {
			return 0, err
		}
		return 6, nil
	}
	synchronizer := &historySynchronizer{app: h.app, service: service, ctx: context.Background(), email: h.service.account.Email, historyID: 5}
	if err := synchronizer.sync(5); !errors.Is(err, store.saveThreadErr) {
		t.Fatalf("sync error = %v, want %v", err, store.saveThreadErr)
	}
	if synchronizer.current() != 5 || h.saved != 0 {
		t.Fatalf("checkpoint advanced after cache failure: current=%d saved=%d", synchronizer.current(), h.saved)
	}
}

func TestHistorySyncDeletesMissingThread(t *testing.T) {
	h, store, service := newHistoryCacheHarness()
	email := h.service.account.Email
	store.threads[cacheThreadKey{email: email, id: "thread-1"}] = mail.Thread{ID: "thread-1"}
	h.service.threadFunc = func(string) (mail.Thread, error) {
		return mail.Thread{}, &gmail.APIError{StatusCode: 404}
	}
	service.history = func(_ uint64, fn func(gmail.HistoryPage) error) (uint64, error) {
		if err := fn(gmail.HistoryPage{ChangedThreadIDs: []string{"thread-1"}}); err != nil {
			return 0, err
		}
		return 6, nil
	}
	synchronizer := &historySynchronizer{app: h.app, service: service, ctx: context.Background(), email: email, historyID: 5}
	if err := synchronizer.sync(5); err != nil {
		t.Fatal(err)
	}
	_, found, err := store.Thread(context.Background(), email, "thread-1")
	if err != nil || found {
		t.Fatalf("deleted thread still cached: found=%v, err=%v", found, err)
	}
}

func TestNotificationsIgnoreNonInboxHistory(t *testing.T) {
	h := newNotificationHarness()
	h.app.Start()
	h.app.updateUnread([]mail.ThreadSummary{{
		ID: "sent-1", Subject: "Outgoing", Unread: true, LabelIDs: []string{mail.Sent},
	}})
	if len(h.notifications) != 0 {
		t.Fatalf("non-Inbox history produced notifications: %v", h.notifications)
	}
}
