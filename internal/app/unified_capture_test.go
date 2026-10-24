package app

import (
	"os"
	"testing"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

func TestCaptureUnified(t *testing.T) {
	if os.Getenv("MICA_CAPTURE") == "" {
		t.Skip("capture is opt-in")
	}
	h := newUnifiedHarness()
	first := unifiedSummary("alpha", "a@example.com", time.Now().Unix())
	first.Subject, first.Senders, first.Snippet = "Alpha update", "Alice", "A short message"
	second := unifiedSummary("work", "b@example.com", time.Now().Add(-time.Minute).Unix())
	second.Subject, second.Senders, second.Snippet = "Work receipt", "Bob", "B short message"
	h.setPages("a@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{first}})
	h.setPages("b@example.com", mail.ThreadPage{Threads: []mail.ThreadSummary{second}})
	h.services["a@example.com"].threadFunc = func(id string) (mail.Thread, error) {
		return mail.Thread{ID: id, Subject: "Alpha update", Messages: []mail.Message{{ID: "alpha-message", ThreadID: id, From: "Alice", To: "you@example.com", Subject: "Alpha update", Text: "A short message"}}}, nil
	}
	h.services["b@example.com"].threadFunc = func(id string) (mail.Thread, error) {
		return mail.Thread{ID: id, Subject: "Work receipt", Messages: []mail.Message{{ID: "work-message", ThreadID: id, From: "Bob", To: "you@example.com", Subject: "Work receipt", Text: "B short message"}}}, nil
	}
	h.app.refreshUnified()
	tt := ui.NewTester(h.app.View, 1280, 800)
	tt.SetLocale("en")
	captureFrame(t, tt, "unified")
}
