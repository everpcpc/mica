package app

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestCaptureMotion(t *testing.T) {
	if os.Getenv("MICA_CAPTURE") == "" {
		t.Skip("capture is opt-in")
	}
	a := newTestApp(newFakeService())
	tt := ui.NewTester(a.View, 1280, 800)
	if err := tt.Click("Launch plan"); err != nil {
		t.Fatal(err)
	}
	captureFrame(t, tt, "reader-start")
	tt.Advance(90 * time.Millisecond)
	captureFrame(t, tt, "reader-mid")
	tt.Advance(200 * time.Millisecond)
	captureFrame(t, tt, "reader-end")

	r, ok := tt.Find("Reply")
	if !ok {
		t.Fatal("reply button not found")
	}
	x, y := r.X+r.W/2, r.Y+r.H/2
	tt.Press(x, y)
	captureFrame(t, tt, "button-pressed")
	tt.Release(x, y)
	captureFrame(t, tt, "compose-start")
	tt.Advance(100 * time.Millisecond)
	captureFrame(t, tt, "compose-mid")
	tt.Advance(250 * time.Millisecond)
	captureFrame(t, tt, "compose-end")
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}

	a.notice = "Conversation archived"
	tt.Frame()
	captureFrame(t, tt, "toast-start")
	tt.Advance(100 * time.Millisecond)
	captureFrame(t, tt, "toast-mid")
	tt.Advance(5 * time.Second)
	captureFrame(t, tt, "toast-end")
}

func captureFrame(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "screenshots", name+".png")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, tt.Image()); err != nil {
		t.Fatal(err)
	}
}
