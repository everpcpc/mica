package app

import (
	"context"
	"testing"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

type searchService struct {
	*fakeService
	queries []mail.Query
}

func (s *searchService) ListThreads(ctx context.Context, query mail.Query) ([]mail.ThreadSummary, error) {
	s.queries = append(s.queries, query)
	return s.fakeService.ListThreads(ctx, query)
}

func TestEscapeClearsSearchAndRestoresMailbox(t *testing.T) {
	service := &searchService{fakeService: newFakeService()}
	a := New(service)
	a.schedule = func(fn func()) { fn() }
	a.restoreAttempted = true
	a.Start()
	tt := ui.NewTester(a.View, 1280, 800)
	if err := tt.Click("Search mail"); err != nil {
		t.Fatal(err)
	}
	tt.Type("offline")
	tt.Key(0, ui.KeyEnter)
	if got := service.queries[len(service.queries)-1].Search; got != "offline" {
		t.Fatalf("search query = %q, want offline", got)
	}
	if got := service.queries[len(service.queries)-1].LabelID; got != "" {
		t.Fatalf("Gmail search is limited to label %q, want a global search", got)
	}
	tt.Key(0, ui.KeyEscape)
	if got := service.queries[len(service.queries)-1].Search; got != "" {
		t.Fatalf("query after Escape = %q, want an empty mailbox query", got)
	}
	if got := service.queries[len(service.queries)-1].LabelID; got != "INBOX" {
		t.Fatalf("mailbox after Escape = %q, want INBOX", got)
	}
}
