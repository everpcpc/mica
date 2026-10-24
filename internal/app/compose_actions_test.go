package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	mailmodel "mica/internal/mail"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
)

type individualAttachmentService struct {
	*fakeService
	data  map[string][]byte
	errs  map[string]error
	calls []string
}

func (s *individualAttachmentService) Attachment(_ context.Context, messageID, attachmentID string) ([]byte, error) {
	key := messageID + "/" + attachmentID
	s.calls = append(s.calls, key)
	if err := s.errs[key]; err != nil {
		return nil, err
	}
	return append([]byte(nil), s.data[key]...), nil
}

func TestOpenReplyMessageUsesSelectedMessage(t *testing.T) {
	a, service := newIndividualMessageFixture()
	clickIndividualMessageAction(t, a, 0, "Reply")
	m := a.compose
	if m == nil {
		t.Fatal("selected message did not open a reply")
	}
	if m.to != "First Replies <first-replies@example.com>" || m.subject != "Re: First subject" {
		t.Fatalf("reply recipients or subject came from another message: %+v", m)
	}
	if m.threadID != "thread-1" || m.inReplyTo != "<first-id@example.com>" || m.references != "<root@example.com> <first-id@example.com>" {
		t.Fatalf("reply threading metadata = %+v", m)
	}
	if m.quote == nil || m.quote.ID != "message-first" || !strings.Contains(replyQuoteText(*m.quote), "First body") || strings.Contains(replyQuoteText(*m.quote), "Third body") {
		t.Fatalf("reply quote = %+v", m.quote)
	}
	m.doc.SetText(richtext.NewText("Body"))
	a.sendCompose()
	if !service.sent.ThreadingResolved {
		t.Fatal("selected reply did not preserve its resolved threading headers")
	}
}

func TestOpenReplyAllMessageUsesSelectedMessage(t *testing.T) {
	a, _ := newIndividualMessageFixture()
	clickIndividualMessageAction(t, a, 0, "Reply all")
	m := a.compose
	if m == nil {
		t.Fatal("selected message did not open reply all")
	}
	for _, want := range []string{"first-replies@example.com", "first-to@example.com"} {
		if !strings.Contains(m.to, want) {
			t.Errorf("reply-all To = %q, missing %s", m.to, want)
		}
	}
	if !strings.Contains(m.cc, "first-cc@example.com") {
		t.Errorf("reply-all Cc = %q, want the selected message Cc", m.cc)
	}
	if strings.Contains(m.to+m.cc, "third") || m.inReplyTo != "<first-id@example.com>" || m.references != "<root@example.com> <first-id@example.com>" {
		t.Fatalf("reply-all used another message: %+v", m)
	}
}

func TestOpenForwardMessageUsesSelectedMessageAndAttachments(t *testing.T) {
	a, service := newIndividualMessageFixture()
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	clickIndividualMessageAction(t, a, 0, "Forward")
	m := a.compose
	if m == nil || !m.forward || m.threadID != "" || m.inReplyTo != "" || m.references != "" {
		t.Fatalf("forward metadata = %+v", m)
	}
	if m.subject != "Fwd: First subject" {
		t.Fatalf("forward subject = %q", m.subject)
	}
	body := m.doc.Text().PlainText()
	if !strings.Contains(body, "First body") || !strings.Contains(body, "First Sender <first@example.com>") || strings.Contains(body, "Third body") {
		t.Fatalf("forward quote = %q", body)
	}
	if len(m.attachments) != 2 || len(jobs) != 1 {
		t.Fatalf("forward attachments = %+v, jobs = %d", m.attachments, len(jobs))
	}
	for _, item := range m.attachments {
		if strings.Contains(item.attachment.Name, "second") || strings.Contains(item.attachment.Name, "third") {
			t.Fatalf("forward included another message attachment: %+v", item)
		}
	}
	runNextComposeJob(t, &jobs)
	if len(jobs) != 1 || len(service.calls) != 1 || service.calls[0] != "message-first/first-normal" {
		t.Fatalf("attachment reads were not serialized: jobs=%d calls=%v", len(jobs), service.calls)
	}
	runNextComposeJob(t, &jobs)
	if len(service.calls) != 1 {
		t.Fatalf("inline attachment used AttachmentFetcher: %v", service.calls)
	}
	m.to = "destination@example.com"
	a.schedule = func(fn func()) { fn() }
	a.sendCompose()
	if a.compose != nil {
		t.Fatal("forward with loaded attachments stayed open")
	}
	sent := service.sent
	if sent.ThreadID != "" || sent.InReplyTo != "" || sent.References != "" {
		t.Fatalf("sent forward threading metadata = %+v", sent)
	}
	if len(sent.Attachments) != 2 {
		t.Fatalf("sent forward attachments = %+v", sent.Attachments)
	}
	if sent.Attachments[0].Name != "first-normal.bin" || !bytes.Equal(sent.Attachments[0].Data, []byte{0, 1, '\r', '\n', 255}) {
		t.Fatalf("downloaded attachment bytes = %+v", sent.Attachments[0])
	}
	if sent.Attachments[1].Name != "first-inline.txt" || string(sent.Attachments[1].Data) != "inline first" {
		t.Fatalf("inline attachment bytes = %+v", sent.Attachments[1])
	}
}

