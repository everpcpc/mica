package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"

	"mica/internal/gmail"
	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

type filterTestService struct {
	mail.Service
	account     mail.Account
	labels      []mail.Label
	filters     []mail.Filter
	listCalls   int
	createCalls int
	deleteCalls int
	created     []mail.Filter
	deleted     []string
	listErr     error
	createErr   error
	deleteErr   error
}

func newFilterTestService() *filterTestService {
	base := newFakeService()
	service := &filterTestService{Service: base, filters: []mail.Filter{}}
	service.account = mail.Account{Name: "Real User", Email: "real@example.com"}
	service.labels = append(service.labels, base.labels...)
	service.labels = append(service.labels, mail.Label{ID: "Label_7", Name: "Receipts"})
	return service
}

func (f *filterTestService) Account(context.Context) (mail.Account, error) {
	return f.account, nil
}

func (f *filterTestService) Labels(context.Context) ([]mail.Label, error) {
	return f.labels, nil
}

func (f *filterTestService) Filters(context.Context) ([]mail.Filter, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]mail.Filter(nil), f.filters...), nil
}

func (f *filterTestService) CreateFilter(_ context.Context, filter mail.Filter) (mail.Filter, error) {
	f.createCalls++
	f.created = append(f.created, filter)
	if f.createErr != nil {
		return mail.Filter{}, f.createErr
	}
	filter.ID = fmt.Sprintf("server-filter-%d", f.createCalls)
	filter.Action.AddLabelIDs = append([]string(nil), filter.Action.AddLabelIDs...)
	filter.Action.RemoveLabelIDs = append([]string(nil), filter.Action.RemoveLabelIDs...)
	return filter, nil
}

func (f *filterTestService) DeleteFilter(_ context.Context, id string) error {
	f.deleteCalls++
	f.deleted = append(f.deleted, id)
	return f.deleteErr
}

func newFilterTestApp(service *filterTestService) *App {
	return newTestApp(service)
}

func openTestFilters(t *testing.T, a *App) *ui.Tester {
	t.Helper()
	tt := ui.NewTester(a.View, 1280, 800)
	if err := tt.Click("Filters"); err != nil {
		t.Fatal(err)
	}
	return tt
}

func TestFiltersPrimaryEntryOutsideSettings(t *testing.T) {
	service := newFilterTestService()
	a := newTestApp(service)
	tt := ui.NewTester(a.View, 1280, 800)
	entry, ok := tt.Find("Filters")
	if !ok || entry.W <= 0 || entry.H <= 0 {
		t.Fatalf("main window has no Filters feature entry: %+v", entry)
	}
	if entry.X >= a.sidebarWidth || entry.Y+entry.H < 760 {
		t.Fatalf("Filters is not a fixed sidebar footer entry: %+v", entry)
	}
	if err := tt.Click("Filters"); err != nil {
		t.Fatal(err)
	}
	if a.filterDialog == nil || service.listCalls != 1 {
		t.Fatalf("sidebar Filters did not load the active account: dialog=%p calls=%d", a.filterDialog, service.listCalls)
	}
}

func TestFiltersPrimaryEntryStaysVisibleWithLongSidebar(t *testing.T) {
	service := newFilterTestService()
	for i := 1; i <= 40; i++ {
		service.labels = append(service.labels, mail.Label{ID: fmt.Sprintf("Label_sidebar_%d", i), Name: fmt.Sprintf("Sidebar label %d", i)})
	}
	a := newTestApp(service)
	tt := ui.NewTester(a.View, 1280, 600)
	entry, ok := tt.Find("Filters")
	if !ok || entry.W <= 0 || entry.H <= 0 || entry.Y+entry.H < 560 {
		t.Fatalf("Filters footer is not visible at the minimum window height: %+v", entry)
	}
	if err := tt.Click("Filters"); err != nil {
		t.Fatal(err)
	}
	if a.filterDialog == nil || service.listCalls != 1 {
		t.Fatalf("minimum-height Filters entry is not usable: dialog=%p calls=%d", a.filterDialog, service.listCalls)
	}
}

