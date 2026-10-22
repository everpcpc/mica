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
