package mail

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestDemoLabelsListAndSearch(t *testing.T) {
	ctx := context.Background()
	service := NewDemo()

	account, err := service.Account(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !account.Demo || account.Email != "alex@mica.example" {
		t.Fatalf("unexpected account: %+v", account)
	}
	labels, err := service.Labels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) < 9 {
		t.Fatalf("got %d labels, want at least 9", len(labels))
	}
	var inbox Label
	for _, label := range labels {
		if label.ID == Inbox {
			inbox = label
		}
	}
	if inbox.Messages == 0 || inbox.Unread == 0 {
		t.Fatalf("inbox counts are not populated: %+v", inbox)
	}

	threads, err := service.ListThreads(ctx, Query{LabelID: Inbox, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 3 {
		t.Fatalf("got %d threads, want 3", len(threads))
	}
	for i, thread := range threads {
		if !hasString(thread.LabelIDs, Inbox) {
			t.Errorf("thread %q has no inbox label", thread.ID)
		}
		if i > 0 && threads[i-1].Updated.Before(thread.Updated) {
			t.Error("threads are not sorted by update time")
		}
	}
	threads[0].LabelIDs[0] = "changed"
	again, err := service.ListThreads(ctx, Query{LabelID: Inbox, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if again[0].LabelIDs[0] == "changed" {
		t.Fatal("caller mutated stored labels")
	}

	threads, err = service.ListThreads(ctx, Query{Search: `subject:"offline sync"`})
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].ID != "t-sync" || threads[0].MessageCount != 3 {
		t.Fatalf("unexpected subject search result: %+v", threads)
	}
	threads, err = service.ListThreads(ctx, Query{Search: "is:unread launch"})
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].ID != "t-launch" || !threads[0].Unread {
		t.Fatalf("unexpected unread search result: %+v", threads)
	}
	threads, err = service.ListThreads(ctx, Query{Search: "from:leo has:attachment"})
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].ID != "t-launch" {
		t.Fatalf("unexpected attachment search result: %+v", threads)
	}

	thread, err := service.Thread(ctx, "t-launch")
	if err != nil {
		t.Fatal(err)
	}
	if len(thread.Messages) != 4 || len(thread.Messages[1].Attachments) != 2 {
		t.Fatalf("unexpected launch thread: %+v", thread)
	}
	if thread.Messages[0].Text == "" || thread.Messages[0].HTML == "" {
		t.Fatal("seed message does not contain plain text and HTML")
	}
	if thread.Messages[3].References == "" || thread.Messages[3].MessageID == "" {
		t.Fatal("seed message does not contain threading headers")
	}
}

func TestDemoListThreadPage(t *testing.T) {
	service := NewDemo()
	first, err := service.ListThreadPage(context.Background(), Query{LabelID: Inbox, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Threads) != 2 || first.NextPageToken != "2" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := service.ListThreadPage(context.Background(), Query{LabelID: Inbox, Limit: 100, PageToken: first.NextPageToken})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Threads) == 0 || second.NextPageToken != "" {
		t.Fatalf("last page = %+v", second)
	}
	if first.Threads[0].ID == second.Threads[0].ID {
		t.Fatal("second page repeated the first thread")
	}
}

func TestDemoCreateAndDeleteLabel(t *testing.T) {
	ctx := context.Background()
	service := NewDemo()
	label, err := service.CreateLabel(ctx, "Receipts")
	if err != nil {
		t.Fatal(err)
	}
	if label.ID == "" || label.Name != "Receipts" {
		t.Fatalf("created label = %+v", label)
	}
	if err := service.ModifyThread(ctx, "t-launch", []string{label.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteLabel(ctx, label.ID); err != nil {
		t.Fatal(err)
	}
	thread, err := service.Thread(ctx, "t-launch")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range thread.Messages {
		if hasString(message.LabelIDs, label.ID) {
			t.Fatalf("deleted label remains on a message: %v", message.LabelIDs)
		}
	}
	labels, err := service.Labels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range labels {
		if candidate.ID == label.ID {
			t.Fatal("deleted label remains in the demo label list")
		}
	}
	other := NewDemo()
	otherLabels, err := other.Labels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range otherLabels {
		if candidate.ID == label.ID {
			t.Fatal("label creation leaked into another demo instance")
		}
	}
}

func TestDemoModifyTrashAndSend(t *testing.T) {
	ctx := context.Background()
	service := NewDemo()

	if err := service.ModifyThread(ctx, "t-sync", []string{Starred}, []string{Unread}); err != nil {
		t.Fatal(err)
	}
	thread, err := service.Thread(ctx, "t-sync")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range thread.Messages {
		if hasString(message.LabelIDs, Unread) || !hasString(message.LabelIDs, Starred) {
			t.Fatalf("labels were not applied to every message: %+v", message.LabelIDs)
		}
	}
	threads, err := service.ListThreads(ctx, Query{LabelID: Starred, Search: "subject:offline"})
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].Unread {
		t.Fatalf("modify did not affect list results: %+v", threads)
	}

	if err := service.TrashThread(ctx, "t-sync"); err != nil {
		t.Fatal(err)
	}
	threads, err = service.ListThreads(ctx, Query{LabelID: Inbox, Search: "subject:offline"})
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 0 {
		t.Fatalf("trashed thread remains in inbox: %+v", threads)
	}
	threads, err = service.ListThreads(ctx, Query{LabelID: Trash, Search: "subject:offline"})
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].ID != "t-sync" {
		t.Fatalf("trashed thread is not searchable in trash: %+v", threads)
	}

	before, err := service.Thread(ctx, "t-launch")
	if err != nil {
		t.Fatal(err)
	}
	last := before.Messages[len(before.Messages)-1]
	if err := service.Send(ctx, Compose{To: "maya@mica.example", ThreadID: "t-launch", Text: "Final checklist confirmed."}); err != nil {
		t.Fatal(err)
	}
	after, err := service.Thread(ctx, "t-launch")
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Messages) != len(before.Messages)+1 {
		t.Fatalf("reply did not join the original thread: %d messages", len(after.Messages))
	}
	reply := after.Messages[len(after.Messages)-1]
	if !hasString(reply.LabelIDs, Sent) || !strings.Contains(reply.References, last.MessageID) {
		t.Fatalf("unexpected sent reply: %+v", reply)
	}
	threads, err = service.ListThreads(ctx, Query{LabelID: Sent, Search: "final checklist"})
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].ID != "t-launch" {
		t.Fatalf("sent reply is not searchable: %+v", threads)
	}

	if err := service.Send(ctx, Compose{
		To:      "team@mica.example",
		Cc:      "maya@mica.example",
		Subject: "Launch day support rotation",
		Text:    "I will cover the first shift.",
		HTML:    "<p>I will cover the <strong>first shift</strong>.</p>",
	}); err != nil {
		t.Fatal(err)
	}
	threads, err = service.ListThreads(ctx, Query{LabelID: Sent, Search: `subject:"support rotation"`})
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 {
		t.Fatalf("new sent message not found: %+v", threads)
	}
	sent, err := service.Thread(ctx, threads[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sent.Messages) != 1 || sent.Messages[0].HTML == "" || !hasString(sent.LabelIDs, Sent) {
		t.Fatalf("unexpected new sent thread: %+v", sent)
	}
}

