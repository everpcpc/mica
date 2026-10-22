package main

import (
	"testing"

	"mica/internal/app"
	"mica/internal/mail"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/plugins/updater"
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

func TestAppMenuIncludesSettings(t *testing.T) {
	mygo.Use(updater.New(updater.Options{Language: "en"}))
	i18n.SetLocaleFunc(func() string { return "en" })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })
	opened := false
	menu := appMenu(func() { opened = true })
	var settings *mygo.MenuItem
	for _, item := range menu.Items()[0].Submenu {
		if item.Label == "Settings…" {
			settings = item
			break
		}
	}
	if settings == nil {
		t.Fatal("application menu has no Settings item")
	}
	if settings.Accelerator != "CmdOrCtrl+," {
		t.Fatalf("Settings accelerator = %q, want CmdOrCtrl+,", settings.Accelerator)
	}
	settings.Click(settings, nil)
	if !opened {
		t.Fatal("Settings did not invoke its action")
	}
}

func TestSettingsHasNoSidebarEntry(t *testing.T) {
	a := app.New(mail.NewDemo())
	tt := ui.NewTester(a.View, 1280, 800)
	if tt.HasText("Settings") {
		t.Fatalf("Settings still has a sidebar entry: %q", tt.Texts())
	}
}

func TestSidebarHasNoBrand(t *testing.T) {
	a := app.New(mail.NewDemo())
	tt := ui.NewTester(a.View, 1280, 800)
	name, ok := tt.Find("Mica")
	if !ok {
		t.Fatal("account name not found")
	}
	compose, ok := tt.Find("Compose")
	if !ok {
		t.Fatal("Compose button not found")
	}
	if name.Y < compose.Y+compose.H {
		t.Fatalf("Mica remains beside Compose in the title bar: name=%+v compose=%+v", name, compose)
	}
}
