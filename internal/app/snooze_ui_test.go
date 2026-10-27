package app

import (
	"os"
	"strings"
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
	openReaderActions(t, tt)
	if err := tt.ChooseMenuItem("Snooze", "Tomorrow morning"); err != nil {
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
	openReaderActions(t, tt)
	if err := tt.ChooseMenuItem("Snooze", "Next week"); err != nil {
		t.Fatal(err)
	}
	if len(a.threads) != 1 || len(store.snoozes) != 1 {
		t.Fatalf("rescheduling removed the Snoozed row: threads=%v store=%v", a.threads, store.snoozes)
	}
	tt.Frame()
	openReaderActions(t, tt)
	if err := tt.ChooseMenuItem("Snooze", "Unsnooze"); err != nil {
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

func TestSnoozeWebViewShortcutMapping(t *testing.T) {
	if !strings.Contains(emailHeightScript, `b: "snooze"`) || !strings.Contains(emailHeightScript, "MicaMail.Shortcut") {
		t.Fatal("WebView does not forward the b shortcut to the native Snooze action")
	}
}
