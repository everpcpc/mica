package app

import (
	"image/color"
	"testing"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

func TestPrimaryActionsUseFlatAccent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		label string
		open  func(*App, *ui.Tester)
	}{
		{name: "compose", label: "Compose", open: func(a *App, _ *ui.Tester) {}},
		{name: "send", label: "Send", open: func(a *App, _ *ui.Tester) { a.openCompose(nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(newFakeService())
			var accent ui.Color
			tt := ui.NewTester(func(c *ui.Context) {
				accent = c.Theme().Accent
				a.View(c)
			}, 1280, 800)
			tc.open(a, tt)
			tt.Frame()
			r, ok := tt.Find(tc.label)
			if !ok {
				t.Fatalf("primary action %q not found: %q", tc.label, tt.Texts())
			}
			y := int(r.Y + r.H/4)
			img := tt.Image()
			left := img.RGBAAt(int(r.X+r.W/3), y)
			right := img.RGBAAt(int(r.X+2*r.W/3), y)
			for _, point := range []struct {
				name  string
				pixel color.RGBA
			}{{name: "left", pixel: left}, {name: "right", pixel: right}} {
				if point.pixel.R != accent.R || point.pixel.G != accent.G || point.pixel.B != accent.B || point.pixel.A != accent.A {
					t.Errorf("primary action %q %s pixel = %v, want flat accent %v", tc.label, point.name, point.pixel, accent)
				}
			}
		})
	}
}

func controlPixel(tt *ui.Tester, button ui.Rect, x, y float32) (color.RGBA, color.RGBA) {
	scaleX := float32(tt.Image().Bounds().Dx()) / 1280
	scaleY := float32(tt.Image().Bounds().Dy()) / 800
	corner := tt.Image().RGBAAt(int((button.X+x)*scaleX), int((button.Y+y)*scaleY))
	outside := tt.Image().RGBAAt(int((button.X+x)*scaleX), int((button.Y-2)*scaleY))
	return corner, outside
}

func assertRoundedControl(t *testing.T, tt *ui.Tester, button ui.Rect, hover bool) {
	t.Helper()
	if button.W <= 0 || button.H <= 0 {
		t.Fatal("control has no committed bounds")
	}
	if hover {
		tt.Move(button.X+button.W/2, button.Y+button.H/2)
		tt.Frame()
	}
	corner, outside := controlPixel(tt, button, 1, 4)
	if corner != outside {
		t.Fatalf("control has a rectangular corner: corner=%v outside=%v button=%+v", corner, outside, button)
	}
}

func TestReaderControlsUseRoundedShapes(t *testing.T) {
	t.Run("reply", func(t *testing.T) {
		a := newTestApp(newFakeService())
		a.selectThread("thread-1")
		a.selectedLabel = mail.Inbox
		var button ui.Rect
		tt := ui.NewTester(func(c *ui.Context) {
			a.View(c)
			button = a.readerReply.Bounds(c)
		}, 1280, 800)
		tt.SetScale(2)
		tt.Frame()
		assertRoundedControl(t, tt, button, true)
	})
	for _, label := range []string{"Message actions", "Filter unread"} {
		t.Run(label, func(t *testing.T) {
			a := newTestApp(newFakeService())
			a.selectThread("thread-1")
			if label == "Message actions" {
				if a.expandedMessages == nil {
					a.expandedMessages = make(map[string]bool)
				}
				a.expandedMessages[readerMessageKey(a.thread.Messages[0])] = false
			}
			tt := ui.NewTester(a.View, 1280, 800)
			button, ok := tt.Find(label)
			if !ok {
				t.Fatalf("control %q not found", label)
			}
			assertRoundedControl(t, tt, button, true)
		})
	}
}
