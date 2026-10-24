package app

import (
	"testing"

	"mica/internal/mail"
)

func TestThreadAndMessageRefsRoundTrip(t *testing.T) {
	ref := threadRefForAccount(" User@Example.com ", "thread-1")
	if got := parseThreadRef(ref.key()); got != (threadRef{AccountEmail: "user@example.com", ID: "thread-1"}) {
		t.Fatalf("parsed ref = %+v", got)
	}
	if got := parseThreadRef("thread-1"); got != (threadRef{ID: "thread-1"}) {
		t.Fatalf("parsed legacy ref = %+v", got)
	}
	message := messageRefForAccount("user@example.com", "thread-1", "message-1")
	if message.key() != "user@example.com\x00thread-1\x00message-1" {
		t.Fatalf("message key = %q", message.key())
	}
}

func TestAccountRegistryReplacementOnlyInvalidatesOwner(t *testing.T) {
	active := &deactivatingService{fakeService: newFakeService()}
	a := New(active)
	a.storeAccount = "a@example.com"
	a.account = mail.Account{Email: "a@example.com"}
	a.accountVerified = true
	first := &deactivatingService{fakeService: newFakeService()}
	second := &deactivatingService{fakeService: newFakeService()}
	account := mail.Account{Email: " B@Example.com "}
	a.registerAccountClient(first, account, []mail.Label{{ID: "first"}}, true)
	_, firstGeneration := a.ownerService("b@example.com")
	a.registerAccountClient(second, account, []mail.Label{{ID: "second"}}, true)
	service, secondGeneration := a.ownerService("b@example.com")
	if service != second || secondGeneration <= firstGeneration {
		t.Fatalf("replacement did not advance generation: service=%p first=%d second=%d", service, firstGeneration, secondGeneration)
	}
	if !first.deactivated || active.deactivated {
		t.Fatalf("replacement deactivated the wrong service: first=%v active=%v", first.deactivated, active.deactivated)
	}
	if !a.ownerVerified("b@example.com") || len(a.ownerLabels("b@example.com")) != 1 || a.ownerLabels("b@example.com")[0].ID != "second" {
		t.Fatal("replacement did not publish verified account metadata")
	}
	a.removeAccountClient("b@example.com")
	if !second.deactivated || active.deactivated {
		t.Fatalf("removal deactivated the wrong service: second=%v active=%v", second.deactivated, active.deactivated)
	}
}
