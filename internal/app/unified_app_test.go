package app

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
)

type unifiedFakeService struct {
	*fakeService
	drafts          *draftTestService
	attachments     map[string][]byte
	attachmentCalls []string
	pages           []mail.ThreadPage
	modifyErr       error
	listErr         error
	pageErr         map[string]error
	listCalls       []mail.Query
}

func newUnifiedFakeService(email string) *unifiedFakeService {
	s := &unifiedFakeService{
		fakeService: newFakeService(),
		drafts:      newDraftTestService(),
		attachments: make(map[string][]byte),
		pageErr:     make(map[string]error),
	}
	s.account = mail.Account{Email: email}
	return s
}

func (s *unifiedFakeService) ListThreadPage(_ context.Context, query mail.Query) (mail.ThreadPage, error) {
	s.listCalls = append(s.listCalls, query)
	if s.listErr != nil {
		return mail.ThreadPage{}, s.listErr
	}
	if err := s.pageErr[query.PageToken]; err != nil {
		return mail.ThreadPage{}, err
	}
	if len(s.pages) == 0 {
		return mail.ThreadPage{}, nil
	}
	if query.PageToken == "" {
		return s.pages[0], nil
	}
	for i, page := range s.pages {
		if page.NextPageToken == query.PageToken && i+1 < len(s.pages) {
			return s.pages[i+1], nil
		}
	}
	return mail.ThreadPage{}, nil
}

func (s *unifiedFakeService) ModifyThread(ctx context.Context, id string, add, remove []string) error {
	if s.modifyErr != nil {
		s.modified = append(s.modified, id)
		return s.modifyErr
	}
	return s.fakeService.ModifyThread(ctx, id, add, remove)
}

func (s *unifiedFakeService) Drafts(ctx context.Context, query mail.Query) (mail.DraftPage, error) {
	return s.drafts.Demo.Drafts(ctx, query)
}

func (s *unifiedFakeService) Draft(ctx context.Context, id string) (mail.DraftRecord, error) {
	return s.drafts.Draft(ctx, id)
}

func (s *unifiedFakeService) CreateDraft(ctx context.Context, compose mail.Compose) (mail.DraftRecord, error) {
	return s.drafts.CreateDraft(ctx, compose)
}

func (s *unifiedFakeService) UpdateDraft(ctx context.Context, id string, compose mail.Compose) (mail.DraftRecord, error) {
	return s.drafts.UpdateDraft(ctx, id, compose)
}

func (s *unifiedFakeService) DeleteDraft(ctx context.Context, id string) error {
	return s.drafts.DeleteDraft(ctx, id)
}

func (s *unifiedFakeService) SendDraft(ctx context.Context, id string, compose mail.Compose) error {
	s.drafts.sendDraftCalls++
	if s.drafts.sendDraftErr != nil {
		return s.drafts.sendDraftErr
	}
	if err := mail.ValidateComposeForSend(compose); err != nil {
		return err
	}
	if _, err := s.drafts.UpdateDraft(ctx, id, compose); err != nil {
		return err
	}
	s.sent = mail.CloneCompose(compose)
	return s.drafts.DeleteDraft(ctx, id)
}

func (s *unifiedFakeService) Attachment(ctx context.Context, messageID, attachmentID string) ([]byte, error) {
	key := messageID + "/" + attachmentID
	s.attachmentCalls = append(s.attachmentCalls, key)
	if data, found := s.attachments[key]; found {
		return append([]byte(nil), data...), nil
	}
	return s.drafts.Demo.Attachment(ctx, messageID, attachmentID)
}

type unifiedHarness struct {
	app      *App
	store    *fakeMailStore
	services map[string]*unifiedFakeService
	query    mail.Query
}

func newUnifiedHarness() *unifiedHarness {
	h := &unifiedHarness{services: make(map[string]*unifiedFakeService), query: mail.Query{LabelID: mail.Inbox, Limit: threadPageSize}}
	a := New(newFakeService())
	a.storeAccount = "a@example.com"
	a.account = mail.Account{Email: "a@example.com"}
	a.accountVerified = true
	a.restoreAttempted = true
	a.selectedLabel = mail.Unified
	a.schedule = func(fn func()) { fn() }
	h.store = newFakeMailStore()
	for _, email := range []string{"a@example.com", "b@example.com"} {
		h.services[email] = newUnifiedFakeService(email)
		if err := h.store.SaveAccount(context.Background(), mail.Account{Email: email}); err != nil {
			panic(err)
		}
	}
	if err := h.store.SetLastAccount(context.Background(), "a@example.com"); err != nil {
		panic(err)
	}
	a.service = h.services["a@example.com"]
	a.SetMailStore(h.store)
	a.accountCount = 2
	a.accountService = func(_ context.Context, email string) (mail.Service, error) {
		return h.services[email], nil
	}
	h.app = a
	return h
}

func (h *unifiedHarness) setPages(email string, pages ...mail.ThreadPage) {
	h.services[email].pages = pages
}

