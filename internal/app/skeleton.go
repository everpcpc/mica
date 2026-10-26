package app

import (
	"time"

	"mica/locales"

	"github.com/egoist/mygo/ui"
)

const (
	skeletonPeriod = 1800 * time.Millisecond
	skeletonSweep  = 1100 * time.Millisecond
	skeletonFrame  = time.Second / 30
	skeletonBand   = 180
)

type skeletonStyle struct {
	base  ui.Color
	sheen ui.Color
	span  float32
	still bool
}

func newSkeletonStyle(c *ui.Context) skeletonStyle {
	t := c.Theme()
	base, sheen := t.Text.Alpha(0.055), t.Text.Alpha(0.025)
	if t.Dark {
		base, sheen = t.Text.Alpha(0.08), t.Text.Alpha(0.16)
	}
	width, _ := c.Size()
	return skeletonStyle{
		base: base, sheen: sheen, span: width,
		still: c.Preferences().ReduceMotion,
	}
}

func (s skeletonStyle) paint(p *ui.Painter, r ui.Rect, radius float32) {
	if s.still || r.W <= 0 {
		p.Fill(r, s.base, radius)
		return
	}
	at := time.Duration(p.Now().UnixNano() % int64(skeletonPeriod))
	if at >= skeletonSweep {
		p.Fill(r, s.base, radius)
		p.After(skeletonPeriod - at)
		return
	}
	p.After(skeletonFrame)
	progress := float32(at) / float32(skeletonSweep)
	x := -skeletonBand/2 + progress*(s.span+skeletonBand)
	if x+skeletonBand/2 < r.X || x-skeletonBand/2 > r.X+r.W {
		p.Fill(r, s.base, radius)
		return
	}
	along := func(x float32) float32 { return (x - r.X) / r.W }
	p.FillGradient(r, ui.LinearGradient{Angle: 90, Stops: []ui.GradientStop{
		{Color: s.base, At: along(x - skeletonBand/2)},
		{Color: s.sheen, At: along(x)},
		{Color: s.base, At: along(x + skeletonBand/2)},
	}}, radius)
}

func (s skeletonStyle) block(c *ui.Context, radius float32) ui.Element {
	return ui.Box(c).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		s.paint(p, r, radius)
	})
}

func (s skeletonStyle) line(c *ui.Context, height, width float32) ui.Element {
	return s.block(c, height/2).Height(height).WidthPercent(width)
}

func (a *App) threadListSkeleton(c *ui.Context) {
	s := newSkeletonStyle(c)
	_, height := c.Size()
	rows := max(3, int((height-150)/90))
	ui.Column(c).Grow(1).Clip().Role(ui.RoleProgress).Label(locales.LoadingConversations()).Padding(8, 8).Children(func() {
		for i := range rows {
			ui.Column(c.Key(i)).Padding(10, 12).Gap(8).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					s.line(c, 10, 42+float32(i%3)*8)
					ui.Spacer(c)
					s.block(c, 4).Size(44, 9)
				})
				s.line(c, 10, 68+float32(i%2)*12)
				s.line(c, 9, 94)
				s.line(c, 9, 58+float32(i%4)*7)
			})
		}
	})
}

func (a *App) readerSkeleton(c *ui.Context) {
	s := newSkeletonStyle(c)
	ui.Column(c).Grow(1).Clip().Role(ui.RoleProgress).Label(locales.OpeningThread()).Padding(titleBarHeight, 0, 0, 0).Children(func() {
		ui.Row(c).Justify(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MaxWidth(960).Padding(24, 24, 32, 24).Gap(18).Children(func() {
				s.line(c, 24, 56)
				ui.Row(c).Gap(6).Children(func() {
					s.block(c, 8).Size(48, 16)
					s.block(c, 8).Size(64, 16)
					s.line(c, 9, 18)
				})
				ui.Row(c).Margin(10, 0, 0, 0).Gap(10).AlignItems(ui.Center).Children(func() {
					s.block(c, 16).Size(32, 32)
					ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
						s.line(c, 10, 30)
						s.line(c, 9, 46)
					})
					s.block(c, 4).Size(92, 9)
				})
				ui.Column(c).Gap(10).Children(func() {
					s.line(c, 11, 96)
					s.line(c, 11, 91)
					s.line(c, 11, 97)
					s.line(c, 11, 72)
				})
				ui.Row(c).Gap(10).Margin(4, 0, 0, 0).Children(func() {
					s.block(c, 16).Size(76, 32)
					s.block(c, 16).Size(92, 32)
					s.block(c, 16).Size(88, 32)
				})
			})
		})
	})
}
