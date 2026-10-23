package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestPrimaryActionsUseGradient(t *testing.T) {
	for _, tc := range []struct {
		name  string
		label string
		open  func(*App)
	}{
		{name: "compose", label: "Compose", open: func(a *App) {}},
		{name: "send", label: "Send", open: func(a *App) { a.openCompose(nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(newFakeService())
			tc.open(a)
			tt := ui.NewTester(a.View, 1280, 800)
			r, ok := tt.Find(tc.label)
			if !ok {
				t.Fatalf("primary action %q not found", tc.label)
			}
			y := int(r.Y + r.H/4)
			left := tt.Image().RGBAAt(int(r.X+r.W/3), y)
			right := tt.Image().RGBAAt(int(r.X+2*r.W/3), y)
			delta := 0
			for _, channels := range [3][2]uint8{{left.R, right.R}, {left.G, right.G}, {left.B, right.B}} {
				difference := int(channels[0]) - int(channels[1])
				if difference < 0 {
					difference = -difference
				}
				delta += difference
			}
			if delta < 4 {
				t.Fatalf("primary action %q uses a flat fill: left=%v right=%v", tc.label, left, right)
			}
		})
	}
}
