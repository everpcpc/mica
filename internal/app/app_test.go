package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui/richtext"
)

type fakeService struct {
	account       mail.Account
	labels        []mail.Label
	threads       []mail.ThreadSummary
	thread        mail.Thread
	threadFunc    func(string) (mail.Thread, error)
	modified      []string
	add           []string
	remove        []string
	trashed       string
	sent          mail.Compose
	sendErr       error
	createdLabels []string
	deletedLabels []string
	watchID       uint64
	historyFunc   func(uint64, func([]mail.ThreadSummary) error) (uint64, error)
	stream        func(context.Context, func(uint64) error) error
}

func newFakeService() *fakeService {
	return &fakeService{
		account: mail.Account{Name: "Demo User", Email: "demo@example.com", Demo: true},
		labels:  []mail.Label{{ID: mail.Inbox, Name: "Inbox", Unread: 1}},
		threads: []mail.ThreadSummary{{
			ID: "thread-1", Subject: "Launch plan", Senders: "Ada", Snippet: "The latest plan",
			Updated: time.Now(), MessageCount: 1, LabelIDs: []string{mail.Inbox, mail.Unread}, Unread: true,
		}},
		thread: mail.Thread{ID: "thread-1", Subject: "Launch plan", Messages: []mail.Message{{
			ID: "message-1", ThreadID: "thread-1", From: "Ada <ada@example.com>", To: "demo@example.com",
			Subject: "Launch plan", MessageID: "<m1@example.com>", Date: time.Now(), Text: "The latest plan",
		}}},
	}
}

func (f *fakeService) Account(context.Context) (mail.Account, error) { return f.account, nil }
func (f *fakeService) Labels(context.Context) ([]mail.Label, error)  { return f.labels, nil }
func (f *fakeService) ListThreads(context.Context, mail.Query) ([]mail.ThreadSummary, error) {
	return append([]mail.ThreadSummary(nil), f.threads...), nil
}
func (f *fakeService) Thread(_ context.Context, id string) (mail.Thread, error) {
	if f.threadFunc != nil {
		return f.threadFunc(id)
	}
	return f.thread, nil
}

func (f *fakeService) ThreadState(_ context.Context, id string) (mail.ThreadSummary, mail.Thread, error) {
	thread, err := f.Thread(context.Background(), id)
	if err != nil {
		return mail.ThreadSummary{}, mail.Thread{}, err
	}
	for _, summary := range f.threads {
		if summary.ID == id {
			return summary, thread, nil
		}
	}
	return mail.ThreadSummary{ID: id, Subject: thread.Subject, LabelIDs: append([]string(nil), thread.LabelIDs...), MessageCount: len(thread.Messages)}, thread, nil
}
func (f *fakeService) ModifyThread(_ context.Context, id string, add, remove []string) error {
	f.modified = append(f.modified, id)
	f.add, f.remove = add, remove
	return nil
}
func (f *fakeService) TrashThread(_ context.Context, id string) error {
	f.trashed = id
	return nil
}
func (f *fakeService) CreateLabel(_ context.Context, name string) (mail.Label, error) {
	f.createdLabels = append(f.createdLabels, name)
	label := mail.Label{ID: "Label_new", Name: name}
	f.labels = append(f.labels, label)
	return label, nil
}
func (f *fakeService) DeleteLabel(_ context.Context, id string) error {
	f.deletedLabels = append(f.deletedLabels, id)
	for i := range f.labels {
		if f.labels[i].ID == id {
			f.labels = append(f.labels[:i], f.labels[i+1:]...)
			break
		}
	}
	return nil
}
func (f *fakeService) Send(_ context.Context, compose mail.Compose) error {
	f.sent = compose
	return f.sendErr
}

func newTestApp(service *fakeService) *App {
	a := New(service)
	a.schedule = func(fn func()) { fn() }
	a.restoreAttempted = true
	a.Start()
	return a
}

func TestSelectThreadLoadsMessagesAndMarksRead(t *testing.T) {
	service := newFakeService()
	a := newTestApp(service)
	a.selectedRow = 0
	a.selectThread("thread-1")
	if len(a.thread.Messages) != 1 || a.threadLoading {
		t.Fatalf("thread was not loaded: %+v", a.thread)
	}
	if a.threads[0].Unread {
		t.Error("selected thread is still unread")
	}
	if len(service.remove) != 1 || service.remove[0] != mail.Unread {
		t.Fatalf("removed labels = %v, want UNREAD", service.remove)
	}
}

func TestArchiveOnlyRemovesThreadFromInboxView(t *testing.T) {
	service := newFakeService()
	a := newTestApp(service)
	a.selectThread("thread-1")
	a.archive()
	if len(a.threads) != 0 || a.selectedThread != "" {
		t.Fatalf("archived thread remains in inbox: %+v", a.threads)
	}
	if len(service.remove) != 1 || service.remove[0] != mail.Inbox {
		t.Fatalf("removed labels = %v, want INBOX", service.remove)
	}
}

