package app

import (
	"context"
	"errors"
	"testing"

	"mica/internal/gmail"
	"mica/internal/mail"
)

func TestRemoveActiveCleanupFailureStillActivatesTarget(t *testing.T) {
	h := newAccountTransitionHarness()
	a := h.app
	a.settings = &settingsState{open: true}
	a.removeCredentials = func(context.Context, string) error { return errors.New("keychain unavailable") }
	a.deleteCheckpoint = func(string) error { return nil }
	a.removeAccount(h.old.account.Email)
	h.runJob(t)
	h.runUpdate(t)
	h.finishTransition(t)
	if a.service != h.target || h.active != h.target.account.Email {
		t.Fatalf("cleanup failure left a partial active account: service=%p active=%q", a.service, h.active)
	}
	if a.settings.err == "" {
		t.Fatal("cleanup failure was not reported")
	}
	if _, found, _ := h.store.Account(context.Background(), h.old.account.Email); !found {
		t.Fatal("failed credential cleanup removed the account catalog entry")
	}
}

func TestRemoveActiveFallsBackToDemoWithoutUsableCredentials(t *testing.T) {
	h := newAccountTransitionHarness()
	a := h.app
	a.settings = &settingsState{open: true}
	a.accountService = func(context.Context, string) (mail.Service, error) {
		return nil, gmail.ErrNoSession
	}
	a.removeCredentials = func(context.Context, string) error { return nil }
	a.deleteCheckpoint = func(string) error { return nil }
	markerDeleted := false
	a.deleteActiveAccount = func() error {
		markerDeleted = true
		return nil
	}
	a.removeAccount(h.old.account.Email)
	h.runJob(t)
	h.runUpdate(t)
	h.finishTransition(t)
	if !a.account.Demo || !markerDeleted {
		t.Fatalf("active removal did not reach Demo: demo=%v markerDeleted=%v", a.account.Demo, markerDeleted)
	}
	if _, found, _ := h.store.Account(context.Background(), h.target.account.Email); !found {
		t.Fatal("inactive account without usable credentials was removed")
	}
}

func TestRemoveActiveDemoCleanupFailureKeepsCurrentState(t *testing.T) {
	h := newAccountTransitionHarness()
	a := h.app
	a.settings = &settingsState{open: true}
	a.accountService = func(context.Context, string) (mail.Service, error) {
		return nil, gmail.ErrNoSession
	}
	a.removeCredentials = func(context.Context, string) error { return errors.New("keychain unavailable") }
	a.deleteCheckpoint = func(string) error { return nil }
	markerDeleted := false
	a.deleteActiveAccount = func() error {
		markerDeleted = true
		return nil
	}
	a.removeAccount(h.old.account.Email)
	h.runJob(t)
	h.runUpdate(t)
	h.finishTransition(t)
	if a.service != h.old || markerDeleted {
		t.Fatalf("failed Demo cleanup changed current state: service=%p markerDeleted=%v", a.service, markerDeleted)
	}
	last, found, err := h.store.LastAccount(context.Background())
	if err != nil || !found || last.Email != h.old.account.Email {
		t.Fatalf("last account = %+v, found=%v, err=%v", last, found, err)
	}
}

func TestRemoveActiveDemoMarkerFailureStillActivatesDemo(t *testing.T) {
	h := newAccountTransitionHarness()
	a := h.app
	a.settings = &settingsState{open: true}
	a.accountService = func(context.Context, string) (mail.Service, error) {
		return nil, gmail.ErrNoSession
	}
	a.removeCredentials = func(context.Context, string) error { return nil }
	a.deleteCheckpoint = func(string) error { return nil }
	a.deleteActiveAccount = func() error { return errors.New("keychain unavailable") }
	a.removeAccount(h.old.account.Email)
	h.runJob(t)
	h.runUpdate(t)
	h.finishTransition(t)
	if !a.account.Demo || a.settings.err == "" {
		t.Fatalf("marker failure left unexpected state: demo=%v settings=%+v", a.account.Demo, a.settings)
	}
	if _, found, _ := h.store.LastAccount(context.Background()); found {
		t.Fatal("removed account remained as the startup fallback")
	}
}
