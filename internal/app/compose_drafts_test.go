package app

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	mailmodel "mica/internal/mail"
	mailstore "mica/internal/mail/store"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
)

type draftTestService struct {
	*mailmodel.Demo
	createErr      error
	updateErr      error
	deleteErr      error
	sendDraftErr   error
	sendErr        error
	createCalls    int
	updateCalls    int
	getCalls       int
	deleteCalls    int
	sendDraftCalls int
	sendCalls      int
}

func newDraftTestService() *draftTestService {
	return &draftTestService{Demo: mailmodel.NewDemo()}
}

func (s *draftTestService) Draft(ctx context.Context, id string) (mailmodel.DraftRecord, error) {
	s.getCalls++
	return s.Demo.Draft(ctx, id)
}

func (s *draftTestService) CreateDraft(ctx context.Context, compose mailmodel.Compose) (mailmodel.DraftRecord, error) {
	s.createCalls++
	if s.createErr != nil {
		return mailmodel.DraftRecord{}, s.createErr
	}
	return s.Demo.CreateDraft(ctx, compose)
}

func (s *draftTestService) UpdateDraft(ctx context.Context, id string, compose mailmodel.Compose) (mailmodel.DraftRecord, error) {
	s.updateCalls++
	if s.updateErr != nil {
		return mailmodel.DraftRecord{}, s.updateErr
	}
	return s.Demo.UpdateDraft(ctx, id, compose)
}

func (s *draftTestService) DeleteDraft(ctx context.Context, id string) error {
	s.deleteCalls++
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.Demo.DeleteDraft(ctx, id)
}

func (s *draftTestService) SendDraft(ctx context.Context, id string, compose mailmodel.Compose) error {
	s.sendDraftCalls++
	if s.sendDraftErr != nil {
		return s.sendDraftErr
	}
	return s.Demo.SendDraft(ctx, id, compose)
}

func (s *draftTestService) Send(ctx context.Context, compose mailmodel.Compose) error {
	s.sendCalls++
	if s.sendErr != nil {
		return s.sendErr
	}
	return s.Demo.Send(ctx, compose)
}

type draftHarness struct {
	t       *testing.T
	app     *App
	service *draftTestService
	jobs    []func()
	updates []func()
	timers  []func()
}

func newDraftHarness(t *testing.T) *draftHarness {
	service := newDraftTestService()
	h := &draftHarness{t: t, service: service}
	a := New(service)
	h.app = a
	a.account, _ = service.Account(context.Background())
	a.schedule = func(fn func()) { h.jobs = append(h.jobs, fn) }
	a.update = func(fn func()) { h.updates = append(h.updates, fn) }
	a.after = func(_ time.Duration, fn func()) { h.timers = append(h.timers, fn) }
	return h
}

func (h *draftHarness) edit(text string) {
	m := h.app.compose
	m.doc.SetText(richtext.NewText(text))
	h.app.markComposerEdited(m)
}

func (h *draftHarness) startSave() {
	if len(h.timers) == 0 {
		h.t.Fatal("no draft debounce scheduled")
	}
	timer := h.timers[0]
	h.timers = h.timers[1:]
	timer()
	h.runUpdate()
}

func (h *draftHarness) finishJob() {
	h.runJobAt(0)
	h.runUpdate()
}

func (h *draftHarness) runJobAt(index int) {
	if len(h.jobs) <= index {
		h.t.Fatalf("job %d is not scheduled: %d jobs", index, len(h.jobs))
	}
	job := h.jobs[index]
	h.jobs = append(h.jobs[:index], h.jobs[index+1:]...)
	job()
}

func (h *draftHarness) runUpdate() {
	if len(h.updates) == 0 {
		h.t.Fatal("no published update")
	}
	update := h.updates[0]
	h.updates = h.updates[1:]
	update()
}