func TestFilterMenuTracksFeatureAvailability(t *testing.T) {
	service := newFilterTestService()
	a := newTestApp(service)
	var states []bool
	a.SetFilterMenuHandler(func(enabled bool) { states = append(states, enabled) })
	tt := ui.NewTester(a.View, 1280, 800)
	a.OpenFilters()
	tt.Frame()
	if len(states) == 0 || !states[len(states)-1] {
		t.Fatalf("an open Filters dialog disabled window restoration: %v", states)
	}
	opened := a.filterDialog
	a.OpenFilters()
	tt.Frame()
	if opened == nil || a.filterDialog != opened || service.listCalls != 1 {
		t.Fatalf("reopening replaced the dialog or reloaded filters: dialog=%p old=%p calls=%d", a.filterDialog, opened, service.listCalls)
	}
	a.closeFilterDialog(opened)
	tt.Frame()
	a.settings = &settingsState{open: true}
	tt.Frame()
	a.OpenFilters()
	tt.Frame()
	if a.filterDialog != nil || service.listCalls != 1 {
		t.Fatalf("Settings allowed Filters to open: dialog=%p calls=%d", a.filterDialog, service.listCalls)
	}
	want := []bool{true, false}
	if len(states) != len(want) {
		t.Fatalf("menu states = %v, want %v", states, want)
	}
	for i := range want {
		if states[i] != want[i] {
			t.Fatalf("menu states = %v, want %v", states, want)
		}
	}
}

func TestFiltersAuthenticationDisablesWriteControls(t *testing.T) {
	service := newFilterTestService()
	a := newFilterTestApp(service)
	a.openFilters()
	s := a.filterDialog
	s.creating = true
	s.from = "sender@example.com"
	s.starred = true
	a.authCancel = func() {}
	tt := ui.NewTester(a.View, 1280, 800)
	if err := tt.Click("Create filter"); err != nil {
		t.Logf("Create is disabled during authentication: %v", err)
	}
	if service.createCalls != 0 {
		t.Errorf("authentication allowed Create: %d calls", service.createCalls)
	}
	a.loadFilters(s)
	a.requestFilterDelete("filter-existing")
	if service.listCalls != 1 || s.deleteID != "" {
		t.Fatalf("authentication allowed Refresh or Delete: lists=%d delete=%q", service.listCalls, s.deleteID)
	}
}

func TestFiltersAuthenticationPreventsMenuAndReopen(t *testing.T) {
	service := newFilterTestService()
	a := newFilterTestApp(service)
	a.openFilters()
	a.closeFilterDialog(a.filterDialog)
	a.authCancel = func() {}
	var states []bool
	a.SetFilterMenuHandler(func(enabled bool) { states = append(states, enabled) })
	tt := ui.NewTester(a.View, 1280, 800)
	if len(states) != 1 || states[0] {
		t.Errorf("authentication left the Mailbox action enabled: %v", states)
	}
	if err := tt.Click("Filters"); err != nil {
		t.Logf("sidebar Filters is disabled during authentication: %v", err)
	}
	if a.filterDialog != nil || service.listCalls != 1 {
		t.Fatalf("authentication allowed Filters to reopen: dialog=%p calls=%d", a.filterDialog, service.listCalls)
	}
	a.authCancel = nil
	tt.Frame()
	if err := tt.Click("Filters"); err != nil {
		t.Fatal(err)
	}
	if a.filterDialog == nil || service.listCalls != 2 {
		t.Fatalf("authentication completion did not restore Filters: dialog=%p calls=%d", a.filterDialog, service.listCalls)
	}
	want := []bool{false, true}
	if len(states) != len(want) {
		t.Fatalf("menu states = %v, want %v", states, want)
	}
	for i := range want {
		if states[i] != want[i] {
			t.Fatalf("menu states = %v, want %v", states, want)
		}
	}
}

func TestSettingsHasNoFilterEntry(t *testing.T) {
	service := newFilterTestService()
	a := newTestApp(service)
	a.settings = &settingsState{open: true, accounts: []mail.Account{service.account}}
	tt := ui.NewTester(a.settingsView, 1280, 800)
	if tt.HasText("Filters") {
		t.Fatalf("Settings still contains the Filters feature: %q", tt.Texts())
	}
}