func TestDemoMailboxSearches(t *testing.T) {
	thread := Thread{ID: "receipt", Messages: []Message{{
		Text: "Your receipt", LabelIDs: []string{CategoryPurchases, Snoozed},
	}}}
	for _, tc := range []struct {
		name   string
		search string
		want   bool
	}{
		{name: "matching category", search: "category:purchases", want: true},
		{name: "other category", search: "category:social"},
		{name: "snoozed", search: "in:snoozed", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchesDemoSearch(&thread, tc.search); got != tc.want {
				t.Fatalf("matchesDemoSearch(%q) = %v, want %v", tc.search, got, tc.want)
			}
		})
	}
}

func TestDemoConcurrentSendAndList(t *testing.T) {
	ctx := context.Background()
	service := NewDemo()
	const count = 16

	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := service.Send(ctx, Compose{
				To:      "team@mica.example",
				Subject: fmt.Sprintf("Concurrency check %02d", i),
				Text:    "Concurrent demo send.",
			}); err != nil {
				errs <- err
				return
			}
			_, err := service.ListThreads(ctx, Query{Search: "concurrency", Limit: count + 1})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	threads, err := service.ListThreads(ctx, Query{Search: "concurrency", Limit: count + 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != count {
		t.Fatalf("got %d concurrent sends, want %d", len(threads), count)
	}
}
