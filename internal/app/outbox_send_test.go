package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui/richtext"
)

type outboxOrderService struct {
	*draftTestService
	store      *fakeMailStore
	owner      string
	beforeSend []uint64
	sent       mail.Compose
	listCalls  int
}

func (s *outboxOrderService) ListThreads(ctx context.Context, query mail.Query) ([]mail.ThreadSummary, error) {
	s.listCalls++
	return s.draftTestService.ListThreads(ctx, query)
}

func (s *outboxOrderService) Send(ctx context.Context, compose mail.Compose) error {
	for _, record := range s.store.outbox {
		if record.AccountEmail == s.owner && record.Compose.MessageID == compose.MessageID {
			s.beforeSend = append(s.beforeSend, record.Attempt)
		}
	}
	s.sent = mail.CloneCompose(compose)
	return s.draftTestService.Send(ctx, compose)
}

func newOutboxOrderHarness() (*App, *outboxOrderService, *fakeMailStore) {
	base := newDraftTestService()
	account, _ := base.Account(context.Background())
	store := newFakeMailStore()
	service := &outboxOrderService{draftTestService: base, store: store, owner: account.Email}
	a := New(service)
	a.account = account
	a.storeAccount = account.Email
	a.accountVerified = true
	a.restoreAttempted = true
	a.SetMailStore(store)
	a.schedule = func(fn func()) { fn() }
	a.after = func(time.Duration, func()) {}
	return a, service, store
}

func openOutboxTestCompose(a *App) *composer {
	a.openCompose(nil)
	m := a.compose
	m.to = "to@example.com"
	m.subject = "Outgoing"
	m.doc.SetText(richtext.NewText("body"))
	m.nextAttachmentID++
	m.attachments = append(m.attachments, composerAttachment{id: m.nextAttachmentID, attachment: mail.OutgoingAttachment{
		Name: "payload.bin", MIMEType: "application/octet-stream", Data: []byte{0, 1, '\r', '\n', 255},
	}})
	return m
}

func onlyOutboxRecord(t *testing.T, store *fakeMailStore) mail.OutboxMessage {
	t.Helper()
	if len(store.outbox) != 1 {
		t.Fatalf("outbox records = %+v, want one", store.outbox)
	}
	for _, record := range store.outbox {
		return record
	}
	return mail.OutboxMessage{}
}

func TestOutboxSendPersistsBeforeNetworkAndClearsSuccess(t *testing.T) {
	a, service, store := newOutboxOrderHarness()
	m := openOutboxTestCompose(a)
	a.sendCompose()
	if a.compose != nil || len(service.beforeSend) != 1 || service.beforeSend[0] != 1 {
		t.Fatalf("send was not persisted before the request: compose=%p attempts=%v", a.compose, service.beforeSend)
	}
	if len(store.outbox) != 0 || service.sent.MessageID == "" {
		t.Fatalf("successful send left outbox state: records=%+v sent=%+v", store.outbox, service.sent)
	}
	if _, found := store.drafts[a.storeAccount]; found {
		t.Fatal("successful send retained the linked snapshot")
	}
	if m.outboxID == "" || m.messageID == "" {
		t.Fatalf("send identity was not assigned: %+v", m)
	}
}

func TestOutboxPersistenceFailureBlocksSend(t *testing.T) {
	a, service, store := newOutboxOrderHarness()
	store.outboxErr = errOffline
	m := openOutboxTestCompose(a)
	a.sendCompose()
	if service.sendCalls != 0 || service.sendDraftCalls != 0 {
		t.Fatalf("persistence failure made a send call: %d/%d", service.sendCalls, service.sendDraftCalls)
	}
	if a.compose != m || !strings.Contains(m.err, "offline") {
		t.Fatalf("persistence failure did not preserve an actionable composer: %+v", m)
	}
}

