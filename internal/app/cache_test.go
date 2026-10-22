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
func (offlineMailService) TrashThread(context.Context, string) error { return errOffline }
func (offlineMailService) Send(context.Context, mail.Compose) error  { return errOffline }

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
	history func(uint64, func(gmail.HistoryPage) error) (uint64, error)
}

func (s *historyPageService) History(_ context.Context, start uint64, fn func(gmail.HistoryPage) error) (uint64, error) {
	return s.history(start, fn)
}

func newHistoryCacheHarness() (*notificationHarness, *fakeMailStore, *historyPageService) {
	h := newNotificationHarness()
	store := newFakeMailStore()
	h.app.store = store
	h.app.storeAccount = h.service.account.Email
	h.app.accountVerified = true
	service := &historyPageService{fakeService: h.service}
	h.app.service = service
	return h, store, service
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
