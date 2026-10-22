package app

import (
	"strings"
	"testing"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

func TestMessageBodyPreservesImageSources(t *testing.T) {
	a := New(newFakeService())
	if a.markdown.LoadImage == nil {
		t.Error("HTML image loader is not configured")
	}
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

func TestMessageViewCentersMaxWidthContainer(t *testing.T) {
	service := newFakeService()
	service.thread.Messages[0].Text = ""
	text := "Centered " + strings.TrimSpace(strings.Repeat("long-content ", 20))
	service.thread.Messages[0].HTML = `<div style="margin:0px auto;max-width:200px"><p>` + text + `</p></div><img src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==">`
	a := newTestApp(service)
	a.selectThread("thread-1")
	tt := ui.NewTester(func(c *ui.Context) {
		a.messageView(c, 0, a.thread.Messages[0])
	}, 900, 700)
	textBounds, ok := tt.Find(text)
	if !ok {
		t.Fatal("centered text not found")
	}
	if textBounds.X < 300 {
		t.Fatalf("max-width container was not centered: %+v", textBounds)
	}
}

func TestMessageViewIgnoresFormattingWhitespace(t *testing.T) {
	render := func(source string) float32 {
		t.Helper()
		service := newFakeService()
		service.thread.Messages[0].Text = ""
		service.thread.Messages[0].HTML = source
		a := newTestApp(service)
		a.selectThread("thread-1")
		tt := ui.NewTester(func(c *ui.Context) {
			a.messageView(c, 0, a.thread.Messages[0])
		}, 900, 700)
		bounds, ok := tt.Find("Heading")
		if !ok {
			t.Fatal("heading not found")
		}
		return bounds.Y
	}
	compact := render(`<div><table><tbody><tr><td><p>Heading</p></td></tr></tbody></table></div>`)
	indented := render(`<div>
  <table>
    <tbody>
      <tr>
        <td>
          <p>Heading</p>
        </td>
      </tr>
    </tbody>
  </table>
</div>`)
	if difference := indented - compact; difference > 1 || difference < -1 {
		t.Fatalf("formatting whitespace changed heading position: compact=%v indented=%v", compact, indented)
	}
}

func TestMessageViewInheritsEmailTextStyle(t *testing.T) {
	service := newFakeService()
	service.thread.Messages[0].Text = ""
	service.thread.Messages[0].HTML = `<div style="font-size:22px;font-weight:bold;line-height:1.6;text-align:center;color:#1a2433"><p><a href="https://example.com" style="color:#1a2433;text-decoration:none">Heading</a></p></div>`
	a := newTestApp(service)
	a.selectThread("thread-1")
	tt := ui.NewTester(func(c *ui.Context) {
		a.messageView(c, 0, a.thread.Messages[0])
	}, 900, 700)
	bounds, ok := tt.Find("Heading")
	if !ok {
		t.Fatal("heading not found")
	}
	if bounds.H < 30 {
		t.Fatalf("heading did not inherit its font size and line height: %+v", bounds)
	}
	foundColor := false
	image := tt.Image()
	for y := int(bounds.Y); y < int(bounds.Y+bounds.H); y++ {
		for x := int(bounds.X); x < int(bounds.X+bounds.W); x++ {
			pixel := image.RGBAAt(x, y)
			if pixel.R == 0x1a && pixel.G == 0x24 && pixel.B == 0x33 {
				foundColor = true
				break
			}
		}
	}
	if !foundColor {
		t.Fatal("link did not keep the color declared by the email")
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

func TestMessageViewKeepsTableCellsOnSameRow(t *testing.T) {
	service := newFakeService()
	service.thread.Messages[0].Text = ""
	service.thread.Messages[0].HTML = `<table><tr><td><img alt="cover" src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="></td><td><strong>Article title</strong></td></tr></table>`
	a := newTestApp(service)
	a.selectThread("thread-1")
	tt := ui.NewTester(func(c *ui.Context) {
		a.messageView(c, 0, a.thread.Messages[0])
	}, 900, 700)
	tt.Advance(time.Second)
	if tt.HasText("[cover]") {
		t.Fatalf("image is shown as alt text instead of a rendered image: %q", tt.Texts())
	}
	cover, ok := tt.Find("cover")
	if !ok {
		t.Fatalf("cover image not found: %q", tt.Texts())
	}
	title, ok := tt.Find("Article title")
	if !ok {
		t.Fatal("article title not found")
	}
	if title.X < cover.X+cover.W || title.Y >= cover.Y+cover.H {
		t.Fatalf("table cells were linearized: cover=%+v title=%+v", cover, title)
	}
}

func TestMessageViewKeepsInlineBlockColumnsOnSameRow(t *testing.T) {
	service := newFakeService()
	service.thread.Messages[0].Text = ""
	service.thread.Messages[0].HTML = `<td><div class="mj-column-per-40" style="display:inline-block"><img alt="cover" src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="></div><div class="mj-column-per-60" style="display:inline-block"><strong>Article title</strong></div></td>`
	a := newTestApp(service)
	a.selectThread("thread-1")
	tt := ui.NewTester(func(c *ui.Context) {
		a.messageView(c, 0, a.thread.Messages[0])
	}, 900, 700)
	tt.Advance(time.Second)
	if tt.HasText("[cover]") {
		t.Fatalf("image is shown as alt text instead of a rendered image: %q", tt.Texts())
	}
	cover, ok := tt.Find("cover")
	if !ok {
		t.Fatalf("cover image not found: %q", tt.Texts())
	}
	title, ok := tt.Find("Article title")
	if !ok {
		t.Fatal("article title not found")
	}
	if title.X < cover.X+cover.W || title.Y >= cover.Y+cover.H {
		t.Fatalf("inline-block columns were linearized: cover=%+v title=%+v", cover, title)
	}
}

func TestMessageViewKeepsInlineBlockTextInColumn(t *testing.T) {
	service := newFakeService()
	service.thread.Messages[0].Text = ""
	service.thread.Messages[0].HTML = `<h4>First section</h4><p style="display:inline-block">Second section</p><h4 style="display:inline-block">Third section</h4><p style="display:inline-block">Fourth section</p>`
	a := newTestApp(service)
	a.selectThread("thread-1")
	tt := ui.NewTester(func(c *ui.Context) {
		a.messageView(c, 0, a.thread.Messages[0])
	}, 900, 700)
	first, ok := tt.Find("First section")
	if !ok {
		t.Fatal("first section not found")
	}
	second, ok := tt.Find("Second section")
	if !ok {
		t.Fatal("second section not found")
	}
	if second.Y < first.Y+first.H {
		t.Fatalf("inline-block text was laid out horizontally: first=%+v second=%+v", first, second)
	}
}

func TestMessageViewRendersInlineImageWithText(t *testing.T) {
	service := newFakeService()
	service.thread.Messages[0].Text = ""
	service.thread.Messages[0].HTML = `<div style="line-height:20px"><img alt="favicon" width="20" height="20" src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="><span>Site</span></div>`
	a := newTestApp(service)
	a.selectThread("thread-1")
	tt := ui.NewTester(func(c *ui.Context) {
		a.messageView(c, 0, a.thread.Messages[0])
	}, 900, 700)
	tt.Advance(time.Second)
	if tt.HasText("[favicon]") {
		t.Fatalf("inline image is shown as alt text: %q", tt.Texts())
	}
	icon, ok := tt.Find("favicon")
	if !ok {
		t.Fatalf("inline image not found: %q", tt.Texts())
	}
	site, ok := tt.Find("Site")
	if !ok {
		t.Fatal("site text not found")
	}
	if site.X < icon.X+icon.W || site.Y >= icon.Y+icon.H {
		t.Fatalf("inline image and text were separated: icon=%+v site=%+v", icon, site)
	}
}
