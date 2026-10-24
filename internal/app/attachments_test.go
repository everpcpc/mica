package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mica/internal/mail"

	"github.com/egoist/mygo"
)

type fakeAttachmentService struct {
	*fakeService
	data         []byte
	err          error
	messageID    string
	attachmentID string
}

func (s *fakeAttachmentService) Attachment(_ context.Context, messageID, attachmentID string) ([]byte, error) {
	s.messageID, s.attachmentID = messageID, attachmentID
	return s.data, s.err
}

func TestSafeAttachmentName(t *testing.T) {
	for _, test := range []struct {
		name string
		in   string
		want string
	}{
		{name: "unix traversal", in: "../../etc/passwd", want: "passwd"},
		{name: "windows traversal", in: `..\..\evil.pdf`, want: "evil.pdf"},
		{name: "control characters", in: "rep\x00ort\n.pdf", want: "report.pdf"},
		{name: "empty", in: "  ", want: "attachment"},
		{name: "parent", in: "..", want: "attachment"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := safeAttachmentName(test.in); got != test.want {
				t.Fatalf("safeAttachmentName(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}

func TestWriteAttachmentTemp(t *testing.T) {
	path, err := writeAttachmentTemp(mail.Attachment{Name: "report.pdf"}, []byte("pdf-data"))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if filepath.Ext(path) != ".pdf" {
		t.Fatalf("temporary file = %q, want .pdf extension", path)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "pdf-data" {
		t.Fatalf("temporary content = %q, err=%v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("temporary file mode = %v, err=%v", info.Mode(), err)
	}
}

func TestDownloadAttachmentOpen(t *testing.T) {
	service := &fakeAttachmentService{fakeService: newFakeService(), data: []byte("pdf-data")}
	a := New(service)
	a.schedule = func(fn func()) { fn() }
	var opened string
	a.openPath = func(path string) error {
		opened = path
		return nil
	}
	a.downloadAttachment(mail.Message{ID: "message-1"}, mail.Attachment{ID: "attachment-1", Name: "report.pdf"}, false, 0)
	defer os.Remove(opened)
	if opened == "" {
		t.Fatal("attachment was not opened")
	}
	data, err := os.ReadFile(opened)
	if err != nil || string(data) != "pdf-data" {
		t.Fatalf("opened content = %q, err=%v", data, err)
	}
	if service.messageID != "message-1" || service.attachmentID != "attachment-1" {
		t.Fatalf("downloaded IDs = %q, %q", service.messageID, service.attachmentID)
	}
}

func TestDownloadInlineAttachmentWithoutFetcher(t *testing.T) {
	a := New(newFakeService())
	a.schedule = func(fn func()) { fn() }
	var opened string
	a.openPath = func(path string) error {
		opened = path
		return nil
	}
	a.downloadAttachment(mail.Message{ID: "message-inline"}, mail.Attachment{Name: "inline.txt", Data: []byte("inline data")}, false, 0)
	defer os.Remove(opened)
	if opened == "" {
		t.Fatal("inline attachment was not opened")
	}
	data, err := os.ReadFile(opened)
	if err != nil || string(data) != "inline data" {
		t.Fatalf("inline content = %q, err=%v", data, err)
	}
}

func TestInlineAttachmentsHaveIndependentDownloadState(t *testing.T) {
	a := New(newFakeService())
	a.schedule = func(fn func()) { fn() }
	var opened []string
	a.openPath = func(path string) error {
		opened = append(opened, path)
		return nil
	}
	defer func() {
		for _, path := range opened {
			_ = os.Remove(path)
		}
	}()
	message := mail.Message{ID: "message-inline", Attachments: []mail.Attachment{
		{Name: "first.txt", Data: []byte("first")},
		{Name: "second.txt", Data: []byte("second")},
	}}
	a.downloadAttachment(message, message.Attachments[0], false, 0)
	a.downloadAttachment(message, message.Attachments[1], false, 1)
	if len(a.attachmentDownloads) != 2 {
		t.Fatalf("inline attachments share download state: %+v", a.attachmentDownloads)
	}
}

func TestDownloadAttachmentSave(t *testing.T) {
	service := &fakeAttachmentService{fakeService: newFakeService(), data: []byte("saved-data")}
	a := New(service)
	a.schedule = func(fn func()) { fn() }
	path := filepath.Join(t.TempDir(), "saved.pdf")
	a.saveDialog = func(opts mygo.SaveDialogOptions) (string, error) {
		if opts.DefaultPath != "report.pdf" {
			t.Fatalf("default path = %q, want sanitized report.pdf", opts.DefaultPath)
		}
		return path, nil
	}
	a.downloadAttachment(mail.Message{ID: "message-1"}, mail.Attachment{ID: "attachment-1", Name: "../../report.pdf"}, true, 0)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "saved-data" {
		t.Fatalf("saved content = %q, err=%v", data, err)
	}
	state := a.attachmentDownloads[attachmentKey("message-1", "attachment-1", 0)]
	if state.loading || state.err != "" {
		t.Fatalf("download state = %+v, want success", state)
	}
}

func TestDownloadAttachmentFailure(t *testing.T) {
	service := &fakeAttachmentService{fakeService: newFakeService(), err: errors.New("offline")}
	a := New(service)
	a.schedule = func(fn func()) { fn() }
	a.downloadAttachment(mail.Message{ID: "message-1"}, mail.Attachment{ID: "attachment-1", Name: "report.pdf"}, false, 0)
	state := a.attachmentDownloads[attachmentKey("message-1", "attachment-1", 0)]
	if state.loading || !strings.Contains(state.err, "offline") {
		t.Fatalf("download state = %+v, want offline error", state)
	}
}