func TestUnifiedRefreshMergesAccounts(t *testing.T) {
	h := newUnifiedHarness()
	h.store.lists[cacheListKey{email: "a@example.com", query: h.query}] = []mail.ThreadSummary{unifiedSummary("a-cached", "a@example.com", 10)}
	h.store.lists[cacheListKey{email: "b@example.com", query: h.query}] = []mail.ThreadSummary{unifiedSummary("b-cached", "b@example.com", 20)}
	h.setPages("a@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedSummary("a-new", "a@example.com", 30)}})
	h.setPages("b@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedSummary("b-new", "b@example.com", 25)}})
	h.app.refreshUnified()
	if h.app.loading || h.app.err != "" || len(h.app.threads) != 2 {
		t.Fatalf("unified refresh state: loading=%v err=%q threads=%+v", h.app.loading, h.app.err, h.app.threads)
	}
	if h.app.threads[0].ID != "a-new" || h.app.threads[1].ID != "b-new" {
		t.Fatalf("network pages did not replace cached snapshots: %+v", h.app.threads)
	}
	if h.app.threads[0].AccountEmail != "a@example.com" || h.app.threads[1].AccountEmail != "b@example.com" {
		t.Fatalf("merged owners = %+v", h.app.threads)
	}
	if h.app.accountCount != 2 || h.app.currentQuery().LabelID != mail.Inbox {
		t.Fatalf("unified query/count = %+v count=%d", h.app.currentQuery(), h.app.accountCount)
	}
}

func TestUnifiedLoadMoreUsesCacheBeforeFetching(t *testing.T) {
	h := newUnifiedHarness()
	aThreads := make([]mail.ThreadSummary, 0, 20)
	for i := 0; i < 20; i++ {
		aThreads = append(aThreads, unifiedSummary("a-"+string(rune('a'+i)), "a@example.com", int64(100-i)))
	}
	bThreads := make([]mail.ThreadSummary, 0, 10)
	for i := 0; i < 10; i++ {
		bThreads = append(bThreads, unifiedSummary("b-"+string(rune('a'+i)), "b@example.com", int64(50-i)))
	}
	h.setPages("a@example.com", mail.ThreadPage{Threads: aThreads, NextPageToken: "a-next"}, mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedSummary("a-next-1", "a@example.com", 1), unifiedSummary("a-next-2", "a@example.com", 2)}})
	h.setPages("b@example.com", mail.ThreadPage{Threads: bThreads})
	h.app.refreshUnified()
	if len(h.app.threads) != 25 {
		t.Fatalf("initial merged size = %d, want 25", len(h.app.threads))
	}
	h.app.loadMoreUnified()
	if len(h.app.threads) != 30 || len(h.services["a@example.com"].listCalls) != 1 {
		t.Fatalf("cached remainder was not used first: threads=%d calls=%d", len(h.app.threads), len(h.services["a@example.com"].listCalls))
	}
	h.app.loadMoreUnified()
	if len(h.app.threads) != 32 {
		t.Fatalf("next page was not merged: threads=%d", len(h.app.threads))
	}
	calls := h.services["a@example.com"].listCalls
	if len(calls) != 2 || calls[1].PageToken != "a-next" {
		t.Fatalf("next-page calls = %+v", calls)
	}
	if len(h.services["b@example.com"].listCalls) != 1 {
		t.Fatalf("B was fetched unnecessarily: %v", h.services["b@example.com"].listCalls)
	}
}

