package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

func TestParseMailto(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want mail.Compose
	}{
		{
			name: "literal plus and multiple recipients",
			raw:  "mailto:ada+tag@example.com,bob@example.com?cc=cc+one@example.com&bcc=bcc@example.com&subject=Hello+World&body=Line+one%0ALine%20two%20%2B%20plus",
			want: mail.Compose{To: "ada+tag@example.com,bob@example.com", Cc: "cc+one@example.com", Bcc: "bcc@example.com", Subject: "Hello+World", Text: "Line+one\nLine two + plus"},
		},
		{
			name: "percent encoding",
			raw:  "mailto:ada%2Btag@example.com?subject=Percent%20encoded&body=100%25",
			want: mail.Compose{To: "ada+tag@example.com", Subject: "Percent encoded", Text: "100%"},
		},
		{
			name: "host form and repeated cc",
			raw:  "mailto://ada@example.com?cc=a@example.com&cc=b@example.com&body=Hello",
			want: mail.Compose{To: "ada@example.com", Cc: "a@example.com, b@example.com", Text: "Hello"},
		},
		{
			name: "path decoded once",
			raw:  "mailto:///ada%252Btag@example.com",
			want: mail.Compose{To: "ada%2Btag@example.com"},
		},
		{
			name: "path percent literal",
			raw:  "mailto:///ada%25tag@example.com",
			want: mail.Compose{To: "ada%tag@example.com"},
		},
		{
			name: "empty recipient draft",
			raw:  "mailto:?subject=Project+Update&body=Hello+World",
			want: mail.Compose{Subject: "Project+Update", Text: "Hello+World"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMailto(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got.To != tc.want.To || got.Cc != tc.want.Cc || got.Bcc != tc.want.Bcc || got.Subject != tc.want.Subject || got.Text != tc.want.Text {
				t.Fatalf("parseMailto() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseMailtoRejectsInvalidLinks(t *testing.T) {
	for _, raw := range []string{
		"https://example.com",
		"mailto:bad%ZZ",
		"mailto:ada@example.com?subject=bad%0Aheader",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := parseMailto(raw); err == nil {
				t.Fatal("invalid mailto link was accepted")
			}
		})
	}
}

func TestOpenMailtoWaitsForRestore(t *testing.T) {
	a := New(newFakeService())
	a.after = func(time.Duration, func()) {}
	raw := "mailto:ada+tag@example.com?subject=Hello+World&body=Line+one%0ALine%20two"
	if err := a.OpenMailto(raw); err != nil {
		t.Fatal(err)
	}
	if a.compose != nil || a.pendingMailto == "" {
		t.Fatal("mailto opened before account restoration")
	}
	a.restoreAttempted = true
	a.authCancel = func() {}
	a.openPendingMailto()
	if a.compose != nil {
		t.Fatal("mailto opened while authentication was still running")
	}
	a.authCancel = nil
	a.openPendingMailto()
	m := a.compose
	if m == nil || m.to != "ada+tag@example.com" || m.subject != "Hello+World" || m.doc.Text().PlainText() != "Line+one\nLine two" {
		t.Fatalf("restored mailto composer = %+v", m)
	}
	if a.pendingMailto != "" || m.revision != 1 {
		t.Fatalf("pending mailto was not consumed as an unsent draft: pending=%q revision=%d", a.pendingMailto, m.revision)
	}
	if sent := a.service.(*fakeService).sent; sent.To != "" {
		t.Fatal("mailto automatically sent a message")
	}
}

func TestOpenMailtoWaitsForStartup(t *testing.T) {
	a := New(newFakeService())
	a.restoreAttempted = true
	a.after = func(time.Duration, func()) {}
	a.pendingMailto = "mailto:ada@example.com?subject=Hello"
	var jobs, updates []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.update = func(fn func()) { updates = append(updates, fn) }
	a.Start()
	if a.compose != nil || a.pendingMailto == "" || !a.startupPending {
		t.Fatalf("mailto opened before startup recovery completed: compose=%p pending=%q startup=%v", a.compose, a.pendingMailto, a.startupPending)
	}
	for steps := 0; len(jobs) > 0 || len(updates) > 0; steps++ {
		if steps > 30 {
			t.Fatal("startup recovery did not settle")
		}
		if len(updates) > 0 {
			updates[0]()
			updates = updates[1:]
		} else {
			jobs[0]()
			jobs = jobs[1:]
		}
	}
	if a.compose == nil || a.pendingMailto != "" {
		t.Fatalf("mailto did not open after startup recovery: compose=%p pending=%q", a.compose, a.pendingMailto)
	}
}

func TestOpenMailtoWaitsForDraftRecovery(t *testing.T) {
	a := New(newFakeService())
	a.restoreAttempted = true
	a.storeAccount = "demo@example.com"
	a.account = mail.Account{Email: "demo@example.com"}
	store := newFakeMailStore()
	store.drafts["demo@example.com"] = mail.DraftSnapshot{Compose: mail.Compose{To: "keep@example.com", Text: "Existing draft"}}
	a.store = store
	a.pendingMailto = "mailto:other@example.com?subject=Other"
	var jobs, updates []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.update = func(fn func()) { updates = append(updates, fn) }
	a.restoreDraftSnapshot()
	if !a.draftRestorePending || a.compose != nil {
		t.Fatal("draft recovery did not enter its pending state")
	}
	a.openPendingMailto()
	if a.compose != nil || a.pendingMailto == "" {
		t.Fatal("mailto opened before draft recovery completed")
	}
	jobs[0]()
	if len(updates) != 1 {
		t.Fatalf("draft recovery updates = %d, want 1", len(updates))
	}
	updates[0]()
	if a.compose == nil || a.compose.to != "keep@example.com" || a.pendingMailto != "" {
		t.Fatalf("draft recovery was overwritten by mailto: compose=%+v pending=%q", a.compose, a.pendingMailto)
	}
}

func TestOpenMailtoDoesNotOverwriteComposer(t *testing.T) {
	a := newTestApp(newFakeService())
	a.openCompose(nil)
	original := a.compose
	original.to = "keep@example.com"
	a.pendingMailto = "mailto:other@example.com?subject=Other"
	a.openPendingMailto()
	if a.compose != original || a.compose.to != "keep@example.com" || a.pendingMailto != "" {
		t.Fatalf("mailto replaced an existing composer: compose=%p original=%p pending=%q", a.compose, original, a.pendingMailto)
	}
	if a.notice == "" {
		t.Fatal("blocked mailto did not explain why it was ignored")
	}
}

func TestRestoreGmailLoadFailureReleasesPending(t *testing.T) {
	a := New(newFakeService())
	a.schedule = func(fn func()) { fn() }
	a.after = func(time.Duration, func()) {}
	a.pendingMailto = "mailto:ada@example.com?subject=Hello"
	a.loadActiveAccount = func() (string, error) { return "", errOffline }
	a.restoreGmail()
	if a.compose == nil || a.pendingMailto != "" || a.authCancel != nil {
		t.Fatalf("load failure left mailto blocked: compose=%p pending=%q cancel=%v", a.compose, a.pendingMailto, a.authCancel != nil)
	}
}

func TestOpenMailtoAfterTransitionFailure(t *testing.T) {
	a := New(newFakeService())
	a.restoreAttempted = true
	a.pendingMailto = "mailto:ada@example.com?subject=Hello"
	transition := &accountTransition{email: "b@example.com"}
	a.accountTransition = transition
	a.failAccountTransition(transition, errOffline)
	if a.compose == nil || a.pendingMailto != "" {
		t.Fatalf("transition failure left mailto blocked: compose=%p pending=%q", a.compose, a.pendingMailto)
	}
}

func TestOpenMailtoAfterSettingsFailure(t *testing.T) {
	a := New(newFakeService())
	a.restoreAttempted = true
	a.pendingMailto = "mailto:ada@example.com?subject=Hello"
	a.settings = &settingsState{open: true}
	a.accountAuthenticationFailed(a.settings, "", errOffline)
	if a.compose == nil || a.pendingMailto != "" {
		t.Fatalf("authentication failure left mailto blocked: compose=%p pending=%q", a.compose, a.pendingMailto)
	}
}

func TestOpenMailtoAfterSettingsClose(t *testing.T) {
	a := New(newFakeService())
	a.restoreAttempted = true
	a.pendingMailto = "mailto:ada@example.com?subject=Hello"
	a.settings = &settingsState{open: true}
	a.closeSettings()
	if a.compose == nil || a.pendingMailto != "" {
		t.Fatalf("closing Settings left mailto blocked: compose=%p pending=%q", a.compose, a.pendingMailto)
	}
}

func TestOpenMailtoAfterInactiveRemoval(t *testing.T) {
	h := newAccountTransitionHarness()
	h.app.after = func(time.Duration, func()) {}
	h.app.pendingMailto = "mailto:ada@example.com?subject=Hello"
	h.app.settings = &settingsState{open: true}
	h.app.removeCredentials = func(context.Context, string) error { return nil }
	h.app.deleteCheckpoint = func(string) error { return nil }
	h.app.removeAccount(h.target.account.Email)
	h.runJob(t)
	h.runUpdate(t)
	for steps := 0; len(h.jobs) > 0 || len(h.updates) > 0; steps++ {
		if steps > 20 {
			t.Fatal("inactive removal recovery did not settle")
		}
		if len(h.updates) > 0 {
			h.runUpdate(t)
		} else {
			h.runJob(t)
		}
	}
	if h.app.compose == nil || h.app.pendingMailto != "" {
		t.Fatalf("inactive removal left mailto blocked: compose=%p pending=%q", h.app.compose, h.app.pendingMailto)
	}
}

func TestCloseSettingsDuringAuthOpensPending(t *testing.T) {
	a := New(newFakeService())
	a.restoreAttempted = true
	a.pendingMailto = "mailto:ada@example.com?subject=Hello"
	cancel := func() {}
	a.authCancel = cancel
	a.settings = &settingsState{open: true, cancel: cancel}
	a.closeSettings()
	if a.authCancel != nil {
		t.Fatal("closing Settings retained the authentication cancel state")
	}
	if a.compose == nil || a.pendingMailto != "" {
		t.Fatalf("closing during authentication left mailto blocked: compose=%p pending=%q", a.compose, a.pendingMailto)
	}
}

func TestConnectGmailFailureReleasesPending(t *testing.T) {
	a := New(newFakeService())
	a.schedule = func(fn func()) { fn() }
	a.after = func(time.Duration, func()) {}
	a.restoreAttempted = true
	a.pendingMailto = "mailto:ada@example.com?subject=Hello"
	a.loginGmail = func(context.Context, mailStore, string, string, string, string) (mail.Service, string, error) {
		return nil, "", errOffline
	}
	a.settings = &settingsState{open: true, clientID: "id", clientSecret: "secret", projectID: "project"}
	a.connectGmailWith("id", "secret", "project", false)
	if a.compose == nil || a.pendingMailto != "" || a.authCancel != nil {
		t.Fatalf("manual Connect failure left mailto blocked: compose=%p pending=%q cancel=%v", a.compose, a.pendingMailto, a.authCancel != nil)
	}
}

func TestFailTransitionRestoresDraftBeforeMailto(t *testing.T) {
	a := New(newFakeService())
	a.restoreAttempted = true
	a.storeAccount = "demo@example.com"
	a.account = mail.Account{Email: "demo@example.com"}
	store := newFakeMailStore()
	store.drafts["demo@example.com"] = mail.DraftSnapshot{Compose: mail.Compose{To: "keep@example.com", Text: "Existing draft"}}
	a.store = store
	a.pendingMailto = "mailto:other@example.com?subject=Other"
	a.draftRestoreAccount = "demo@example.com"
	a.notificationsStarted = true
	transition := &accountTransition{email: "b@example.com"}
	a.accountTransition = transition
	var jobs, updates []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.update = func(fn func()) { updates = append(updates, fn) }
	a.failAccountTransition(transition, errOffline)
	if a.compose != nil || !a.draftRestorePending || a.pendingMailto == "" {
		t.Fatalf("failure opened mailto before restoring the draft: compose=%p draftPending=%v pending=%q", a.compose, a.draftRestorePending, a.pendingMailto)
	}
	if len(jobs) != 1 {
		t.Fatalf("draft restore jobs = %d, want 1", len(jobs))
	}
	jobs[0]()
	if len(updates) != 1 {
		t.Fatalf("draft restore updates = %d, want 1", len(updates))
	}
	updates[0]()
	if a.compose == nil || a.compose.to != "keep@example.com" || a.pendingMailto != "" {
		t.Fatalf("mail to overwrote the recovered draft: compose=%+v pending=%q", a.compose, a.pendingMailto)
	}
}

func TestUseServiceClearsDraftRestorePending(t *testing.T) {
	a := New(newFakeService())
	a.schedule = func(fn func()) { fn() }
	a.restoreAttempted = true
	a.draftRestorePending = true
	a.useService(newFakeService())
	if a.draftRestorePending {
		t.Fatal("service replacement retained the old draft restore pending state")
	}
}

func TestDefaultMailAppRegistrationIsExplicit(t *testing.T) {
	a := newTestApp(newFakeService())
	registered := false
	a.registerURLScheme = func(scheme string) error {
		if scheme != "mailto" {
			t.Fatalf("registered scheme = %q", scheme)
		}
		registered = true
		return nil
	}
	a.isURLSchemeRegistered = func(string) bool { return registered }
	a.settings = &settingsState{open: true}
	tt := ui.NewTester(a.View, 1280, 800)
	tt.SetLocale("en")
	if !tt.HasText("Default email app") {
		t.Fatal("default email app setting is missing")
	}
	if registered {
		t.Fatal("mailto was registered before user action")
	}
	if err := tt.Click("Set as default"); err != nil {
		t.Fatal(err)
	}
	if !registered || !a.defaultMailAppRegistered() {
		t.Fatal("explicit action did not register mailto")
	}
}

func TestDefaultMailAppSettingsUsesCachedStatus(t *testing.T) {
	a := newTestApp(newFakeService())
	a.defaultMailApp = true
	a.isURLSchemeRegistered = func(string) bool {
		t.Fatal("system handler was queried during rendering")
		return false
	}
	a.settings = &settingsState{open: true}
	tt := ui.NewTester(a.View, 1280, 800)
	tt.Frame()
}

func TestDefaultMailAppFailure(t *testing.T) {
	a := newTestApp(newFakeService())
	a.registerURLScheme = func(string) error { return errors.New("denied") }
	a.settings = &settingsState{open: true}
	a.setDefaultMailApp()
	if a.settings.err == "" {
		t.Fatal("registration failure was not shown")
	}
}

func TestMailtoConfigDeclaresScheme(t *testing.T) {
	data, err := os.ReadFile("../../mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		URLSchemes []string `json:"urlSchemes"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	for _, scheme := range config.URLSchemes {
		if scheme == "mailto" {
			return
		}
	}
	t.Fatalf("mygo.json does not declare mailto: %v", config.URLSchemes)
}
