package app

import (
	"errors"
	netmail "net/mail"
	"strings"
	"testing"
	"time"

	mailmodel "mica/internal/mail"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
)

func TestReplyUsesReplyTo(t *testing.T) {
	a := New(newFakeService())
	a.openCompose(&mailmodel.Message{
		From: "Sender <sender@example.com>", ReplyTo: "Replies <replies@example.com>", Subject: "Question",
	})
	if a.compose == nil || a.compose.to != "Replies <replies@example.com>" {
		t.Fatalf("reply recipient = %q, want Reply-To", a.compose.to)
	}
}

func TestReplyAllUsesReplyTo(t *testing.T) {
	to, cc := replyAllRecipients(mailmodel.Message{
		From: "Sender <sender@example.com>", ReplyTo: "Replies <replies@example.com>",
		To: "Alex <alex@example.com>", Cc: "Carol <carol@example.com>",
	}, "alex@example.com")
	addresses, err := netmail.ParseAddressList(to)
	if err != nil || len(addresses) != 1 || addresses[0].Address != "replies@example.com" {
		t.Fatalf("reply-all To = %q (%v), want only Reply-To", to, err)
	}
	if strings.Contains(to, "sender@example.com") {
		t.Fatalf("reply-all used From instead of Reply-To: %q", to)
	}
	ccAddresses, err := netmail.ParseAddressList(cc)
	if err != nil || len(ccAddresses) != 1 || ccAddresses[0].Address != "carol@example.com" {
		t.Fatalf("reply-all Cc = %q (%v)", cc, err)
	}
}

func TestReplyQuoteCollapsible(t *testing.T) {
	a := New(newFakeService())
	a.openCompose(&mailmodel.Message{From: "Ada <ada@example.com>", Subject: "Launch", Text: "Hello world"})
	tt := ui.NewTester(a.View, 1280, 800)
	if tt.HasText("Hello world") {
		t.Fatal("collapsed quote is visible")
	}
	a.compose.quoteOpen = true
	tt.Frame()
	if !tt.HasText("Hello world") {
		t.Fatal("expanded quote is not visible")
	}
}

func TestReplyQuoteComposition(t *testing.T) {
	service := newFakeService()
	a := newTestApp(service)
	a.openCompose(&mailmodel.Message{
		From: "Ada <ada@example.com>", ReplyTo: "Replies <replies@example.com>", To: "demo@example.com",
		Subject: "Launch", ThreadID: "thread-1", MessageID: "<m1@example.com>", Date: time.Now(),
		HTML: `<p>Hello <b>world</b></p><script>alert("x")</script>`,
	})
	a.compose.doc = richtext.NewDocument(richtext.NewText("Thanks"))
	a.sendCompose()
	if a.compose != nil {
		t.Fatal("successful reply kept the composer open")
	}
	sent := service.sent
	if sent.To != "Replies <replies@example.com>" {
		t.Fatalf("sent recipient = %q, want Reply-To", sent.To)
	}
	for _, want := range []string{"Thanks", "Original message", "Hello world"} {
		if !strings.Contains(sent.Text, want) {
			t.Fatalf("sent text lacks %q: %q", want, sent.Text)
		}
	}
	if !strings.Contains(sent.HTML, "<blockquote") {
		t.Fatalf("sent HTML lacks quote: %q", sent.HTML)
	}
	if strings.Contains(sent.HTML, "<script>") {
		t.Fatalf("sent HTML contains an unescaped script: %q", sent.HTML)
	}
}

func TestReplyFailurePreservesQuoteAndInput(t *testing.T) {
	service := newFakeService()
	service.sendErr = errors.New("offline")
	a := newTestApp(service)
	a.openCompose(&mailmodel.Message{From: "Ada <ada@example.com>", Subject: "Launch", Text: "Original"})
	a.compose.doc = richtext.NewDocument(richtext.NewText("Draft reply"))
	a.sendCompose()
	if a.compose == nil || a.compose.quote == nil || a.compose.doc.Text().PlainText() != "Draft reply" {
		t.Fatal("failed reply discarded the quote or input")
	}
}