func TestFiltersListAndAdvancedSummary(t *testing.T) {
	service := newFilterTestService()
	service.filters = []mail.Filter{{
		ID: "filter-existing",
		Criteria: mail.FilterCriteria{
			From: "sender@example.com", To: "recipient@example.com", Subject: "Launch",
			Query: "is:unread", NegatedQuery: "label:archive", HasAttachment: true,
			ExcludeChats: true, Size: 2097152, SizeComparison: "larger",
		},
		Action: mail.FilterAction{
			AddLabelIDs: []string{"Label_7", mail.Starred}, RemoveLabelIDs: []string{mail.Inbox, mail.Unread},
			Forward: "archive@example.com",
		},
	}}
	a := newFilterTestApp(service)
	tt := openTestFilters(t, a)
	if service.listCalls != 1 {
		t.Fatalf("list calls = %d, want 1", service.listCalls)
	}
	for _, want := range []string{
		"Gmail filters", "individual messages", "From: sender@example.com", "To: recipient@example.com",
		"Subject contains: Launch", "Query: is:unread", "Does not match query: label:archive",
		"Has attachment", "Exclude chats", "Larger than 2097152 B", "Add label: Receipts",
		"Add label: Starred", "Remove label: Inbox", "Remove label: Unread", "Forward to: archive@example.com",
	} {
		if !tt.HasText(want) {
			t.Errorf("filter summary is missing %q: %q", want, tt.Texts())
		}
	}
	if tt.Image().Bounds().Empty() {
		t.Fatal("filter dialog rendered no pixels")
	}
	if err := tt.Click("Close"); err != nil {
		t.Fatal(err)
	}
	if a.filterDialog != nil {
		t.Fatal("closing retained the filter dialog")
	}
}

