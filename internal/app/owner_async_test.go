package app

import (
	"context"
	"testing"

	"mica/internal/mail"
)

type ownerAttachmentSource struct {
	data []byte
}

func (s ownerAttachmentSource) name() string { return "shared.txt" }
func (s ownerAttachmentSource) read(context.Context, int64) (mail.OutgoingAttachment, error) {
	return mail.OutgoingAttachment{Name: "shared.txt", Data: s.data}, nil
}

func TestOwnerSnapshotWriteRejectsReplacement(t *testing.T) {
	h := newOwnerThreadHarness()
	var jobs []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	h.app.writeDraftSnapshot("b@example.com", &mail.DraftSnapshot{DraftID: "draft-shared", Compose: mail.Compose{Text: "B draft"}})
	if len(jobs) != 1 {
		t.Fatalf("snapshot jobs = %d, want 1", len(jobs))
	}
	h.app.registerAccountClient(newFakeService(), mail.Account{Email: "b@example.com"}, nil, true)
	jobs[0]()
	if _, found, _ := h.store.DraftSnapshot(context.Background(), "b@example.com"); found {
		t.Fatal("replaced account accepted an old snapshot write")
	}
}

func TestOwnerComposeAttachmentRejectsReplacement(t *testing.T) {
	h := newOwnerThreadHarness()
	var jobs []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	m := newDraftComposer(mail.Compose{}, "b@example.com")
	m.attachments = []composerAttachment{{id: 1, loading: true, source: ownerAttachmentSource{data: []byte("B")}}}
	m.readingAttachment = true
	h.app.compose = m
	h.app.readComposeAttachment(m, 1, m.attachments[0].source, 100)
	if len(jobs) != 1 {
		t.Fatalf("attachment jobs = %d, want 1", len(jobs))
	}
	h.app.registerAccountClient(newFakeService(), mail.Account{Email: "b@example.com"}, nil, true)
	jobs[0]()
	if len(m.attachments[0].attachment.Data) != 0 || !m.attachments[0].loading {
		t.Fatalf("stale attachment was accepted: %+v", m.attachments[0])
	}
}
