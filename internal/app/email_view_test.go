package app

import (
	"strings"
	"testing"

	"mica/internal/mail"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestEmailViewShowsWebViewBeforeFirstMeasurement(t *testing.T) {
	a := New(newFakeService())
	a.selectedThread = "thread-1"
	a.thread = mail.Thread{ID: "thread-1", Messages: []mail.Message{{ID: "message-1", HTML: "<p>Body</p>"}}}
	a.emailPages = map[string]*emailPage{
		"message-1": {messageID: "message-1", view: new(mygo.WebView), height: emailInitialHeight},
	}
	tt := ui.NewTester(a.View, 1280, 800)
	found := false
	for y := float32(0); y < 800 && !found; y += 20 {
		for x := float32(0); x < 1280; x += 20 {
			if tt.WebViewAt(x, y) != nil {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("email WebView is gated behind the first height callback")
	}
}

func TestEmailShortcutBinding(t *testing.T) {
	binding, ok := emailShortcutBinding("archive")
	if !ok || binding.key != ui.KeyE || binding.mods != 0 {
		t.Fatalf("archive binding = %+v, ok=%v, want E without modifiers", binding, ok)
	}
	if _, ok := emailShortcutBinding("search"); ok {
		t.Fatal("search focus was exposed to the email bridge without a window context")
	}
}

func TestEmailHeightEstimateAndUpdate(t *testing.T) {
	a := New(newFakeService())
	height := a.estimatedEmailHeight("message-1")
	if height != 24 {
		t.Fatalf("initial email height = %v, want one-line placeholder", height)
	}
	page := &emailPage{messageID: "message-1", height: height, failed: true}
	a.setEmailHeight(page, 480)
	if page.height != 480 || page.failed {
		t.Fatalf("updated page = %+v, want ready 480px page", page)
	}
	if height := a.estimatedEmailHeight("message-1"); height != 480 {
		t.Fatalf("estimated height = %v, want cached 480px", height)
	}
}

func TestEmailHeightScriptBatchesMeasurements(t *testing.T) {
	if !strings.Contains(emailHeightScript, "measureQueued") {
		t.Fatal("email measurements are not coalesced before reporting height")
	}
}

func TestEmailScriptForwardsApplicationShortcuts(t *testing.T) {
	for _, want := range []string{
		`addEventListener("keydown"`,
		`MicaMail.Shortcut`,
		`isComposing`,
	} {
		if !strings.Contains(emailHeightScript, want) {
			t.Fatalf("email script does not forward application shortcuts: missing %q", want)
		}
	}
}
