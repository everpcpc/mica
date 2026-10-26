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

func primaryGradient() ui.LinearGradient {
	return ui.LinearGradient{From: ui.Hex("#3b82f6"), To: ui.Hex("#6366f1"), Angle: 135, Oklab: true}
}

func primaryAction(c *ui.Context, label string) ui.Element {
	button := ui.PrimaryButton(c, label).LinearGradient(primaryGradient()).Shadow(0, 2, 8, 0, ui.RGBA(37, 99, 235, 0.22))
	return pressFeedback(button, 0.97)
}

func sectionPanel(c *ui.Context) ui.Element {
	t := c.Theme()
	return ui.Column(c).Padding(14).Gap(12).Radius(12).Background(t.Background).Border(1, t.Border).Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.05))
}
