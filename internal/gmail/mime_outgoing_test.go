package gmail

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	stdmail "net/mail"
	"strings"
	"testing"

	"mica/internal/mail"
)

func TestBuildRFC5322Attachments(t *testing.T) {
	binary := append([]byte{0, 1, 2, '\r', '\n', 0, 255}, bytes.Repeat([]byte{0, 3, '\r', '\n', 254}, 40)...)
	raw, err := buildRFC5322(mail.Compose{
		To:      "maya@example.com",
		Subject: "Attachments",
		Text:    "Plain body\n第二行",
		HTML:    "<p>HTML body</p>",
		Attachments: []mail.OutgoingAttachment{
			{Name: "报告.bin", MIMEType: "application/octet-stream", Data: binary},
			{Name: "notes.txt", MIMEType: "text/plain; charset=utf-8", Data: []byte("first\r\nsecond\x00third")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	message := readRFC5322(t, raw)
	mediaType, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/mixed" {
		t.Fatalf("root Content-Type = %q, %v", message.Header.Get("Content-Type"), err)
	}
	mixed := multipart.NewReader(message.Body, params["boundary"])
	body, err := mixed.NextRawPart()
	if err != nil {
		t.Fatal(err)
	}
	bodyType, bodyParams, err := mime.ParseMediaType(body.Header.Get("Content-Type"))
	if err != nil || bodyType != "multipart/alternative" {
		t.Fatalf("body Content-Type = %q, %v", body.Header.Get("Content-Type"), err)
	}
	alternative := multipart.NewReader(body, bodyParams["boundary"])
	for i, want := range []string{"Plain body\r\n第二行", "<p>HTML body</p>"} {
		part, err := alternative.NextRawPart()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(quotedprintable.NewReader(part))
		if err != nil || string(content) != want {
			t.Fatalf("alternative part %d = %q, %v; want %q", i, content, err, want)
		}
	}
	if _, err := alternative.NextRawPart(); !errors.Is(err, io.EOF) {
		t.Fatalf("alternative has extra parts: %v", err)
	}

	for i, want := range []struct {
		name     string
		mimeType string
		data     []byte
	}{
		{name: "报告.bin", mimeType: "application/octet-stream", data: binary},
		{name: "notes.txt", mimeType: "text/plain", data: []byte("first\r\nsecond\x00third")},
	} {
		part, err := mixed.NextRawPart()
		if err != nil {
			t.Fatal(err)
		}
		if got := part.FileName(); got != want.name {
			t.Errorf("attachment %d filename = %q, want %q", i, got, want.name)
		}
		contentType, contentParams, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err != nil || contentType != want.mimeType || contentParams["name"] != want.name {
			t.Errorf("attachment %d Content-Type = %q, %v", i, part.Header.Get("Content-Type"), err)
		}
		if i == 0 && !strings.Contains(part.Header.Get("Content-Disposition"), "filename*=") {
			t.Errorf("non-ASCII filename is not RFC encoded: %q", part.Header.Get("Content-Disposition"))
		}
		if got := part.Header.Get("Content-Transfer-Encoding"); got != "base64" {
			t.Errorf("attachment %d transfer encoding = %q", i, got)
		}
		encoded, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		longest := 0
		for _, line := range bytes.Split(encoded, []byte("\r\n")) {
			longest = max(longest, len(line))
			if len(line) > 76 {
				t.Errorf("attachment %d base64 line has %d columns", i, len(line))
			}
		}
		if len(want.data) > 57 && longest != 76 {
			t.Errorf("attachment %d longest base64 line = %d, want 76", i, longest)
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(encoded), "\r\n", ""))
		if err != nil || !bytes.Equal(decoded, want.data) {
			t.Fatalf("attachment %d decoded content differs: %v", i, err)
		}
	}
	if _, err := mixed.NextRawPart(); !errors.Is(err, io.EOF) {
		t.Fatalf("mixed message has extra parts: %v", err)
	}
}

func TestBuildRFC5322AttachmentWithSingleBody(t *testing.T) {
	for _, tc := range []struct {
		name     string
		compose  mail.Compose
		wantType string
		wantBody string
	}{
		{name: "plain", compose: mail.Compose{Text: "plain"}, wantType: "text/plain", wantBody: "plain"},
		{name: "html", compose: mail.Compose{HTML: "<p>html</p>"}, wantType: "text/html", wantBody: "<p>html</p>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.compose.To = "maya@example.com"
			tc.compose.Attachments = []mail.OutgoingAttachment{{Name: "empty.bin", MIMEType: "application/octet-stream"}}
			raw, err := buildRFC5322(tc.compose)
			if err != nil {
				t.Fatal(err)
			}
			message := readRFC5322(t, raw)
			_, params, _ := mime.ParseMediaType(message.Header.Get("Content-Type"))
			mixed := multipart.NewReader(message.Body, params["boundary"])
			body, err := mixed.NextRawPart()
			if err != nil {
				t.Fatal(err)
			}
			mediaType, _, _ := mime.ParseMediaType(body.Header.Get("Content-Type"))
			content, err := io.ReadAll(quotedprintable.NewReader(body))
			if err != nil || mediaType != tc.wantType || string(content) != tc.wantBody {
				t.Fatalf("body = %q %q, %v", mediaType, content, err)
			}
			attachment, err := mixed.NextRawPart()
			if err != nil {
				t.Fatal(err)
			}
			content, err = io.ReadAll(attachment)
			if err != nil || len(content) != 0 {
				t.Fatalf("empty attachment = %q, %v", content, err)
			}
		})
	}
}

func TestBuildRFC5322WithoutAttachmentsKeepsBodyStructure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		compose  mail.Compose
		wantType string
	}{
		{name: "plain", compose: mail.Compose{Text: "plain"}, wantType: "text/plain"},
		{name: "html", compose: mail.Compose{HTML: "<p>html</p>"}, wantType: "text/html"},
		{name: "alternative", compose: mail.Compose{Text: "plain", HTML: "<p>html</p>"}, wantType: "multipart/alternative"},
		{name: "empty attachment slice", compose: mail.Compose{Text: "plain", Attachments: []mail.OutgoingAttachment{}}, wantType: "text/plain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.compose.To = "maya@example.com"
			raw, err := buildRFC5322(tc.compose)
			if err != nil {
				t.Fatal(err)
			}
			message := readRFC5322(t, raw)
			mediaType, _, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
			if err != nil || mediaType != tc.wantType {
				t.Fatalf("Content-Type = %q, %v; want %s", message.Header.Get("Content-Type"), err, tc.wantType)
			}
		})
	}
}