func TestForwardAttachmentDownloadFailureBlocksSend(t *testing.T) {
	a, service := newIndividualMessageFixture()
	service.errs["message-first/first-normal"] = errors.New("offline")
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.openForwardMessage(a.thread.Messages[0])
	m := a.compose
	m.to = "destination@example.com"
	for len(jobs) > 0 {
		runNextComposeJob(t, &jobs)
	}
	if !strings.Contains(m.attachments[0].err, "offline") {
		t.Fatalf("download failure state = %+v", m.attachments[0])
	}
	a.sendCompose()
	if service.sent.To != "" || !strings.Contains(m.err, "Remove attachments") {
		t.Fatalf("forward sent after a download failure: sent=%+v err=%q", service.sent, m.err)
	}
	if len(m.attachments) != 2 || string(m.attachments[1].attachment.Data) != "inline first" {
		t.Fatalf("download failure discarded attachment state: %+v", m.attachments)
	}
}

func TestForwardAttachmentRemovalWhileLoading(t *testing.T) {
	a, service := newIndividualMessageFixture()
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.openForwardMessage(a.thread.Messages[0])
	m := a.compose
	removedID := m.attachments[0].id
	a.removeComposeAttachment(removedID)
	for len(jobs) > 0 {
		runNextComposeJob(t, &jobs)
	}
	if len(m.attachments) != 1 || m.attachments[0].attachment.Name != "first-inline.txt" || string(m.attachments[0].attachment.Data) != "inline first" {
		t.Fatalf("late download restored a removed attachment: %+v", m.attachments)
	}
	m.to = "destination@example.com"
	a.schedule = func(fn func()) { fn() }
	a.sendCompose()
	if len(service.sent.Attachments) != 1 || string(service.sent.Attachments[0].Data) != "inline first" {
		t.Fatalf("sent attachments after removal = %+v", service.sent.Attachments)
	}
}

func TestCollapsedMessageActionsUseSelectedMessage(t *testing.T) {
	a, _ := newIndividualMessageFixture()
	a.thread.Messages[0].LabelIDs = nil
	tt := ui.NewTester(func(c *ui.Context) {
		a.messageView(c, 0, a.thread.Messages[0])
	}, 900, 300)
	target, ok := tt.Find("Message actions")
	if !ok {
		t.Fatalf("collapsed message actions not found: %v", tt.Texts())
	}
	x, y := target.X+target.W/2, target.Y+target.H/2
	tt.Press(x, y)
	if menu := tt.Menu(); len(menu) != 3 || menu[0] != "Reply" || menu[1] != "Reply all" || menu[2] != "Forward" {
		t.Fatalf("collapsed message menu = %v", menu)
	}
	tt.Release(x, y)
	if err := tt.ChooseMenuItem("Reply"); err != nil {
		t.Fatal(err)
	}
	if a.compose == nil || a.compose.inReplyTo != "<first-id@example.com>" {
		t.Fatalf("collapsed action used another message: %+v", a.compose)
	}
}