func TestOutboxFailureRetryPreservesIdentityAndAttachments(t *testing.T) {
	a, service, store := newOutboxOrderHarness()
	service.sendErr = errOffline
	openOutboxTestCompose(a)
	a.sendCompose()
	record := onlyOutboxRecord(t, store)
	if record.State != mail.OutboxUncertain || record.Attempt != 1 || len(record.Compose.Attachments) != 1 {
		t.Fatalf("failed send record = %+v", record)
	}
	if store.drafts[a.storeAccount].OutboxID != record.ID {
		t.Fatalf("failed send snapshot is not linked: %+v", store.drafts[a.storeAccount])
	}
	var checked []string
	service.sentMessageExistsFunc = func(messageID string) (bool, error) {
		checked = append(checked, messageID)
		return false, nil
	}
	service.sendErr = nil
	a.sendCompose()
	if a.compose != nil || len(store.outbox) != 0 {
		t.Fatalf("retry did not finish: compose=%+v records=%+v", a.compose, store.outbox)
	}
	if len(service.beforeSend) != 2 || service.beforeSend[0] != 1 || service.beforeSend[1] != 2 {
		t.Fatalf("retry attempts = %v", service.beforeSend)
	}
	if service.sent.MessageID != record.Compose.MessageID || len(checked) != 1 || checked[0] != record.Compose.MessageID {
		t.Fatalf("retry changed Message-ID: sent=%q checked=%v want=%q", service.sent.MessageID, checked, record.Compose.MessageID)
	}
	if len(service.sent.Attachments) != 1 || string(service.sent.Attachments[0].Data) != string(record.Compose.Attachments[0].Data) {
		t.Fatalf("retry attachments = %+v", service.sent.Attachments)
	}
}

func TestOutboxAlreadySentDoesNotDuplicate(t *testing.T) {
	a, service, store := newOutboxOrderHarness()
	service.sendErr = errOffline
	openOutboxTestCompose(a)
	a.sendCompose()
	record := onlyOutboxRecord(t, store)
	service.sentMessageExistsFunc = func(messageID string) (bool, error) { return messageID == record.Compose.MessageID, nil }
	service.sendErr = nil
	a.sendCompose()
	if service.sendCalls != 1 || len(store.outbox) != 0 || a.compose != nil {
		t.Fatalf("already-sent retry duplicated send: calls=%d records=%+v compose=%+v", service.sendCalls, store.outbox, a.compose)
	}
}

func TestOutboxFinderFailureBlocksRetry(t *testing.T) {
	a, service, store := newOutboxOrderHarness()
	service.sendErr = errOffline
	m := openOutboxTestCompose(a)
	a.sendCompose()
	service.sentMessageExistsFunc = func(string) (bool, error) { return false, errors.New("status unavailable") }
	service.sendErr = nil
	a.sendCompose()
	if service.sendCalls != 1 || a.compose != m || !strings.Contains(m.err, "status unavailable") {
		t.Fatalf("finder failure did not block retry: calls=%d composer=%+v", service.sendCalls, m)
	}
	if record := onlyOutboxRecord(t, store); record.State != mail.OutboxUncertain || record.Attempt != 1 {
		t.Fatalf("finder failure changed outbox state: %+v", record)
	}
}

func TestOutboxExistingDraftUsesDraftSend(t *testing.T) {
	a, service, store := newOutboxOrderHarness()
	draft, err := service.CreateDraft(context.Background(), mail.Compose{To: "to@example.com", Text: "saved"})
	if err != nil {
		t.Fatal(err)
	}
	a.openDraftMessage(draft.ID, draft.Message)
	a.sendCompose()
	if service.sendDraftCalls != 1 || service.sendCalls != 0 || len(store.outbox) != 0 || a.compose != nil {
		t.Fatalf("existing draft send: draft=%d message=%d records=%d compose=%+v", service.sendDraftCalls, service.sendCalls, len(store.outbox), a.compose)
	}
}
