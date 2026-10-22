package app

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

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
	height, ready := a.estimatedEmailHeight("message-1")
	if height != 24 || ready {
		t.Fatalf("initial email height = %v, ready=%v, want one-line placeholder", height, ready)
	}
	page := &emailPage{messageID: "message-1", height: height, ready: ready, failed: true}
	a.setEmailHeight(page, 480)
	if page.height != 480 || !page.ready || page.failed {
		t.Fatalf("updated page = %+v, want ready 480px page", page)
	}
	height, ready = a.estimatedEmailHeight("message-1")
	if height != 480 || !ready {
		t.Fatalf("estimated height = %v, ready=%v, want cached 480px", height, ready)
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
