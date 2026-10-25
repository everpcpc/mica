package main

import (
	"sync"
	"testing"

	"mica/internal/app"
	"mica/internal/mail"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/ui"
)

var testUpdaterOnce sync.Once

func useTestUpdater() {
	testUpdaterOnce.Do(func() { mygo.Use(updater.New(updater.Options{Language: "en"})) })
}

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
	useTestUpdater()
	i18n.SetLocaleFunc(func() string { return "en" })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })
	opened := false
	menu, _, _ := appMenu(func() { opened = true }, nil, nil)
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

func TestAppMenuIncludesMailboxFilters(t *testing.T) {
	useTestUpdater()
	i18n.SetLocaleFunc(func() string { return "en" })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })
	opened := false
	menu, _, setEnabled := appMenu(nil, func() { opened = true }, nil)
	var filters *mygo.MenuItem
	for _, top := range menu.Items() {
		if top.Label == "Mailbox" && len(top.Submenu) == 1 {
			filters = top.Submenu[0]
			break
		}
	}
	if filters == nil || filters.Label != "Gmail filters…" {
		t.Fatalf("Mailbox menu has no Gmail Filters action: %+v", filters)
	}
	setEnabled(false)
	if filters.IsEnabled() {
		t.Fatal("Gmail Filters remained enabled while unavailable")
	}
	setEnabled(true)
	if !filters.IsEnabled() {
		t.Fatal("Gmail Filters did not become available")
	}
	filters.Click(filters, nil)
	if !opened {
		t.Fatal("Gmail Filters did not invoke its action")
	}
}

func TestAppMenuShiftedGmailShortcuts(t *testing.T) {
	useTestUpdater()
	i18n.SetLocaleFunc(func() string { return "en" })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })
	var keys []ui.Key
	menu, setEnabled, _ := appMenu(nil, nil, func(key ui.Key, _ ui.Modifiers) { keys = append(keys, key) })
	var items []*mygo.MenuItem
	for _, top := range menu.Items() {
		if top.Label == "Conversation actions" || top.Role == mygo.RoleHelp {
			items = append(items, top.Submenu...)
		}
	}
	if len(items) != 3 {
		t.Fatalf("shifted shortcut menu items = %d, want 3", len(items))
	}
	for i, want := range []struct {
		label, accelerator string
		key                ui.Key
	}{
		{label: "Move to Trash", accelerator: "Shift+3", key: ui.Key3},
		{label: "Report spam", accelerator: "Shift+1", key: ui.Key1},
		{label: "Keyboard shortcuts", accelerator: "Shift+/", key: ui.KeySlash},
	} {
		item := items[i]
		if item.Label != want.label || item.Accelerator != want.accelerator {
			t.Errorf("menu item %d = %q %q, want %q %q", i, item.Label, item.Accelerator, want.label, want.accelerator)
		}
		setEnabled(false, "Move to Trash", "Report spam")
		if item.IsEnabled() {
			t.Errorf("%s remained enabled with text input", item.Label)
		}
		setEnabled(true, "Move to Trash", "Report spam")
		item.Click(item, nil)
		if len(keys) != i+1 || keys[i] != want.key {
			t.Errorf("%s invoked %v, want %v", item.Label, keys, want.key)
		}
	}
	setEnabled(true, "Move out of Trash", "Not spam")
	if items[0].Label != "Move out of Trash" || items[1].Label != "Not spam" {
		t.Fatalf("recovery menu labels = %q, %q", items[0].Label, items[1].Label)
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
