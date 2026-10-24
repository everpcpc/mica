package app

import (
	"context"
	"testing"

	"mica/internal/mail"
)

type draftNotificationService struct {
	*fakeService
	drafts *draftTestService
}

func (s *draftNotificationService) Drafts(ctx context.Context, query mail.Query) (mail.DraftPage, error) {
	return s.drafts.Drafts(ctx, query)
}
func (s *draftNotificationService) Draft(ctx context.Context, id string) (mail.DraftRecord, error) {
	return s.drafts.Draft(ctx, id)
}
func (s *draftNotificationService) CreateDraft(ctx context.Context, compose mail.Compose) (mail.DraftRecord, error) {
	return s.drafts.CreateDraft(ctx, compose)
}
func (s *draftNotificationService) UpdateDraft(ctx context.Context, id string, compose mail.Compose) (mail.DraftRecord, error) {
	return s.drafts.UpdateDraft(ctx, id, compose)
}
func (s *draftNotificationService) DeleteDraft(ctx context.Context, id string) error {
	return s.drafts.DeleteDraft(ctx, id)
}
func (s *draftNotificationService) SendDraft(ctx context.Context, id string, compose mail.Compose) error {
	return s.drafts.SendDraft(ctx, id, compose)
}

func TestCurrentQueryKeepsDraftLabelDuringSearch(t *testing.T) {
	a := New(newFakeService())
	a.selectedLabel = mail.Draft
	a.query = "needle"
	query := a.currentQuery()
	if query.LabelID != mail.Draft || query.Search != "needle" {
		t.Fatalf("draft search query = %+v", query)
	}
}

func TestListThreadPageWithoutDraftManager(t *testing.T) {
	service := newFakeService()
	if _, err := listThreadPage(context.Background(), service, mail.Query{LabelID: mail.Draft}); err == nil {
		t.Fatal("draft list without a manager did not fail")
	}
}

func TestHistoryRefreshesDraftSearchWithoutOrdinaryThreads(t *testing.T) {
	h := newNotificationHarness()
	service := &draftNotificationService{fakeService: h.service, drafts: newDraftTestService()}
	h.app.service = service
	matching, err := service.drafts.CreateDraft(context.Background(), mail.Compose{Subject: "Needle subject", Text: "Matching body"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.drafts.CreateDraft(context.Background(), mail.Compose{Subject: "Other", Text: "Other body"}); err != nil {
		t.Fatal(err)
	}
	h.app.Start()
	h.app.selectedLabel = mail.Draft
	h.app.query = "needle"
	h.service.threads[0].LabelIDs = []string{mail.Draft}
	h.service.threads[0].Subject = "needle ordinary thread"
	h.syncNow(t)
	if len(h.app.threads) != 1 || h.app.threads[0].ID != matching.ID {
		t.Fatalf("draft history mixed ordinary threads into the result: %+v", h.app.threads)
	}
}

func TestHistoryRefreshesDraftViewWithoutSearch(t *testing.T) {
	h := newNotificationHarness()
	service := &draftNotificationService{fakeService: h.service, drafts: newDraftTestService()}
	h.app.service = service
	draft, err := service.drafts.CreateDraft(context.Background(), mail.Compose{Subject: "Draft only"})
	if err != nil {
		t.Fatal(err)
	}
	h.app.Start()
	h.app.selectedLabel = mail.Draft
	h.service.threads[0].LabelIDs = []string{mail.Draft}
	h.syncNow(t)
	if len(h.app.threads) != 1 || h.app.threads[0].ID != draft.ID {
		t.Fatalf("draft view merged an ordinary thread ID: %+v", h.app.threads)
	}
}
