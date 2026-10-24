package app

import (
	"os"
	"testing"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

func openScheduleComposerMenu(t *testing.T, tt *ui.Tester) []string {
	t.Helper()
	target, ok := tt.Find("Schedule send")
	if !ok {
		t.Fatalf("Schedule send menu not found: %q", tt.Texts())
	}
	x, y := target.X+target.W/2, target.Y+target.H/2
	tt.Press(x, y)
	items := tt.Menu()
	tt.Release(x, y)
	return items
}

func TestScheduledComposerPreset(t *testing.T) {
	a, service, store := newScheduledHarness()
	openOutboxTestCompose(a)
	tt := ui.NewTester(a.View, 1280, 800)
	tt.SetLocale("en")
	items := openScheduleComposerMenu(t, tt)
	if len(items) != 5 || items[0] != "Tomorrow morning" || items[3] != "-" || items[4] != "Pick date and time…" {
		t.Fatalf("schedule menu = %q", items)
	}
	if err := tt.ChooseMenuItem("Tomorrow morning"); err != nil {
		t.Fatal(err)
	}
	if a.compose != nil || service.sendCalls != 0 {
		t.Fatalf("preset sent instead of scheduling: compose=%p calls=%d", a.compose, service.sendCalls)
	}
	if record := onlyOutboxRecord(t, store); record.State != mail.OutboxScheduled || record.SendAt.In(a.currentTime().Location()).Hour() != 8 {
		t.Fatalf("preset record = %+v", record)
	}
}

func TestScheduledComposerCustomTime(t *testing.T) {
	a, service, store := newScheduledHarness()
	openOutboxTestCompose(a)
	tt := ui.NewTester(a.View, 1280, 800)
	tt.SetLocale("en")
	openScheduleComposerMenu(t, tt)
	if err := tt.ChooseMenuItem("Pick date and time…"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tt.Find("Send date"); !ok {
		t.Fatalf("date input not found: %q", tt.Texts())
	}
	if _, ok := tt.Find("Send time"); !ok {
		t.Fatalf("time input not found: %q", tt.Texts())
	}
	a.compose.scheduleAt = a.currentTime().Add(2 * time.Hour)
	tt.Frame()
	if err := tt.Click("Schedule"); err != nil {
		t.Fatal(err)
	}
	if a.compose != nil || service.sendCalls != 0 {
		t.Fatalf("custom schedule sent immediately: compose=%p calls=%d", a.compose, service.sendCalls)
	}
	if record := onlyOutboxRecord(t, store); record.State != mail.OutboxScheduled || !record.SendAt.Equal(a.currentTime().Add(2*time.Hour)) {
		t.Fatalf("custom schedule = %+v", record)
	}
}

func newScheduledOutboxViewHarness(t *testing.T) (*App, *outboxOrderService, *fakeMailStore, *ui.Tester) {
	t.Helper()
	a, service, store := newScheduledHarness()
	openOutboxTestCompose(a)
	a.scheduleCompose(a.currentTime().Add(2 * time.Hour))
	var record mail.OutboxMessage
	for _, record = range store.outbox {
	}
	a.selectedLabel = mail.Outbox
	a.selectLabel()
	a.selectThread("outbox-" + record.ID)
	tt := ui.NewTester(a.View, 1280, 800)
	tt.SetLocale("en")
	return a, service, store, tt
}

func TestScheduledOutboxSendNow(t *testing.T) {
	_, service, store, tt := newScheduledOutboxViewHarness(t)
	record := onlyOutboxRecord(t, store)
	if got := outboxMessage(record).Date; got.Hour() != record.SendAt.Local().Hour() {
		t.Fatalf("scheduled display uses %v, want local time %v", got, record.SendAt.Local())
	}
	for _, text := range []string{"Scheduled for", "Send now", "Cancel schedule"} {
		if !tt.HasText(text) {
			t.Fatalf("scheduled Outbox missing %q: %q", text, tt.Texts())
		}
	}
	if err := tt.Click("Send now"); err != nil {
		t.Fatal(err)
	}
	if service.sendCalls != 1 || len(store.outbox) != 0 || tt.HasText("Check before sending again") {
		t.Fatalf("Send now did not complete once: calls=%d records=%v texts=%q", service.sendCalls, store.outbox, tt.Texts())
	}
}

func TestScheduledOutboxDoesNotOfferDelete(t *testing.T) {
	a, _, store, tt := newScheduledOutboxViewHarness(t)
	if tt.HasText("Delete") {
		t.Fatal("scheduled message offered the unsafe generic Delete action")
	}
	a.requestDeleteOutbox()
	if a.outboxDeleteOpen || len(store.outbox) != 1 {
		t.Fatalf("scheduled delete was allowed: open=%v records=%v", a.outboxDeleteOpen, store.outbox)
	}
}

func TestScheduledOutboxCancel(t *testing.T) {
	a, service, store, tt := newScheduledOutboxViewHarness(t)
	if err := tt.Click("Cancel schedule"); err != nil {
		t.Fatal(err)
	}
	if len(store.outbox) != 0 || a.compose == nil || len(a.compose.attachments) != 1 || service.sendCalls != 0 {
		t.Fatalf("Cancel schedule did not restore draft: records=%v compose=%+v calls=%d", store.outbox, a.compose, service.sendCalls)
	}
	if !tt.HasText("New message") {
		t.Fatalf("restored composer is not visible: %q", tt.Texts())
	}
}

func TestScheduledRejectsPastTime(t *testing.T) {
	a, service, store := newScheduledHarness()
	m := openOutboxTestCompose(a)
	a.scheduleCompose(a.currentTime().Add(-time.Minute))
	if a.compose != m || m.err == "" || len(store.outbox) != 0 || service.sendCalls != 0 {
		t.Fatalf("past schedule was accepted: compose=%+v records=%v calls=%d", a.compose, store.outbox, service.sendCalls)
	}
}

func TestCaptureScheduledOutbox(t *testing.T) {
	if os.Getenv("MICA_CAPTURE") == "" {
		t.Skip("capture is opt-in")
	}
	_, _, _, tt := newScheduledOutboxViewHarness(t)
	captureFrame(t, tt, "scheduled")
}

func TestCaptureScheduledComposer(t *testing.T) {
	if os.Getenv("MICA_CAPTURE") == "" {
		t.Skip("capture is opt-in")
	}
	a, _, _ := newScheduledHarness()
	openOutboxTestCompose(a)
	tt := ui.NewTester(a.View, 1280, 800)
	tt.SetLocale("en")
	openScheduleComposerMenu(t, tt)
	if err := tt.ChooseMenuItem("Pick date and time…"); err != nil {
		t.Fatal(err)
	}
	captureFrame(t, tt, "schedule-compose")
}