func TestThreadActionWrappersStillUseLatestMessage(t *testing.T) {
	a, _ := newIndividualMessageFixture()
	a.openReply()
	if a.compose.inReplyTo != "<third-id@example.com>" || a.compose.quote.ID != "message-third" {
		t.Fatalf("reply wrapper did not use the latest message: %+v", a.compose)
	}
	a.openReplyAllMessage(a.thread.Messages[1])
	a.openReplyAll()
	if a.compose.inReplyTo != "<third-id@example.com>" || !strings.Contains(a.compose.to, "third-replies@example.com") {
		t.Fatalf("reply-all wrapper did not use the latest message: %+v", a.compose)
	}
	a.openForward()
	if a.compose.subject != "Fwd: Third subject" || !strings.Contains(a.compose.doc.Text().PlainText(), "Third body") {
		t.Fatalf("forward wrapper did not use the latest message: %+v", a.compose)
	}
}

func newIndividualMessageFixture() (*App, *individualAttachmentService) {
	base := newFakeService()
	base.thread.Messages = []mailmodel.Message{
		{
			ID: "message-first", ThreadID: "thread-1", From: "First Sender <first@example.com>", ReplyTo: "First Replies <first-replies@example.com>",
			To: "demo@example.com, First To <first-to@example.com>", Cc: "First Cc <first-cc@example.com>", Subject: "First subject",
			MessageID: "<first-id@example.com>", References: "<root@example.com>", Date: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC),
			Text: "First body", LabelIDs: []string{mailmodel.Unread},
			Attachments: []mailmodel.Attachment{
				{ID: "first-normal", Name: "first-normal.bin", MIMEType: "application/octet-stream", Size: 5},
				{Name: "first-inline.txt", MIMEType: "text/plain", Size: 12, Data: []byte("inline first")},
			},
		},
		{
			ID: "message-second", ThreadID: "thread-1", From: "Second Sender <second@example.com>", ReplyTo: "Second Replies <second-replies@example.com>",
			To: "second-to@example.com", Subject: "Second subject", MessageID: "<second-id@example.com>", References: "<second-root@example.com>",
			Date: time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC), Text: "Second body",
			Attachments: []mailmodel.Attachment{{ID: "second-normal", Name: "second-normal.txt", MIMEType: "text/plain", Size: 6}},
		},
		{
			ID: "message-third", ThreadID: "thread-1", From: "Third Sender <third@example.com>", ReplyTo: "Third Replies <third-replies@example.com>",
			To: "third-to@example.com", Subject: "Third subject", MessageID: "<third-id@example.com>", References: "<third-root@example.com>",
			Date: time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC), Text: "Third body",
			Attachments: []mailmodel.Attachment{{ID: "third-normal", Name: "third-normal.txt", MIMEType: "text/plain", Size: 5}},
		},
	}
	service := &individualAttachmentService{
		fakeService: base,
		data: map[string][]byte{
			"message-first/first-normal":   {0, 1, '\r', '\n', 255},
			"message-second/second-normal": []byte("second"),
			"message-third/third-normal":   []byte("third"),
		},
		errs: make(map[string]error),
	}
	a := New(service)
	a.outbox = newFakeMailStore()
	a.schedule = func(fn func()) { fn() }
	a.restoreAttempted = true
	a.startupSelectionDone = true
	a.Start()
	a.selectThread("thread-1")
	return a, service
}

func clickIndividualMessageAction(t *testing.T, a *App, index int, label string) {
	t.Helper()
	a.setMessageExpanded(a.thread.Messages[index].ID, true)
	tt := ui.NewTester(func(c *ui.Context) {
		a.messageView(c, index, a.thread.Messages[index])
	}, 900, 700)
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
}
