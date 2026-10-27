package app

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

func openReaderActions(t *testing.T, tt *ui.Tester) {
	t.Helper()
	button, ok := tt.Find("Message actions")
	if !ok {
		t.Fatalf("reader actions menu not found: %q", tt.Texts())
	}
	x, y := button.X+button.W/2, button.Y+button.H/2
	tt.Press(x, y)
	tt.Release(x, y)
}

func TestAccountRowIsSidebarFooter(t *testing.T) {
	a := newTestApp(newFakeService())
	tt := ui.NewTester(a.View, 1280, 800)
	account, ok := tt.Find("Demo User")
	if !ok {
		t.Fatalf("account row not found: %q", tt.Texts())
	}
	if account.Y < 700 {
		t.Fatalf("account row is not in the sidebar footer: %+v", account)
	}
	if bottom := 800 - account.Y - account.H; bottom < 8 {
		t.Fatalf("account row has only %vpt below it, want at least 8pt", bottom)
	}
}

func TestThreadListUsesSenderAvatar(t *testing.T) {
	a := newTestApp(newFakeService())
	tt := ui.NewTester(a.threadList, 400, 700)
	avatar, ok := tt.Find("Ada")
	if !ok {
		t.Fatalf("sender avatar not found: %q", tt.Texts())
	}
	if avatar.W < 24 || avatar.H < 24 {
		t.Fatalf("sender has text only, no avatar: %+v", avatar)
	}
}

func TestThreadListMessageCountDoesNotChangeRowHeight(t *testing.T) {
	rowHeight := func(count int) float32 {
		service := newFakeService()
		service.threads[0].MessageCount = count
		a := newTestApp(service)
		tt := ui.NewTester(a.threadList, 400, 700)
		row, ok := tt.Find("Ada, Launch plan, The latest plan")
		if !ok {
			t.Fatalf("conversation row not found: %q", tt.Texts())
		}
		return row.H
	}
	one, several := rowHeight(1), rowHeight(3)
	if one != several {
		t.Fatalf("message count changes row height: one=%v several=%v", one, several)
	}
}

func TestThreadListSelectionAndUnreadColors(t *testing.T) {
	service := newFakeService()
	service.threads = append(service.threads, mail.ThreadSummary{
		ID: "thread-2", Subject: "Second", Senders: "Bob", Snippet: "Another snippet", Updated: time.Now(),
		MessageCount: 1, LabelIDs: []string{mail.Inbox, mail.Unread}, Unread: true,
	})
	a := newTestApp(service)
	a.selectThread("thread-1")
	var selected, accent, surface ui.Color
	tt := ui.NewTester(func(c *ui.Context) {
		selected, accent, surface = c.Theme().SurfaceHover, c.Theme().Accent, c.Theme().Surface
		a.View(c)
	}, 1280, 800)
	tt.SetScale(2)
	tt.Advance(time.Second)
	first, ok := tt.Find("Ada, Launch plan, The latest plan")
	if !ok {
		t.Fatal("selected conversation not found")
	}
	second, ok := tt.Find("Bob, Second, Another snippet")
	if !ok {
		t.Fatal("unread conversation not found")
	}
	img := tt.Image()
	scale := float32(img.Bounds().Dx()) / 1280
	pixel := func(row ui.Rect) ui.Color {
		p := img.RGBAAt(int((row.X+row.W/2)*scale), int((row.Y+4)*scale))
		return ui.Color{R: p.R, G: p.G, B: p.B, A: p.A}
	}
	if got := pixel(first); !sameVisualColor(got, selected) {
		t.Fatalf("selected row = %v, want neutral surface %v", got, selected)
	}
	wantUnread := accent.Alpha(0.06).Over(surface)
	if got := pixel(second); !sameVisualColor(got, wantUnread) {
		t.Fatalf("unread row = %v, want soft accent %v", got, wantUnread)
	}
}

func sameVisualColor(a, b ui.Color) bool {
	for _, channels := range [4][2]uint8{{a.R, b.R}, {a.G, b.G}, {a.B, b.B}, {a.A, b.A}} {
		difference := int(channels[0]) - int(channels[1])
		if difference < 0 {
			difference = -difference
		}
		if difference > 1 {
			return false
		}
	}
	return true
}

func TestReaderActionsMenuKeepsSecondaryActions(t *testing.T) {
	a := newTestApp(newFakeService())
	a.selectThread("thread-1")
	tt := ui.NewTester(a.View, 1280, 800)
	button, ok := tt.Find("Message actions")
	if !ok {
		t.Fatalf("reader actions menu not found: %q", tt.Texts())
	}
	x, y := button.X+button.W/2, button.Y+button.H/2
	tt.Press(x, y)
	menu := tt.Menu()
	for _, want := range []string{"Report spam", "Snooze", "Edit labels"} {
		if !slices.Contains(menu, want) {
			t.Errorf("reader actions menu lacks %q: %q", want, menu)
		}
	}
	tt.Release(x, y)
}

