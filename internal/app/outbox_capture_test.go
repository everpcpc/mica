package app

import (
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestCaptureOutbox(t *testing.T) {
	if os.Getenv("MICA_CAPTURE") == "" {
		t.Skip("capture is opt-in")
	}
	a, _, _, _ := newOutboxViewHarness(t)
	tt := ui.NewTester(a.View, 1280, 800)
	tt.SetLocale("en")
	captureFrame(t, tt, "outbox")
}

func TestCaptureOutboxReview(t *testing.T) {
	if os.Getenv("MICA_CAPTURE") == "" {
		t.Skip("capture is opt-in")
	}
	a, _, _, _ := newOutboxViewHarness(t)
	tt := ui.NewTester(a.View, 1280, 800)
	tt.SetLocale("en")
	if err := tt.Click("Retry"); err != nil {
		t.Fatal(err)
	}
	captureFrame(t, tt, "outbox-review")
}