func TestComposerAutosaveCreatesOnceThenUpdates(t *testing.T) {
	h := newDraftHarness(t)
	a := h.app
	a.openCompose(nil)
	m := a.compose
	m.to = "to@example.com"
	h.edit("first")
	h.startSave()
	if m.saveState != draftSaveSaving || len(h.jobs) != 1 {
		t.Fatalf("autosave did not start one create: %+v", m)
	}
	h.edit("latest")
	h.finishJob()
	if h.service.createCalls != 1 || h.service.updateCalls != 0 || m.draftID == "" || m.savedRevision != 1 || m.revision != 2 {
		t.Fatalf("create completion = %+v, calls = %d/%d", m, h.service.createCalls, h.service.updateCalls)
	}
	h.finishJob()
	if h.service.createCalls != 1 || h.service.updateCalls != 1 || m.savedRevision != 2 || m.saveState != draftSaveSaved {
		t.Fatalf("follow-up update = %+v, calls = %d/%d", m, h.service.createCalls, h.service.updateCalls)
	}
	draft, err := h.service.Draft(context.Background(), m.draftID)
	if err != nil || draft.Message.Text != "latest" {
		t.Fatalf("saved draft = %+v, %v", draft, err)
	}
	h.startSave()
	if h.service.createCalls != 1 || h.service.updateCalls != 1 {
		t.Fatal("stale debounce started another save")
	}
}

func TestComposerCreateCompletionPersistsDraftIDForRecovery(t *testing.T) {
	h := newDraftHarness(t)
	db, err := mailstore.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	h.app.store = db
	h.app.storeAccount = "demo@example.com"
	h.app.update = nil
	h.app.openCompose(nil)
	m := h.app.compose
	m.to = "to@example.com"
	h.edit("latest")
	m.revision = 2
	m.savedRevision = 0
	m.saveState = draftSaveSaving
	h.app.persistDraftSnapshot(m)
	h.app.finishDraftSave(m, m.generation, 1, "", h.app.serviceVersion, mailmodel.DraftRecord{ID: "draft-1"}, nil)
	for len(h.jobs) > 0 {
		h.runJobAt(0)
	}
	snapshot, found, err := db.DraftSnapshot(context.Background(), "demo@example.com")
	if err != nil || !found {
		t.Fatalf("recovery snapshot = %+v, found=%v err=%v", snapshot, found, err)
	}
	if snapshot.DraftID != m.draftID || snapshot.Compose.Text != "latest" {
		t.Fatalf("recovery snapshot lost the new draft identity: %+v, want ID %q and latest text", snapshot, m.draftID)
	}
}

func TestDraftComposeEmptyTreatsHTMLAsContent(t *testing.T) {
	if draftComposeEmpty(mailmodel.Compose{HTML: "<p>Hello</p>"}) {
		t.Fatal("HTML-only draft was treated as empty")
	}
}

func TestComposerSaveFailureRetries(t *testing.T) {
	h := newDraftHarness(t)
	h.service.createErr = errors.New("offline")
	h.app.openCompose(nil)
	h.app.compose.to = "to@example.com"
	h.edit("first")
	h.startSave()
	h.finishJob()
	m := h.app.compose
	if m.saveState != draftSaveFailed || h.service.createCalls != 1 {
		t.Fatalf("failed save = %+v", m)
	}
	h.service.createErr = nil
	h.edit("retry")
	h.startSave()
	h.finishJob()
	if h.service.createCalls != 2 || m.saveState != draftSaveSaved || m.draftID == "" {
		t.Fatalf("retried save = %+v, calls = %d", m, h.service.createCalls)
	}
}

func TestComposerEmptyNewDraftDoesNotCreate(t *testing.T) {
	h := newDraftHarness(t)
	h.app.openCompose(nil)
	m := h.app.compose
	h.edit("temporary")
	m.doc.SetText(richtext.NewText(""))
	h.app.markComposerEdited(m)
	h.startSave()
	h.startSave()
	if h.service.createCalls != 0 || len(h.jobs) != 0 || m.savedRevision != m.revision {
		t.Fatalf("empty new composer created a draft: %+v, calls = %d", m, h.service.createCalls)
	}
}

