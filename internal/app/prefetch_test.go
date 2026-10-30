package app

import (
	"context"
	"fmt"
	"testing"

	"mica/internal/mail"
)

type recordingThreadService struct {
	*fakeService
	calls []string
}

func (s *recordingThreadService) Thread(_ context.Context, id string) (mail.Thread, error) {
	s.calls = append(s.calls, id)
	return mail.Thread{ID: id, Subject: id, Messages: []mail.Message{{
		ID: "message-" + id, ThreadID: id, Text: id + " body",
	}}}, nil
}

func newPrefetchService(count int) *recordingThreadService {
	service := &recordingThreadService{fakeService: newFakeService()}
	service.threads = nil
	for i := range count {
		id := fmt.Sprintf("thread-%d", i+1)
		service.threads = append(service.threads, mail.ThreadSummary{ID: id, Subject: id, LabelIDs: []string{mail.Inbox}})
	}
	return service
}

func TestVisibleThreadsPrefetchDetailsWithinLimit(t *testing.T) {
	service := newPrefetchService(7)
	a := New(service)
	a.EnableThreadPrefetch()
	a.schedule = func(fn func()) { fn() }
	a.restoreAttempted = true
	a.Start()
	if len(service.calls) != 5 {
		t.Fatalf("detail requests = %v, want five visible conversations", service.calls)
	}
	if _, ok := a.threadCache["thread-2"]; !ok {
		t.Fatal("the next visible conversation was not prepared")
	}
}

func TestSelectThreadJoinsVisiblePrefetch(t *testing.T) {
	service := newPrefetchService(2)
	a := New(service)
	a.EnableThreadPrefetch()
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.startupSelectionDone = true
	a.threadCache = map[string]mail.Thread{"thread-1": {ID: "thread-1"}}
	a.invalidateThreadPrefetch()
	a.applyThreadList(service.threads, false)
	if len(jobs) != 1 {
		t.Fatalf("initial prefetch jobs = %d, want 1", len(jobs))
	}
	jobs[0]()
	if len(jobs) != 2 {
		t.Fatalf("queued prefetch jobs = %d, want 2", len(jobs))
	}
	if a.threadPrefetch == nil || a.threadPrefetch.current == nil || a.threadPrefetch.current.key != "thread-2" {
		t.Fatalf("prefetch current = %+v, want thread-2", a.threadPrefetch)
	}
	a.selectThread("thread-2")
	if len(jobs) != 2 || len(service.calls) != 0 {
		t.Fatalf("selection started duplicate detail work: jobs=%d calls=%v", len(jobs), service.calls)
	}
	jobs[1]()
	if a.thread.ID != "thread-2" || a.threadLoading {
		t.Fatalf("selected conversation = %+v, loading=%v", a.thread, a.threadLoading)
	}
}

func TestPrefetchDoesNotOverwriteNewerHistory(t *testing.T) {
	service := newPrefetchService(1)
	a := New(service)
	a.EnableThreadPrefetch()
	var jobs, updates []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.update = func(fn func()) { updates = append(updates, fn) }
	a.storeAccount = "demo@example.com"
	a.accountVerified = true
	a.SetMailStore(newFakeMailStore())
	a.startupSelectionDone = true
	a.invalidateThreadPrefetch()
	a.applyThreadList(service.threads, false)
	jobs[0]()
	for _, update := range updates {
		update()
	}
	version, revision := a.nextThreadCacheVersion("demo@example.com", "thread-1")
	jobs[1]()
	for _, update := range updates {
		update()
	}
	if version.Load() != revision {
		t.Fatal("prefetch persisted over a newer history write")
	}
	if len(jobs) != 2 {
		t.Fatalf("cache write jobs = %d, want no stale prefetch write", len(jobs))
	}
}

func TestPrefetchDelayDoesNotSurviveAccountReplacement(t *testing.T) {
	service := newPrefetchService(1)
	a := New(service)
	a.EnableThreadPrefetch()
	var jobs, updates []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.update = func(fn func()) { updates = append(updates, fn) }
	a.startupSelectionDone = true
	a.invalidateThreadPrefetch()
	a.applyThreadList(service.threads, false)
	a.useService(newFakeService())
	jobs[0]()
	for _, update := range updates {
		update()
	}
}

func TestSelectThreadDuringAnotherPrefetch(t *testing.T) {
	service := newPrefetchService(2)
	a := New(service)
	a.EnableThreadPrefetch()
	var jobs, updates []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.update = func(fn func()) { updates = append(updates, fn) }
	a.startupSelectionDone = true
	a.invalidateThreadPrefetch()
	a.applyThreadList(service.threads, false)
	jobs[0]()
	for _, update := range updates {
		update()
	}
	a.selectThread("thread-2")
	if len(jobs) != 3 || len(service.calls) != 0 {
		t.Fatalf("another prefetch duplicated selected detail work: jobs=%d calls=%v", len(jobs), service.calls)
	}
	jobs[1]()
	for _, update := range updates {
		update()
	}
	jobs[2]()
	for _, update := range updates {
		update()
	}
	if a.thread.ID != "thread-2" || a.threadLoading {
		t.Fatalf("selected conversation = %+v, loading=%v", a.thread, a.threadLoading)
	}
}

func TestPrefetchFailureIsSilent(t *testing.T) {
	service := newPrefetchService(2)
	service.fakeService.threadFunc = func(id string) (mail.Thread, error) {
		service.calls = append(service.calls, id)
		return mail.Thread{}, context.DeadlineExceeded
	}
	a := New(service)
	a.EnableThreadPrefetch()
	a.schedule = func(fn func()) { fn() }
	a.startupSelectionDone = true
	a.invalidateThreadPrefetch()
	a.applyThreadList(service.threads, false)
	if a.threadErr != "" || a.notice != "" {
		t.Fatalf("background prefetch surfaced an error: thread=%q notice=%q", a.threadErr, a.notice)
	}
	if len(service.calls) != 2 {
		t.Fatalf("failed prefetch did not advance the bounded queue: %v", service.calls)
	}
}