func TestCaptureFilters(t *testing.T) {
	if os.Getenv("MICA_CAPTURE") == "" {
		t.Skip("capture is opt-in")
	}
	service := newFilterTestService()
	service.filters = []mail.Filter{{
		ID: "filter-receipts", Criteria: mail.FilterCriteria{From: "receipts@example.com"},
		Action: mail.FilterAction{AddLabelIDs: []string{"Label_7"}},
	}}
	a := newFilterTestApp(service)
	tt := openTestFilters(t, a)
	tt.SetLocale("en")
	if err := tt.Click("New filter"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Sender or address list"); err != nil {
		t.Fatal(err)
	}
	tt.Type("receipts@example.com")
	if err := tt.Click("Email subject"); err != nil {
		t.Fatal(err)
	}
	tt.Type("Invoice")
	if err := tt.Click("No label"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	for _, action := range []string{"Star message", "Skip Inbox"} {
		if err := tt.Click(action); err != nil {
			t.Fatal(err)
		}
	}
	captureFrame(t, tt, "filters")
}

func TestFiltersEmptyState(t *testing.T) {
	service := newFilterTestService()
	a := newFilterTestApp(service)
	tt := openTestFilters(t, a)
	if !tt.HasText("No Gmail filters") || !tt.HasText("organize new incoming mail") {
		t.Fatalf("empty state is missing: %q", tt.Texts())
	}
}

func TestFiltersCreateCapturesRealInputAndServerID(t *testing.T) {
	service := newFilterTestService()
	a := newFilterTestApp(service)
	tt := openTestFilters(t, a)
	if err := tt.Click("New filter"); err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct{ label, value string }{
		{"Sender or address list", "Team <team@example.com>, other@example.com"},
		{"Recipient or address list", "alias@example.com"},
		{"Email subject", "Release"},
		{"Gmail search syntax", "  has:drive  "},
	} {
		if err := tt.Click(input.label); err != nil {
			t.Fatal(err)
		}
		tt.Type(input.value)
	}
	if err := tt.Click("Has attachment"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("No label"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	for _, action := range []string{"Star message", "Skip Inbox", "Mark as read"} {
		if err := tt.Click(action); err != nil {
			t.Fatal(err)
		}
	}
	if s := a.filterDialog; s.labelID != "Label_7" || !s.starred || !s.skipInbox || !s.markRead {
		t.Fatalf("action controls did not update their bound values: %+v", s)
	}
	if err := tt.Click("Create filter"); err != nil {
		t.Fatal(err)
	}
	if service.createCalls != 1 || len(service.created) != 1 {
		t.Fatalf("create calls = %d, captured = %d", service.createCalls, len(service.created))
	}
	created := service.created[0]
	wantCriteria := mail.FilterCriteria{
		From: "Team <team@example.com>, other@example.com", To: "alias@example.com", Subject: "Release",
		Query: "  has:drive  ", HasAttachment: true,
	}
	if created.ID != "" || created.Criteria != wantCriteria {
		t.Fatalf("created request = %+v, want criteria %+v and no local ID", created, wantCriteria)
	}
	wantAdd := []string{"Label_7", mail.Starred}
	wantRemove := []string{mail.Inbox, mail.Unread}
	if !equalStrings(created.Action.AddLabelIDs, wantAdd) || !equalStrings(created.Action.RemoveLabelIDs, wantRemove) {
		t.Fatalf("created action = %+v, want add %v remove %v", created.Action, wantAdd, wantRemove)
	}
	s := a.filterDialog
	if s == nil || len(s.filters) != 1 || s.filters[0].ID != "server-filter-1" {
		t.Fatalf("server result was not installed: %+v", s)
	}
	if s.creating || s.from != "" || s.query != "" || s.starred || s.labelID != "" {
		t.Fatalf("successful create did not reset the form: %+v", s)
	}
	if !tt.HasText("Query:   has:drive") {
		t.Fatalf("server-created rule is not rendered: %q", tt.Texts())
	}
}

func TestFiltersReauthorizeFitsMinimumWindow(t *testing.T) {
	service := newFilterTestService()
	service.createErr = &gmail.APIError{StatusCode: http.StatusForbidden, Reason: "insufficientPermissions", Message: "scope required"}
	a := newFilterTestApp(service)
	a.openFilters()
	s := a.filterDialog
	s.creating = true
	s.from = "sender@example.com"
	s.starred = true
	a.createFilter()
	if !s.reauthorize {
		t.Fatal("permission fixture did not enable reauthorization")
	}
	tt := ui.NewTester(a.View, 1280, 600)
	for _, name := range []string{"Reauthorize", "Create filter", "Close"} {
		r, ok := tt.Find(name)
		if !ok || r.W <= 0 || r.H <= 0 || r.Y < 0 || r.Y+r.H > 600 {
			t.Fatalf("%s is outside the 600pt window: %+v", name, r)
		}
	}
	if err := tt.Click("Close"); err != nil {
		t.Fatal(err)
	}
	if a.filterDialog != nil {
		t.Fatal("footer Close was not usable at the minimum height")
	}
}

func TestFiltersLabelNamedNoLabelUsesID(t *testing.T) {
	service := newFilterTestService()
	service.labels = append(service.labels, mail.Label{ID: "Label_no_label", Name: "No label", Hide: true})
	a := newFilterTestApp(service)
	tt := openTestFilters(t, a)
	if err := tt.Click("New filter"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Sender or address list"); err != nil {
		t.Fatal(err)
	}
	tt.Type("sender@example.com")
	if err := tt.Click("No label"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	if a.filterDialog == nil {
		t.Fatalf("choosing the same-named option closed the dialog: %q", tt.Texts())
	}
	if got := a.filterDialog.labelID; got != "Label_no_label" {
		t.Fatalf("label named No label was not selected by ID: %q", got)
	}
	a.createFilter()
	if len(service.created) != 1 || !equalStrings(service.created[0].Action.AddLabelIDs, []string{"Label_no_label"}) {
		t.Fatalf("created action did not include the same-named label: %+v", service.created)
	}
}

func TestFiltersLongLabelListScrollsToLast(t *testing.T) {
	service := newFilterTestService()
	for i := 1; i <= 30; i++ {
		id := fmt.Sprintf("Label_%02d", i)
		service.labels = append(service.labels, mail.Label{ID: id, Name: fmt.Sprintf("Label %02d", i), Hide: true})
	}
	a := newFilterTestApp(service)
	a.openFilters()
	tt := ui.NewTester(a.View, 1280, 600)
	if err := tt.Click("New filter"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Add label: No label"); err != nil {
		t.Fatal(err)
	}
	first, ok := tt.Find("Label 01")
	if !ok {
		t.Fatalf("first custom label is missing: %q", tt.Texts())
	}
	x, y := first.X+first.W/2, first.Y+first.H/2
	tt.Move(x, y)
	for range 30 {
		tt.Scroll(x, y, 0, 120)
	}
	if a.filterDialog.optionsScroll.Y <= 0 {
		t.Fatal("label popup did not scroll")
	}
	last, ok := tt.Find("Label 30")
	if !ok || last.W <= 0 || last.H <= 0 || last.Y < 0 || last.Y+last.H > 600 {
		t.Fatalf("last label is not visible after scrolling: %+v", last)
	}
	if err := tt.Click("Label 30"); err != nil {
		t.Fatal(err)
	}
	if got := a.filterDialog.labelID; got != "Label_30" {
		t.Fatalf("last label was not selected: %q", got)
	}
}

func TestFiltersCreateBusyCannotDismiss(t *testing.T) {
	service := newFilterTestService()
	a := newFilterTestApp(service)
	a.openFilters()
	s := a.filterDialog
	s.creating = true
	s.from = "sender@example.com"
	s.starred = true
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.createFilter()
	tt := ui.NewTester(a.View, 1280, 800)
	a.settings = &settingsState{open: true}
	a.closeSettings()
	if a.settings != nil {
		t.Fatal("busy Filters kept the unrelated Settings window open")
	}
	if a.filterDialog != s || !s.open {
		t.Fatal("closing Settings dismissed a busy Filters operation")
	}
	if err := tt.Click("Close"); err != nil {
		t.Logf("busy Close is disabled: %v", err)
	}
	if a.filterDialog != s || !s.open {
		t.Fatal("busy create allowed the footer Close action")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.filterDialog != s || !s.open {
		t.Fatal("busy create allowed Escape to dismiss the dialog")
	}
	if len(jobs) != 1 {
		t.Fatalf("create jobs = %d, want 1", len(jobs))
	}
	jobs[0]()
	tt.Frame()
	if s.busy != "" || s.creating || len(s.filters) != 1 {
		t.Fatalf("create did not complete after the dismissal checks: %+v", s)
	}
}

func TestFilterCreateWaitsForAccountTransition(t *testing.T) {
	h := newAccountTransitionHarness()
	service := newFilterTestService()
	a := h.app
	a.service = service
	a.account = service.account
	a.storeAccount = service.account.Email
	a.filterDialog = &filterDialogState{open: true, owner: service.account.Email, creating: true, from: "sender@example.com", starred: true}
	a.createFilter()
	a.transitionToService(h.target, h.target.account.Email, false, "")
	if len(h.jobs) != 2 {
		t.Fatalf("scheduled jobs = %d, want create and notification drain", len(h.jobs))
	}
	h.runJobAt(t, 1)
	h.runUpdate(t)
	if a.service != service || !a.mutationRunning {
		t.Fatalf("account changed before Create completed: service=%p running=%v", a.service, a.mutationRunning)
	}
	h.runJobAt(t, 0)
	if len(service.created) != 1 {
		t.Fatalf("Create did not finish on the old account: %+v", service.created)
	}
	h.runUpdate(t)
	h.finishTransition(t)
	if a.service != h.target || len(service.created) != 1 {
		t.Fatalf("transition completed without the old account Create: service=%p created=%d", a.service, len(service.created))
	}
}

func TestFiltersValidationMakesNoWriteRequests(t *testing.T) {
	service := newFilterTestService()
	a := newFilterTestApp(service)
	tt := openTestFilters(t, a)
	if err := tt.Click("New filter"); err != nil {
		t.Fatal(err)
	}
	createRect, ok := tt.Find("Create filter")
	if !ok || createRect.W <= 0 || createRect.H <= 0 {
		t.Fatalf("create action is not visible: %+v", createRect)
	}
	if err := tt.Click("Create filter"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Add at least one matching condition") || service.createCalls != 0 {
		t.Fatalf("missing criterion was not rejected locally: calls=%d texts=%q", service.createCalls, tt.Texts())
	}
	if err := tt.Click("Sender or address list"); err != nil {
		t.Fatal(err)
	}
	tt.Type("sender@example.com")
	if a.filterDialog.from != "sender@example.com" {
		t.Fatalf("From input did not update its bound value: %q", a.filterDialog.from)
	}
	if err := tt.Click("Create filter"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Choose at least one action") || service.createCalls != 0 {
		t.Fatalf("missing action was not rejected locally: calls=%d texts=%q", service.createCalls, tt.Texts())
	}
	if a.filterDialog.from != "sender@example.com" || a.filterDialog.busy != "" {
		t.Fatalf("validation lost input or left busy state: %+v", a.filterDialog)
	}
}

func TestFiltersDeleteRequiresConfirmation(t *testing.T) {
	service := newFilterTestService()
	service.filters = []mail.Filter{{ID: "filter-delete", Criteria: mail.FilterCriteria{From: "sender@example.com"}}}
	a := newFilterTestApp(service)
	tt := openTestFilters(t, a)
	if err := tt.Click("Delete…"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Delete this Gmail filter permanently?") || service.deleteCalls != 0 {
		t.Fatalf("delete happened before confirmation: calls=%d texts=%q", service.deleteCalls, tt.Texts())
	}
	if err := tt.Click("Delete permanently"); err != nil {
		t.Fatal(err)
	}
	if service.deleteCalls != 1 || len(service.deleted) != 1 || service.deleted[0] != "filter-delete" {
		t.Fatalf("deleted filters = %v, calls=%d", service.deleted, service.deleteCalls)
	}
	if len(a.filterDialog.filters) != 0 || a.filterDialog.deleteID != "" {
		t.Fatalf("deleted rule remains: %+v", a.filterDialog)
	}
	if !tt.HasText("No Gmail filters") {
		t.Fatalf("empty state did not follow deletion: %q", tt.Texts())
	}
}

func TestFiltersCreateErrorPreservesInputListAndBusyRecovery(t *testing.T) {
	service := newFilterTestService()
	service.filters = []mail.Filter{{ID: "filter-existing", Criteria: mail.FilterCriteria{From: "existing@example.com"}}}
	a := newFilterTestApp(service)
	tt := openTestFilters(t, a)
	if err := tt.Click("New filter"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Sender or address list"); err != nil {
		t.Fatal(err)
	}
	tt.Type("new@example.com")
	if err := tt.Click("Gmail search syntax"); err != nil {
		t.Fatal(err)
	}
	tt.Type("  is:unread  ")
	if err := tt.Click("Star message"); err != nil {
		t.Fatal(err)
	}
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	service.createErr = &gmail.APIError{StatusCode: http.StatusForbidden, Reason: "insufficientPermissions", Message: "scope required"}
	if err := tt.Click("Create filter"); err != nil {
		t.Fatal(err)
	}
	s := a.filterDialog
	if s.busy != "create" || len(jobs) != 1 {
		t.Fatalf("create did not enter one pending operation: busy=%q jobs=%d", s.busy, len(jobs))
	}
	a.createFilter()
	if len(jobs) != 1 {
		t.Fatalf("busy create admitted another request: %d", len(jobs))
	}
	jobs[0]()
	tt.Frame()
	if s.busy != "" || !s.creating || s.from != "new@example.com" || s.query != "  is:unread  " || !s.starred {
		t.Fatalf("create error did not preserve input and recover busy: %+v", s)
	}
	if len(s.filters) != 1 || s.filters[0].ID != "filter-existing" {
		t.Fatalf("create error changed existing rules: %+v", s.filters)
	}
	if !tt.HasText("Couldn't create filter") || !tt.HasText("Reauthorize") || !tt.HasText("From: existing@example.com") {
		t.Fatalf("error, reauthorization, or existing list is missing: %q", tt.Texts())
	}
}

func TestFiltersListErrorKeepsRulesAndForm(t *testing.T) {
	service := newFilterTestService()
	service.filters = []mail.Filter{{ID: "filter-existing", Criteria: mail.FilterCriteria{From: "existing@example.com"}}}
	a := newFilterTestApp(service)
	tt := openTestFilters(t, a)
	if err := tt.Click("New filter"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Sender or address list"); err != nil {
		t.Fatal(err)
	}
	tt.Type("draft@example.com")
	service.listErr = &gmail.APIError{StatusCode: http.StatusUnauthorized, Reason: "authError", Message: "expired"}
	if err := tt.Click("Refresh filters"); err != nil {
		t.Fatal(err)
	}
	s := a.filterDialog
	if s.loading || s.from != "draft@example.com" || !s.creating || len(s.filters) != 1 {
		t.Fatalf("list error discarded state: %+v", s)
	}
	if !tt.HasText("Couldn't load filters") || !tt.HasText("Reauthorize") || !tt.HasText("From: existing@example.com") {
		t.Fatalf("list error rendering is incomplete: %q", tt.Texts())
	}
}

func TestFiltersDeleteErrorRestoresBusyAndKeepsRule(t *testing.T) {
	service := newFilterTestService()
	service.filters = []mail.Filter{{ID: "filter-delete", Criteria: mail.FilterCriteria{From: "sender@example.com"}}}
	a := newFilterTestApp(service)
	tt := openTestFilters(t, a)
	if err := tt.Click("Delete…"); err != nil {
		t.Fatal(err)
	}
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	service.deleteErr = errors.New("offline")
	if err := tt.Click("Delete permanently"); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || a.filterDialog.busy != "delete:filter-delete" {
		t.Fatalf("delete pending state is wrong: jobs=%d state=%+v", len(jobs), a.filterDialog)
	}
	jobs[0]()
	tt.Frame()
	if a.filterDialog.busy != "" || len(a.filterDialog.filters) != 1 || a.filterDialog.deleteID != "filter-delete" {
		t.Fatalf("delete failure lost state: %+v", a.filterDialog)
	}
	if !tt.HasText("Couldn't delete filter") || !tt.HasText("From: sender@example.com") {
		t.Fatalf("delete failure is not rendered: %q", tt.Texts())
	}
}

func TestFiltersDemoAndUnsupportedServicesDoNotWrite(t *testing.T) {
	t.Run("demo", func(t *testing.T) {
		service := newFilterTestService()
		service.account.Demo = true
		a := newTestApp(service)
		a.settings = &settingsState{open: true, accounts: []mail.Account{service.account}}
		tt := ui.NewTester(a.View, 1280, 800)
		if tt.HasText("Filters") {
			t.Fatalf("Demo exposes filter management: %q", tt.Texts())
		}
		a.openFilters()
		if a.filterDialog != nil || service.listCalls != 0 {
			t.Fatalf("Demo opened filter management: dialog=%p lists=%d", a.filterDialog, service.listCalls)
		}
		a.filterDialog = &filterDialogState{open: true, creating: true, from: "sender@example.com", starred: true}
		a.createFilter()
		a.requestFilterDelete("filter-1")
		a.deleteFilter("filter-1")
		if service.listCalls+service.createCalls+service.deleteCalls != 0 || a.filterDialog.busy != "" {
			t.Fatalf("Demo made filter requests: list=%d create=%d delete=%d busy=%q", service.listCalls, service.createCalls, service.deleteCalls, a.filterDialog.busy)
		}
	})
	t.Run("unsupported", func(t *testing.T) {
		service := newFakeService()
		service.account = mail.Account{Name: "Real User", Email: "real@example.com"}
		a := newTestApp(struct{ mail.Service }{service})
		a.openFilters()
		s := a.filterDialog
		if s == nil || s.err == "" || s.loading || s.busy != "" {
			t.Fatalf("unsupported service state = %+v", s)
		}
		s.creating = true
		s.from = "sender@example.com"
		s.starred = true
		a.createFilter()
		a.requestFilterDelete("filter-1")
		a.deleteFilter("filter-1")
		if s.busy != "" || s.err == "" {
			t.Fatalf("unsupported service started a write: %+v", s)
		}
	})
}

func TestFiltersLateListResponseIsolatedAfterClose(t *testing.T) {
	service := newFilterTestService()
	a := newFilterTestApp(service)
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.openFilters()
	first := a.filterDialog
	tt := ui.NewTester(a.View, 1280, 800)
	if len(jobs) != 1 || !first.loading || !tt.HasText("Loading filters") {
		t.Fatalf("initial loading state is missing: jobs=%d texts=%q", len(jobs), tt.Texts())
	}
	a.closeFilterDialog(first)
	a.openFilters()
	second := a.filterDialog
	if first == second || len(jobs) != 2 {
		t.Fatalf("reopen did not create independent work: %p %p jobs=%d", first, second, len(jobs))
	}
	service.filters = []mail.Filter{{ID: "old-response", Criteria: mail.FilterCriteria{From: "old@example.com"}}}
	jobs[0]()
	tt.Frame()
	if !first.loading || first.loaded || len(first.filters) != 0 {
		t.Fatalf("closed dialog accepted its obsolete response: %+v", first)
	}
	if !second.loading || second.loaded || len(second.filters) != 0 {
		t.Fatalf("closed dialog response polluted the replacement: %+v", second)
	}
	service.filters = []mail.Filter{{ID: "new-response", Criteria: mail.FilterCriteria{From: "new@example.com"}}}
	jobs[1]()
	tt.Frame()
	if second.loading || len(second.filters) != 1 || second.filters[0].ID != "new-response" || !tt.HasText("From: new@example.com") {
		t.Fatalf("current response was not applied: state=%+v texts=%q", second, tt.Texts())
	}
	if tt.HasText("From: old@example.com") {
		t.Fatalf("stale response was rendered: %q", tt.Texts())
	}
}

func TestFiltersLateCreateResponseIsolatedAfterClose(t *testing.T) {
	service := newFilterTestService()
	a := newFilterTestApp(service)
	a.openFilters()
	s := a.filterDialog
	s.creating = true
	s.from = "sender@example.com"
	s.starred = true
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.createFilter()
	if len(jobs) != 1 {
		t.Fatalf("create jobs = %d, want 1", len(jobs))
	}
	a.closeFilterDialog(s)
	jobs[0]()
	if a.filterDialog != nil || len(s.filters) != 0 || s.busy != "create" {
		t.Fatalf("closed create callback changed state: dialog=%p old=%+v", a.filterDialog, s)
	}
}

func TestFiltersLateResponseIsolatedAfterAccountSwitch(t *testing.T) {
	service := newFilterTestService()
	a := newFilterTestApp(service)
	a.openFilters()
	oldDialog := a.filterDialog
	var oldJobs []func()
	a.schedule = func(fn func()) { oldJobs = append(oldJobs, fn) }
	a.loadFilters(oldDialog)
	if len(oldJobs) != 1 {
		t.Fatalf("old account refresh jobs = %d", len(oldJobs))
	}
	replacement := newFilterTestService()
	replacement.account.Email = "other@example.com"
	a.schedule = func(fn func()) { fn() }
	a.useService(replacement, replacement.account.Email)
	if a.filterDialog != nil {
		t.Fatal("account replacement retained the old filter dialog")
	}
	var newJobs []func()
	a.schedule = func(fn func()) { newJobs = append(newJobs, fn) }
	a.openFilters()
	newDialog := a.filterDialog
	service.filters = []mail.Filter{{ID: "old-account", Criteria: mail.FilterCriteria{From: "old@example.com"}}}
	oldJobs[0]()
	if !oldDialog.loading || len(oldDialog.filters) != 0 {
		t.Fatalf("old account accepted its obsolete response: %+v", oldDialog)
	}
	if !newDialog.loading || len(newDialog.filters) != 0 {
		t.Fatalf("old account response polluted the new account: %+v", newDialog)
	}
	replacement.filters = []mail.Filter{{ID: "new-account", Criteria: mail.FilterCriteria{From: "other@example.com"}}}
	newJobs[0]()
	if len(newDialog.filters) != 1 || newDialog.filters[0].ID != "new-account" {
		t.Fatalf("new account response was not applied: %+v", newDialog)
	}
}

func TestSettingsCloseKeepsFilterDialog(t *testing.T) {
	service := newFilterTestService()
	a := newTestApp(service)
	a.openFilters()
	opened := a.filterDialog
	a.settings = &settingsState{open: true}
	a.closeSettings()
	if a.settings != nil {
		t.Fatal("Settings remained open")
	}
	if opened == nil || a.filterDialog != opened || !opened.open {
		t.Fatal("Settings close dismissed the independent Filters feature")
	}
	if !a.shortcutsDisabled() {
		t.Fatal("the open Filters dialog no longer protects mail shortcuts")
	}
}
