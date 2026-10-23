package app

import (
	"testing"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

func TestToastExpiresWhileHovered(t *testing.T) {
	a := newTestApp(newFakeService())
	var expire func()
	a.after = func(_ time.Duration, fn func()) { expire = fn }
	a.undo = &undoAction{id: "thread-1"}
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
