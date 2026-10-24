package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mica/internal/mail"
	mailstore "mica/internal/mail/store"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
)

func TestDraftViewReopensSelectedDraft(t *testing.T) {
	service := mail.NewDemo()
	draft, err := service.CreateDraft(context.Background(), mail.Compose{
		To: "to@example.com", Bcc: "bcc@example.com", Subject: "Draft reply", Text: "First paragraph\n\nOriginal message\nSecond paragraph",
		ThreadID: "t-launch", InReplyTo: "<selected@example.com>", References: "<root@example.com> <selected@example.com>", ThreadingResolved: true,
		Attachments: []mail.OutgoingAttachment{{Name: "payload.bin", MIMEType: "application/octet-stream", Data: []byte{0, 1, '\r', '\n', 255}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := New(service)
	a.schedule = func(fn func()) { fn() }
	a.restoreAttempted = true
	a.startupSelectionDone = true
	a.Start()
	a.selectedLabel = mail.Draft
	a.selectLabel()
	if len(a.threads) != 1 || a.threads[0].ID != draft.ID {
		t.Fatalf("draft list = %+v", a.threads)
	}
	a.selectThread(draft.ID)
	if a.thread.ID != draft.ID || len(a.thread.Messages) != 1 || a.thread.Messages[0].ID != draft.Message.ID {
		t.Fatalf("full draft = %+v", a.thread)
	}
	tt := ui.NewTester(a.View, 1280, 900)
	if !tt.HasText("Continue editing") {
		t.Fatalf("draft continuation is not visible: %v", tt.Texts())
	}
	if err := tt.Click("Continue editing"); err != nil {
		t.Fatal(err)
	}
	m := a.compose
	if m == nil || m.draftID != draft.ID || m.bcc != "bcc@example.com" || m.threadID != "t-launch" || m.inReplyTo != "<selected@example.com>" || m.references != "<root@example.com> <selected@example.com>" {
		t.Fatalf("reopened draft = %+v", m)
	}
	if m.quote != nil || strings.Count(m.doc.Text().PlainText(), "Original message") != 1 {
		t.Fatalf("draft quote was duplicated: quote=%+v body=%q", m.quote, m.doc.Text().PlainText())
	}
	if len(m.attachments) != 1 || string(m.attachments[0].attachment.Data) != string([]byte{0, 1, '\r', '\n', 255}) {
		t.Fatalf("reopened draft attachments = %+v", m.attachments)
	}
}

func TestSQLiteDraftSnapshotRestoresComposer(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	db, err := mailstore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	service := mail.NewDemo()
	account, _ := service.Account(ctx)
	a := New(service)
	a.account = account
	a.storeAccount = account.Email
	a.SetMailStore(db)
	a.schedule = func(fn func()) { fn() }
	a.after = func(time.Duration, func()) {}
	a.openCompose(nil)
	m := a.compose
	m.draftID = "draft-existing"
	m.to = "to@example.com"
	m.bcc = "bcc@example.com"
	m.subject = "Unsynced"
	m.threadID = "t-launch"
	m.inReplyTo = "<selected@example.com>"
	m.references = "<root@example.com> <selected@example.com>"
	m.doc.SetText(richtext.NewText("Unsynced body"))
	attachment := writeComposeAttachment(t, "payload.bin", []byte{0, 1, '\r', '\n', 255})
	a.addComposeAttachments([]string{attachment})
	if len(m.attachments) != 1 || m.attachments[0].loading || m.revision == 0 {
		t.Fatalf("attachment was not loaded before the snapshot: %+v", m.attachments)
	}
	drafts, err := service.Drafts(ctx, mail.Query{})
	if err != nil || len(drafts.Drafts) != 0 {
		t.Fatalf("snapshot was unexpectedly synchronized: %+v, %v", drafts, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	m.attachments[0].attachment.Data[0] = 'X'

	db, err = mailstore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(service)
	restarted.account = account
	restarted.storeAccount = account.Email
	restarted.SetMailStore(db)
	restarted.schedule = func(fn func()) { fn() }
	restarted.after = func(time.Duration, func()) {}
	restarted.restoreDraftSnapshot()
	recovered := restarted.compose
	if recovered == nil || recovered.draftID != "draft-existing" || recovered.recoveryAccount != account.Email {
		t.Fatalf("recovered composer = %+v", recovered)
	}
	if recovered.bcc != "bcc@example.com" || recovered.threadID != "t-launch" || recovered.inReplyTo != "<selected@example.com>" || recovered.references != "<root@example.com> <selected@example.com>" {
		t.Fatalf("recovered metadata = %+v", recovered)
	}
	if recovered.doc.Text().PlainText() != "Unsynced body" || recovered.quote != nil || len(recovered.attachments) != 1 || recovered.attachments[0].attachment.Data[0] != 0 {
		t.Fatalf("recovered content = %+v", recovered)
	}
	if recovered.revision != 1 || recovered.savedRevision != 0 {
		t.Fatalf("recovered revision = %d/%d", recovered.revision, recovered.savedRevision)
	}

	other := New(service)
	other.account = account
	other.storeAccount = account.Email
	other.SetMailStore(db)
	other.schedule = func(fn func()) { fn() }
	other.openCompose(nil)
	existing := other.compose
	other.restoreDraftSnapshot()
	if other.compose != existing {
		t.Fatal("draft recovery replaced an existing composer")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteDraftSnapshotPreservesUnreadAttachmentState(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	db, err := mailstore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	service := newDraftTestService()
	account, _ := service.Account(ctx)
	a := New(service)
	a.account = account
	a.storeAccount = account.Email
	a.SetMailStore(db)
	a.after = func(time.Duration, func()) {}
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.openCompose(nil)
	a.compose.to = "to@example.com"
	a.compose.doc.SetText(richtext.NewText("Latest body"))
	attachment := writeComposeAttachment(t, "pending.bin", []byte{0, 1, 2})
	a.addComposeAttachments([]string{attachment})
	if len(jobs) != 2 || len(a.compose.attachments) != 1 || !a.compose.attachments[0].loading {
		t.Fatalf("attachment did not remain in flight: jobs=%d state=%+v", len(jobs), a.compose.attachments)
	}
	jobs[0]()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = mailstore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(service)
	restarted.account = account
	restarted.storeAccount = account.Email
	restarted.SetMailStore(db)
	restarted.schedule = func(fn func()) { fn() }
	restarted.after = func(time.Duration, func()) {}
	restarted.restoreDraftSnapshot()
	recovered := restarted.compose
	if recovered == nil || recovered.doc.Text().PlainText() != "Latest body" || len(recovered.attachments) != 1 || recovered.attachments[0].err == "" {
		t.Fatalf("recovery lost body or unread attachment state: %+v", recovered)
	}
	restarted.sendCompose()
	if service.sendCalls != 0 {
		t.Fatal("recovery sent with an unread attachment")
	}
	restarted.removeComposeAttachment(recovered.attachments[0].id)
	restarted.sendCompose()
	if service.sendCalls != 1 {
		t.Fatal("recovery could not send after removing the unread attachment")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}
