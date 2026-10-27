package app

import (
	"time"

	"github.com/egoist/mygo/ui"
)

var controlPressAnimation = ui.Animation{Spring: ui.SpringOptions{Duration: 220 * time.Millisecond, Bounce: 0.05}}

func pressFeedback(e ui.Element, scale float32) ui.Element {
	pose := ui.Pose{}
	if e.Pressed() {
		pose.Scale = scale
	}
	return e.AnimatePose(pose, controlPressAnimation)
}

func primaryAction(c *ui.Context, label string) ui.Element {
	return pressFeedback(ui.PrimaryButton(c, label), 0.97)
}

func sectionPanel(c *ui.Context) ui.Element {
	t := c.Theme()
	return ui.Column(c).Padding(14).Gap(12).Radius(12).Background(t.Background).Border(1, t.Border).Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.05))
}