func TestArchiveSelectsAdjacentThread(t *testing.T) {
	for _, tc := range []struct {
		name         string
		archiveIndex int
		wantID       string
		wantRow      int
	}{
		{name: "next", archiveIndex: 0, wantID: "thread-2", wantRow: 0},
		{name: "previous after last", archiveIndex: 2, wantID: "thread-2", wantRow: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := newFakeService()
			service.threads = append(service.threads,
				mail.ThreadSummary{ID: "thread-2", Subject: "Second", Senders: "Bob", LabelIDs: []string{mail.Inbox}},
				mail.ThreadSummary{ID: "thread-3", Subject: "Third", Senders: "Cid", LabelIDs: []string{mail.Inbox}},
			)
			service.thread = mail.Thread{ID: "thread-2", Subject: "Second", Messages: []mail.Message{{ID: "message-2", ThreadID: "thread-2", Text: "Second body"}}}
			a := newTestApp(service)
			a.selectedRow = tc.archiveIndex
			a.selectThread(service.threads[tc.archiveIndex].ID)
			a.archive()
			if a.selectedThread != tc.wantID || a.selectedRow != tc.wantRow || a.thread.ID != tc.wantID {
				t.Fatalf("selection after archive: thread=%q row=%d loaded=%q, want %q at %d", a.selectedThread, a.selectedRow, a.thread.ID, tc.wantID, tc.wantRow)
			}
		})
	}
}

func TestArchiveKeepsStarredSearchResult(t *testing.T) {
	service := newFakeService()
	a := newTestApp(service)
	a.selectedLabel = mail.Starred
	a.selectThread("thread-1")
	a.archive()
	if len(a.threads) != 1 {
		t.Fatal("archiving removed a thread from the starred view")
	}
}

func TestSendFailurePreservesComposer(t *testing.T) {
	service := newFakeService()
	service.sendErr = errors.New("offline")
	a := newTestApp(service)
	a.openCompose(nil)
	a.compose.to = "ada@example.com"
	a.compose.subject = "Status"
	a.compose.doc.SetText(richtext.NewText("Still here"))
	a.sendCompose()
	if a.compose == nil || !strings.Contains(a.compose.err, "offline") {
		t.Fatalf("composer was not kept after the failure: %+v", a.compose)
	}
	if got := a.compose.doc.Text().PlainText(); got != "Still here" {
		t.Fatalf("body changed after failure: %q", got)
	}
	service.sendErr = nil
	a.sendCompose()
	if a.compose != nil {
		t.Fatal("composer stayed open after a successful send")
	}
	if service.sent.To != "ada@example.com" || service.sent.Text != "Still here" {
		t.Fatalf("sent message = %+v", service.sent)
	}
}

func TestReplyHeadersComeFromLatestMessage(t *testing.T) {
	service := newFakeService()
	a := newTestApp(service)
	a.selectThread("thread-1")
	a.openReply()
	if a.compose.threadID != "thread-1" || a.compose.inReplyTo != "<m1@example.com>" {
		t.Fatalf("reply metadata = %+v", a.compose)
	}
	if !strings.Contains(a.compose.references, "<m1@example.com>") {
		t.Fatalf("reply references = %q", a.compose.references)
	}
}

type countingThreadService struct {
	*fakeService
	threadCalls int
}

func (s *countingThreadService) Thread(_ context.Context, id string) (mail.Thread, error) {
	s.threadCalls++
	return s.fakeService.Thread(context.Background(), id)
}

func TestSelectThreadUsesCache(t *testing.T) {
	service := &countingThreadService{fakeService: newFakeService()}
	a := New(service)
	a.schedule = func(fn func()) { fn() }
	a.restoreAttempted = true
	a.Start()
	a.selectThread("thread-1")
	a.closeReader()
	a.selectThread("thread-1")
	if service.threadCalls != 1 {
		t.Fatalf("thread requests = %d, want 1 for a cached conversation", service.threadCalls)
	}
	if a.threadLoading || len(a.thread.Messages) != 1 {
		t.Fatalf("cached thread was not restored immediately: %+v", a.thread)
	}
}

func TestRefreshReloadsThreadOnceAndRestoresCache(t *testing.T) {
	service := &countingThreadService{fakeService: newFakeService()}
	a := New(service)
	a.schedule = func(fn func()) { fn() }
	a.restoreAttempted = true
	a.Start()
	a.selectThread("thread-1")
	a.refresh()
	if service.threadCalls != 2 {
		t.Fatalf("thread requests after refresh = %d, want 2", service.threadCalls)
	}
	a.closeReader()
	a.selectThread("thread-1")
	if service.threadCalls != 2 {
		t.Fatalf("refreshed thread was not cached: %d requests", service.threadCalls)
	}
}

