package app

import (
	netmail "net/mail"
	"strings"
	"testing"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

func TestGmailShortcutsNavigateStarArchiveAndUndo(t *testing.T) {
	service := newFakeService()
	a := newTestApp(service)
	tt := ui.NewTester(a.View, 1280, 800)
	tt.Key(0, ui.KeyJ)
	if a.selectedRow != 0 || len(a.thread.Messages) != 1 {
		t.Fatalf("j did not open the first conversation: row %d, thread %+v", a.selectedRow, a.thread)
	}
	tt.Key(0, ui.KeyS)
	if len(service.add) != 1 || service.add[0] != mail.Starred || a.undo == nil {
		t.Fatalf("star action was not recorded: add %v, undo %+v", service.add, a.undo)
	}
	tt.Key(0, ui.KeyZ)
	if len(service.remove) != 1 || service.remove[0] != mail.Starred {
		t.Fatalf("undo did not remove the star: %v", service.remove)
	}
	tt.Key(0, ui.KeyE)
	if len(a.threads) != 0 || len(service.remove) != 1 || service.remove[0] != mail.Inbox {
		t.Fatalf("archive failed: threads %v, remove %v", a.threads, service.remove)
	}
	tt.Key(0, ui.KeyZ)
	if len(service.add) != 1 || service.add[0] != mail.Inbox {
		t.Fatalf("undo did not restore INBOX: %v", service.add)
	}
	tt.Key(0, ui.KeyG)
	tt.Key(0, ui.KeyS)
	if a.selectedLabel != mail.Starred {
		t.Fatalf("g+s selected %q, want STARRED", a.selectedLabel)
	}
	tt.Key(ui.Shift, ui.KeySlash)
	if !a.helpOpen || !tt.HasText("Keyboard shortcuts") {
		t.Fatal("shortcut help did not open")
	}
}

func TestArchiveShortcutTwiceAdvancesSelection(t *testing.T) {
	service := newFakeService()
	service.threads = append(service.threads,
		mail.ThreadSummary{ID: "thread-2", Subject: "Second", Senders: "Bob", LabelIDs: []string{mail.Inbox}},
		mail.ThreadSummary{ID: "thread-3", Subject: "Third", Senders: "Cid", LabelIDs: []string{mail.Inbox}},
	)
	service.threadFunc = func(id string) (mail.Thread, error) {
		return mail.Thread{ID: id, Subject: id, Messages: []mail.Message{{ID: "message-" + id, ThreadID: id, Text: id + " body"}}}, nil
	}
	a := newTestApp(service)
	tt := ui.NewTester(a.View, 1280, 800)
	tt.Key(0, ui.KeyJ)
	tt.Frame()
	service.modified = nil
	tt.Key(0, ui.KeyE)
	tt.Frame()
	if a.selectedThread != "thread-2" || a.thread.ID != "thread-2" {
		t.Fatalf("first archive selected %q and loaded %q, want thread-2", a.selectedThread, a.thread.ID)
	}
	tt.Key(0, ui.KeyE)
	tt.Frame()
	if len(service.modified) != 2 || service.modified[0] != "thread-1" || service.modified[1] != "thread-2" {
		t.Fatalf("archived IDs = %v, want thread-1 then thread-2", service.modified)
	}
	if a.selectedThread != "thread-3" || a.thread.ID != "thread-3" || a.selectedRow != 0 {
		t.Fatalf("second archive left thread=%q loaded=%q row=%d, want thread-3 at row 0", a.selectedThread, a.thread.ID, a.selectedRow)
	}
}

func TestReportSpamAndUndo(t *testing.T) {
	service := newFakeService()
	a := newTestApp(service)
	a.selectThread("thread-1")
	a.reportSpam()
	if len(service.add) != 1 || service.add[0] != mail.Spam || len(service.remove) != 1 || service.remove[0] != mail.Inbox {
		t.Fatalf("spam labels: add %v, remove %v", service.add, service.remove)
	}
	a.undoLast()
	if len(service.add) != 1 || service.add[0] != mail.Inbox || len(service.remove) != 1 || service.remove[0] != mail.Spam {
		t.Fatalf("spam undo labels: add %v, remove %v", service.add, service.remove)
	}
}

func TestReplyAllRecipients(t *testing.T) {
	message := mail.Message{
		From: "Ada <ada@example.com>",
		To:   "Alex <alex@example.com>, Bob <bob@example.com>",
		Cc:   "Carol <carol@example.com>, Alex <alex@example.com>",
	}
	to, cc := replyAllRecipients(message, "alex@example.com")
	toAddresses, err := netmail.ParseAddressList(to)
	if err != nil || len(toAddresses) != 2 || toAddresses[0].Address != "ada@example.com" || toAddresses[1].Address != "bob@example.com" {
		t.Fatalf("reply-all To = %q (%v)", to, err)
	}
	ccAddresses, err := netmail.ParseAddressList(cc)
	if err != nil || len(ccAddresses) != 1 || ccAddresses[0].Address != "carol@example.com" {
		t.Fatalf("reply-all Cc = %q (%v)", cc, err)
	}
}

func TestForwardComposition(t *testing.T) {
	a := newTestApp(newFakeService())
	a.selectThread("thread-1")
	a.openForward()
	if a.compose == nil || !a.compose.forward || a.compose.threadID != "" {
		t.Fatalf("forward state = %+v", a.compose)
	}
	if a.compose.subject != "Fwd: Launch plan" {
		t.Fatalf("forward subject = %q", a.compose.subject)
	}
	if body := a.compose.doc.Text().PlainText(); !strings.Contains(body, "The latest plan") || !strings.Contains(body, "From: Ada <ada@example.com>") {
		t.Fatalf("forward body = %q", body)
	}
}

func TestComposeShortcuts(t *testing.T) {
	a := newTestApp(newFakeService())
	tt := ui.NewTester(a.View, 1280, 800)
	tt.Key(0, ui.KeyC)
	if a.compose == nil {
		t.Fatal("c did not open the composer")
	}
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyC)
	t.Logf("after Ctrl+Shift+C: showCc=%v showBcc=%v", a.compose.showCc, a.compose.showBcc)
	tt.Key(ui.Cmd|ui.Shift, ui.KeyB)
	t.Logf("after Ctrl+Shift+B: showCc=%v showBcc=%v", a.compose.showCc, a.compose.showBcc)
	if !a.compose.showCc || !a.compose.showBcc {
		t.Fatal("recipient shortcuts did not expand Cc and Bcc")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyD)
	if a.compose != nil {
		t.Fatal("discard shortcut did not close the composer")
	}
}

func TestMoveMessageAndTextInputIsolation(t *testing.T) {
	service := newFakeService()
	service.thread.Messages = append(service.thread.Messages, mail.Message{ID: "message-2", ThreadID: "thread-1", From: "Bob <bob@example.com>", Date: time.Now(), Text: "Second"})
	a := newTestApp(service)
	a.selectThread("thread-1")
	a.moveMessage(-1)
	if a.focusedMessage != 0 || a.messageScroll != 0 {
		t.Fatalf("previous message state = %d, %d", a.focusedMessage, a.messageScroll)
	}
	a.moveMessage(1)
	if a.focusedMessage != 1 || a.messageScroll != 1 {
		t.Fatalf("next message state = %d, %d", a.focusedMessage, a.messageScroll)
	}
	tt := ui.NewTester(a.View, 1280, 800)
	if err := tt.Click("Search mail"); err != nil {
		t.Fatal(err)
	}
	tt.Type("c")
	if a.compose != nil {
		t.Fatal("typing in search opened the composer")
	}
}
