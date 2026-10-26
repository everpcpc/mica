package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestLoadingViewsUseSkeletons(t *testing.T) {
	t.Run("conversation list", func(t *testing.T) {
		a := newTestApp(newFakeService())
		a.clearThread()
		a.threads = nil
		a.loading = true
		tt := ui.NewTester(a.View, 1280, 800)
		tt.SetLocale("en")
		r, ok := tt.Find("Loading conversations…")
		if !ok {
			t.Fatalf("loading region not found: %q", tt.Texts())
		}
		if r.H < 200 {
			t.Fatalf("loading region height = %v, want a conversation-shaped placeholder", r.H)
		}
	})

	t.Run("reader", func(t *testing.T) {
		a := newTestApp(newFakeService())
		a.clearThread()
		a.selectedThread = "thread-1"
		a.threadLoading = true
		tt := ui.NewTester(a.View, 1280, 800)
		tt.SetLocale("en")
		r, ok := tt.Find("Opening conversation…")
		if !ok {
			t.Fatalf("loading region not found: %q", tt.Texts())
		}
		if r.H < 200 {
			t.Fatalf("loading region height = %v, want a reader-shaped placeholder", r.H)
		}
	})
}

func TestLoadingSkeletonDoesNotReplaceContent(t *testing.T) {
	a := newTestApp(newFakeService())
	a.loading = true
	tt := ui.NewTester(a.View, 1280, 800)
	tt.SetLocale("en")
	if _, ok := tt.Find("Loading conversations…"); ok {
		t.Fatal("skeleton replaced loaded conversations during revalidation")
	}
	if !tt.HasText("Launch plan") {
		t.Fatalf("loaded conversation disappeared: %q", tt.Texts())
	}
}

func TestLoadingSkeletonDoesNotReplaceFilteredEmptyState(t *testing.T) {
	a := newTestApp(newFakeService())
	a.threads[0].Unread = false
	a.unreadOnly = true
	a.loading = true
	tt := ui.NewTester(a.View, 1280, 800)
	tt.SetLocale("en")
	if _, ok := tt.Find("Loading conversations…"); ok {
		t.Fatal("skeleton replaced the filtered empty state")
	}
	if !tt.HasText("No unread conversations") {
		t.Fatalf("filtered empty state is missing: %q", tt.Texts())
	}
}

func TestSkeletonShimmer(t *testing.T) {
	newTester := func() *ui.Tester {
		return ui.NewTester(func(c *ui.Context) {
			s := newSkeletonStyle(c)
			ui.Row(c).Padding(20).Children(func() {
				s.block(c, 6).Size(240, 12)
			})
		}, 300, 60)
	}

	t.Run("sweep", func(t *testing.T) {
		tt := newTester()
		before := append([]byte(nil), tt.Image().Pix...)
		changed := false
		for range 18 {
			tt.Advance(skeletonPeriod / 18)
			got := tt.Image()
			for i, value := range before {
				if got.Pix[i] != value {
					changed = true
					break
				}
			}
			if changed {
				break
			}
		}
		if !changed {
			t.Fatal("skeleton shimmer did not change any pixels over a full period")
		}
	})

	t.Run("reduced motion", func(t *testing.T) {
		tt := newTester()
		tt.SetPreferences(ui.Preferences{ReduceMotion: true})
		before := append([]byte(nil), tt.Image().Pix...)
		tt.Advance(2 * skeletonPeriod)
		got := tt.Image()
		if len(got.Pix) != len(before) {
			t.Fatalf("image size changed: got=%d want=%d", len(got.Pix), len(before))
		}
		for i, value := range before {
			if got.Pix[i] != value {
				t.Fatalf("reduced-motion skeleton changed pixel %d", i)
			}
		}
	})
}
