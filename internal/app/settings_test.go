package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestConnectRequiresOAuthConfiguration(t *testing.T) {
	a := newTestApp(newFakeService())
	a.settings = &settingsState{open: true}
	a.connectCustomGmail()
	if a.settings.err == "" {
		t.Fatal("empty OAuth configuration started a connection")
	}
	if a.settings.busy {
		t.Fatal("validation failure left settings busy")
	}
}

func TestSettingsCustomOAuthIsCollapsed(t *testing.T) {
	a := newTestApp(newFakeService())
	a.settings = &settingsState{open: true}
	tt := ui.NewTester(a.View, 1280, 800)
	if !tt.HasText("Mica opens Google sign-in in your browser and connects directly to Gmail.") {
		t.Fatal("built-in Gmail connection is not the primary choice")
	}
	for _, text := range []string{"Client ID", "Client Secret", "Open Google Cloud credentials"} {
		if tt.HasText(text) {
			t.Fatalf("custom OAuth configuration is visible while collapsed: %q", text)
		}
	}
	if err := tt.Click("Use a custom OAuth client"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, text := range []string{"Client ID", "Client Secret", "Open Google Cloud credentials"} {
		if !tt.HasText(text) {
			t.Fatalf("custom OAuth configuration is missing after expanding: %q", text)
		}
	}
}