func TestBuildRFC5322RejectsInvalidAttachments(t *testing.T) {
	for _, tc := range []struct {
		name       string
		attachment mail.OutgoingAttachment
	}{
		{name: "empty name", attachment: mail.OutgoingAttachment{MIMEType: "application/octet-stream"}},
		{name: "header injection", attachment: mail.OutgoingAttachment{Name: "report.pdf\r\nX-Injected: yes", MIMEType: "application/pdf"}},
		{name: "control character", attachment: mail.OutgoingAttachment{Name: "report\x00.pdf", MIMEType: "application/pdf"}},
		{name: "invalid MIME", attachment: mail.OutgoingAttachment{Name: "report.pdf", MIMEType: "not a MIME type"}},
		{name: "MIME header injection", attachment: mail.OutgoingAttachment{Name: "report.pdf", MIMEType: "application/pdf\r\nX-Injected: yes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := buildRFC5322(mail.Compose{To: "maya@example.com", Text: "body", Attachments: []mail.OutgoingAttachment{tc.attachment}})
			if err == nil || raw != nil {
				t.Fatalf("invalid attachment was accepted: raw=%q err=%v", raw, err)
			}
		})
	}
}

func TestBuildRFC5322AttachmentSizeLimit(t *testing.T) {
	const limit = 25 << 20
	attachments := []mail.OutgoingAttachment{
		{Name: "first.bin", MIMEType: "application/octet-stream", Data: make([]byte, limit-1)},
		{Name: "second.bin", MIMEType: "application/octet-stream", Data: []byte{1}},
	}
	raw, err := buildRFC5322(mail.Compose{To: "maya@example.com", Text: "body", Attachments: attachments})
	if err != nil || len(raw) == 0 {
		t.Fatalf("25 MiB of attachments was rejected: %v", err)
	}
	attachments[1].Data = []byte{1, 2}
	raw, err = buildRFC5322(mail.Compose{To: "maya@example.com", Text: "body", Attachments: attachments})
	if err == nil || raw != nil {
		t.Fatal("attachments above 25 MiB were accepted")
	}
}

func readRFC5322(t *testing.T, raw []byte) *stdmail.Message {
	t.Helper()
	message, err := stdmail.ReadMessage(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		t.Fatalf("invalid RFC 5322 message: %v", err)
	}
	return message
}
