package app

import (
	"testing"

	"mica/locales"

	"github.com/egoist/mygo/ui"
)

func TestSettingsLanguageUsesDisplayLabels(t *testing.T) {
	for _, tc := range []struct {
		name     string
		language string
		want     string
		count    int
	}{
		{name: "system", language: "", want: locales.LanguageSystem(), count: 2},
		{name: "english", language: "en", want: locales.LanguageEnglish(), count: 1},
		{name: "chinese", language: "zh-Hans", want: locales.LanguageChinese(), count: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(newFakeService())
			a.settings = &settingsState{open: true, language: tc.language}
			tt := ui.NewTester(a.View, 1280, 800)
			count := 0
			for _, text := range tt.Texts() {
				if text == tc.want {
					count++
				}
			}
			if count != tc.count {
				t.Fatalf("display label %q appears %d times, want %d; rendered texts: %q", tc.want, count, tc.count, tt.Texts())
			}
		})
	}
}

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