func TestUnifiedHistoryStartsNewGeneration(t *testing.T) {
	h := newUnifiedHarness()
	h.setPages("a@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedSummary("a-old", "a@example.com", 10)}})
	h.setPages("b@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedSummary("b-old", "b@example.com", 20)}})
	h.app.refreshUnified()
	oldRequest, oldState := h.app.request, h.app.unified
	h.app.selectedThread = threadRefForAccount("b@example.com", "b-old").key()
	h.setPages("a@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedSummary("a-new", "a@example.com", 30)}})
	h.app.applyHistoryChanges(nil, nil)
	if h.app.request <= oldRequest || h.app.unified == oldState {
		t.Fatal("history did not start a new merge generation")
	}
	if h.app.selectedThread != threadRefForAccount("b@example.com", "b-old").key() {
		t.Fatalf("history moved selection to %q", h.app.selectedThread)
	}
	if len(h.app.threads) != 2 || h.app.threads[0].ID != "a-new" {
		t.Fatalf("history did not refresh merged content: %+v", h.app.threads)
	}
}

func TestUnifiedPartialFailureKeepsCachedRows(t *testing.T) {
	h := newUnifiedHarness()
	h.store.lists[cacheListKey{email: "a@example.com", query: h.query}] = []mail.ThreadSummary{unifiedSummary("a-cached", "a@example.com", 10)}
	h.store.lists[cacheListKey{email: "b@example.com", query: h.query}] = []mail.ThreadSummary{unifiedSummary("b-cached", "b@example.com", 20)}
	h.services["a@example.com"].listErr = errOffline
	h.setPages("b@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedSummary("b-new", "b@example.com", 30)}})
	h.app.refreshUnified()
	if h.app.err == "" || len(h.app.threads) != 2 || h.app.threads[0].ID != "b-new" || h.app.threads[1].ID != "a-cached" {
		t.Fatalf("partial failure discarded usable content: err=%q threads=%+v", h.app.err, h.app.threads)
	}
	h.app.loadMoreUnified()
	if len(h.app.threads) != 2 {
		t.Fatal("load more retried after an error")
	}
}

func TestUnifiedSidebarAndOwnerReader(t *testing.T) {
	h := newUnifiedHarness()
	summary := unifiedSummary("b-subject", "b@example.com", time.Now().Unix())
	summary.Subject, summary.Senders, summary.Snippet = "B subject", "Bob", "B body"
	h.setPages("b@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{summary}})
	h.setPages("a@example.com", mail.ThreadPage{})
	h.app.refreshUnified()
	tt := ui.NewTester(h.app.View, 1280, 800)
	tt.SetLocale("en")
	if !tt.HasText("All Inboxes") || !tt.HasText("b@example.com") {
		t.Fatalf("unified navigation or owner is missing: %q", tt.Texts())
	}
	if err := tt.Click("B subject"); err != nil {
		t.Fatal(err)
	}
	if h.app.selectedThread != threadRefForAccount("b@example.com", "b-subject").key() || h.app.thread.AccountEmail != "b@example.com" {
		t.Fatalf("owner reader did not open B: selected=%q thread=%+v", h.app.selectedThread, h.app.thread)
	}
}

func TestUnifiedLabelHiddenForSingleAccount(t *testing.T) {
	h := newUnifiedHarness()
	h.app.accountCount = 1
	h.app.selectedLabel = mail.Inbox
	h.app.compositeThreads = false
	tt := ui.NewTester(h.app.View, 1280, 800)
	if tt.HasText("All Inboxes") {
		t.Fatal("single-account sidebar exposed All Inboxes")
	}
}

func TestUnifiedLoadMoreFailureDoesNotRetry(t *testing.T) {
	h := newUnifiedHarness()
	h.setPages("a@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedSummary("a-1", "a@example.com", 10), unifiedSummary("a-2", "a@example.com", 9)}, NextPageToken: "a-next"})
	h.services["a@example.com"].pageErr["a-next"] = errOffline
	h.setPages("b@example.com", mail.ThreadPage{})
	h.app.refreshUnified()
	if len(h.services["a@example.com"].listCalls) != 1 {
		t.Fatal("initial page was not loaded")
	}
	h.app.loadMoreUnified()
	if h.app.err == "" || len(h.services["a@example.com"].listCalls) != 2 {
		t.Fatalf("next-page failure was not handled: err=%q calls=%v", h.app.err, h.services["a@example.com"].listCalls)
	}
	if h.app.loadingMore {
		t.Fatal("load more remained busy after failure")
	}
	h.app.loadMoreUnified()
	if len(h.services["a@example.com"].listCalls) != 2 {
		t.Fatal("failed next page was retried")
	}
}

func unifiedInboxSummary(id, email string, updated int64) mail.ThreadSummary {
	summary := unifiedSummary(id, email, updated)
	summary.Subject = id
	summary.Senders = email
	summary.LabelIDs = []string{mail.Inbox}
	return summary
}

func unifiedAccountThread(email, id string) mail.Thread {
	return mail.Thread{ID: id, LabelIDs: []string{mail.Inbox}, Messages: []mail.Message{{
		ID: id + "-message", ThreadID: id, From: "sender@example.com", To: email,
		Subject: id, Text: id + " body",
	}}}
}

func unifiedContainsThread(threads []mail.ThreadSummary, email, id string) bool {
	key := threadRefForAccount(email, id).key()
	for _, thread := range threads {
		if threadRefForAccount(thread.AccountEmail, thread.ID).key() == key {
			return true
		}
	}
	return false
}

func TestUnifiedLeavingRestoresMailboxRequests(t *testing.T) {
	h := newUnifiedHarness()
	h.setPages("a@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedInboxSummary("a-1", "a@example.com", 10)}})
	h.app.refreshUnified()
	h.app.selectedLabel = mail.Inbox
	h.app.selectLabel()
	if h.app.compositeThreads || h.app.unified != nil {
		t.Fatal("leaving All Inboxes retained composite state")
	}
	if h.app.loading || h.app.err != "" {
		t.Fatalf("ordinary mailbox refresh did not finish: loading=%v err=%q", h.app.loading, h.app.err)
	}
}

func TestUnifiedStarPreservesCompleteAccountSnapshots(t *testing.T) {
	h := newUnifiedHarness()
	for _, email := range []string{"a@example.com", "b@example.com"} {
		threads := make([]mail.ThreadSummary, 30)
		for i := range threads {
			updated := int64(200 - i)
			if email == "b@example.com" {
				updated = 100 - int64(i)
			}
			threads[i] = unifiedInboxSummary(email[:1]+"-"+string(rune('a'+i)), email, updated)
		}
		h.setPages(email, mail.ThreadPage{Threads: threads, NextPageToken: email[:1] + "-next"})
	}
	h.app.refreshUnified()
	if len(h.app.threads) != threadPageSize {
		t.Fatalf("initial merged size = %d, want %d", len(h.app.threads), threadPageSize)
	}
	h.app.threadSelection.Add(threadRefForAccount("a@example.com", "a-a").key())
	h.app.toggleStar()
	for _, email := range []string{"a@example.com", "b@example.com"} {
		cached := h.store.lists[cacheListKey{email: email, query: h.query}]
		if len(cached) != 30 {
			t.Fatalf("%s cache contains %d threads after starring, want the complete 30-thread snapshot", email, len(cached))
		}
	}
}

func TestUnifiedRemovalPreservesCompleteAccountSnapshots(t *testing.T) {
	h := newUnifiedHarness()
	for _, email := range []string{"a@example.com", "b@example.com"} {
		threads := make([]mail.ThreadSummary, 30)
		for i := range threads {
			threads[i] = unifiedInboxSummary(email[:1]+"-"+string(rune('a'+i)), email, int64(100-i))
		}
		h.setPages(email, mail.ThreadPage{Threads: threads})
	}
	h.app.refreshUnified()
	h.app.removeThread(threadRefForAccount("a@example.com", "a-a").key())
	if got := len(h.store.lists[cacheListKey{email: "a@example.com", query: h.query}]); got != 29 {
		t.Fatalf("A cache contains %d threads after removal, want 29", got)
	}
	if got := len(h.store.lists[cacheListKey{email: "b@example.com", query: h.query}]); got != 30 {
		t.Fatalf("B cache contains %d threads after removing from A, want 30", got)
	}
}

func TestUnifiedArchiveRemovesOnlyOwnerThread(t *testing.T) {
	h := newUnifiedHarness()
	h.app.startupSelectionDone = true
	h.setPages("a@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedInboxSummary("a-1", "a@example.com", 10)}})
	h.setPages("b@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedInboxSummary("b-1", "b@example.com", 20)}})
	h.services["b@example.com"].threadFunc = func(id string) (mail.Thread, error) {
		return unifiedAccountThread("b@example.com", id), nil
	}
	h.app.refreshUnified()
	h.app.selectThread(threadRefForAccount("b@example.com", "b-1").key())
	h.app.archive()
	if unifiedContainsThread(h.app.threads, "b@example.com", "b-1") {
		t.Fatal("archived B thread remains in All Inboxes")
	}
	if !unifiedContainsThread(h.app.threads, "a@example.com", "a-1") {
		t.Fatal("archiving B removed A thread")
	}
	if got := h.services["b@example.com"].modified; len(got) != 1 || got[0] != "b-1" {
		t.Fatalf("B archive calls = %v", got)
	}
	if got := h.services["a@example.com"].modified; len(got) != 0 {
		t.Fatalf("archiving B modified A: %v", got)
	}
}

func TestUnifiedBatchArchiveAndUndo(t *testing.T) {
	h := newUnifiedHarness()
	h.setPages("a@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{
		unifiedInboxSummary("a-1", "a@example.com", 30), unifiedInboxSummary("a-2", "a@example.com", 10),
	}})
	h.setPages("b@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{
		unifiedInboxSummary("b-1", "b@example.com", 20), unifiedInboxSummary("b-2", "b@example.com", 5),
	}})
	h.app.refreshUnified()
	h.app.threadSelection.Add(threadRefForAccount("a@example.com", "a-1").key())
	h.app.threadSelection.Add(threadRefForAccount("b@example.com", "b-1").key())
	h.app.archive()
	if len(h.app.threads) != 2 || unifiedContainsThread(h.app.threads, "a@example.com", "a-1") || unifiedContainsThread(h.app.threads, "b@example.com", "b-1") {
		t.Fatalf("batch archive left incorrect rows: %+v", h.app.threads)
	}
	state, request := h.app.unified, h.app.request
	h.app.undoLast()
	if len(h.app.threads) != 4 || h.app.unified != state || h.app.request != request {
		t.Fatalf("batch undo did not restore locally: threads=%d state changed=%v request changed=%v", len(h.app.threads), h.app.unified != state, h.app.request != request)
	}
	for _, id := range []string{"a-1", "a-2", "b-1", "b-2"} {
		email := id[:1] + "@example.com"
		if !unifiedContainsThread(h.app.threads, email, id) {
			t.Fatalf("undo did not restore %s/%s", email, id)
		}
	}
}

func TestUnifiedHistoryKeepsInvisibleSelection(t *testing.T) {
	h := newUnifiedHarness()
	h.setPages("a@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedInboxSummary("a-1", "a@example.com", 10)}})
	h.setPages("b@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedInboxSummary("b-1", "b@example.com", 20)}})
	h.app.refreshUnified()
	key := threadRefForAccount("b@example.com", "b-1").key()
	h.app.selectedThread = key
	h.app.threadSelection.Add(key)
	h.app.thread = unifiedAccountThread("b@example.com", "b-1")
	h.setPages("b@example.com", mail.ThreadPage{})
	h.app.applyHistoryChanges(nil, nil)
	if !h.app.threadSelection.Has(key) {
		t.Fatal("history pruned an invisible Unified selection")
	}
	if h.app.selectedThread != key || h.app.thread.ID != "b-1" {
		t.Fatalf("history changed the open reader: selected=%q thread=%q", h.app.selectedThread, h.app.thread.ID)
	}
}

func TestUnifiedCacheRestorePublishesBeforeMutatingState(t *testing.T) {
	h := newUnifiedHarness()
	h.store.lists[cacheListKey{email: "a@example.com", query: h.query}] = []mail.ThreadSummary{unifiedInboxSummary("a-cached", "a@example.com", 10)}
	h.store.lists[cacheListKey{email: "b@example.com", query: h.query}] = []mail.ThreadSummary{unifiedInboxSummary("b-cached", "b@example.com", 20)}
	var jobs, updates []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	h.app.update = func(fn func()) { updates = append(updates, fn) }
	h.app.refreshUnified()
	jobs[0]()
	updates[0]()
	jobs, updates = jobs[1:], updates[1:]
	if len(jobs) != 1 {
		t.Fatalf("cache restore jobs = %d, want 1", len(jobs))
	}
	jobs[0]()
	jobs = jobs[1:]
	for email, stream := range h.app.unified.accounts {
		if len(stream.threads) != 0 {
			t.Fatalf("background cache restore mutated %s before publishing", email)
		}
	}
	if len(updates) != 1 {
		t.Fatalf("cache restore updates = %d, want 1", len(updates))
	}
	updates[0]()
	if len(h.app.threads) != 2 {
		t.Fatalf("published cache restore did not render both accounts: %+v", h.app.threads)
	}
}

func TestUnifiedRejectsRemovedAccountPage(t *testing.T) {
	h := newUnifiedHarness()
	h.setPages("a@example.com", mail.ThreadPage{})
	h.setPages("b@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedInboxSummary("b-stale", "b@example.com", 20)}})
	var jobs, updates []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	h.app.update = func(fn func()) { updates = append(updates, fn) }
	h.app.refreshUnified()
	for steps := 0; len(jobs) > 0 || len(updates) > 0; steps++ {
		if steps > 20 {
			t.Fatal("account page setup did not settle")
		}
		if len(updates) > 0 {
			updates[0]()
			updates = updates[1:]
			continue
		}
		if h.app.accountClients["b@example.com"] != nil && len(jobs) == 1 {
			break
		}
		jobs[0]()
		jobs = jobs[1:]
	}
	if len(jobs) != 1 || len(updates) != 0 {
		t.Fatalf("pending account page setup: jobs=%d updates=%d", len(jobs), len(updates))
	}
	h.app.removeAccountClient("b@example.com")
	jobs[0]()
	updates[0]()
	if unifiedContainsThread(h.app.threads, "b@example.com", "b-stale") {
		t.Fatal("removed account page resurrected its thread")
	}
	if stream := h.app.unified.accounts["b@example.com"]; stream.err == nil || len(stream.threads) != 0 {
		t.Fatalf("stale account response was accepted: %+v", stream)
	}
}

func TestUnifiedSameRawIDAndIndependentTokens(t *testing.T) {
	h := newUnifiedHarness()
	for _, email := range []string{"a@example.com", "b@example.com"} {
		threads := make([]mail.ThreadSummary, threadPageSize)
		for i := range threads {
			id := email[:1] + "-" + string(rune('a'+i))
			if i == 0 {
				id = "shared"
			}
			threads[i] = unifiedInboxSummary(id, email, int64(100-i))
		}
		h.setPages(email,
			mail.ThreadPage{Threads: threads, NextPageToken: email[:1] + "-next"},
			mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedInboxSummary(email[:1]+"-next", email, 1)}},
		)
	}
	h.app.refreshUnified()
	if len(h.app.threads) != threadPageSize || !unifiedContainsThread(h.app.threads, "a@example.com", "shared") || !unifiedContainsThread(h.app.threads, "b@example.com", "shared") {
		t.Fatalf("same raw ID was not kept per account: %+v", h.app.threads)
	}
	for range 4 {
		h.app.loadMoreUnified()
	}
	for _, email := range []string{"a@example.com", "b@example.com"} {
		calls := h.services[email].listCalls
		if len(calls) != 2 || calls[1].PageToken != email[:1]+"-next" {
			t.Fatalf("%s page calls = %+v", email, calls)
		}
	}
	if len(h.app.threads) != 2*threadPageSize+2 {
		t.Fatalf("merged second pages = %d threads, want %d", len(h.app.threads), 2*threadPageSize+2)
	}
}

func TestUnifiedAllOfflineKeepsBothCachedAccounts(t *testing.T) {
	h := newUnifiedHarness()
	h.store.lists[cacheListKey{email: "a@example.com", query: h.query}] = []mail.ThreadSummary{unifiedInboxSummary("a-cached", "a@example.com", 10)}
	h.store.lists[cacheListKey{email: "b@example.com", query: h.query}] = []mail.ThreadSummary{unifiedInboxSummary("b-cached", "b@example.com", 20)}
	for _, email := range []string{"a@example.com", "b@example.com"} {
		h.services[email].listErr = errOffline
	}
	h.app.refreshUnified()
	if h.app.err == "" || len(h.app.threads) != 2 {
		t.Fatalf("offline Unified state: err=%q threads=%+v", h.app.err, h.app.threads)
	}
	for _, email := range []string{"a@example.com", "b@example.com"} {
		if !unifiedContainsThread(h.app.threads, email, email[:1]+"-cached") {
			t.Fatalf("offline cache lost %s", email)
		}
		if len(h.store.lists[cacheListKey{email: email, query: h.query}]) != 1 {
			t.Fatalf("offline refresh discarded %s cache", email)
		}
	}
}

func TestUnifiedRenderedOwnerWorkflow(t *testing.T) {
	h := newUnifiedHarness()
	h.app.startupSelectionDone = true
	h.app.after = func(time.Duration, func()) {}
	for _, email := range []string{"a@example.com", "b@example.com"} {
		email := email
		summary := unifiedInboxSummary("shared", email, 10)
		if email == "b@example.com" {
			summary.Subject = "B subject"
			summary.Updated = summary.Updated.Add(time.Second)
		} else {
			summary.Subject = "A subject"
		}
		h.setPages(email, mail.ThreadPage{Threads: []mail.ThreadSummary{summary}})
		h.services[email].threadFunc = func(id string) (mail.Thread, error) {
			thread := unifiedAccountThread(email, id)
			thread.Subject = summary.Subject
			thread.Messages[0].MessageID = "<" + email[:1] + "@example.com>"
			if email == "b@example.com" {
				thread.Messages[0].Attachments = []mail.Attachment{{
					ID: "shared-attachment", Name: "shared.txt", MIMEType: "text/plain", Size: int64(len("B attachment")),
				}}
			}
			return thread, nil
		}
	}
	h.services["b@example.com"].attachments["shared-message/shared-attachment"] = []byte("B attachment")
	h.app.refreshUnified()
	tt := ui.NewTester(h.app.View, 1280, 800)
	tt.SetLocale("en")
	if err := tt.Click("B subject"); err != nil {
		t.Fatal(err)
	}
	key := threadRefForAccount("b@example.com", "shared").key()
	if h.app.selectedThread != key || h.app.thread.AccountEmail != "b@example.com" {
		t.Fatalf("B reader did not open from rendered list: selected=%q thread=%+v", h.app.selectedThread, h.app.thread)
	}
	if err := tt.Click("Archive"); err != nil {
		t.Fatal(err)
	}
	if unifiedContainsThread(h.app.threads, "b@example.com", "shared") {
		t.Fatal("rendered archive action left B in All Inboxes")
	}
	h.app.undoLast()
	tt.Frame()
	if !unifiedContainsThread(h.app.threads, "b@example.com", "shared") {
		t.Fatal("undo did not restore B in the rendered Unified list")
	}
	if err := tt.Click("B subject"); err != nil {
		t.Fatal(err)
	}
	h.app.openReply()
	m := h.app.compose
	if m == nil || m.accountEmail != "b@example.com" {
		t.Fatalf("reply owner = %+v", m)
	}
	m.doc.SetText(richtext.NewText("Unified reply"))
	m.revision = 1
	h.app.pumpDraftSave(m)
	if m.draftID == "" || h.services["b@example.com"].drafts.createCalls != 1 {
		t.Fatalf("B draft was not created: %+v", m)
	}
	saved, err := h.services["b@example.com"].drafts.Draft(context.Background(), m.draftID)
	if err != nil || !strings.HasPrefix(saved.Message.Text, "Unified reply\n") || saved.Message.ThreadID != "shared" || saved.Message.InReplyTo != "<b@example.com>" {
		t.Fatalf("B saved draft = %+v, err=%v", saved, err)
	}
	aDraft, err := h.services["a@example.com"].drafts.CreateDraft(context.Background(), mail.Compose{To: "a@example.com", Text: "A draft"})
	if err != nil || aDraft.ID != m.draftID {
		t.Fatalf("same-ID draft fixture: A=%q B=%q err=%v", aDraft.ID, m.draftID, err)
	}
	tt.Frame()
	if !tt.HasText("b@example.com") {
		t.Fatal("composer did not render its B account owner")
	}
	if err := tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	if h.app.compose != nil || h.services["b@example.com"].drafts.sendDraftCalls != 1 {
		t.Fatalf("B draft send did not finish: compose=%+v calls=%d", h.app.compose, h.services["b@example.com"].drafts.sendDraftCalls)
	}
	if h.services["a@example.com"].drafts.sendDraftCalls != 0 || h.services["a@example.com"].sent.To != "" || h.services["b@example.com"].sent.ThreadID != "shared" {
		t.Fatal("Unified draft send used the wrong account or lost its thread")
	}
	if unchanged, err := h.services["a@example.com"].drafts.Draft(context.Background(), aDraft.ID); err != nil || unchanged.Message.Text != "A draft" {
		t.Fatalf("B send changed A draft: %+v, err=%v", unchanged, err)
	}
	var opened []byte
	h.app.openPath = func(path string) error {
		data, err := os.ReadFile(path)
		if err == nil {
			opened = data
			_ = os.Remove(path)
		}
		return err
	}
	message := h.app.thread.Messages[0]
	h.app.downloadAttachment(message, message.Attachments[0], false, 0)
	tt.Frame()
	if string(opened) != "B attachment" {
		t.Fatalf("Unified attachment content = %q", opened)
	}
	if len(h.services["b@example.com"].attachmentCalls) != 1 || len(h.services["a@example.com"].attachmentCalls) != 0 {
		t.Fatalf("attachment calls: A=%v B=%v", h.services["a@example.com"].attachmentCalls, h.services["b@example.com"].attachmentCalls)
	}
}

func TestUnifiedRemoveAccountReturnsToSingleInbox(t *testing.T) {
	h := newAccountTransitionHarness()
	a := h.app
	a.settings = &settingsState{open: true}
	a.removeCredentials = func(context.Context, string) error { return nil }
	a.deleteCheckpoint = func(string) error { return nil }
	a.selectedLabel = mail.Unified
	a.compositeThreads = true
	a.accountCount = 2
	a.threads = []mail.ThreadSummary{
		unifiedInboxSummary("old-thread", h.old.account.Email, 10),
		unifiedInboxSummary("target-thread", h.target.account.Email, 20),
	}
	key := threadRefForAccount(h.target.account.Email, "target-thread").key()
	a.selectedThread = key
	a.threadSelection.Add(key)
	a.thread = mail.Thread{ID: "target-thread", AccountEmail: h.target.account.Email}
	a.unified = &unifiedState{accounts: map[string]*unifiedAccountStream{
		h.old.account.Email:    newUnifiedAccountStream(h.old.account.Email, a.threads[:1]),
		h.target.account.Email: newUnifiedAccountStream(h.target.account.Email, a.threads[1:]),
	}}
	a.undo = &undoAction{items: []undoItem{{id: key, summary: a.threads[1]}}}
	a.removeAccount(h.target.account.Email)
	h.runJob(t)
	h.runUpdate(t)
	for steps := 0; len(h.jobs) > 0 || len(h.updates) > 0; steps++ {
		if steps > 30 {
			t.Fatal("account removal refresh did not settle")
		}
		if len(h.updates) > 0 {
			h.runUpdate(t)
		} else {
			h.runJob(t)
		}
	}
	if a.accountTransition != nil || a.selectedLabel != mail.Inbox || a.compositeThreads || a.unified != nil || a.undo != nil {
		t.Fatalf("single-account view was not restored: transition=%p label=%q composite=%v unified=%p undo=%p", a.accountTransition, a.selectedLabel, a.compositeThreads, a.unified, a.undo)
	}
	if unifiedContainsThread(a.threads, h.target.account.Email, "target-thread") {
		t.Fatal("removed account remained in the visible list")
	}
	if a.selectedThread != "" || a.thread.ID != "" || a.threadSelection.Has(key) {
		t.Fatalf("removed account reader or selection survived: selected=%q thread=%q selection=%v", a.selectedThread, a.thread.ID, a.threadSelection.All())
	}
	if a.accountCount != 1 || a.err != "" {
		t.Fatalf("account removal refresh state: count=%d err=%q", a.accountCount, a.err)
	}
}

func TestUnifiedPersistsLatestMutationOutOfOrder(t *testing.T) {
	h := newUnifiedHarness()
	h.app.startupSelectionDone = true
	h.setPages("a@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedInboxSummary("a-1", "a@example.com", 10)}})
	h.setPages("b@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedInboxSummary("b-1", "b@example.com", 20)}})
	h.app.refreshUnified()
	var jobs []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	key := threadRefForAccount("a@example.com", "a-1").key()
	h.app.updateThreadLabels(key, []string{mail.Starred}, nil)
	h.app.removeThread(key)
	if len(jobs) != 4 {
		t.Fatalf("mutation cache writes = %d, want 4", len(jobs))
	}
	for i := len(jobs) - 1; i >= 0; i-- {
		jobs[i]()
	}
	cached := h.store.lists[cacheListKey{email: "a@example.com", query: h.query}]
	if unifiedContainsThread(cached, "a@example.com", "a-1") {
		t.Fatalf("stale cache write restored an archived thread: %+v", cached)
	}
	if got := len(h.store.lists[cacheListKey{email: "b@example.com", query: h.query}]); got != 1 {
		t.Fatalf("out-of-order A writes changed B cache: %d threads", got)
	}
}

func TestUnifiedNetworkPagePreservesLocalMutation(t *testing.T) {
	h := newUnifiedHarness()
	h.app.startupSelectionDone = true
	h.app.compositeThreads = true
	state := &unifiedState{accounts: map[string]*unifiedAccountStream{
		"a@example.com": newUnifiedAccountStream("a@example.com", []mail.ThreadSummary{unifiedInboxSummary("a-1", "a@example.com", 10)}),
		"b@example.com": newUnifiedAccountStream("b@example.com", nil),
	}, removed: make(map[string]bool), query: h.query, pending: 1}
	h.app.unified = state
	h.app.applyUnifiedMerge(state, threadPageSize)
	key := threadRefForAccount("a@example.com", "a-1").key()
	h.app.updateThreadLabels(key, []string{mail.Starred}, nil)
	h.app.finishUnifiedAccount(state, h.app.request, "a@example.com", 0, mail.ThreadPage{
		Threads: []mail.ThreadSummary{unifiedInboxSummary("a-1", "a@example.com", 10)},
	}, nil)
	if len(h.app.threads) != 1 || !h.app.threads[0].Starred {
		t.Fatalf("network page overwrote local star: %+v", h.app.threads)
	}
	cached := h.store.lists[cacheListKey{email: "a@example.com", query: h.query}]
	if len(cached) != 1 || !cached[0].Starred {
		t.Fatalf("network page overwrote cached star: %+v", cached)
	}
}

func TestUnifiedFallbackCatalogIsCapturedBeforeScheduling(t *testing.T) {
	h := newUnifiedHarness()
	a := h.app
	a.store = nil
	a.registerAccountClient(h.services["b@example.com"], mail.Account{Email: "b@example.com"}, nil, true)
	var jobs, updates []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.update = func(fn func()) { updates = append(updates, fn) }
	a.refreshUnified()
	a.accountClients = nil
	if len(jobs) != 1 {
		t.Fatalf("account catalog jobs = %d, want 1", len(jobs))
	}
	jobs[0]()
	if len(updates) != 1 {
		t.Fatalf("account catalog updates = %d, want 1", len(updates))
	}
	updates[0]()
	if a.selectedLabel != mail.Unified || a.unified == nil || len(a.unified.accounts) != 2 {
		t.Fatalf("fallback catalog read mutable registry state: label=%q unified=%+v", a.selectedLabel, a.unified)
	}
}

func TestUnifiedUnavailableAccountMutationDoesNotPanic(t *testing.T) {
	h := newUnifiedHarness()
	h.app.startupSelectionDone = true
	summary := unifiedInboxSummary("b-cached", "b@example.com", 20)
	h.store.lists[cacheListKey{email: "b@example.com", query: h.query}] = []mail.ThreadSummary{summary}
	h.app.accountService = func(context.Context, string) (mail.Service, error) { return nil, errOffline }
	h.app.refreshUnified()
	if service, _ := h.app.ownerService("b@example.com"); service != nil {
		t.Fatal("unavailable account fixture unexpectedly has a service")
	}
	key := threadRefForAccount("b@example.com", "b-cached").key()
	h.app.threadSelection.Add(key)
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		h.app.archive()
	}()
	if panicked {
		t.Fatal("archiving an unavailable cached account panicked")
	}
	if !unifiedContainsThread(h.app.threads, "b@example.com", "b-cached") || !unifiedContainsThread(h.store.lists[cacheListKey{email: "b@example.com", query: h.query}], "b@example.com", "b-cached") {
		t.Fatal("unavailable account mutation changed local or cached mail")
	}
	if h.app.mutationsPending() || h.app.notice == "" {
		t.Fatalf("unavailable mutation state: pending=%v notice=%q", h.app.mutationsPending(), h.app.notice)
	}
}

func TestUnifiedUndoClearedWhenLeaving(t *testing.T) {
	h := newUnifiedHarness()
	h.app.startupSelectionDone = true
	for _, email := range []string{"a@example.com", "b@example.com"} {
		h.setPages(email, mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedInboxSummary("shared", email, 10)}})
	}
	h.app.refreshUnified()
	h.app.threadSelection.Add(threadRefForAccount("b@example.com", "shared").key())
	h.app.archive()
	h.app.selectedLabel = mail.Inbox
	h.app.selectLabel()
	if h.app.undo != nil {
		t.Fatal("Unified Undo survived the transition to a single-account view")
	}
	h.app.undoLast()
	if unifiedContainsThread(h.app.threads, "b@example.com", "shared") {
		t.Fatal("Unified Undo inserted B into the ordinary A list")
	}
	if got := len(h.services["b@example.com"].modified); got != 1 {
		t.Fatalf("Undo after leaving Unified issued another B mutation: %d calls", got)
	}
}

func TestUnifiedTrashUndoDoesNotRestoreNonInbox(t *testing.T) {
	h := newUnifiedHarness()
	h.app.startupSelectionDone = true
	h.setPages("a@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedInboxSummary("a-1", "a@example.com", 10)}})
	h.setPages("b@example.com", mail.ThreadPage{})
	h.app.refreshUnified()
	key := threadRefForAccount("a@example.com", "a-1").key()
	h.app.threadSelection.Add(key)
	h.app.trash()
	h.app.undoLast()
	if unifiedContainsThread(h.app.threads, "a@example.com", "a-1") || unifiedContainsThread(h.app.unified.accounts["a@example.com"].threads, "a@example.com", "a-1") {
		t.Fatal("Trash Undo restored a non-Inbox thread to All Inboxes")
	}
	cached := h.store.lists[cacheListKey{email: "a@example.com", query: h.query}]
	if unifiedContainsThread(cached, "a@example.com", "a-1") {
		t.Fatal("Trash Undo wrote a non-Inbox thread to the INBOX cache")
	}
	if h.services["a@example.com"].untrashed != "a-1" {
		t.Fatalf("Untrash was not sent to the owner: %q", h.services["a@example.com"].untrashed)
	}
}

func TestUnifiedSearchUndoDoesNotInsertNonInbox(t *testing.T) {
	h := newUnifiedHarness()
	h.app.startupSelectionDone = true
	h.app.query = "project"
	summary := unifiedSummary("a-search", "a@example.com", 10)
	h.setPages("a@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{summary}})
	h.setPages("b@example.com", mail.ThreadPage{})
	h.app.refreshUnified()
	key := threadRefForAccount("a@example.com", "a-search").key()
	h.app.threadSelection.Add(key)
	h.app.toggleStar()
	h.app.query = ""
	h.setPages("a@example.com", mail.ThreadPage{})
	h.app.refresh()
	h.app.undoLast()
	if unifiedContainsThread(h.app.threads, "a@example.com", "a-search") || unifiedContainsThread(h.app.unified.accounts["a@example.com"].threads, "a@example.com", "a-search") {
		t.Fatal("search Undo inserted a non-Inbox thread into All Inboxes")
	}
}

func TestUnifiedCacheWriteVersionsCrossViews(t *testing.T) {
	h := newUnifiedHarness()
	state := &unifiedState{query: h.query, removed: make(map[string]bool)}
	stream := newUnifiedAccountStream("a@example.com", []mail.ThreadSummary{unifiedInboxSummary("old-a", "a@example.com", 10)})
	h.app.unified = state
	h.app.compositeThreads = true
	var jobs []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	h.app.persistUnifiedStream(state, stream)
	h.app.selectedLabel = mail.Inbox
	h.app.compositeThreads = false
	h.app.unified = nil
	h.app.persistList(h.query, []mail.ThreadSummary{unifiedInboxSummary("new-a", "a@example.com", 20)})
	if len(jobs) != 2 {
		t.Fatalf("cross-view cache writes = %d, want 2", len(jobs))
	}
	jobs[1]()
	jobs[0]()
	cached := h.store.lists[cacheListKey{email: "a@example.com", query: h.query}]
	if len(cached) != 1 || cached[0].ID != "new-a" {
		t.Fatalf("old Unified write overwrote the ordinary Inbox cache: %+v", cached)
	}
}

func TestUnifiedDeleteLabelOnlyCleansActiveOwner(t *testing.T) {
	h := newUnifiedHarness()
	h.app.startupSelectionDone = true
	for _, email := range []string{"a@example.com", "b@example.com"} {
		summary := unifiedInboxSummary("shared", email, 10)
		summary.LabelIDs = append(summary.LabelIDs, "Label_shared")
		h.setPages(email, mail.ThreadPage{Threads: []mail.ThreadSummary{summary}})
	}
	h.app.refreshUnified()
	h.app.labels = []mail.Label{{ID: "Label_shared", Name: "Shared"}}
	h.app.removeLabelDefinition("Label_shared")
	for _, summary := range h.app.threads {
		hasLabel := containsLabel(summary.LabelIDs, "Label_shared")
		if summary.AccountEmail == "a@example.com" && hasLabel {
			t.Fatal("active account retained the deleted label")
		}
		if summary.AccountEmail == "b@example.com" && !hasLabel {
			t.Fatal("deleting A label removed B same-ID label")
		}
	}
}

func TestUnifiedLeaveDuringMutationDoesNotExposeOwnerlessRows(t *testing.T) {
	h := newUnifiedHarness()
	h.app.startupSelectionDone = true
	for _, email := range []string{"a@example.com", "b@example.com"} {
		h.setPages(email, mail.ThreadPage{Threads: []mail.ThreadSummary{unifiedInboxSummary("shared", email, 10)}})
	}
	h.app.refreshUnified()
	var jobs []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	h.app.threadSelection.Add(threadRefForAccount("b@example.com", "shared").key())
	h.app.toggleStar()
	if !h.app.mutationsPending() {
		t.Fatal("mutation fixture did not remain pending")
	}
	previousRequest := h.app.request
	h.app.selectedLabel = mail.Inbox
	h.app.selectLabel()
	if len(h.app.threads) != 0 || !h.app.loading || h.app.request <= previousRequest {
		t.Fatalf("mode transition exposed stale rows: threads=%d loading=%v request=%d previous=%d", len(h.app.threads), h.app.loading, h.app.request, previousRequest)
	}
	if len(h.app.actionSummaries()) != 0 {
		t.Fatal("stale merged rows remained actionable during the transition")
	}
	for steps := 0; len(jobs) > 0; steps++ {
		if steps > 20 {
			t.Fatal("deferred mailbox refresh did not settle")
		}
		job := jobs[0]
		jobs = jobs[1:]
		job()
	}
	if len(h.app.threads) != 1 || h.app.threads[0].AccountEmail != "a@example.com" || unifiedContainsThread(h.app.threads, "b@example.com", "shared") {
		t.Fatalf("deferred refresh installed the wrong list: %+v", h.app.threads)
	}
}

func TestUnifiedReadFailureAfterModeChangePreservesCache(t *testing.T) {
	h := newUnifiedHarness()
	h.app.startupSelectionDone = true
	summary := unifiedInboxSummary("shared", "b@example.com", 20)
	summary.Unread = true
	summary.LabelIDs = append(summary.LabelIDs, mail.Unread)
	h.setPages("a@example.com", mail.ThreadPage{})
	h.setPages("b@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{summary}})
	h.services["b@example.com"].threadFunc = func(id string) (mail.Thread, error) {
		thread := unifiedAccountThread("b@example.com", id)
		thread.LabelIDs = []string{mail.Inbox, mail.Unread}
		thread.Messages[0].LabelIDs = []string{mail.Unread}
		return thread, nil
	}
	h.app.refreshUnified()
	var jobs []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	h.app.selectThread(threadRefForAccount("b@example.com", "shared").key())
	if len(jobs) != 1 {
		t.Fatalf("thread detail jobs = %d, want 1", len(jobs))
	}
	jobs[0]()
	jobs = jobs[1:]
	for len(jobs) > 1 {
		job := jobs[0]
		jobs = jobs[1:]
		job()
	}
	if len(jobs) != 1 || !h.app.mutationsPending() {
		t.Fatalf("automatic read did not leave one pending mutation: jobs=%d pending=%v", len(jobs), h.app.mutationsPending())
	}
	h.app.selectedLabel = mail.Inbox
	h.app.selectLabel()
	cached := unifiedInboxSummary("cached-a", "a@example.com", 10)
	h.store.lists[cacheListKey{email: "a@example.com", query: h.query}] = []mail.ThreadSummary{cached}
	h.services["b@example.com"].modifyErr = errOffline
	h.services["a@example.com"].listErr = errOffline
	h.app.schedule = func(fn func()) { fn() }
	jobs[0]()
	got := h.store.lists[cacheListKey{email: "a@example.com", query: h.query}]
	if len(got) != 1 || got[0].ID != "cached-a" {
		t.Fatalf("read failure overwrote the ordinary cache during transition: %+v", got)
	}
	ownerList := h.store.lists[cacheListKey{email: "b@example.com", query: h.query}]
	if len(ownerList) != 1 || !ownerList[0].Unread || !containsLabel(ownerList[0].LabelIDs, mail.Unread) {
		t.Fatalf("read failure did not restore the owner list's unread state: %+v", ownerList)
	}
	ownerThread := h.store.threads[cacheThreadKey{email: "b@example.com", id: "shared"}]
	if !containsLabel(ownerThread.LabelIDs, mail.Unread) || len(ownerThread.Messages) != 1 || !containsLabel(ownerThread.Messages[0].LabelIDs, mail.Unread) {
		t.Fatalf("read failure did not restore the owner detail's unread state: %+v", ownerThread)
	}
}

func TestUnifiedNewerReadSupersedesUnreadRollback(t *testing.T) {
	h := newUnifiedHarness()
	h.app.compositeThreads = true
	h.app.registerAccountClient(h.services["b@example.com"], mail.Account{Email: "b@example.com"}, nil, true)
	cached := unifiedInboxSummary("shared", "b@example.com", 20)
	cached.Unread = true
	cached.LabelIDs = append(cached.LabelIDs, mail.Unread)
	cachedThread := unifiedAccountThread("b@example.com", "shared")
	cachedThread.AccountEmail = "b@example.com"
	cachedThread.LabelIDs = []string{mail.Inbox, mail.Unread}
	cachedThread.Messages[0].LabelIDs = []string{mail.Unread}
	h.store.lists[cacheListKey{email: "b@example.com", query: h.query}] = []mail.ThreadSummary{cached}
	h.store.threads[cacheThreadKey{email: "b@example.com", id: "shared"}] = cachedThread
	var jobs []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	key := threadRefForAccount("b@example.com", "shared").key()
	h.app.restoreUnread(key, h.query)
	updated := cached
	updated.LabelIDs = modifyLabelIDs(updated.LabelIDs, nil, []string{mail.Unread})
	updated.Unread = false
	h.app.persistList(h.query, []mail.ThreadSummary{updated})
	updatedThread := cloneMailThread(cachedThread)
	updatedThread.LabelIDs = modifyLabelIDs(updatedThread.LabelIDs, nil, []string{mail.Unread})
	updatedThread.Messages[0].LabelIDs = modifyLabelIDs(updatedThread.Messages[0].LabelIDs, nil, []string{mail.Unread})
	h.app.persistThread(updatedThread)
	if len(jobs) != 4 {
		t.Fatalf("cache jobs = %d, want rollback, two owner lists, and detail", len(jobs))
	}
	for _, job := range jobs[1:] {
		job()
	}
	jobs[0]()
	ownerList := h.store.lists[cacheListKey{email: "b@example.com", query: h.query}]
	if len(ownerList) != 1 || ownerList[0].Unread || containsLabel(ownerList[0].LabelIDs, mail.Unread) {
		t.Fatalf("stale rollback restored an unread list item: %+v", ownerList)
	}
	ownerThread := h.store.threads[cacheThreadKey{email: "b@example.com", id: "shared"}]
	if containsLabel(ownerThread.LabelIDs, mail.Unread) || containsLabel(ownerThread.Messages[0].LabelIDs, mail.Unread) {
		t.Fatalf("stale rollback restored an unread detail: %+v", ownerThread)
	}
}

func TestUnifiedHistoryReadSupersedesUnreadRollback(t *testing.T) {
	h := newUnifiedHarness()
	cached := unifiedInboxSummary("shared", "a@example.com", 20)
	cached.Unread = true
	cached.LabelIDs = append(cached.LabelIDs, mail.Unread)
	cachedThread := unifiedAccountThread("a@example.com", "shared")
	cachedThread.AccountEmail = "a@example.com"
	cachedThread.LabelIDs = []string{mail.Inbox, mail.Unread}
	cachedThread.Messages[0].LabelIDs = []string{mail.Unread}
	h.store.lists[cacheListKey{email: "a@example.com", query: h.query}] = []mail.ThreadSummary{cached}
	h.store.threads[cacheThreadKey{email: "a@example.com", id: "shared"}] = cachedThread
	h.services["a@example.com"].threads = []mail.ThreadSummary{{ID: "shared", LabelIDs: []string{mail.Inbox}}}
	h.services["a@example.com"].threadFunc = func(id string) (mail.Thread, error) {
		thread := unifiedAccountThread("a@example.com", id)
		thread.AccountEmail = "a@example.com"
		return thread, nil
	}
	var jobs []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	key := threadRefForAccount("a@example.com", "shared").key()
	h.app.restoreUnread(key, h.query)
	synchronizer := historySynchronizer{app: h.app, service: h.services["a@example.com"], ctx: context.Background(), email: "a@example.com"}
	if _, _, err := synchronizer.cacheThread("shared"); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("rollback jobs = %d, want 1", len(jobs))
	}
	jobs[0]()
	ownerThread := h.store.threads[cacheThreadKey{email: "a@example.com", id: "shared"}]
	if containsLabel(ownerThread.LabelIDs, mail.Unread) || containsLabel(ownerThread.Messages[0].LabelIDs, mail.Unread) {
		t.Fatalf("stale rollback overwrote history detail: %+v", ownerThread)
	}
}
