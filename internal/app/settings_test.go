package app

import (
	"errors"
	"testing"

	"mica/internal/gmail"
	"mica/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/oauth2"
)

func TestPendingTokenStore(t *testing.T) {
	store := &pendingTokenStore{}
	if _, err := store.Load(); !errors.Is(err, gmail.ErrNoSession) {
		t.Fatalf("empty pending store = %v", err)
	}
	want := &oauth2.Token{AccessToken: "pending", RefreshToken: "refresh"}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil || got != want {
		t.Fatalf("pending token = %+v, %v", got, err)
	}
	if err := store.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); !errors.Is(err, gmail.ErrNoSession) {
		t.Fatalf("deleted pending store = %v", err)
	}
}

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

type deactivatingService struct {
	*fakeService
	deactivated bool
}

func (s *deactivatingService) Deactivate() {
	s.deactivated = true
}

func TestCancelAuthenticationDeactivatesService(t *testing.T) {
	service := &deactivatingService{fakeService: newFakeService()}
	a := New(service)
	a.cancelAuthentication()
	if !service.deactivated {
		t.Fatal("service was not deactivated before authentication changed")
	}
}

func TestRemoveAccountRejectsUnconfirmedIdentity(t *testing.T) {
	a := newTestApp(newFakeService())
	a.storeAccount = "a@example.com"
	a.account.Email = "b@example.com"
	a.accountVerified = false
	a.settings = &settingsState{open: true}
	a.removeAccount("a@example.com")
	if a.settings.err == "" || a.settings.busy {
		t.Fatalf("removal with unconfirmed identity did not fail safely: %+v", a.settings)
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
