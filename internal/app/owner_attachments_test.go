package app

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	mailmodel "mica/internal/mail"
)

type ownerAttachmentService struct {
	*draftTestService
	data            map[string][]byte
	attachmentCalls []string
	sent            mailmodel.Compose
}

func newOwnerAttachmentService() *ownerAttachmentService {
	return &ownerAttachmentService{draftTestService: newDraftTestService(), data: make(map[string][]byte)}
}

func (s *ownerAttachmentService) Attachment(ctx context.Context, messageID, attachmentID string) ([]byte, error) {
	key := messageID + "/" + attachmentID
	s.attachmentCalls = append(s.attachmentCalls, key)
	if data, found := s.data[key]; found {
		return append([]byte(nil), data...), nil
	}
	return s.Demo.Attachment(ctx, messageID, attachmentID)
}

func (s *ownerAttachmentService) Send(ctx context.Context, compose mailmodel.Compose) error {
	s.sent = mailmodel.CloneCompose(compose)
	return s.draftTestService.Send(ctx, compose)
}

type ownerAttachmentHarness struct {
	app *App
	a   *ownerAttachmentService
	b   *ownerAttachmentService
}

func newOwnerAttachmentHarness() *ownerAttachmentHarness {
	a := newOwnerAttachmentService()
	b := newOwnerAttachmentService()
	app := New(a)
	app.storeAccount = ownerComposeA
	app.account = mailmodel.Account{Email: ownerComposeA}
	app.accountVerified = true
	app.after = func(time.Duration, func()) {}
	app.schedule = func(fn func()) { fn() }
	app.registerAccountClient(b, mailmodel.Account{Email: ownerComposeB}, nil, true)
	return &ownerAttachmentHarness{app: app, a: a, b: b}
}

func TestOwnerAttachmentKeysAndDownloadsIncludeAccount(t *testing.T) {
	refA := messageRefForAccount(ownerComposeA, "shared-thread", "shared-message")
	refB := messageRefForAccount(ownerComposeB, "shared-thread", "shared-message")
	keyA := attachmentKey(refA, "shared-attachment", 0)
	keyB := attachmentKey(refB, "shared-attachment", 0)
	if keyA == keyB {
		t.Fatalf("same-ID attachment key is shared across accounts: %q", keyA)
	}

	h := newOwnerAttachmentHarness()
	h.a.data["shared-message/shared-attachment"] = []byte("A data")
	h.b.data["shared-message/shared-attachment"] = []byte("B data")
	var opened [][]byte
	h.app.openPath = func(path string) error {
		data, err := os.ReadFile(path)
		if err == nil {
			opened = append(opened, data)
			_ = os.Remove(path)
		}
		return err
	}
	attachment := mailmodel.Attachment{ID: "shared-attachment", Name: "shared.txt"}
	for _, owner := range []string{ownerComposeA, ownerComposeB} {
		message := mailmodel.Message{AccountEmail: owner, ID: "shared-message", ThreadID: "shared-thread"}
		h.app.downloadAttachment(message, attachment, false, 0)
	}
	if len(h.a.attachmentCalls) != 1 || len(h.b.attachmentCalls) != 1 {
		t.Fatalf("attachment calls: A=%v B=%v", h.a.attachmentCalls, h.b.attachmentCalls)
	}
	if len(opened) != 2 || !bytes.Equal(opened[0], []byte("A data")) || !bytes.Equal(opened[1], []byte("B data")) {
		t.Fatalf("downloaded content = %q", opened)
	}
	if len(h.app.attachmentDownloads) != 2 {
		t.Fatalf("download states = %+v, want independent A/B entries", h.app.attachmentDownloads)
	}
	for _, key := range []string{keyA, keyB} {
		if state := h.app.attachmentDownloads[key]; state.loading || state.err != "" {
			t.Fatalf("download state %q = %+v", key, state)
		}
	}
}

func TestOwnerForwardAttachmentAndSendUseSourceAccount(t *testing.T) {
	h := newOwnerAttachmentHarness()
	h.a.data["shared-message/shared-attachment"] = []byte("A data")
	h.b.data["shared-message/shared-attachment"] = []byte("B data")
	message := mailmodel.Message{
		AccountEmail: ownerComposeB,
		ID:           "shared-message",
		ThreadID:     "shared-thread",
		From:         "sender@example.com",
		Subject:      "Subject",
		Text:         "Body",
		Attachments: []mailmodel.Attachment{{
			ID: "shared-attachment", Name: "shared.txt", MIMEType: "text/plain", Size: int64(len("B data")),
		}},
	}
	h.app.openForwardMessage(message)
	m := h.app.compose
	if m.accountEmail != ownerComposeB || len(m.attachments) != 1 || m.attachments[0].loading || m.attachments[0].err != "" {
		t.Fatalf("B forward attachment state = %+v", m)
	}
	if len(h.a.attachmentCalls) != 0 || len(h.b.attachmentCalls) != 1 {
		t.Fatalf("forward attachment calls: A=%v B=%v", h.a.attachmentCalls, h.b.attachmentCalls)
	}
	m.to = "to@example.com"
	h.app.sendCompose()
	if h.a.sendCalls != 0 || h.b.sendCalls != 1 || h.b.sendDraftCalls != 0 {
		t.Fatalf("forward send calls: A=%d B message=%d draft=%d", h.a.sendCalls, h.b.sendCalls, h.b.sendDraftCalls)
	}
	if len(h.b.sent.Attachments) != 1 || !bytes.Equal(h.b.sent.Attachments[0].Data, []byte("B data")) {
		t.Fatalf("B forward attachments = %+v", h.b.sent.Attachments)
	}
}

func TestOwnerLegacyAttachmentUsesStoredAccount(t *testing.T) {
	h := newOwnerAttachmentHarness()
	h.a.data["shared-message/shared-attachment"] = []byte("A data")
	h.b.data["shared-message/shared-attachment"] = []byte("B data")
	var opened []byte
	h.app.openPath = func(path string) error {
		data, err := os.ReadFile(path)
		if err == nil {
			opened = data
			_ = os.Remove(path)
		}
		return err
	}
	message := mailmodel.Message{ID: "shared-message", ThreadID: "shared-thread"}
	attachment := mailmodel.Attachment{ID: "shared-attachment", Name: "shared.txt"}
	h.app.downloadAttachment(message, attachment, false, 0)
	if len(h.a.attachmentCalls) != 1 || len(h.b.attachmentCalls) != 0 || !bytes.Equal(opened, []byte("A data")) {
		t.Fatalf("legacy download: A calls=%v B calls=%v content=%q", h.a.attachmentCalls, h.b.attachmentCalls, opened)
	}
	key := attachmentKey(messageRefForAccount(ownerComposeA, "shared-thread", "shared-message"), "shared-attachment", 0)
	if state := h.app.attachmentDownloads[key]; state.loading || state.err != "" {
		t.Fatalf("legacy download state = %+v", state)
	}
}
