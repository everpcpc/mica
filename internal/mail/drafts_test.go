package mail

import (
	"context"
	"testing"
)

func TestDemoDraftLifecycle(t *testing.T) {
	ctx := context.Background()
	service := NewDemo()
	first, err := service.CreateDraft(ctx, Compose{})
	if err != nil || first.ID == "" || first.Message.Text != "" || first.Message.To != "" {
		t.Fatalf("empty draft = %+v, %v", first, err)
	}
	second, err := service.CreateDraft(ctx, Compose{Subject: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	original := Compose{
		To: "to@example.com", Bcc: "bcc@example.com", Subject: "Draft reply", Text: "Draft body",
		ThreadID: "t-launch", InReplyTo: "<selected@example.com>", References: "<root@example.com> <selected@example.com>", ThreadingResolved: true,
		Attachments: []OutgoingAttachment{{Name: "payload.bin", MIMEType: "application/octet-stream", Data: []byte{0, 1, '\r', '\n', 255}}},
	}
	third, err := service.CreateDraft(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.Drafts(ctx, Query{Limit: 2})
	if err != nil || len(page.Drafts) != 2 || page.NextPageToken == "" {
		t.Fatalf("first draft page = %+v, %v", page, err)
	}
	page.Drafts[0].Message.Subject = "mutated"
	next, err := service.Drafts(ctx, Query{Limit: 2, PageToken: page.NextPageToken})
	if err != nil || len(next.Drafts) != 1 {
		t.Fatalf("second draft page = %+v, %v", next, err)
	}
	restored, err := service.Draft(ctx, third.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Message.Subject == "mutated" || restored.Message.Bcc != original.Bcc || restored.Message.ThreadID != original.ThreadID || restored.Message.InReplyTo != original.InReplyTo || restored.Message.References != original.References {
		t.Fatalf("restored draft = %+v", restored)
	}
	if len(restored.Message.Attachments) != 1 {
		t.Fatalf("restored attachments = %+v", restored.Message.Attachments)
	}
	data, err := service.Attachment(ctx, restored.Message.ID, restored.Message.Attachments[0].ID)
	if err != nil || string(data) != string(original.Attachments[0].Data) {
		t.Fatalf("draft attachment = %v, %v", data, err)
	}
	data[0] = 'X'
	again, err := service.Attachment(ctx, restored.Message.ID, restored.Message.Attachments[0].ID)
	if err != nil || again[0] != 0 {
		t.Fatalf("draft attachment was aliased: %v, %v", again, err)
	}
	updated := original
	updated.Text = "Updated body"
	updated.Attachments[0].Data = []byte("updated attachment")
	if _, err := service.UpdateDraft(ctx, third.ID, updated); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteDraft(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Draft(ctx, second.ID); err == nil {
		t.Fatal("deleted draft still exists")
	}
	if err := service.SendDraft(ctx, third.ID, updated); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Draft(ctx, third.ID); err == nil {
		t.Fatal("sent draft remains in drafts")
	}
	drafts, err := service.Drafts(ctx, Query{Limit: 10})
	if err != nil || len(drafts.Drafts) != 1 || drafts.Drafts[0].ID != first.ID {
		t.Fatalf("drafts after delete and send = %+v, %v", drafts, err)
	}
	thread, err := service.Thread(ctx, "t-launch")
	if err != nil {
		t.Fatal(err)
	}
	sent := thread.Messages[len(thread.Messages)-1]
	if sent.Text != "Updated body" || sent.Bcc != "bcc@example.com" || sent.InReplyTo != "<selected@example.com>" || !hasString(sent.LabelIDs, Sent) || hasString(sent.LabelIDs, Draft) {
		t.Fatalf("sent draft message = %+v", sent)
	}
	if len(sent.Attachments) != 1 {
		t.Fatalf("sent draft attachments = %+v", sent.Attachments)
	}
	data, err = service.Attachment(ctx, sent.ID, sent.Attachments[0].ID)
	if err != nil || string(data) != "updated attachment" {
		t.Fatalf("sent draft attachment = %q, %v", data, err)
	}
}

func TestDemoDraftSendValidation(t *testing.T) {
	service := NewDemo()
	draft, err := service.CreateDraft(context.Background(), Compose{})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SendDraft(context.Background(), draft.ID, Compose{}); err == nil {
		t.Fatal("empty draft was sent")
	}
	if _, err := service.Draft(context.Background(), draft.ID); err != nil {
		t.Fatalf("invalid send removed the draft: %v", err)
	}
}
