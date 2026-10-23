package app

import (
	"bytes"
	"image"
	"image/gif"
	"time"

	"github.com/egoist/mygo/ui"
)

const maxPhotoFrames = 120

type photoAnimation struct {
	bitmap *ui.Bitmap
	frames []*image.Paletted
	delays []time.Duration
	index  int
	next   time.Time
	loops  int
	done   bool
}

func newPhotoAnimation(data []byte) (*photoAnimation, error) {
	if len(data) == 0 || len(data) > maxImageBytes {
		return nil, errImageUnavailable
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxImagePixels {
		return nil, errImageUnavailable
	}
	if format == "gif" {
		animation, err := gif.DecodeAll(bytes.NewReader(data))
		if err != nil {
			return nil, errImageUnavailable
		}
		if len(animation.Image) > 1 {
			totalPixels := int64(config.Width) * int64(config.Height) * int64(len(animation.Image))
			if len(animation.Image) > maxPhotoFrames || totalPixels > 4*maxImagePixels {
				return nil, errImageUnavailable
			}
			delays := make([]time.Duration, len(animation.Image))
			for i, delay := range animation.Delay {
				delays[i] = max(100*time.Millisecond, time.Duration(delay)*10*time.Millisecond)
			}
			return &photoAnimation{
				bitmap: ui.NewBitmap(animation.Image[0]),
				frames: animation.Image,
				delays: delays,
				loops:  animation.LoopCount,
			}, nil
		}
	}
	bitmap, err := ui.DecodeBitmap(data)
	if err != nil {
		return nil, errImageUnavailable
	}
	return &photoAnimation{bitmap: bitmap}, nil
}

func (a *photoAnimation) frame(c *ui.Context) *ui.Bitmap {
	if a == nil || a.bitmap == nil {
		return nil
	}
	if len(a.frames) < 2 || c.Preferences().ReduceMotion {
		if a.index != 0 {
			a.index = 0
			a.bitmap.Update(a.frames[0])
		}
		a.next = time.Time{}
		a.done = false
		return a.bitmap
	}
	if a.done {
		return a.bitmap
	}
	now := c.Now()
	if a.next.IsZero() {
		a.next = now.Add(a.delays[a.index])
		c.After(a.delays[a.index])
		return a.bitmap
	}
	if now.Before(a.next) {
		c.After(a.next.Sub(now))
		return a.bitmap
	}
	if a.index == len(a.frames)-1 && a.loops > 0 {
		a.loops--
		if a.loops == 0 {
			a.done = true
			a.next = time.Time{}
			return a.bitmap
		}
	}
	a.index = (a.index + 1) % len(a.frames)
	a.bitmap.Update(a.frames[a.index])
	a.next = now.Add(a.delays[a.index])
	c.After(a.delays[a.index])
	return a.bitmap
}

func (a *App) accountPhotoFrame(c *ui.Context) *ui.Bitmap {
	if a.photoAnimation == nil {
		return a.accountPhoto
	}
	return a.photoAnimation.frame(c)
}
