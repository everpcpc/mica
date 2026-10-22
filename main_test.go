package main

import (
	"testing"

	"mica/internal/app"
	"mica/internal/mail"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

func TestView(t *testing.T) {
	a := app.New(mail.NewDemo())
	tt := ui.NewTester(a.View, 1280, 800)
	if !tt.HasText("Mica") || !tt.HasText("Inbox") {
		t.Fatalf("main window is missing its mailbox UI: %q", tt.Texts())
	}
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	t.Cleanup(func() { i18n.SetLocaleFunc(mygo.App.Locale) })
	tt.SetLocale("zh-Hans")
	if !tt.HasText("收件箱") {
		t.Fatalf("Simplified Chinese mailbox is missing: %q", tt.Texts())
	}
}
