package app

import (
	"strings"
	"testing"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestMessageBodyPreservesImageSources(t *testing.T) {
	message := mail.Message{
		ID: "message-1",
		HTML: `<p><img alt="pixel" src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="></p>` +
			`<p><img alt="inline" src="cid:attachment-1"></p>`,
		Attachments: []mail.Attachment{{ID: "attachment-1"}},
	}
	body := prepareMessageHTML(message)
	if !strings.Contains(body, "iVBOR") {
		t.Errorf("data image source was lost: %q", body)
	}
	if strings.Contains(body, "cid:") || !strings.Contains(body, "message-1") {
		t.Errorf("inline attachment was not resolved: %q", body)
	}
}

func TestMessageBodyCarriesImageDimensions(t *testing.T) {
	body := prepareMessageHTML(mail.Message{
		ID:   "message-1",
		HTML: `<img src="https://images.example/pixel.png" width="40" height="30" style="width:20px;height:10px">`,
	})
	if !strings.Contains(body, "width=20") || !strings.Contains(body, "height=10") {
		t.Fatalf("image dimensions were not carried into the loader URL: %q", body)
	}
}

func TestThreadContentUsesBackground(t *testing.T) {
	for _, tc := range []struct {
		name string
		dark bool
	}{
		{name: "light"},
		{name: "dark", dark: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(newFakeService())
			a.selectThread("thread-1")
			var want ui.Color
			tt := ui.NewTester(func(c *ui.Context) {
				want = c.Theme().Background
				a.threadContent(c)
			}, 900, 700)
			tt.SetDark(tc.dark)
			tt.Advance(time.Second)
			for _, point := range []struct{ x, y int }{{0, 0}, {899, 0}, {0, 699}, {899, 699}} {
				got := tt.Image().RGBAAt(point.x, point.y)
				if got.R != want.R || got.G != want.G || got.B != want.B || got.A != want.A {
					t.Fatalf("reader background at %v = %v, want %v", point, got, want)
				}
			}
		})
	}
}

func TestReaderToolbarStaysLightOverWebView(t *testing.T) {
	for _, tc := range []struct {
		name string
		dark bool
	}{
		{name: "light"},
		{name: "dark", dark: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(newFakeService())
			a.selectThread("thread-1")
			a.thread.Messages[0].HTML = "<p>Body</p>"
			a.emailPages = map[string]*emailPage{
				"message-1": {messageID: "message-1", view: new(mygo.WebView), height: 1000},
			}
			tt := ui.NewTester(a.View, 1280, 800)
			tt.SetDark(tc.dark)
			tt.Advance(time.Second)
			archive, ok := tt.Find("Archive")
			if !ok {
				t.Fatal("reader toolbar not found")
			}
			x, y := int(archive.X+archive.W/2), int(archive.Y)-2
			before := tt.Image().RGBAAt(x, y)
			a.readerScroll.Y = 650
			tt.Frame()
			tt.Advance(time.Second)
			after := tt.Image().RGBAAt(x, y)
			delta := func(a, b uint8) int {
				if a > b {
					return int(a - b)
				}
				return int(b - a)
			}
			for _, difference := range []int{delta(before.R, after.R), delta(before.G, after.G), delta(before.B, after.B)} {
				if difference > 20 {
					t.Fatalf("toolbar changed too much over WebView: before=%v after=%v at (%d,%d)", before, after, x, y)
				}
			}
		})
	}
}

func TestThreadActionsIncludeReplyAll(t *testing.T) {
	service := newFakeService()
	service.thread.Messages[0].To = "demo@example.com, Bob <bob@example.com>"
	a := newTestApp(service)
	a.selectThread("thread-1")
	tt := ui.NewTester(a.View, 1280, 800)
	if err := tt.Click("Reply all"); err != nil {
		t.Fatal(err)
	}
	if a.compose == nil || !strings.Contains(a.compose.to, "bob@example.com") {
		t.Fatalf("Reply all recipients = %+v, want Bob included", a.compose)
	}
}