func TestThreadCacheTracksLabelChanges(t *testing.T) {
	service := &countingThreadService{fakeService: newFakeService()}
	a := New(service)
	a.schedule = func(fn func()) { fn() }
	a.restoreAttempted = true
	a.Start()
	a.selectThread("thread-1")
	a.toggleStar()
	a.closeReader()
	a.selectThread("thread-1")
	if service.threadCalls != 1 {
		t.Fatalf("label change invalidated the cached body: %d requests", service.threadCalls)
	}
	for _, id := range a.thread.LabelIDs {
		if id == mail.Starred {
			return
		}
	}
	t.Fatalf("cached labels = %v, want STARRED", a.thread.LabelIDs)
}

func TestArchiveIsOptimisticAndSerializesRequests(t *testing.T) {
	service := newFakeService()
	service.threads = append(service.threads,
		mail.ThreadSummary{ID: "thread-2", Subject: "Second", LabelIDs: []string{mail.Inbox}},
		mail.ThreadSummary{ID: "thread-3", Subject: "Third", LabelIDs: []string{mail.Inbox}},
	)
	service.threadFunc = func(id string) (mail.Thread, error) {
		return mail.Thread{
			ID: id, Subject: id,
			Messages: []mail.Message{{ID: "message-" + id, ThreadID: id, Text: id + " body"}},
		}, nil
	}
	a := newTestApp(service)
	for _, id := range []string{"thread-1", "thread-2", "thread-3"} {
		a.selectThread(id)
	}
	a.selectThread("thread-1")
	service.modified = nil
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }

	a.archive()
	if len(a.threads) != 2 || a.selectedThread != "thread-2" {
		t.Fatalf("archive did not update the UI before the request: threads=%v selected=%q", a.threads, a.selectedThread)
	}
	a.archive()
	if len(a.threads) != 1 || a.selectedThread != "thread-3" {
		t.Fatalf("second archive did not update the UI immediately: threads=%v selected=%q", a.threads, a.selectedThread)
	}
	if len(jobs) != 1 || len(service.modified) != 0 {
		t.Fatalf("queued requests ran ahead of schedule: jobs=%d modified=%v", len(jobs), service.modified)
	}
	jobs[0]()
	if len(jobs) != 2 || len(service.modified) != 1 || service.modified[0] != "thread-1" {
		t.Fatalf("first request did not release the second in order: jobs=%d modified=%v", len(jobs), service.modified)
	}
	jobs[1]()
	if len(service.modified) != 2 || service.modified[1] != "thread-2" {
		t.Fatalf("request order = %v, want thread-1 then thread-2", service.modified)
	}
}

func TestUndoRestoresArchivedThreadBeforeRequest(t *testing.T) {
	service := newFakeService()
	a := newTestApp(service)
	a.selectThread("thread-1")
	service.modified = nil
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }

	a.archive()
	if len(a.threads) != 0 {
		t.Fatal("archive waited for the service before removing the thread")
	}
	a.undoLast()
	if len(a.threads) != 1 || !containsLabel(a.threads[0].LabelIDs, mail.Inbox) {
		t.Fatalf("undo did not restore the thread locally: %+v", a.threads)
	}
	if len(jobs) != 1 {
		t.Fatalf("undo ran concurrently with archive: %d scheduled jobs", len(jobs))
	}
	jobs[0]()
	if len(jobs) != 2 {
		t.Fatal("undo was not queued after archive")
	}
	jobs[1]()
	if len(service.modified) != 2 || len(service.add) != 1 || service.add[0] != mail.Inbox {
		t.Fatalf("undo requests = %v, final added labels %v", service.modified, service.add)
	}
}

func TestTrashUndoMatchesQueuedLabels(t *testing.T) {
	service := newFakeService()
	a := newTestApp(service)
	a.selectThread("thread-1")
	service.modified = nil
	service.add = nil
	service.remove = nil
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }

	a.trash()
	if len(a.threads) != 0 {
		t.Fatal("trash waited for the service before removing the thread")
	}
	a.selectedLabel = mail.All
	a.undoLast()
	if len(a.threads) != 1 {
		t.Fatal("trash undo did not restore the thread locally")
	}
	labels := a.threads[0].LabelIDs
	if containsLabel(labels, mail.Inbox) || containsLabel(labels, mail.Trash) {
		t.Fatalf("local labels %v do not match the queued undo: add=%v remove=%v", labels, service.add, service.remove)
	}
	jobs[0]()
	if len(jobs) != 2 {
		t.Fatal("trash undo was not queued after trash")
	}
	jobs[1]()
	if service.trashed != "thread-1" || len(service.remove) != 1 || service.remove[0] != mail.Trash {
		t.Fatalf("trash requests = %q, undo removals = %v", service.trashed, service.remove)
	}
}

type pagingService struct {
	*fakeService
	pages   []mail.ThreadPage
	queries []mail.Query
}

func (s *pagingService) ListThreadPage(_ context.Context, query mail.Query) (mail.ThreadPage, error) {
	s.queries = append(s.queries, query)
	if query.PageToken == "next" {
		return s.pages[1], nil
	}
	return s.pages[0], nil
}
