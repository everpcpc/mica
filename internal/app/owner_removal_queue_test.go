package app

import (
	"context"
	"testing"
)

func TestRemoveInactiveAccountWaitsForMutationQueue(t *testing.T) {
	h := newAccountTransitionHarness()
	a := h.app
	a.settings = &settingsState{open: true}
	var events []string
	a.enqueueMutation(mutationJob{
		id: threadRefForAccount(h.target.account.Email, "shared").key(),
		runFunc: func() error {
			events = append(events, "mutation")
			return nil
		},
	})
	a.removeCredentials = func(context.Context, string) error {
		events = append(events, "delete")
		return nil
	}
	a.deleteCheckpoint = func(string) error { return nil }
	a.removeAccount(h.target.account.Email)
	if a.accountTransition == nil || len(h.jobs) != 1 {
		t.Fatalf("removal started before the mutation drained: transition=%p jobs=%d", a.accountTransition, len(h.jobs))
	}
	if a.service != h.old || len(events) != 0 {
		t.Fatalf("removal changed the current account before draining: service=%p events=%v", a.service, events)
	}
	h.runJob(t)
	h.runUpdate(t)
	if len(h.jobs) != 2 {
		t.Fatalf("removal was not scheduled after mutation completion: jobs=%d", len(h.jobs))
	}
	h.runJob(t)
	h.runUpdate(t)
	h.runJob(t)
	h.runUpdate(t)
	if len(events) != 2 || events[0] != "mutation" || events[1] != "delete" {
		t.Fatalf("operation order = %v, want mutation then delete", events)
	}
	if a.accountTransition != nil || a.service != h.old {
		t.Fatalf("inactive removal left unexpected state: transition=%p service=%p", a.accountTransition, a.service)
	}
}
