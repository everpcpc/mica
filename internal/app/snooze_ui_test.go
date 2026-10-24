package app

import (
	"os"
	"testing"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

func TestSnoozeMenuAndShortcutEnglish(t *testing.T) {
	store := newFakeMailStore()
	service := &snoozeService{fakeService: newFakeService()}
	a := newSnoozeApp(service, store)
	a.selectThread("thread-1")
	tt := ui.NewTester(a.View, 1280, 800)
	tt.SetLocale("en")
	r, ok := tt.Find("Snooze")
	if !ok {
		t.Fatal("snooze menu button not found")
	}
	x, y := r.X+r.W/2, r.Y+r.H/2
	tt.Press(x, y)
	menu := tt.Menu()
	if len(menu) != 3 || menu[0] != "Tomorrow morning" || menu[1] != "This weekend" || menu[2] != "Next week" {
		t.Fatalf("snooze menu = %q", menu)
	}
	tt.Release(x, y)
	if err := tt.ChooseMenuItem("Tomorrow morning"); err != nil {
		t.Fatal(err)
	}
	if len(store.snoozes) != 1 {
		t.Fatalf("menu did not persist snooze: %v", store.snoozes)
	}
	a.selectedLabel = mail.Snoozed
	a.refreshList()
	a.selectThread("thread-1")
	tt.Frame()
	if !tt.HasText("Snoozed until") {
		t.Fatal("snoozed wake time is not rendered")
	}
	r, ok = tt.Find("Snooze")
	if !ok {
		t.Fatal("snooze menu button not found in Snoozed view")
	}
	x, y = r.X+r.W/2, r.Y+r.H/2
	tt.Press(x, y)
	menu = tt.Menu()
	if len(menu) == 0 || menu[len(menu)-1] != "Unsnooze" {
		t.Fatalf("snooze menu lacks Unsnooze: %q", menu)
	}
	tt.Release(x, y)
	if err := tt.ChooseMenuItem("Unsnooze"); err != nil {
		t.Fatal(err)
	}
	if len(store.snoozes) != 0 {
		t.Fatalf("Unsnooze left a pending record: %v", store.snoozes)
	}
	a.selectedLabel = mail.Inbox
	a.refreshList()
	a.selectThread("thread-1")
	a.PerformShortcut(ui.KeyB, 0)
	if len(store.snoozes) != 1 {
		t.Fatalf("B shortcut did not snooze: %v", store.snoozes)
	}
}

func TestCaptureSnoozed(t *testing.T) {
	if os.Getenv("MICA_CAPTURE") == "" {
		t.Skip("capture is opt-in")
	}
	store := newFakeMailStore()
	service := &snoozeService{fakeService: newFakeService()}
	a := newSnoozeApp(service, store)
	a.snoozeUntil(a.threads, time.Now().Add(24*time.Hour))
	a.selectedLabel = mail.Snoozed
	a.refreshList()
	a.selectThread("thread-1")
	tt := ui.NewTester(a.View, 1280, 800)
	tt.SetLocale("en")
	captureFrame(t, tt, "snoozed")
}
