package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mailmodel "mica/internal/mail"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
)

func TestComposeChooseMultipleAttachmentsAndRemove(t *testing.T) {
	first := writeComposeAttachment(t, "alpha.txt", []byte("alpha"))
	second := writeComposeAttachment(t, "报告.bin", []byte{0, 1, 2})
	service := newFakeService()
	a := newTestApp(service)
	a.openCompose(nil)
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.openDialog = func(opts mygo.OpenDialogOptions) ([]string, error) {
		if !opts.Multiple {
			t.Error("attachment dialog is not multi-select")
		}
		return []string{first, second}, nil
	}
	tt := ui.NewTester(a.View, 1280, 900)
	if err := tt.Click("Attach files"); err != nil {
		t.Fatal(err)
	}
	runNextComposeJob(t, &jobs)
	tt.Frame()
	for _, name := range []string{"alpha.txt", "报告.bin", "Reading…"} {
		if !tt.HasText(name) {
			t.Fatalf("loading attachments do not show %q: %v", name, tt.Texts())
		}
	}
	runNextComposeJob(t, &jobs)
	runNextComposeJob(t, &jobs)
	tt.Frame()
	if tt.HasText("Reading…") || !tt.HasText("5 B") || !tt.HasText("3 B") {
		t.Fatalf("loaded attachment state is wrong: %v", tt.Texts())
	}
	if len(a.compose.attachments) != 2 {
		t.Fatalf("attachments = %+v, want 2", a.compose.attachments)
	}
	if got := a.compose.attachments[0].attachment.MIMEType; got != "text/plain; charset=utf-8" {
		t.Fatalf("text MIME type = %q", got)
	}
	if got := a.compose.attachments[1].attachment.MIMEType; got != "application/octet-stream" {
		t.Fatalf("binary MIME type = %q", got)
	}
	if err := tt.Click("Remove alpha.txt"); err != nil {
		t.Fatal(err)
	}
	if len(a.compose.attachments) != 1 || a.compose.attachments[0].attachment.Name != "报告.bin" {
		t.Fatalf("attachments after removal = %+v", a.compose.attachments)
	}
	if tt.HasText("alpha.txt") {
		t.Fatal("removed attachment is still rendered")
	}
}

func TestComposeDropFiles(t *testing.T) {
	first := writeComposeAttachment(t, "first.txt", []byte("first"))
	second := writeComposeAttachment(t, "second.txt", []byte("second"))
	a := newTestApp(newFakeService())
	a.openCompose(nil)
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	tt := ui.NewTester(a.View, 1280, 900)
	target, ok := tt.Find("Attach files")
	if !ok {
		t.Fatal("attachment drop target is missing")
	}
	if !tt.DropFiles(target.X+target.W/2, target.Y+target.H/2, first, second) {
		t.Fatal("composer did not accept dropped files")
	}
	if len(a.compose.attachments) != 2 || len(jobs) != 1 {
		t.Fatalf("dropped attachments = %+v, jobs = %d", a.compose.attachments, len(jobs))
	}
	runNextComposeJob(t, &jobs)
	runNextComposeJob(t, &jobs)
	tt.Frame()
	for _, want := range []string{"first.txt", "second.txt", "5 B", "6 B"} {
		if !tt.HasText(want) {
			t.Fatalf("dropped attachments do not show %q: %v", want, tt.Texts())
		}
	}
}

func TestComposeDropFilesWhileSendingShowsError(t *testing.T) {
	path := writeComposeAttachment(t, "during-send.txt", []byte("during send"))
	a := newTestApp(newFakeService())
	a.openCompose(nil)
	a.compose.sending = true
	tt := ui.NewTester(a.View, 1280, 900)
	target, ok := tt.Find("Attach files")
	if !ok {
		t.Fatal("attachment drop target is missing")
	}
	if !tt.DropFiles(target.X+target.W/2, target.Y+target.H/2, path) {
		t.Fatal("composer did not report the dropped files")
	}
	if len(a.compose.attachments) != 0 {
		t.Fatal("attachments changed while sending")
	}
	if !tt.HasText("Wait for the current send to finish before adding attachments") {
		t.Fatalf("sending drop was silently discarded: %v", tt.Texts())
	}
}

func TestComposeDropFilesWhileDiscardShowsError(t *testing.T) {
	path := writeComposeAttachment(t, "during-discard.txt", []byte("during discard"))
	a := newTestApp(newFakeService())
	a.openCompose(nil)
	a.compose.pendingAction = composerActionDiscard
	tt := ui.NewTester(a.View, 1280, 900)
	target, ok := tt.Find("Attach files")
	if !ok {
		t.Fatal("attachment drop target is missing")
	}
	if !tt.DropFiles(target.X+target.W/2, target.Y+target.H/2, path) {
		t.Fatal("composer did not report the dropped files")
	}
	if len(a.compose.attachments) != 0 {
		t.Fatal("attachments changed while discard was pending")
	}
	if !tt.HasText("Wait for the current action to finish before adding attachments") {
		t.Fatalf("discard drop was silently discarded: %v", tt.Texts())
	}
}

