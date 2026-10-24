package app

import (
	"context"
	"strings"
	"testing"
	"time"

	mailmodel "mica/internal/mail"

	"github.com/egoist/mygo/ui/richtext"
)

const (
	ownerComposeA = "a@example.com"
	ownerComposeB = "b@example.com"
)

type ownerComposeHarness struct {
	app   *App
	a     *draftTestService
	b     *draftTestService
	store *fakeMailStore
}

func newOwnerComposeHarness() *ownerComposeHarness {
	a := newDraftTestService()
	b := newDraftTestService()
	app := New(a)
	app.storeAccount = ownerComposeA
	app.account = mailmodel.Account{Email: ownerComposeA}
	app.accountVerified = true
	app.after = func(time.Duration, func()) {}
	app.schedule = func(fn func()) { fn() }
	app.registerAccountClient(b, mailmodel.Account{Email: ownerComposeB}, nil, true)
	store := newFakeMailStore()
	app.SetMailStore(store)
	return &ownerComposeHarness{app: app, a: a, b: b, store: store}
}

func (h *ownerComposeHarness) draftPair(t *testing.T) (mailmodel.DraftRecord, mailmodel.DraftRecord) {
	t.Helper()
	a, err := h.a.CreateDraft(context.Background(), mailmodel.Compose{To: "to@example.com", Text: "A body"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.b.CreateDraft(context.Background(), mailmodel.Compose{To: "to@example.com", Text: "B body"})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID {
		t.Fatalf("draft IDs = %q and %q, want the same ID", a.ID, b.ID)
	}
	return a, b
}

func (h *ownerComposeHarness) openDraft(t *testing.T, draft mailmodel.DraftRecord, owner string) *composer {
	t.Helper()
	message := draft.Message
	message.AccountEmail = owner
	h.app.openDraftMessage(draft.ID, message)
	if h.app.compose == nil {
		t.Fatal("draft did not open")
	}
	return h.app.compose
}

func TestComposeOwnerInheritanceAndReplyAll(t *testing.T) {
	h := newOwnerComposeHarness()
	h.app.openCompose(nil)
	if got := h.app.compose.accountEmail; got != ownerComposeA {
		t.Fatalf("new composer owner = %q, want %q", got, ownerComposeA)
	}

	message := mailmodel.Message{
		AccountEmail: ownerComposeB,
		From:         "Sender <sender@example.com>",
		To:           "a@example.com, b@example.com, carol@example.com",
		Cc:           "b@example.com, dave@example.com",
		Subject:      "Subject",
		Text:         "Body",
	}
	h.app.openReplyAllMessage(message)
	m := h.app.compose
	if m.accountEmail != ownerComposeB {
		t.Fatalf("reply-all owner = %q, want %q", m.accountEmail, ownerComposeB)
	}
	recipients := m.to + m.cc
	if strings.Contains(recipients, ownerComposeB) {
		t.Fatalf("reply-all included its owner: %q", recipients)
	}
	for _, want := range []string{"sender@example.com", ownerComposeA, "carol@example.com", "dave@example.com"} {
		if !strings.Contains(recipients, want) {
			t.Fatalf("reply-all recipients = %q, missing %q", recipients, want)
		}
	}

	h.app.openForwardMessage(message)
	if h.app.compose.accountEmail != ownerComposeB {
		t.Fatalf("forward owner = %q, want %q", h.app.compose.accountEmail, ownerComposeB)
	}
	message.AccountEmail = ""
	h.app.openReplyMessage(message)
	if h.app.compose.accountEmail != ownerComposeA {
		t.Fatalf("legacy reply owner = %q, want stored account %q", h.app.compose.accountEmail, ownerComposeA)
	}
}

func TestComposeEmptyOwnerUsesActiveService(t *testing.T) {
	for _, tc := range []struct {
		name         string
		storeAccount string
	}{
		{name: "stored account", storeAccount: ownerComposeA},
		{name: "no stored account"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newOwnerComposeHarness()
			h.app.storeAccount = tc.storeAccount
			h.app.openCompose(nil)
			m := h.app.compose
			m.accountEmail = ""
			m.to = "to@example.com"
			m.doc.SetText(richtext.NewText("Body"))
			m.revision = 1
			h.app.pumpDraftSave(m)
			if h.a.createCalls != 1 || h.b.createCalls != 0 {
				t.Fatalf("empty owner create calls: A=%d B=%d", h.a.createCalls, h.b.createCalls)
			}
		})
	}
}

func TestOwnerDraftCreateAndUpdateUseComposerOwner(t *testing.T) {
	h := newOwnerComposeHarness()
	h.app.openReplyMessage(mailmodel.Message{AccountEmail: ownerComposeB, From: "sender@example.com"})
	m := h.app.compose
	m.quote = nil
	m.to = "to@example.com"
	m.doc.SetText(richtext.NewText("B draft"))
	m.revision = 1
	h.app.pumpDraftSave(m)
	if h.a.createCalls != 0 || h.b.createCalls != 1 || m.draftID == "" {
		t.Fatalf("create calls: A=%d B=%d draft=%q", h.a.createCalls, h.b.createCalls, m.draftID)
	}
	m.doc.SetText(richtext.NewText("B updated"))
	m.revision++
	h.app.pumpDraftSave(m)
	if h.a.updateCalls != 0 || h.b.updateCalls != 1 {
		t.Fatalf("update calls: A=%d B=%d", h.a.updateCalls, h.b.updateCalls)
	}
	draft, err := h.b.Demo.Draft(context.Background(), m.draftID)
	if err != nil || draft.Message.Text != "B updated" {
		t.Fatalf("B draft = %+v, err=%v", draft, err)
	}
}

func TestOwnerDraftSameIDUpdateAndDeleteDoNotTouchA(t *testing.T) {
	h := newOwnerComposeHarness()
	a, b := h.draftPair(t)
	h.store.drafts[ownerComposeA] = mailmodel.DraftSnapshot{Compose: mailmodel.Compose{Text: "A snapshot"}}
	m := h.openDraft(t, b, ownerComposeB)
	m.doc.SetText(richtext.NewText("B updated"))
	m.revision = 1
	h.app.pumpDraftSave(m)
	if h.a.updateCalls != 0 || h.b.updateCalls != 1 {
		t.Fatalf("same-ID update calls: A=%d B=%d", h.a.updateCalls, h.b.updateCalls)
	}
	h.app.discardCompose()
	if h.a.deleteCalls != 0 || h.b.deleteCalls != 1 {
		t.Fatalf("same-ID delete calls: A=%d B=%d", h.a.deleteCalls, h.b.deleteCalls)
	}
	if _, err := h.a.Demo.Draft(context.Background(), a.ID); err != nil {
		t.Fatalf("A draft was deleted: %v", err)
	}
	if _, err := h.b.Demo.Draft(context.Background(), b.ID); err == nil {
		t.Fatal("B draft was not deleted")
	}
	if snapshot := h.store.drafts[ownerComposeA]; snapshot.Compose.Text != "A snapshot" {
		t.Fatalf("A snapshot changed: %+v", snapshot)
	}
	if _, found := h.store.drafts[ownerComposeB]; found {
		t.Fatal("B snapshot was not cleared")
	}
}

func TestOwnerDraftSameIDSendDoesNotTouchA(t *testing.T) {
	h := newOwnerComposeHarness()
	a, b := h.draftPair(t)
	h.openDraft(t, b, ownerComposeB)
	h.app.sendCompose()
	if h.a.sendDraftCalls != 0 || h.a.sendCalls != 0 || h.b.sendDraftCalls != 1 {
		t.Fatalf("same-ID send calls: A draft=%d message=%d, B draft=%d", h.a.sendDraftCalls, h.a.sendCalls, h.b.sendDraftCalls)
	}
	if h.app.compose != nil {
		t.Fatal("successful B send kept the composer open")
	}
	if draft, err := h.a.Demo.Draft(context.Background(), a.ID); err != nil || draft.Message.Text != "A body" {
		t.Fatalf("A draft changed after B send: %+v, err=%v", draft, err)
	}
	if _, err := h.b.Demo.Draft(context.Background(), b.ID); err == nil {
		t.Fatal("B draft remained after sending")
	}
}

func TestOwnerRecoveredDraftUsesSnapshotAccount(t *testing.T) {
	h := newOwnerComposeHarness()
	a, b := h.draftPair(t)
	h.app.openRecoveredDraft(mailmodel.DraftSnapshot{
		DraftID: b.ID,
		Compose: mailmodel.Compose{To: "to@example.com", Text: "Recovered B"},
	}, ownerComposeB)
	m := h.app.compose
	if m.accountEmail != ownerComposeB || m.recoveryAccount != ownerComposeB {
		t.Fatalf("recovered owner = %q recovery = %q", m.accountEmail, m.recoveryAccount)
	}
	h.app.sendCompose()
	if h.a.sendDraftCalls != 0 || h.a.sendCalls != 0 || h.b.sendDraftCalls != 1 {
		t.Fatalf("recovered send calls: A draft=%d message=%d, B draft=%d", h.a.sendDraftCalls, h.a.sendCalls, h.b.sendDraftCalls)
	}
	if draft, err := h.a.Demo.Draft(context.Background(), a.ID); err != nil || draft.Message.Text != "A body" {
		t.Fatalf("A draft changed after recovered B send: %+v, err=%v", draft, err)
	}
}

func TestOwnerDraftReadUsesOwnerAndRawID(t *testing.T) {
	h := newOwnerComposeHarness()
	_, b := h.draftPair(t)
	key := threadRefForAccount(ownerComposeB, b.ID).key()
	h.app.selectDraft(key, h.app.threadCall)
	if h.a.getCalls != 0 || h.b.getCalls != 1 {
		t.Fatalf("draft read calls: A=%d B=%d", h.a.getCalls, h.b.getCalls)
	}
	if h.app.thread.AccountEmail != ownerComposeB || len(h.app.thread.Messages) != 1 || h.app.thread.Messages[0].AccountEmail != ownerComposeB {
		t.Fatalf("B draft thread owner = %+v", h.app.thread)
	}
	message := b.Message
	message.AccountEmail = ""
	h.app.openDraftMessage(key, message)
	if h.app.compose.accountEmail != ownerComposeB || h.app.compose.draftID != b.ID {
		t.Fatalf("opened keyed draft: owner=%q ID=%q", h.app.compose.accountEmail, h.app.compose.draftID)
	}
}

func TestOwnerDraftSnapshotsAreIndependentPerAccount(t *testing.T) {
	h := newOwnerComposeHarness()
	var jobs []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	h.app.openCompose(nil)
	a := h.app.compose
	a.doc.SetText(richtext.NewText("A snapshot"))
	h.app.persistDraftSnapshot(a)

	h.app.openReplyMessage(mailmodel.Message{AccountEmail: ownerComposeB, From: "sender@example.com"})
	b := h.app.compose
	b.quote = nil
	b.doc.SetText(richtext.NewText("B snapshot"))
	h.app.persistDraftSnapshot(b)
	if len(jobs) != 2 {
		t.Fatalf("snapshot jobs = %d, want 2", len(jobs))
	}
	jobs[0]()
	jobs[1]()
	if got := h.store.drafts[ownerComposeA].Compose.Text; got != "A snapshot" {
		t.Fatalf("A snapshot = %q", got)
	}
	if got := h.store.drafts[ownerComposeB].Compose.Text; got != "B snapshot" {
		t.Fatalf("B snapshot = %q", got)
	}

	b.doc.SetText(richtext.NewText("B old"))
	h.app.persistDraftSnapshot(b)
	b.doc.SetText(richtext.NewText("B new"))
	h.app.persistDraftSnapshot(b)
	jobs[2]()
	jobs[3]()
	if got := h.store.drafts[ownerComposeB].Compose.Text; got != "B new" {
		t.Fatalf("latest B snapshot = %q", got)
	}
	h.app.clearDraftSnapshot(b)
	jobs[4]()
	if _, found := h.store.drafts[ownerComposeB]; found {
		t.Fatal("B snapshot was not deleted")
	}
	if got := h.store.drafts[ownerComposeA].Compose.Text; got != "A snapshot" {
		t.Fatalf("clearing B changed A snapshot to %q", got)
	}
}

func TestOwnerDraftSaveRejectsStaleGeneration(t *testing.T) {
	h := newOwnerComposeHarness()
	h.app.store = nil
	var jobs []func()
	h.app.schedule = func(fn func()) { jobs = append(jobs, fn) }
	h.app.openReplyMessage(mailmodel.Message{AccountEmail: ownerComposeB, From: "sender@example.com"})
	m := h.app.compose
	m.to = "to@example.com"
	m.doc.SetText(richtext.NewText("B draft"))
	m.revision = 1
	h.app.pumpDraftSave(m)
	if len(jobs) != 1 {
		t.Fatalf("draft jobs = %d, want 1", len(jobs))
	}
	replacement := newDraftTestService()
	h.app.registerAccountClient(replacement, mailmodel.Account{Email: ownerComposeB}, nil, true)
	jobs[0]()
	if h.b.createCalls != 1 || h.a.createCalls != 0 || replacement.createCalls != 0 {
		t.Fatalf("stale save calls: old B=%d A=%d replacement=%d", h.b.createCalls, h.a.createCalls, replacement.createCalls)
	}
	if m.draftID != "" || m.savedRevision != 0 || m.draftSaving {
		t.Fatalf("stale save completed against a replacement: %+v", m)
	}
}

func TestOwnerDraftRequiresOwnerVerification(t *testing.T) {
	h := newOwnerComposeHarness()
	replacement := newDraftTestService()
	h.app.registerAccountClient(replacement, mailmodel.Account{Email: ownerComposeB}, nil, false)
	h.app.openReplyMessage(mailmodel.Message{AccountEmail: ownerComposeB, From: "sender@example.com"})
	m := h.app.compose
	m.to = "to@example.com"
	m.doc.SetText(richtext.NewText("B draft"))
	m.revision = 1
	h.app.pumpDraftSave(m)
	if h.a.createCalls != 0 || replacement.createCalls != 0 {
		t.Fatalf("unverified B save used a service: A=%d B=%d", h.a.createCalls, replacement.createCalls)
	}
	m.draftID = "shared"
	h.app.sendCompose()
	if h.a.sendCalls != 0 || h.a.sendDraftCalls != 0 || replacement.sendCalls != 0 || replacement.sendDraftCalls != 0 {
		t.Fatalf("unverified B send used a service: A=%d/%d B=%d/%d", h.a.sendCalls, h.a.sendDraftCalls, replacement.sendCalls, replacement.sendDraftCalls)
	}
	if h.app.compose != m || m.err == "" {
		t.Fatalf("blocked B send did not preserve the composer: %+v", h.app.compose)
	}
}

func TestOwnerDraftOpenHonorsAccountTransition(t *testing.T) {
	h := newOwnerComposeHarness()
	h.app.accountTransition = &accountTransition{}
	h.app.openDraftMessage("shared", mailmodel.Message{AccountEmail: ownerComposeB, Text: "B"})
	h.app.openRecoveredDraft(mailmodel.DraftSnapshot{Compose: mailmodel.Compose{Text: "B"}}, ownerComposeB)
	if h.app.compose != nil {
		t.Fatal("draft opened during an account transition")
	}
}
