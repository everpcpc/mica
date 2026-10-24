package mail

import (
	"context"
	"testing"
)

func TestDemoPreservesMessageIDAndFindsSentMessage(t *testing.T) {
	service := NewDemo()
	ctx := context.Background()
	compose := Compose{To: "to@example.com", Subject: "Outgoing", Text: "body", MessageID: "<stable@example.com>"}
	if err := service.Send(ctx, compose); err != nil {
		t.Fatal(err)
	}
	found, err := service.SentMessageExists(ctx, compose.MessageID)
	if err != nil || !found {
		t.Fatalf("SentMessageExists = %v, %v", found, err)
	}
	missing, err := service.SentMessageExists(ctx, "<missing@example.com>")
	if err != nil || missing {
		t.Fatalf("missing SentMessageExists = %v, %v", missing, err)
	}
	threads, err := service.ListThreads(ctx, Query{LabelID: Sent, Search: "Outgoing"})
	if err != nil || len(threads) != 1 {
		t.Fatalf("sent threads = %+v err=%v", threads, err)
	}
	thread, err := service.Thread(ctx, threads[0].ID)
	if err != nil || len(thread.Messages) != 1 || thread.Messages[0].MessageID != compose.MessageID {
		t.Fatalf("sent Message-ID = %+v err=%v", thread.Messages, err)
	}
}
