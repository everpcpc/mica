package app

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"testing"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

func TestAccountPhotoGIFStaysOnFirstFrame(t *testing.T) {
	a := newTestApp(newFakeService())
	a.loadAccountPhoto(mail.Account{PhotoURL: "data:image/gif;base64," + base64.StdEncoding.EncodeToString(testAnimatedGIF(t))})
	if a.accountPhoto == nil {
		t.Fatal("animated account photo was not decoded")
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Padding(10).Children(func() {
			ui.Avatar(c, "Demo", a.accountPhoto)
		})
	}, 100, 100)
	if got := tt.Image().RGBAAt(24, 24); got != (color.RGBA{R: 255, A: 255}) {
		t.Fatalf("first frame = %v, want red", got)
	}
	tt.Advance(150 * time.Millisecond)
	if got := tt.Image().RGBAAt(24, 24); got != (color.RGBA{R: 255, A: 255}) {
		t.Fatalf("frame after delay = %v, want first red frame", got)
	}
	if tt.FramePending() {
		t.Fatal("account photo scheduled animation frames")
	}
}

func TestAccountPhotoStaticDoesNotAnimate(t *testing.T) {
	a := newTestApp(newFakeService())
	a.loadAccountPhoto(mail.Account{PhotoURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(testPNG(t))})
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Padding(10).Children(func() {
			ui.Avatar(c, "Demo", a.accountPhoto)
		})
	}, 100, 100)
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Fatal("static account photo scheduled animation frames")
	}
}

func testAnimatedGIF(t *testing.T) []byte {
	t.Helper()
	palette := color.Palette{color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}}
	red := image.NewPaletted(image.Rect(0, 0, 4, 4), palette)
	blue := image.NewPaletted(image.Rect(0, 0, 4, 4), palette)
	for i := range blue.Pix {
		blue.Pix[i] = 1
	}
	var output bytes.Buffer
	if err := gif.EncodeAll(&output, &gif.GIF{
		Image: []*image.Paletted{red, blue},
		Delay: []int{1, 1},
	}); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