func TestComposerCloseWaitsForFinalSaveAndFailureKeepsOpen(t *testing.T) {
	h := newDraftHarness(t)
	h.service.createErr = errors.New("offline")
	h.app.openCompose(nil)
	h.app.compose.to = "to@example.com"
	h.edit("close content")
	h.app.closeCompose()
	m := h.app.compose
	if m == nil || m.pendingAction != composerActionClose || m.saveState != draftSaveSaving {
		t.Fatalf("close did not wait for save: %+v", m)
	}
	h.finishJob()
	if h.app.compose != m || m.pendingAction != composerActionNone || m.saveState != draftSaveFailed {
		t.Fatalf("failed close discarded composer: %+v", m)
	}
	h.service.createErr = nil
	h.app.closeCompose()
	h.finishJob()
	if h.app.compose != nil || h.service.createCalls != 2 {
		t.Fatalf("close retry did not finish: compose=%+v calls=%d", h.app.compose, h.service.createCalls)
	}
}

func TestComposerCloseReopensUpdatedDraft(t *testing.T) {
	h := newDraftHarness(t)
	draft, err := h.service.CreateDraft(context.Background(), mailmodel.Compose{Text: "before"})
	if err != nil {
		t.Fatal(err)
	}
	h.app.selectedLabel = mailmodel.Draft
	h.app.selectedThread = draft.ID
	h.app.thread = draftThread(draft)
	h.app.threads = []mailmodel.ThreadSummary{draftSummary(draft)}
	h.app.openDraftMessage(draft.ID, draft.Message)
	h.edit("latest")
	h.app.closeCompose()
	if h.app.compose == nil {
		t.Fatal("close did not wait for the update")
	}
	h.finishJob()
	if h.app.compose != nil {
		t.Fatal("composer did not close after the update")
	}
	if h.app.selectedThread != "" || h.app.thread.ID != "" {
		t.Fatalf("closed draft retained stale detail: selected=%q thread=%q", h.app.selectedThread, h.app.thread.ID)
	}
	h.app.selectThread(draft.ID)
	h.finishJob()
	if h.service.getCalls != 1 {
		t.Fatalf("reopen did not full get the draft: %d calls", h.service.getCalls)
	}
	if len(h.app.thread.Messages) != 1 || h.app.thread.Messages[0].Text != "latest" {
		t.Fatalf("reopened detail is stale: %+v", h.app.thread)
	}
	h.app.openSelectedDraft()
	if h.app.compose == nil || h.app.compose.doc.Text().PlainText() != "latest" {
		t.Fatalf("reopened composer is stale: %+v", h.app.compose)
	}
}

func TestComposerCloseKeepsOrdinaryReader(t *testing.T) {
	h := newDraftHarness(t)
	h.app.selectedLabel = mailmodel.Inbox
	h.app.selectedThread = "thread-1"
	h.app.thread = mailmodel.Thread{ID: "thread-1"}
	h.app.openCompose(nil)
	h.app.closeCompose()
	if h.app.selectedThread != "thread-1" || h.app.thread.ID != "thread-1" {
		t.Fatalf("ordinary reader was cleared: selected=%q thread=%q", h.app.selectedThread, h.app.thread.ID)
	}
}

func TestComposerOnlyDiscardDeletesDraft(t *testing.T) {
	h := newDraftHarness(t)
	draft, err := h.service.CreateDraft(context.Background(), mailmodel.Compose{Text: "saved"})
	if err != nil {
		t.Fatal(err)
	}
	h.app.openDraftMessage(draft.ID, draft.Message)
	h.app.closeCompose()
	if h.app.compose != nil || h.service.deleteCalls != 0 {
		t.Fatal("close deleted a draft")
	}
	h.app.openDraftMessage(draft.ID, draft.Message)
	h.service.deleteErr = errors.New("offline")
	h.app.discardCompose()
	h.finishJob()
	m := h.app.compose
	if m == nil || m.draftID != draft.ID || m.pendingAction != composerActionNone {
		t.Fatalf("delete failure discarded composer: %+v", m)
	}
	h.service.deleteErr = nil
	h.app.discardCompose()
	h.finishJob()
	if h.app.compose != nil || h.service.deleteCalls != 2 {
		t.Fatalf("discard did not delete exactly once after retry: %+v, calls=%d", h.app.compose, h.service.deleteCalls)
	}
	h.app.openCompose(nil)
	h.app.discardCompose()
	if h.service.deleteCalls != 2 {
		t.Fatal("discarding a new composer called DELETE")
	}
}

