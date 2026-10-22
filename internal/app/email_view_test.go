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