func TestReaderHeaderCondensesAfterScrolling(t *testing.T) {
	service := newFakeService()
	service.thread.Messages[0].Text = strings.Repeat("Long body\n", 100)
	a := newTestApp(service)
	a.selectThread("thread-1")
	tt := ui.NewTester(a.reader, 1280, 800)
	countSubject := func() int {
		count := 0
		for _, text := range tt.Texts() {
			if text == "Launch plan" {
				count++
			}
		}
		return count
	}
	if got := countSubject(); got != 1 {
		t.Fatalf("subject copies before scrolling = %d, want 1", got)
	}
	a.readerScroll.Y = 96
	a.readerScroll.MaxY = 400
	tt.Frame()
	if got := countSubject(); got < 2 {
		t.Fatalf("compact reader header did not appear after scrolling: %q", tt.Texts())
	}
	a.readerScroll.Y = 0
	a.readerScroll.MaxY = 0
	tt.Frame()
	if got := countSubject(); got != 1 {
		t.Fatalf("short-message subject copies = %d, want 1", got)
	}
}

func TestReaderHeaderMotion(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reduce bool
	}{
		{name: "animated"},
		{name: "reduced motion", reduce: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := newFakeService()
			service.thread.Messages[0].Text = strings.Repeat("Long body\n", 100)
			a := newTestApp(service)
			a.selectThread("thread-1")
			tt := ui.NewTester(a.reader, 1280, 800)
			tt.SetPreferences(ui.Preferences{ReduceMotion: tc.reduce})
			tt.Advance(time.Second)
			a.readerScroll.Y = 96
			a.readerScroll.MaxY = 400
			tt.Frame()
			start := append([]byte(nil), tt.Image().Pix...)
			if got := tt.FramePending(); got == tc.reduce {
				t.Fatalf("header animation pending = %v, reduce motion = %v", got, tc.reduce)
			}
			tt.Advance(60 * time.Millisecond)
			mid := append([]byte(nil), tt.Image().Pix...)
			if !tc.reduce && slices.Equal(start, mid) {
				t.Fatal("header did not change between its start and middle frames")
			}
			if tc.reduce && !slices.Equal(start, mid) {
				t.Fatal("reduced-motion header changed between frames")
			}
			tt.Advance(300 * time.Millisecond)
			if tt.FramePending() {
				t.Fatal("header animation did not settle")
			}
		})
	}
}

func TestCaptureVisualRefresh(t *testing.T) {
	if os.Getenv("MICA_CAPTURE") == "" {
		t.Skip("capture is opt-in")
	}
	t.Run("minimum window", func(t *testing.T) {
		a := newTestApp(mail.NewDemo())
		a.account.Name = "Alex Morgan"
		a.account.Email = "alex@example.com"
		a.account.Demo = false
		a.selectThread("t-sync")
		tt := ui.NewTester(a.View, 980, 600)
		tt.SetLocale("en")
		tt.Advance(time.Second)
		captureFrame(t, tt, "visual-min")
	})
	t.Run("multi selection", func(t *testing.T) {
		service := newFakeService()
		service.threads = append(service.threads, mail.ThreadSummary{
			ID: "thread-2", Subject: "Second", Senders: "Bob", Snippet: "Another snippet", Updated: time.Now(),
			MessageCount: 1, LabelIDs: []string{mail.Inbox, mail.Unread}, Unread: true,
		})
		a := newTestApp(service)
		a.selectThread("thread-1")
		a.threadSelection.Add("thread-2")
		tt := ui.NewTester(a.View, 1280, 800)
		tt.SetLocale("en")
		captureFrame(t, tt, "visual-multi-select")
	})
	t.Run("reader header", func(t *testing.T) {
		service := newFakeService()
		service.thread.Messages[0].Text = strings.Repeat("Long body\n", 100)
		a := newTestApp(service)
		a.selectThread("thread-1")
		tt := ui.NewTester(a.View, 1280, 800)
		tt.SetLocale("en")
		a.readerScroll.Y = 96
		a.readerScroll.MaxY = 400
		tt.Frame()
		captureFrame(t, tt, "visual-header-start")
		tt.Advance(60 * time.Millisecond)
		captureFrame(t, tt, "visual-header-mid")
		tt.Advance(200 * time.Millisecond)
		captureFrame(t, tt, "visual-header-end")
		tt.SetDark(true)
		tt.Advance(time.Second)
		captureFrame(t, tt, "visual-header-dark-end")
	})
}
