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
	account mail.Account
	labels  []mail.Label
	threads []mail.ThreadSummary
	thread  mail.Thread
	add     []string
	remove  []string
	trashed string
	sent    mail.Compose
	sendErr error
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
func (f *fakeService) Thread(context.Context, string) (mail.Thread, error) { return f.thread, nil }
func (f *fakeService) ModifyThread(_ context.Context, _ string, add, remove []string) error {
	f.add, f.remove = add, remove
	return nil
}
func (f *fakeService) TrashThread(_ context.Context, id string) error {
	f.trashed = id
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