func TestComposeBlocksSendUntilAttachmentRead(t *testing.T) {
	path := writeComposeAttachment(t, "report.txt", []byte("report"))
	service := newFakeService()
	a := newTestApp(service)
	a.openCompose(nil)
	a.compose.to = "ada@example.com"
	a.compose.doc.SetText(richtext.NewText("Body"))
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.addComposeAttachments([]string{path})
	a.sendCompose()
	if service.sent.To != "" || !strings.Contains(a.compose.err, "Wait for attachments") {
		t.Fatalf("send while reading = %+v, err = %q", service.sent, a.compose.err)
	}
	runNextComposeJob(t, &jobs)
	a.schedule = func(fn func()) { fn() }
	a.sendCompose()
	if a.compose != nil {
		t.Fatal("composer stayed open after sending a loaded attachment")
	}
	if len(service.sent.Attachments) != 1 || string(service.sent.Attachments[0].Data) != "report" {
		t.Fatalf("sent attachments = %+v", service.sent.Attachments)
	}
}

func TestComposeReadFailureBlocksSend(t *testing.T) {
	service := newFakeService()
	a := newTestApp(service)
	a.openCompose(nil)
	a.compose.to = "ada@example.com"
	a.compose.doc.SetText(richtext.NewText("Keep this body"))
	a.addComposeAttachments([]string{filepath.Join(t.TempDir(), "missing.txt")})
	tt := ui.NewTester(a.View, 1280, 900)
	if !tt.HasText("missing.txt") || !tt.HasText("Couldn't read attachment") {
		t.Fatalf("read failure is not visible: %v", tt.Texts())
	}
	a.sendCompose()
	if service.sent.To != "" || !strings.Contains(a.compose.err, "Remove attachments") {
		t.Fatalf("send after read failure = %+v, err = %q", service.sent, a.compose.err)
	}
	if got := a.compose.doc.Text().PlainText(); got != "Keep this body" || len(a.compose.attachments) != 1 {
		t.Fatalf("failed attachment send changed composer state: %q, %+v", got, a.compose.attachments)
	}
}

func TestComposeLateAttachmentResultDoesNotOverwriteNewState(t *testing.T) {
	oldPath := writeComposeAttachment(t, "old.txt", []byte("old"))
	newPath := writeComposeAttachment(t, "new.txt", []byte("new"))
	a := newTestApp(newFakeService())
	a.openCompose(nil)
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.addComposeAttachments([]string{oldPath})
	oldID := a.compose.attachments[0].id
	a.removeComposeAttachment(oldID)
	a.addComposeAttachments([]string{newPath})
	newID := a.compose.attachments[0].id
	runNextComposeJob(t, &jobs)
	if len(a.compose.attachments) != 1 || a.compose.attachments[0].id != newID || !a.compose.attachments[0].loading {
		t.Fatalf("late result changed the replacement: %+v", a.compose.attachments)
	}
	runNextComposeJob(t, &jobs)
	if len(a.compose.attachments) != 1 || a.compose.attachments[0].attachment.Name != "new.txt" || string(a.compose.attachments[0].attachment.Data) != "new" {
		t.Fatalf("replacement attachment = %+v", a.compose.attachments)
	}
}

func TestComposeAttachmentReadsAreSerialized(t *testing.T) {
	first := writeComposeAttachment(t, "first.txt", []byte("first"))
	second := writeComposeAttachment(t, "second.txt", []byte("second"))
	a := newTestApp(newFakeService())
	a.openCompose(nil)
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.addComposeAttachments([]string{first})
	a.addComposeAttachments([]string{second})
	if len(jobs) != 1 {
		t.Fatalf("two batches scheduled %d readers, want 1", len(jobs))
	}
	runNextComposeJob(t, &jobs)
	if len(jobs) != 1 {
		t.Fatalf("next attachment was not scheduled after the first read: %d jobs", len(jobs))
	}
	runNextComposeJob(t, &jobs)
	for i, item := range a.compose.attachments {
		if item.loading || item.err != "" {
			t.Fatalf("attachment %d did not finish: %+v", i, item)
		}
	}
}

func TestComposeRemovedAttachmentDoesNotConsumeAllowance(t *testing.T) {
	large := writeComposeAttachment(t, "large.bin", make([]byte, 25<<20-1))
	small := writeComposeAttachment(t, "small.txt", []byte("ok"))
	a := newTestApp(newFakeService())
	a.openCompose(nil)
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.addComposeAttachments([]string{large, small})
	removedID := a.compose.attachments[0].id
	a.removeComposeAttachment(removedID)
	for len(jobs) > 0 {
		runNextComposeJob(t, &jobs)
	}
	if len(a.compose.attachments) != 1 {
		t.Fatalf("attachments = %+v, want only small.txt", a.compose.attachments)
	}
	item := a.compose.attachments[0]
	if item.err != "" || string(item.attachment.Data) != "ok" {
		t.Fatalf("removed attachment consumed the remaining allowance: %+v", item)
	}
}

