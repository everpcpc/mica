package gmail

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestMakeMessageParsesReplyTo(t *testing.T) {
	message, err := makeMessage("thread-1", apiMessage{
		ID: "message-1",
		Payload: apiPart{Headers: []apiHeader{
			{Name: "From", Value: "Sender <sender@example.com>"},
			{Name: "Reply-To", Value: "Replies <replies@example.com>"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.ReplyTo != "Replies <replies@example.com>" {
		t.Fatalf("ReplyTo = %q, want replies@example.com", message.ReplyTo)
	}
}

func TestMakeMessageParsesInlineAttachmentData(t *testing.T) {
	payload := []byte{0, 1, '\r', '\n', 255}
	message, err := makeMessage("thread-1", apiMessage{
		ID: "message-1",
		Payload: apiPart{
			MIMEType: "multipart/mixed",
			Parts: []apiPart{
				{MIMEType: "text/plain", Body: apiBody{Data: base64.RawURLEncoding.EncodeToString([]byte("body"))}},
				{MIMEType: "application/octet-stream", Filename: "inline.bin", Body: apiBody{Data: base64.RawURLEncoding.EncodeToString(payload)}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.Text != "body" || len(message.Attachments) != 1 {
		t.Fatalf("parsed message = %+v", message)
	}
	attachment := message.Attachments[0]
	if attachment.ID != "" || attachment.Name != "inline.bin" || attachment.Size != int64(len(payload)) || !bytes.Equal(attachment.Data, payload) {
		t.Fatalf("inline attachment = %+v, want body.data bytes", attachment)
	}
}
