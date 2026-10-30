package app

import (
	"slices"
	"testing"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

func TestSlowConversationSwitchKeepsReadableContent(t *testing.T) {
	service := newFakeService()
	service.threads = append(service.threads, mail.ThreadSummary{ID: "thread-2", Subject: "Second", LabelIDs: []string{mail.Inbox}})
	service.threadFunc = func(id string) (mail.Thread, error) {
		return mail.Thread{ID: id, Subject: id, Messages: []mail.Message{{ID: "message-" + id, ThreadID: id, Text: id + " body"}}}, nil
	}
	a := newTestApp(service)
	tt := ui.NewTester(a.View, 1280, 800)
	tt.Advance(time.Second)
	a.schedule = func(func()) {}
	a.selectThread("thread-2")
	tt.Frame()
	if !tt.HasText("The latest plan") {
		t.Fatal("slow conversation switch blanked the current reader")
	}
	if a.selectedThread != "thread-2" || !a.threadLoading {
		t.Fatalf("selection or pending state was lost: selected=%q loading=%v", a.selectedThread, a.threadLoading)
	}
}

func TestSwitchingConversationResetsReadingPosition(t *testing.T) {
	service := newFakeService()
	service.threads = append(service.threads, mail.ThreadSummary{ID: "thread-2", Subject: "Second", LabelIDs: []string{mail.Inbox}})
	service.threadFunc = func(id string) (mail.Thread, error) {
		return mail.Thread{ID: id, Subject: id, Messages: []mail.Message{{ID: "message-" + id, ThreadID: id, Text: id + " body"}}}, nil
	}
	a := newTestApp(service)
	a.readerScroll.Y = 320
	a.readerScroll.MaxY = 900
	a.focusedMessage = 0
	a.selectThread("thread-2")
	if a.readerScroll.Y != 0 || a.readerScroll.MaxY != 0 {
		t.Fatalf("new conversation retained the previous scroll: %+v", a.readerScroll)
	}
}

func TestCachedConversationSwitchDoesNotReplayReaderMotion(t *testing.T) {
	service := newFakeService()
	service.threads = append(service.threads, mail.ThreadSummary{ID: "thread-2", Subject: "Second", LabelIDs: []string{mail.Inbox}})
	service.threadFunc = func(id string) (mail.Thread, error) {
		return mail.Thread{ID: id, Subject: id, Messages: []mail.Message{{ID: "message-" + id, ThreadID: id, Text: id + " body"}}}, nil
	}
	a := newTestApp(service)
	tt := ui.NewTester(a.reader, 700, 800)
	tt.Advance(time.Second)
	a.selectThread("thread-2")
	tt.Advance(time.Second)
	pixels := func() []byte {
		img := tt.Image()
		return append([]byte(nil), img.Pix[120*img.Stride:700*img.Stride]...)
	}
	a.selectThread("thread-1")
	start := pixels()
	if tt.FramePending() {
		t.Fatal("cached conversation scheduled a new reader entrance")
	}
	tt.Advance(90 * time.Millisecond)
	mid := pixels()
	tt.Advance(200 * time.Millisecond)
	end := pixels()
	if !slices.Equal(start, mid) || !slices.Equal(start, end) {
		t.Fatal("cached conversation changed reader pixels between start, middle, and end")
	}
}

func TestSlowConversationSwitchKeepsLatestSelection(t *testing.T) {
	service := newFakeService()
	service.threads = append(service.threads,
		mail.ThreadSummary{ID: "thread-2", Subject: "Second", LabelIDs: []string{mail.Inbox}},
		mail.ThreadSummary{ID: "thread-3", Subject: "Third", LabelIDs: []string{mail.Inbox}},
	)
	service.threadFunc = func(id string) (mail.Thread, error) {
		return mail.Thread{ID: id, Subject: id, Messages: []mail.Message{{ID: "message-" + id, ThreadID: id, Text: id + " body"}}}, nil
	}
	a := newTestApp(service)
	var jobs, updates []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.update = func(fn func()) { updates = append(updates, fn) }
	a.selectThread("thread-2")
	a.selectThread("thread-3")
	if len(jobs) != 2 {
		t.Fatalf("slow conversation jobs = %d, want two independent requests", len(jobs))
	}
	jobs[1]()
	for _, update := range updates {
		update()
	}
	if a.thread.ID != "thread-3" || a.threadLoading {
		t.Fatalf("latest conversation was not installed: %+v loading=%v", a.thread, a.threadLoading)
	}
	jobs[0]()
	for _, update := range updates {
		update()
	}
	if a.thread.ID != "thread-3" || a.selectedThread != "thread-3" {
		t.Fatalf("stale conversation replaced the latest reader: %+v selected=%q", a.thread, a.selectedThread)
	}
}
