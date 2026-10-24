package app

import (
	"context"
	"testing"

	"mica/internal/mail"
)

func TestFetchAccountClientDoesNotChangeActiveAccount(t *testing.T) {
	h := newAccountTransitionHarness()
	service, account, labels, err := h.app.fetchAccountClient(context.Background(), h.target.account.Email)
	if err != nil {
		t.Fatal(err)
	}
	h.app.registerAccountClient(service, account, labels, true)
	if h.active != "" {
		t.Fatalf("active marker changed to %q", h.active)
	}
	last, found, err := h.store.LastAccount(context.Background())
	if err != nil || !found || last.Email != h.old.account.Email {
		t.Fatalf("last account = %+v, found=%v, err=%v", last, found, err)
	}
	if !h.app.ownerVerified(h.target.account.Email) {
		t.Fatal("restored account was not verified")
	}
}

func TestFetchAccountClientRejectsMismatchedIdentity(t *testing.T) {
	service := &deactivatingService{fakeService: newFakeService()}
	service.account = mail.Account{Email: "other@example.com"}
	a := New(newFakeService())
	a.accountService = func(context.Context, string) (mail.Service, error) {
		return service, nil
	}
	_, _, _, err := a.fetchAccountClient(context.Background(), "expected@example.com")
	if err == nil || !service.deactivated {
		t.Fatalf("mismatched account was accepted: err=%v deactivated=%v", err, service.deactivated)
	}
}
