package gmail

import "testing"

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