func TestComposerSendDuringCreateUsesDraftSend(t *testing.T) {
	h := newDraftHarness(t)
	h.app.openCompose(nil)
	m := h.app.compose
	m.to = "to@example.com"
	h.edit("latest")
	h.startSave()
	h.app.sendCompose()
	if !m.sending || m.pendingAction != composerActionSend || h.service.sendDraftCalls != 0 || h.service.sendCalls != 0 {
		t.Fatalf("send during create did not wait: %+v", m)
	}
	h.finishJob()
	if h.service.sendDraftCalls != 0 {
		t.Fatal("draft send started before create completed")
	}
	h.finishJob()
	if h.service.createCalls != 1 || h.service.sendDraftCalls != 1 || h.service.sendCalls != 0 || h.app.compose != nil {
		t.Fatalf("send during create = calls %d/%d/%d compose=%+v", h.service.createCalls, h.service.sendDraftCalls, h.service.sendCalls, h.app.compose)
	}
}

func TestComposerSendWaitsForExistingDraftUpdate(t *testing.T) {
	h := newDraftHarness(t)
	draft, err := h.service.CreateDraft(context.Background(), mailmodel.Compose{To: "to@example.com", Text: "saved"})
	if err != nil {
		t.Fatal(err)
	}
	h.app.openDraftMessage(draft.ID, draft.Message)
	h.edit("latest")
	h.startSave()
	h.app.sendCompose()
	if len(h.jobs) != 1 || h.service.sendDraftCalls != 0 {
		t.Fatalf("send did not wait for the existing update: jobs=%d calls=%d", len(h.jobs), h.service.sendDraftCalls)
	}
	h.finishJob()
	if h.service.sendDraftCalls != 0 {
		t.Fatal("draft send started before the update completed")
	}
	h.finishJob()
	if h.service.sendDraftCalls != 1 || h.service.sendCalls != 0 || h.app.compose != nil {
		t.Fatalf("draft send = calls %d/%d compose=%+v", h.service.sendDraftCalls, h.service.sendCalls, h.app.compose)
	}
}

func TestComposerSecondDebounceDuringCreateDoesNotDuplicate(t *testing.T) {
	h := newDraftHarness(t)
	h.app.openCompose(nil)
	m := h.app.compose
	m.to = "to@example.com"
	h.edit("first")
	h.startSave()
	h.edit("latest")
	h.startSave()
	if !m.draftSaving || m.saveState != draftSaveScheduled || len(h.jobs) != 1 || h.service.createCalls != 0 {
		t.Fatalf("second debounce started another create: %+v jobs=%d calls=%d", m, len(h.jobs), h.service.createCalls)
	}
	h.finishJob()
	if h.service.createCalls != 1 || h.service.updateCalls != 0 {
		t.Fatalf("create completion = calls %d/%d", h.service.createCalls, h.service.updateCalls)
	}
	h.finishJob()
	if h.service.createCalls != 1 || h.service.updateCalls != 1 || m.draftSaving {
		t.Fatalf("follow-up update = calls %d/%d saving=%v", h.service.createCalls, h.service.updateCalls, m.draftSaving)
	}
}

func TestComposerDiscardWaitsForExistingDraftUpdate(t *testing.T) {
	h := newDraftHarness(t)
	draft, err := h.service.CreateDraft(context.Background(), mailmodel.Compose{Text: "saved"})
	if err != nil {
		t.Fatal(err)
	}
	h.app.openDraftMessage(draft.ID, draft.Message)
	h.edit("latest")
	h.startSave()
	h.service.deleteErr = errors.New("offline")
	h.app.discardCompose()
	m := h.app.compose
	if m.pendingAction != composerActionDiscard || h.service.deleteCalls != 0 || len(h.jobs) != 1 {
		t.Fatalf("discard did not wait for update: %+v jobs=%d", m, len(h.jobs))
	}
	h.finishJob()
	if h.service.updateCalls != 1 || h.service.deleteCalls != 0 {
		t.Fatalf("delete started before update completed: calls %d/%d", h.service.updateCalls, h.service.deleteCalls)
	}
	h.finishJob()
	if h.service.deleteCalls != 1 || m.draftSaving || m.pendingAction != composerActionNone || m.saveState == draftSaveSaving {
		t.Fatalf("delete failure left saving blocked: %+v", m)
	}
	h.app.closeCompose()
	if h.app.compose != nil {
		t.Fatal("delete failure permanently blocked Close")
	}
}

