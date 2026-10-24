package app

import (
	"encoding/base64"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestReaderDocumentsAreAccountScoped(t *testing.T) {
	a := New(newFakeService())
	messageA := mail.Message{AccountEmail: "a@example.com", ID: "shared-message", ThreadID: "shared-thread", HTML: "<p>A body</p>"}
	messageB := mail.Message{AccountEmail: "b@example.com", ID: "shared-message", ThreadID: "shared-thread", HTML: "<p>B body</p>"}
	current := messageA
	a.thread = mail.Thread{AccountEmail: messageA.AccountEmail, ID: messageA.ThreadID, Messages: []mail.Message{messageA}}
	tt := ui.NewTester(func(c *ui.Context) {
		a.messageView(c, 0, current)
	}, 900, 700)
	if !tt.HasText("A body") {
		t.Fatalf("A document was not rendered: %q", tt.Texts())
	}
	current = messageB
	a.thread = mail.Thread{AccountEmail: messageB.AccountEmail, ID: messageB.ThreadID, Messages: []mail.Message{messageB}}
	tt.Frame()
	tt.Advance(time.Second)
	if !tt.HasText("B body") || tt.HasText("A body") {
		t.Fatalf("B reused A's document: %q", tt.Texts())
	}
	if len(a.documents) != 2 {
		t.Fatalf("document keys = %v", a.documents)
	}
	for _, tc := range []struct {
		message mail.Message
		want    string
	}{
		{message: messageA, want: "A body"},
		{message: messageB, want: "B body"},
	} {
		key := messageRefForAccount(tc.message.AccountEmail, tc.message.ThreadID, tc.message.ID).key()
		document := a.documents[key]
		if document == nil || !strings.Contains(document.preview, tc.want) {
			t.Fatalf("document %q = %+v, want %q", key, document, tc.want)
		}
	}
	legacy := mail.Message{ID: "shared-message", ThreadID: "shared-thread", HTML: "<p>Legacy body</p>"}
	a.readerEmailDocument(legacy)
	if a.documents[legacy.ID] == nil {
		t.Fatal("ownerless document did not retain its message ID key")
	}
}

func TestReaderPageAndHeightAreAccountScoped(t *testing.T) {
	a := New(newFakeService())
	messageA := mail.Message{AccountEmail: "a@example.com", ID: "shared-message", ThreadID: "shared-thread", HTML: "<p>A body</p>"}
	messageB := mail.Message{AccountEmail: "b@example.com", ID: "shared-message", ThreadID: "shared-thread", HTML: "<p>B body</p>"}
	keyA, keyB := readerMessageKey(messageA), readerMessageKey(messageB)
	if keyA == keyB || keyA == messageA.ID || keyB == messageB.ID {
		t.Fatalf("owned page keys collide or lost ownership: %q %q", keyA, keyB)
	}
	pageA := &emailPage{messageKey: keyA, view: new(mygo.WebView), height: 100}
	pageB := &emailPage{messageKey: keyB, height: 240}
	a.emailPages = map[string]*emailPage{keyA: pageA}
	a.setEmailHeight(pageA, pageA.height)
	a.setEmailHeight(pageB, pageB.height)
	if got := a.estimatedEmailHeight(keyA); got != 100 {
		t.Fatalf("A height = %v", got)
	}
	if got := a.estimatedEmailHeight(keyB); got != 240 {
		t.Fatalf("B height = %v", got)
	}
	current := messageA
	tt := ui.NewTester(func(c *ui.Context) {
		a.emailView(c, current)
	}, 900, 700)
	if tt.WebViewAt(450, 50) == nil {
		t.Fatal("A's account-keyed page was not found")
	}
	current = messageB
	tt.Frame()
	if tt.WebViewAt(450, 50) != nil {
		t.Fatal("B reused A's page with the same message ID")
	}
	if !tt.HasText("B body") {
		t.Fatalf("B did not render its own fallback: %q", tt.Texts())
	}
}

func TestReaderExpansionAndRevokeAreAccountScoped(t *testing.T) {
	a := newOwnerResourceApp(newFakeService())
	a.registerAccountClient(newFakeService(), mail.Account{Email: "b@example.com"}, nil, true)
	messageA := mail.Message{AccountEmail: "a@example.com", ID: "shared-message", ThreadID: "shared-thread"}
	messageB := mail.Message{AccountEmail: "b@example.com", ID: "shared-message", ThreadID: "shared-thread"}
	a.thread = mail.Thread{AccountEmail: messageA.AccountEmail, ID: messageA.ThreadID, Messages: []mail.Message{messageA}}
	a.setMessageExpanded(messageA.ID, false)
	a.thread = mail.Thread{AccountEmail: messageB.AccountEmail, ID: messageB.ThreadID, Messages: []mail.Message{messageB}}
	if !a.messageExpanded(0, messageB) {
		t.Fatal("B inherited A's collapsed state")
	}
	a.setMessageExpanded(messageB.ID, false)
	a.thread = mail.Thread{AccountEmail: messageA.AccountEmail, ID: messageA.ThreadID, Messages: []mail.Message{messageA}}
	a.setMessageExpanded(messageA.ID, true)
	a.thread = mail.Thread{AccountEmail: messageB.AccountEmail, ID: messageB.ThreadID, Messages: []mail.Message{messageB}}
	if a.messageExpanded(0, messageB) {
		t.Fatal("B's expansion changed with A's state")
	}
	if len(a.expandedMessages) != 2 {
		t.Fatalf("expansion keys = %v", a.expandedMessages)
	}

	dataA, dataB := ownerTestPNG(t, 1, 1), ownerTestPNG(t, 2, 3)
	tokenA := a.emailResources.register(messageA, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(dataA))
	tokenB := a.emailResources.register(messageB, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(dataB))
	a.setMessageExpanded(messageB.ID, false)
	if response := serveOwnerResource(a.emailResources, tokenA); response.Code != http.StatusOK {
		t.Fatalf("closing B revoked A's resources: %d", response.Code)
	}
	if response := serveOwnerResource(a.emailResources, tokenB); response.Code != http.StatusNotFound {
		t.Fatalf("closing B left its resources available: %d", response.Code)
	}
}

func TestReaderLabelMenuUsesThreadOwner(t *testing.T) {
	a := New(newFakeService())
	a.labels = []mail.Label{{ID: "active", Name: "Active label"}}
	a.thread = mail.Thread{AccountEmail: "b@example.com", ID: "shared-thread"}
	a.registerAccountClient(newFakeService(), mail.Account{Email: "b@example.com"}, []mail.Label{
		{ID: "shared-label", Name: "B shared"},
		{ID: "b-only", Name: "B only"},
	}, true)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Button(c, "Labels").Menu(a.labelMenu)
	}, 400, 300)
	if err := tt.Click("Labels"); err != nil {
		t.Fatal(err)
	}
	if got, want := tt.Menu(), []string{"B only", "B shared"}; !slices.Equal(got, want) {
		t.Fatalf("owner label menu = %v, want %v", got, want)
	}
}

func TestReaderLabelMenuWithoutOwnerUsesActiveLabels(t *testing.T) {
	a := New(newFakeService())
	a.storeAccount = "a@example.com"
	a.account = mail.Account{Email: "a@example.com"}
	a.labels = []mail.Label{{ID: "active", Name: "Active label"}}
	a.thread = mail.Thread{ID: "legacy-thread"}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Button(c, "Labels").Menu(a.labelMenu)
	}, 400, 300)
	if err := tt.Click("Labels"); err != nil {
		t.Fatal(err)
	}
	if got, want := tt.Menu(), []string{"Active label"}; !slices.Equal(got, want) {
		t.Fatalf("legacy label menu = %v, want %v", got, want)
	}
}