func TestMessageViewFallsBackToTextWhenHTMLIsEmpty(t *testing.T) {
	service := newFakeService()
	service.thread.Messages[0].Text = "Plain fallback"
	service.thread.Messages[0].HTML = `<img src="hidden.png" style="display:none">`
	a := newTestApp(service)
	a.selectThread("thread-1")
	tt := ui.NewTester(func(c *ui.Context) {
		a.messageView(c, 0, a.thread.Messages[0])
	}, 900, 700)
	if !tt.HasText("Plain fallback") {
		t.Fatalf("plain text fallback was not shown: %q", tt.Texts())
	}
}

func TestMessageBodyHasReadingInset(t *testing.T) {
	inkLeft := func(tt *ui.Tester, r ui.Rect) int {
		img := tt.Image()
		for x := int(r.X); x < int(r.X+r.W); x++ {
			for y := int(r.Y); y < int(r.Y+r.H); y++ {
				p := img.RGBAAt(x, y)
				if p.R < 128 && p.G < 128 && p.B < 128 && p.A > 128 {
					return x
				}
			}
		}
		return -1
	}
	left := make(map[string]int)
	for _, tc := range []struct {
		name string
		text string
		html string
	}{
		{name: "plain text", text: "Body"},
		{name: "html", html: `<p>Body</p>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(newFakeService())
			a.selectThread("thread-1")
			message := a.thread.Messages[0]
			message.Text = tc.text
			message.HTML = tc.html
			tt := ui.NewTester(func(c *ui.Context) {
				a.messageView(c, 0, message)
			}, 900, 700)
			body, ok := tt.Find("Body")
			if !ok {
				t.Fatalf("body not found: %q", tt.Texts())
			}
			left[tc.name] = inkLeft(tt, body)
		})
	}
	if got := left["plain text"] - left["html"]; got != 0 {
		t.Fatalf("body padding difference = %d, want the same inset for plain text and HTML (plain %d, html %d)", got, left["plain text"], left["html"])
	}
}

func TestDecodeSizedImage(t *testing.T) {
	data := testPNG(t)
	for _, tc := range []struct {
		name          string
		width, height string
		wantW, wantH  int
	}{
		{name: "native", wantW: 2, wantH: 3},
		{name: "exact", width: "4", height: "8", wantW: 4, wantH: 8},
		{name: "width keeps ratio", width: "4", wantW: 4, wantH: 6},
		{name: "height keeps ratio", height: "6", wantW: 4, wantH: 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, err := decodeSizedImage(data, tc.width, tc.height)
			if err != nil {
				t.Fatal(err)
			}
			bitmap := source.(interface{ Size() (int, int) })
			if width, height := bitmap.Size(); width != tc.wantW || height != tc.wantH {
				t.Fatalf("size = %dx%d, want %dx%d", width, height, tc.wantW, tc.wantH)
			}
		})
	}
}

func TestPrepareMessageHTMLUsesInlineImageSize(t *testing.T) {
	got := prepareMessageHTML(mail.Message{HTML: `<img width="40" height="30" style="width:20px;height:10px">`})
	if !strings.Contains(got, `width="20"`) || !strings.Contains(got, `height="10"`) {
		t.Fatalf("inline image size was not preserved: %q", got)
	}
}

func TestPrepareMessageHTMLKeepsResponsiveImageWidth(t *testing.T) {
	got := prepareMessageHTML(mail.Message{HTML: `<img width="190" style="width:100%;height:auto">`})
	if !strings.Contains(got, `width="190"`) {
		t.Fatalf("responsive image lost its declared width: %q", got)
	}
}

func TestPrepareMessageHTMLHidesImages(t *testing.T) {
	got := prepareMessageHTML(mail.Message{HTML: `<img src="hidden.png" style="display:none">`})
	if strings.Contains(got, "<img") {
		t.Fatalf("hidden image is still present: %q", got)
	}
}

func TestMessageViewHidesInlineMetadata(t *testing.T) {
	service := newFakeService()
	service.thread.Messages[0].Text = ""
	service.thread.Messages[0].HTML = `<p>Visible text <span style="color: transparent; font-size: 0; display: none; visibility: hidden; overflow: hidden; opacity: 0; width: 0; height: 0; max-width: 0; max-height: 0; mso-hide: all">Message ID: hidden-metadata</span></p>`
	a := newTestApp(service)
	a.selectThread("thread-1")
	tt := ui.NewTester(func(c *ui.Context) {
		a.messageView(c, 0, a.thread.Messages[0])
	}, 900, 700)
	if tt.HasText("hidden-metadata") {
		t.Fatalf("hidden metadata was rendered: %q", tt.Texts())
	}
	if !tt.HasText("Visible text") {
		t.Fatal("visible content was hidden too")
	}
}

func TestMessageExpansionUsesSpringMotion(t *testing.T) {
	for _, tc := range []struct {
		name        string
		reduce      bool
		wantPending bool
	}{
		{name: "spring", wantPending: true},
		{name: "reduced motion", reduce: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := newFakeService()
			service.thread.Messages = []mail.Message{
				{ID: "message-old", ThreadID: "thread-1", From: "Ada <ada@example.com>", Date: time.Now(), Text: strings.TrimSpace(strings.Repeat("Older message details ", 10)) + " OLDER_TAIL"},
				{ID: "message-latest", ThreadID: "thread-1", From: "Cid <cid@example.com>", Date: time.Now(), Text: "Latest body"},
			}
			a := newTestApp(service)
			a.selectThread("thread-1")
			tt := ui.NewTester(func(c *ui.Context) {
				a.messageView(c, 0, a.thread.Messages[0])
			}, 900, 300)
			tt.SetPreferences(ui.Preferences{ReduceMotion: tc.reduce})
			tt.Advance(time.Second)
			if err := tt.Click("Expand message"); err != nil {
				t.Fatal(err)
			}
			if got := tt.FramePending(); got != tc.wantPending {
				t.Fatalf("animation pending = %v, want %v", got, tc.wantPending)
			}
			tt.Advance(2 * time.Second)
			if tt.FramePending() {
				t.Fatal("message expansion did not settle")
			}
			if !tt.HasText("OLDER_TAIL") {
				t.Fatal("expanded message content is missing after the transition")
			}
		})
	}
}

func TestThreadCollapsesOlderReadMessages(t *testing.T) {
	service := newFakeService()
	service.thread.Messages = []mail.Message{
		{ID: "message-old", ThreadID: "thread-1", From: "Ada <ada@example.com>", Date: time.Now(), Text: strings.TrimSpace(strings.Repeat("Older message details ", 10)) + " OLDER_TAIL"},
		{ID: "message-unread", ThreadID: "thread-1", From: "Bob <bob@example.com>", Date: time.Now(), Text: "Unread body", LabelIDs: []string{mail.Unread}},
		{ID: "message-latest", ThreadID: "thread-1", From: "Cid <cid@example.com>", Date: time.Now(), Text: "Latest body"},
	}
	a := newTestApp(service)
	a.selectThread("thread-1")
	tt := ui.NewTester(a.View, 1280, 800)
	if !tt.HasText("Unread body") || !tt.HasText("Latest body") {
		t.Fatalf("unread or latest message was collapsed: %q", tt.Texts())
	}
	if tt.HasText("OLDER_TAIL") {
		t.Fatal("older read message was not collapsed")
	}
	if !tt.HasText("Expand message") {
		t.Fatalf("collapsed message has no accessible expand action: %q", tt.Texts())
	}
	if err := tt.Click("Expand message"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("OLDER_TAIL") {
		t.Fatal("expanding did not restore the full message")
	}
	if err := tt.Click("Collapse message"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("OLDER_TAIL") {
		t.Fatal("collapsing did not hide the full message again")
	}
}

func TestPrepareMessageHTMLDropsTrackingPixels(t *testing.T) {
	for _, tc := range []struct {
		name string
		html string
	}{
		{name: "attributes", html: `<img src="tracker.png" width="1" height="1">`},
		{name: "zero", html: `<img src="tracker.png" width="0" height="0">`},
		{name: "inline style", html: `<img src="tracker.png" style="width:1px;height:1px">`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := prepareMessageHTML(mail.Message{HTML: tc.html})
			if strings.Contains(body, "tracker.png") {
				t.Fatalf("tracking pixel is still present: %q", body)
			}
		})
	}
	body := prepareMessageHTML(mail.Message{HTML: `<img src="icon.png" width="1" height="1" style="width:20px;height:20px"><img src="line.png" width="1">`})
	if !strings.Contains(body, "icon.png") || !strings.Contains(body, "line.png") {
		t.Fatalf("normal images were mistaken for trackers: %q", body)
	}
}
