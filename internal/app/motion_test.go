package app

import (
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestUnreadFilterButtonTransition(t *testing.T) {
	a := newTestApp(newFakeService())
	var accent ui.Color
	tt := ui.NewTester(func(c *ui.Context) {
		accent = c.Theme().Accent
		a.View(c)
	}, 1280, 800)
	r, ok := tt.Find("Filter unread")
	if !ok {
		t.Fatal("unread filter button not found")
	}
	pixel := func() color.RGBA {
		return tt.Image().RGBAAt(int(r.X+4), int(r.Y+4))
	}
	matches := func(got color.RGBA, want ui.Color) bool {
		return got.R == want.R && got.G == want.G && got.B == want.B && got.A == want.A
	}
	before := pixel()
	if err := tt.Click("Filter unread"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(60 * time.Millisecond)
	mid := pixel()
	if mid == before || matches(mid, accent) {
		t.Fatalf("filter button is not transitioning to the active color: before=%v mid=%v accent=%v", before, mid, accent)
	}
	tt.Advance(200 * time.Millisecond)
	if got := pixel(); !matches(got, accent) {
		t.Fatalf("active filter background = %v, want accent %v", got, accent)
	}
	if err := tt.Click("Show all mail"); err != nil {
		t.Fatal(err)
	}
	tt.Move(0, 0)
	tt.Advance(time.Second)
	if got := pixel(); got != before {
		t.Fatalf("inactive filter background = %v, want original %v", got, before)
	}
}

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
