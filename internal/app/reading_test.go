package app

import (
	"strings"
	"testing"

	"mica/internal/mail"
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
	body, rich := a.messageBody(message)
	if !rich {
		t.Fatal("HTML body was not recognized")
	}
	if !strings.Contains(body, "iVBOR") {
		t.Errorf("data image source was lost: %q", body)
	}
	if strings.Contains(body, "cid:") || !strings.Contains(body, "message-1") {
		t.Errorf("inline attachment was not resolved: %q", body)
	}
}

func TestMessageBodyCarriesImageDimensions(t *testing.T) {
	a := New(newFakeService())
	body, _ := a.messageBody(mail.Message{
		ID:   "message-1",
		HTML: `<img src="https://images.example/pixel.png" width="40" height="30" style="width:20px;height:10px">`,
	})
	if !strings.Contains(body, "width=20") || !strings.Contains(body, "height=10") {
		t.Fatalf("image dimensions were not carried into the loader URL: %q", body)
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

func TestPrepareMessageHTMLHidesImages(t *testing.T) {
	got := prepareMessageHTML(mail.Message{HTML: `<img src="hidden.png" style="display:none">`})
	if strings.Contains(got, "<img") {
		t.Fatalf("hidden image is still present: %q", got)
	}
}
