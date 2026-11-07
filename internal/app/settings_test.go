package app

import (
	"context"
	"errors"
	"testing"

	"mica/internal/gmail"
	"mica/internal/mail"
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

func TestProfileAndPushShareAuthenticationAttempt(t *testing.T) {
	a := New(newFakeService())
	a.storeAccount = "demo@example.com"
	a.account.Email = a.storeAccount
	var jobs, updates []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.update = func(fn func()) { updates = append(updates, fn) }
	loginCalls := 0
	a.loginGmail = func(context.Context, mailStore, string, string, string, string) (mail.Service, string, error) {
		loginCalls++
		return nil, "", errors.New("authorization failed")
	}
	a.authenticateAccountWithCredentials(a.storeAccount, "id", "secret", "project")
	if len(jobs) != 1 || a.authCancel == nil {
		t.Fatalf("initial authentication did not start: jobs=%d active=%v", len(jobs), a.authCancel != nil)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	worker := &notificationWorker{ctx: ctx, cancel: cancel, done: make(chan struct{}), version: a.serviceVersion}
	a.notification = worker
	a.notificationsStarted = true
	a.handlePushError(worker, &gmail.APIError{StatusCode: 401})
	for len(updates) > 0 {
		update := updates[0]
		updates = updates[1:]
		update()
	}
	if len(jobs) != 1 {
		t.Fatalf("profile and notification errors started %d OAuth attempts, want 1", len(jobs))
	}
	jobs[0]()
	for len(updates) > 0 {
		update := updates[0]
		updates = updates[1:]
		update()
	}
	if loginCalls != 1 || a.authCancel != nil {
		t.Fatalf("failed authentication did not finish cleanly: calls=%d active=%v", loginCalls, a.authCancel != nil)
	}
	a.authenticateAccountWithCredentials(a.storeAccount, "id", "secret", "project")
	if len(jobs) != 2 {
		t.Fatalf("manual retry was blocked after failure: jobs=%d, want 2", len(jobs))
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