func TestComposeRemovingLoadedAttachmentRetriesRead(t *testing.T) {
	path := writeComposeAttachment(t, "six.bin", make([]byte, 6<<20))
	a := newTestApp(newFakeService())
	a.openCompose(nil)
	m := a.compose
	m.attachments = []composerAttachment{{id: 1, attachment: mailmodel.OutgoingAttachment{Name: "loaded.bin", Data: make([]byte, 20<<20)}}}
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.addComposeAttachments([]string{path})
	a.removeComposeAttachment(1)
	for len(jobs) > 0 {
		runNextComposeJob(t, &jobs)
	}
	if len(m.attachments) != 1 {
		t.Fatalf("attachments = %+v, want only the new file", m.attachments)
	}
	item := m.attachments[0]
	if item.err != "" || len(item.attachment.Data) != 6<<20 {
		t.Fatalf("removing a loaded attachment did not release its allowance: %+v", item)
	}
}

func TestComposeSendFailurePreservesAttachments(t *testing.T) {
	path := writeComposeAttachment(t, "binary.bin", []byte{0, 1, '\r', '\n', 255})
	service := newFakeService()
	service.sendErr = errors.New("offline")
	a := newTestApp(service)
	a.openCompose(&mailmodel.Message{From: "Ada <ada@example.com>", ThreadID: "thread-reply", MessageID: "<reply@example.com>"})
	a.compose.doc.SetText(richtext.NewText("Body"))
	a.addComposeAttachments([]string{path})
	a.sendCompose()
	if a.compose == nil || len(a.compose.attachments) != 1 || !bytes.Equal(a.compose.attachments[0].attachment.Data, []byte{0, 1, '\r', '\n', 255}) {
		t.Fatalf("failed send lost attachments: %+v", a.compose)
	}
	if got := a.compose.doc.Text().PlainText(); got != "Body" {
		t.Fatalf("failed send changed body: %q", got)
	}
	service.sendErr = nil
	a.sendCompose()
	if service.sent.ThreadID != "thread-reply" || len(service.sent.Attachments) != 1 || !bytes.Equal(service.sent.Attachments[0].Data, []byte{0, 1, '\r', '\n', 255}) {
		t.Fatalf("reply retry silently dropped attachments: %+v", service.sent)
	}
}

func TestComposeAttachmentSizeLimit(t *testing.T) {
	t.Run("file above 25 MiB", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "large.bin")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, 25<<20+1); err != nil {
			t.Fatal(err)
		}
		_, err := readOutgoingAttachment(path)
		if !errors.Is(err, errComposeAttachmentTooLarge) {
			t.Fatalf("oversize file error = %v", err)
		}
	})
	t.Run("remaining batch allowance", func(t *testing.T) {
		path := writeComposeAttachment(t, "five.bin", []byte{1, 2, 3, 4, 5})
		if _, err := readOutgoingAttachmentLimit(path, 4); !errors.Is(err, errComposeAttachmentTooLarge) {
			t.Fatalf("read above the remaining allowance: %v", err)
		}
		attachment, err := readOutgoingAttachmentLimit(path, 5)
		if err != nil || len(attachment.Data) != 5 {
			t.Fatalf("read at the remaining allowance = %d bytes, %v", len(attachment.Data), err)
		}
	})
	t.Run("total above 25 MiB", func(t *testing.T) {
		service := newFakeService()
		a := newTestApp(service)
		a.openCompose(nil)
		a.compose.to = "ada@example.com"
		a.compose.doc.SetText(richtext.NewText("Body"))
		m := a.compose
		m.attachments = []composerAttachment{
			{id: 1, attachment: mailmodel.OutgoingAttachment{Name: "large.bin", Data: make([]byte, 25<<20)}},
			{id: 2, attachment: mailmodel.OutgoingAttachment{Name: "extra.bin"}, loading: true},
		}
		a.finishComposeAttachment(m, 2, mailmodel.OutgoingAttachment{Name: "extra.bin", Data: []byte{1}}, nil)
		if !strings.Contains(m.attachments[1].err, "25 MB") || len(m.attachments[1].attachment.Data) != 0 {
			t.Fatalf("total size result = %+v", m.attachments[1])
		}
		a.sendCompose()
		if service.sent.To != "" {
			t.Fatal("message above the total attachment limit was sent")
		}
	})
}

func TestOutgoingMIMEType(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		data []byte
		want string
	}{
		{name: "extension first", path: "document.pdf", data: []byte("plain text"), want: "application/pdf"},
		{name: "content sniff", path: "README.mica-unknown", data: []byte("plain text"), want: "text/plain; charset=utf-8"},
		{name: "binary fallback", path: "payload.mica-unknown", data: []byte{0, 1, 2}, want: "application/octet-stream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := outgoingMIMEType(tc.path, tc.data); got != tc.want {
				t.Fatalf("MIME type = %q, want %q", got, tc.want)
			}
		})
	}
}

func writeComposeAttachment(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runNextComposeJob(t *testing.T, jobs *[]func()) {
	t.Helper()
	if len(*jobs) == 0 {
		t.Fatal("no background job scheduled")
	}
	job := (*jobs)[0]
	*jobs = (*jobs)[1:]
	job()
}