func TestComposerDraftSendFailurePreservesAttachments(t *testing.T) {
	h := newDraftHarness(t)
	draft, err := h.service.CreateDraft(context.Background(), mailmodel.Compose{
		To: "to@example.com", Text: "saved",
		Attachments: []mailmodel.OutgoingAttachment{{Name: "payload.bin", MIMEType: "application/octet-stream", Data: []byte{0, 1, '\r', '\n', 255}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.app.openDraftMessage(draft.ID, draft.Message)
	h.finishJob()
	m := h.app.compose
	h.edit("latest")
	h.service.sendDraftErr = errors.New("offline")
	h.app.sendCompose()
	h.finishJob()
	if h.app.compose != m || m.draftID != draft.ID || len(m.attachments) != 1 || !bytes.Equal(m.attachments[0].attachment.Data, []byte{0, 1, '\r', '\n', 255}) {
		t.Fatalf("failed draft send lost state: %+v", m)
	}
	if h.service.sendCalls != 0 {
		t.Fatal("existing draft used messages/send")
	}
}

func TestDraftSaveWorkerUsesImmutableSnapshot(t *testing.T) {
	h := newDraftHarness(t)
	h.app.openCompose(nil)
	m := h.app.compose
	m.to = "to@example.com"
	m.attachments = []composerAttachment{{id: 1, attachment: mailmodel.OutgoingAttachment{Name: "payload.txt", MIMEType: "text/plain", Data: []byte("before")}}}
	m.nextAttachmentID = 1
	h.edit("before")
	h.startSave()
	m.doc.SetText(richtext.NewText("after"))
	m.attachments[0].attachment.Data[0] = 'X'
	h.finishJob()
	draft, err := h.service.Draft(context.Background(), m.draftID)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Message.Text != "before" {
		t.Fatalf("worker read mutable body state: %q", draft.Message.Text)
	}
	data, err := h.service.Attachment(context.Background(), draft.Message.ID, draft.Message.Attachments[0].ID)
	if err != nil || string(data) != "before" {
		t.Fatalf("worker read mutable attachment state: %q, %v", data, err)
	}
}

func TestComposerCloseIncludesEditsDuringSave(t *testing.T) {
	h := newDraftHarness(t)
	h.app.openCompose(nil)
	h.app.compose.to = "to@example.com"
	h.edit("first")
	h.app.closeCompose()
	h.edit("latest")
	h.finishJob()
	if h.app.compose == nil || h.app.compose.pendingAction != composerActionClose {
		t.Fatal("close finished before the latest revision")
	}
	h.finishJob()
	if h.app.compose != nil {
		t.Fatal("close did not finish after the latest revision")
	}
	drafts, err := h.service.Drafts(context.Background(), mailmodel.Query{})
	if err != nil || len(drafts.Drafts) != 1 || drafts.Drafts[0].Message.Text != "latest" {
		t.Fatalf("close saved %+v, %v", drafts.Drafts, err)
	}
}

func TestComposerDraftStatusFollowsRealEditing(t *testing.T) {
	h := newDraftHarness(t)
	h.app.openCompose(nil)
	tt := ui.NewTester(h.app.View, 1280, 900)
	if h.app.compose.revision != 0 || len(h.timers) != 0 {
		t.Fatal("opening a composer counted as an edit")
	}
	if err := tt.Click("Body"); err != nil {
		t.Fatal(err)
	}
	tt.Type("Hello")
	if h.app.compose.revision == 0 || len(h.timers) == 0 || !tt.HasText("Saving…") {
		t.Fatalf("real editing did not start saving: revision=%d timers=%d texts=%v", h.app.compose.revision, len(h.timers), tt.Texts())
	}
	h.startSave()
	h.finishJob()
	tt.Frame()
	if !tt.HasText("Saved") {
		t.Fatalf("saved state is not visible: %v", tt.Texts())
	}
	h.service.updateErr = errors.New("offline")
	tt.Type(" more")
	h.startSave()
	h.finishJob()
	tt.Frame()
	if !tt.HasText("Save failed") {
		t.Fatalf("save failure is not visible: %v", tt.Texts())
	}
}
