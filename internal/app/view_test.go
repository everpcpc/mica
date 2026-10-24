package app

import (
	"testing"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

func TestToastDurations(t *testing.T) {
	for _, tc := range []struct {
		name string
		undo bool
		want time.Duration
	}{
		{name: "notice", want: 3 * time.Second},
		{name: "action", undo: true, want: 5 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(newFakeService())
			var got time.Duration
			a.after = func(duration time.Duration, _ func()) { got = duration }
			if tc.undo {
				a.undo = &undoAction{items: []undoItem{{id: "thread-1"}}}
			}
			a.notice = "Archived"
			ui.NewTester(a.View, 1280, 800)
			if got != tc.want {
				t.Fatalf("toast duration = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestToastExpiresWhileHovered(t *testing.T) {
	a := newTestApp(newFakeService())
	var expire func()
	a.after = func(_ time.Duration, fn func()) { expire = fn }
	a.undo = &undoAction{items: []undoItem{{id: "thread-1"}}}
	a.notice = "Archived"
	tt := ui.NewTester(a.View, 1280, 800)
	toast, ok := tt.Find("Archived")
	if !ok {
		t.Fatal("toast did not appear")
	}
	tt.Move(toast.X+toast.W/2, toast.Y+toast.H/2)
	if expire == nil {
		t.Fatal("toast did not schedule its expiration")
	}
	expire()
	tt.Frame()
	if tt.HasText("Archived") {
		t.Fatalf("toast remained visible after its deadline: %q", tt.Texts())
	}
}

func TestStaleToastTimerDoesNotExpireCurrentToast(t *testing.T) {
	a := newTestApp(newFakeService())
	var expirations []func()
	a.after = func(_ time.Duration, fn func()) { expirations = append(expirations, fn) }
	a.notice = "First"
	tt := ui.NewTester(a.View, 1280, 800)
	a.notice = "Second"
	tt.Frame()
	if len(expirations) != 2 {
		t.Fatalf("expiration callbacks = %d, want 2", len(expirations))
	}
	expirations[0]()
	if a.expiredNoticeID != "" {
		t.Fatalf("stale timer scheduled an expiration for %q", a.expiredNoticeID)
	}
	tt.Frame()
	if !tt.HasText("Second") {
		t.Fatal("stale timer expired the current toast")
	}
	expirations[1]()
	tt.Frame()
	if tt.HasText("Second") {
		t.Fatal("current toast did not expire at its deadline")
	}
}

func TestAccountPhotoLoads(t *testing.T) {
	a := newTestApp(newFakeService())
	a.loadAccountPhoto(mail.Account{PhotoURL: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="})
	if a.accountPhoto == nil {
		t.Fatal("account photo was not decoded")
	}
}

func TestImportantMarkerAppearsInListAndReader(t *testing.T) {
	service := newFakeService()
	service.threads[0].LabelIDs = append(service.threads[0].LabelIDs, mail.Important)
	service.thread.LabelIDs = append(service.thread.LabelIDs, mail.Important)
	service.thread.Messages[0].LabelIDs = append(service.thread.Messages[0].LabelIDs, mail.Important)
	a := newTestApp(service)
	a.selectThread("thread-1")
	tt := ui.NewTester(a.View, 1280, 800)
	count := 0
	for _, text := range tt.Texts() {
		if text == "Important" {
			count++
		}
	}
	if count < 2 {
		t.Fatalf("Important appears %d time(s), want markers in both list and reader: %q", count, tt.Texts())
	}
}

func TestUnreadFilterKeepsReaderOpen(t *testing.T) {
	service := newFakeService()
	service.threads = append(service.threads, mail.ThreadSummary{
		ID: "thread-2", Subject: "Already read", Senders: "Bob", LabelIDs: []string{mail.Inbox},
	})
	a := newTestApp(service)
	tt := ui.NewTester(a.View, 1280, 800)
	if err := tt.Click("Filter unread"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("Already read") || !tt.HasText("Launch plan") {
		t.Fatalf("unread filter showed the wrong conversations: %q", tt.Texts())
	}
	if err := tt.Click("Launch plan"); err != nil {
		t.Fatal(err)
	}
	if a.thread.ID != "thread-1" || !tt.HasText("Launch plan") {
		t.Fatal("opening a filtered conversation did not keep its reader visible")
	}
	if !tt.HasText("No unread conversations") {
		t.Fatalf("read conversation remained in the unread filter: %q", tt.Texts())
	}
	if err := tt.Click("Show all mail"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Already read") || a.thread.ID != "thread-1" {
		t.Fatalf("restoring all mail lost a conversation or the reader: %q", tt.Texts())
	}
}

func TestUnreadFilterKeyboardNavigation(t *testing.T) {
	service := newFakeService()
	service.threads = append(service.threads,
		mail.ThreadSummary{ID: "thread-2", Subject: "Already read", LabelIDs: []string{mail.Inbox}},
		mail.ThreadSummary{
			ID: "thread-3", Subject: "Another unread", LabelIDs: []string{mail.Inbox, mail.Unread}, Unread: true,
		},
	)
	service.threadFunc = func(id string) (mail.Thread, error) {
		return mail.Thread{
			ID: id, Subject: id,
			Messages: []mail.Message{{ID: "message-" + id, ThreadID: id, Text: id + " body"}},
		}, nil
	}
	a := newTestApp(service)
	tt := ui.NewTester(a.View, 1280, 800)
	if err := tt.Click("Filter unread"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyJ)
	if a.selectedThread != "thread-1" {
		t.Fatalf("first keyboard selection = %q, want thread-1", a.selectedThread)
	}
	tt.Key(0, ui.KeyJ)
	if a.selectedThread != "thread-3" || a.thread.ID != "thread-3" {
		t.Fatalf("keyboard navigation did not follow the filtered list: selected=%q loaded=%q", a.selectedThread, a.thread.ID)
	}
}

func TestThreadListUsesRoundedSelection(t *testing.T) {
	a := newTestApp(newFakeService())
	_ = ui.NewTester(a.View, 1280, 800)
	if !a.list.RoundRows {
		t.Fatal("conversation rows do not use rounded selection")
	}
}

func TestThreadListSelectionHasDividerSpacing(t *testing.T) {
	a := newTestApp(newFakeService())
	a.selectThread("thread-1")
	var border ui.Color
	tt := ui.NewTester(func(c *ui.Context) {
		border = c.Theme().Border
		a.View(c)
	}, 1280, 800)
	row, ok := tt.Find("Ada, Launch plan, The latest plan")
	if !ok {
		t.Fatalf("selected row not found: %q", tt.Texts())
	}
	dividerY := -1
	for y := int(row.Y) - 1; y >= 0; y-- {
		pixel := tt.Image().RGBAAt(int(row.X+8), y)
		if pixel.R == border.R && pixel.G == border.G && pixel.B == border.B && pixel.A == border.A {
			dividerY = y
			break
		}
	}
	if dividerY < 0 {
		t.Fatal("divider above the selected row not found")
	}
	if gap := int(row.Y) - dividerY; gap < 5 {
		t.Fatalf("selected row is %dpx below the divider, want at least 5px", gap)
	}
}

func TestThreadListMultiSelection(t *testing.T) {
	service := newFakeService()
	service.threads = append(service.threads,
		mail.ThreadSummary{ID: "thread-2", Subject: "Second", Senders: "Bob", LabelIDs: []string{mail.Inbox}},
		mail.ThreadSummary{ID: "thread-3", Subject: "Third", Senders: "Cid", LabelIDs: []string{mail.Inbox}},
	)
	service.threadFunc = func(id string) (mail.Thread, error) {
		return mail.Thread{
			ID: id, Subject: id,
			Messages: []mail.Message{{ID: "message-" + id, ThreadID: id, Text: id + " body"}},
		}, nil
	}
	a := newTestApp(service)
	a.selectThread("thread-1")
	tt := ui.NewTester(a.View, 1280, 800)
	if err := tt.Click("Second"); err != nil {
		t.Fatal(err)
	}
	if a.selectedThread != "thread-2" || a.threadSelection.Len() != 1 || !a.threadSelection.Has("thread-2") {
		t.Fatalf("plain click did not collapse selection: %q, %v", a.selectedThread, a.threadSelection)
	}
	if err := tt.ClickWith(ui.Cmd, "Launch plan"); err != nil {
		t.Fatal(err)
	}
	if a.selectedThread != "thread-1" || a.threadSelection.Len() != 2 || !a.threadSelection.Has("thread-1") || !a.threadSelection.Has("thread-2") {
		t.Fatalf("Cmd-click did not add to selection: %q, %v", a.selectedThread, a.threadSelection)
	}
	if _, ok := tt.Find("Archive 2 conversations"); !ok {
		t.Fatal("multi-selection does not show the archive count")
	}
	if err := tt.ClickWith(ui.Cmd, "Launch plan"); err != nil {
		t.Fatal(err)
	}
	if a.selectedThread != "thread-1" || a.threadSelection.Len() != 1 || !a.threadSelection.Has("thread-2") {
		t.Fatalf("Cmd-click did not remove only the clicked row: %q, %v", a.selectedThread, a.threadSelection)
	}
	if err := tt.ClickWith(ui.Shift, "Third"); err != nil {
		t.Fatal(err)
	}
	if a.selectedThread != "thread-3" || a.threadSelection.Len() != 3 {
		t.Fatalf("Shift-click did not select the range: %q, %v", a.selectedThread, a.threadSelection)
	}
	if err := tt.ClickWith(ui.Cmd, "Second"); err != nil {
		t.Fatal(err)
	}
	if a.threadSelection.Len() != 2 || !a.threadSelection.Has("thread-1") || !a.threadSelection.Has("thread-3") {
		t.Fatalf("Cmd-click did not update the range: %v", a.threadSelection)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	if a.threadSelection.Len() != 3 || !a.threadSelection.Has("thread-1") || !a.threadSelection.Has("thread-2") || !a.threadSelection.Has("thread-3") {
		t.Fatalf("Cmd+A did not select every conversation: %v", a.threadSelection)
	}
}

func TestBatchArchiveButtonWithoutReader(t *testing.T) {
	service := newFakeService()
	service.threads = append(service.threads, mail.ThreadSummary{ID: "thread-2", Subject: "Second", LabelIDs: []string{mail.Inbox}})
	service.threadFunc = func(id string) (mail.Thread, error) {
		return mail.Thread{ID: id, Messages: []mail.Message{{ID: "message-" + id, ThreadID: id, Text: id + " body"}}}, nil
	}
	a := newTestApp(service)
	a.selectThread("thread-1")
	tt := ui.NewTester(a.View, 1280, 800)
	if err := tt.ClickWith(ui.Cmd, "Second"); err != nil {
		t.Fatal(err)
	}
	a.closeReader()
	tt.Frame()
	if err := tt.Click("Archive 2 conversations"); err != nil {
		t.Fatal(err)
	}
	if len(a.threads) != 0 {
		t.Fatalf("batch archive without a reader left %+v", a.threads)
	}
}

func TestThreadSelectionSurvivesListChanges(t *testing.T) {
	service := newFakeService()
	service.threads = append(service.threads,
		mail.ThreadSummary{ID: "thread-2", Subject: "Second", LabelIDs: []string{mail.Inbox}},
		mail.ThreadSummary{ID: "thread-3", Subject: "Third", LabelIDs: []string{mail.Inbox}},
	)
	a := newTestApp(service)
	a.selectThread("thread-2")
	for i, id := range []string{"thread-1", "thread-2", "thread-3"} {
		a.threads[i].Unread = id != "thread-3"
		a.threadSelection.Add(id)
	}
	a.unreadOnly = true
	a.syncThreadSelection(false)
	if a.threadSelection.Len() != 2 || !a.threadSelection.Has("thread-1") || !a.threadSelection.Has("thread-2") || a.threadSelection.Has("thread-3") {
		t.Fatalf("filter did not preserve only visible selections: %v", a.threadSelection)
	}
	a.applyHistoryChanges([]mail.ThreadSummary{{ID: "thread-new", Subject: "New", LabelIDs: []string{mail.Inbox}, Unread: true, Updated: time.Now()}}, nil)
	if a.selectedThread != "thread-2" || a.threadSelection.Len() != 2 || !a.threadSelection.Has("thread-1") || !a.threadSelection.Has("thread-2") {
		t.Fatalf("history update changed the multi-selection: %q, %v", a.selectedThread, a.threadSelection)
	}
}

func TestThreadSelectionPrecedesLoading(t *testing.T) {
	for _, tc := range []struct {
		name  string
		click bool
	}{
		{name: "list click", click: true},
		{name: "thread ID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(newFakeService())
			var jobs []func()
			a.schedule = func(fn func()) { jobs = append(jobs, fn) }
			if tc.click {
				tt := ui.NewTester(a.View, 1280, 800)
				if err := tt.Click("Launch plan"); err != nil {
					t.Fatal(err)
				}
			} else {
				a.selectThread("thread-1")
			}
			if a.selectedThread != "thread-1" {
				t.Fatalf("selected thread = %q, want thread-1", a.selectedThread)
			}
			if a.selectedRow != 0 {
				t.Fatalf("selected row before loading = %d, want 0", a.selectedRow)
			}
			if !a.threadLoading || len(jobs) != 1 {
				t.Fatalf("loading state = %v, jobs = %d, want one pending load", a.threadLoading, len(jobs))
			}
		})
	}
}

func TestScrollToEndLoadsNextThreadPage(t *testing.T) {
	first := make([]mail.ThreadSummary, 25)
	second := make([]mail.ThreadSummary, 25)
	for i := range 25 {
		first[i] = mail.ThreadSummary{
			ID: "first-" + string(rune('A'+i)), Subject: "First page", LabelIDs: []string{mail.Inbox},
			Updated: time.Unix(int64(100-i), 0),
		}
		second[i] = mail.ThreadSummary{
			ID: "second-" + string(rune('a'+i)), Subject: "Second page", LabelIDs: []string{mail.Inbox},
			Updated: time.Unix(int64(50-i), 0),
		}
	}
	service := &pagingService{fakeService: newFakeService(), pages: []mail.ThreadPage{
		{Threads: first, NextPageToken: "next"}, {Threads: second},
	}}
	service.threads = first
	a := New(service)
	a.schedule = func(fn func()) { fn() }
	a.restoreAttempted = true
	a.Start()
	if len(a.threads) != 25 {
		t.Fatalf("initial threads = %d, want 25", len(a.threads))
	}
	tt := ui.NewTester(a.View, 1280, 400)
	a.list.ScrollToEnd()
	tt.Frame()
	tt.Frame()
	if len(a.threads) != 50 {
		t.Fatalf("threads after scrolling to the end = %d, want 50", len(a.threads))
	}
	if len(service.queries) != 2 || service.queries[1].PageToken != "next" {
		t.Fatalf("page queries = %+v, want a second query with next token", service.queries)
	}
}
