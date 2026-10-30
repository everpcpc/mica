package app

import (
	"fmt"
	"testing"

	"mica/internal/mail"

	"github.com/egoist/mygo"
)

func emailReuseThread(id string) mail.Thread {
	return mail.Thread{ID: id, Subject: id, Messages: []mail.Message{{
		ID: "message-" + id, ThreadID: id, HTML: "<p>" + id + "</p>",
	}}}
}

func TestRecentEmailViewsSurviveConversationSwitch(t *testing.T) {
	a := New(newFakeService())
	var destroyed []string
	a.destroyEmailView = func(view *mygo.WebView) {
		for key, page := range a.emailPages {
			if page.view == view {
				destroyed = append(destroyed, key)
			}
		}
	}
	first := &emailPage{messageKey: "message-thread-1", view: new(mygo.WebView)}
	a.emailPages = map[string]*emailPage{first.messageKey: first}
	a.emailPageRecent = []string{first.messageKey}
	a.selectedThread = "thread-1"
	a.showThread("thread-1", emailReuseThread("thread-1"))
	a.selectedThread = "thread-2"
	a.thread.ID = "thread-1"
	a.showThread("thread-2", emailReuseThread("thread-2"))
	a.selectedThread = "thread-1"
	a.showThread("thread-1", emailReuseThread("thread-1"))
	if a.emailPages[first.messageKey] != first {
		t.Fatal("A-B-A reopening discarded the first email renderer")
	}
	if len(destroyed) != 0 {
		t.Fatalf("conversation switch destroyed renderers: %v", destroyed)
	}
}

func TestEmailViewRetentionIsBounded(t *testing.T) {
	a := New(newFakeService())
	destroyed := 0
	a.destroyEmailView = func(*mygo.WebView) { destroyed++ }
	for i := range 5 + 1 {
		id := fmt.Sprintf("thread-%d", i)
		key := "message-" + id
		if a.emailPages == nil {
			a.emailPages = make(map[string]*emailPage)
		}
		a.emailPages[key] = &emailPage{messageKey: key, view: new(mygo.WebView)}
		a.touchEmailPage(key)
		a.selectedThread = id
		a.showThread(id, emailReuseThread(id))
	}
	if len(a.emailPages) != 5 {
		t.Fatalf("retained renderers = %d, want 5", len(a.emailPages))
	}
	if a.emailPages["message-thread-0"] != nil {
		t.Fatal("least recently used renderer survived eviction")
	}
	if destroyed != 1 {
		t.Fatalf("evicted renderers = %d, want 1", destroyed)
	}
}

func TestCollapsedEmailViewDoesNotSurvive(t *testing.T) {
	a := New(newFakeService())
	a.destroyEmailView = func(*mygo.WebView) {}
	key := "message-thread-1"
	a.emailPages = map[string]*emailPage{key: {messageKey: key, view: new(mygo.WebView)}}
	a.setExpandedMessage(emailReuseThread("thread-1").Messages[0], false)
	if a.emailPages[key] != nil {
		t.Fatal("collapsing an email retained its renderer")
	}
}

func TestRetainedEmailViewsRespectExplicitCleanup(t *testing.T) {
	a := New(newFakeService())
	destroyed := 0
	a.destroyEmailView = func(*mygo.WebView) { destroyed++ }
	key := "message-thread-1"
	a.emailPages = map[string]*emailPage{key: {messageKey: key, view: new(mygo.WebView)}}
	message := emailReuseThread("thread-1").Messages[0]
	message.HTML = `<img src="https://example.com/pixel.png">`
	a.allowRemoteImages(message)
	if a.emailPages[key] != nil || destroyed != 1 {
		t.Fatal("image permission change retained the old renderer")
	}
	a.emailPages[key] = &emailPage{messageKey: key, view: new(mygo.WebView)}
	a.clearThread()
	if len(a.emailPages) != 0 || destroyed != 2 {
		t.Fatalf("reader cleanup retained %d renderers, destroyed %d", len(a.emailPages), destroyed)
	}
}
