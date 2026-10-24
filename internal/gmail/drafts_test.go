package gmail

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mica/internal/mail"
	"net/http"
	"net/http/httptest"
	stdmail "net/mail"
	"reflect"
	"strings"
	"testing"
)

func TestDraftEndpointsAndRestore(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/me/drafts":
			if r.Method == http.MethodPost {
				assertEmptyDraftRequest(t, r)
				writeTestJSON(t, w, apiDraft{ID: "draft-3", Message: apiMessage{ID: "message-3"}})
				return
			}
			if r.Method != http.MethodGet || r.URL.Query().Get("maxResults") != "1" {
				t.Errorf("drafts.list = %s %s", r.Method, r.URL.String())
			}
			if r.URL.Query().Get("pageToken") == "page-2" {
				writeTestJSON(t, w, apiDraftList{Drafts: []apiDraft{{ID: "draft-2", Message: apiMessage{ID: "message-2", ThreadID: "thread-2"}}}})
				return
			}
			writeTestJSON(t, w, apiDraftList{Drafts: []apiDraft{{ID: "draft-1", Message: apiMessage{ID: "message-1", ThreadID: "thread-1"}}}, NextPageToken: "page-2"})
		case "/users/me/drafts/draft-1":
			if r.Method != http.MethodGet || r.URL.Query().Get("format") != "full" {
				t.Errorf("drafts.get = %s %s", r.Method, r.URL.String())
			}
			writeTestJSON(t, w, apiDraft{ID: "draft-1", Message: apiMessage{
				ID: "message-1", ThreadID: "thread-1",
				Payload: apiPart{
					MIMEType: "text/plain",
					Headers:  []apiHeader{{Name: "Subject", Value: "First draft"}},
					Body:     apiBody{Data: base64.RawURLEncoding.EncodeToString([]byte("First body"))},
				},
			}})
		case "/users/me/drafts/draft-2":
			if r.Method != http.MethodGet || r.URL.Query().Get("format") != "full" {
				t.Errorf("drafts.get = %s %s", r.Method, r.URL.String())
			}
			writeTestJSON(t, w, apiDraft{ID: "draft-2", Message: apiMessage{
				ID: "message-2", ThreadID: "thread-2",
				Payload: apiPart{
					MIMEType: "text/plain",
					Headers: []apiHeader{
						{Name: "To", Value: "to@example.com"}, {Name: "Cc", Value: "cc@example.com"}, {Name: "Bcc", Value: "bcc@example.com"},
						{Name: "Subject", Value: "Draft subject"}, {Name: "Message-ID", Value: "<draft-message@example.com>"},
						{Name: "In-Reply-To", Value: "<selected@example.com>"}, {Name: "References", Value: "<root@example.com> <selected@example.com>"},
					},
					Body: apiBody{Data: base64.RawURLEncoding.EncodeToString([]byte("Draft body"))},
				},
			}})
		case "/users/me/drafts/draft-3":
			switch r.Method {
			case http.MethodPut:
				assertEmptyDraftRequest(t, r)
				writeTestJSON(t, w, apiDraft{ID: "draft-3", Message: apiMessage{ID: "message-3"}})
			case http.MethodDelete:
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Errorf("unexpected draft method %s", r.Method)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := testService(server)

	first, err := service.Drafts(ctx, mail.Query{Limit: 1})
	if err != nil || len(first.Drafts) != 1 || first.NextPageToken != "page-2" {
		t.Fatalf("first draft page = %+v, %v", first, err)
	}
	if first.Drafts[0].Message.Subject != "First draft" || first.Drafts[0].Message.Text != "First body" {
		t.Fatalf("drafts.list did not hydrate full messages: %+v", first.Drafts[0].Message)
	}
	second, err := service.Drafts(ctx, mail.Query{Limit: 1, PageToken: first.NextPageToken})
	if err != nil || len(second.Drafts) != 1 || second.Drafts[0].ID != "draft-2" || second.NextPageToken != "" {
		t.Fatalf("second draft page = %+v, %v", second, err)
	}
	draft, err := service.Draft(ctx, second.Drafts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	message := draft.Message
	if draft.ID != "draft-2" || message.ID != "message-2" || message.ThreadID != "thread-2" || message.Bcc != "bcc@example.com" || message.Text != "Draft body" {
		t.Fatalf("restored draft = %+v", draft)
	}
	if message.InReplyTo != "<selected@example.com>" || message.References != "<root@example.com> <selected@example.com>" {
		t.Fatalf("restored draft threading = %+v", message)
	}
	created, err := service.CreateDraft(ctx, mail.Compose{})
	if err != nil || created.ID != "draft-3" {
		t.Fatalf("empty draft create = %+v, %v", created, err)
	}
	if _, err := service.UpdateDraft(ctx, created.ID, mail.Compose{}); err != nil {
		t.Fatalf("empty draft update: %v", err)
	}
	if err := service.DeleteDraft(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
}

func TestDraftsPassesSearchQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/me/drafts" || r.Method != http.MethodGet || r.URL.Query().Get("q") != "needle" {
			t.Errorf("draft search request = %s %s", r.Method, r.URL.String())
		}
		writeTestJSON(t, w, apiDraftList{NextPageToken: "next"})
	}))
	defer server.Close()
	service := testService(server)
	page, err := service.Drafts(context.Background(), mail.Query{Search: "needle"})
	if err != nil || len(page.Drafts) != 0 || page.NextPageToken != "next" {
		t.Fatalf("draft search page = %+v, %v", page, err)
	}
}

func TestSendDraftUpdatesThenSends(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/users/me/drafts/draft-1" && r.Method == http.MethodPut:
			calls = append(calls, "update")
			var body struct {
				Message struct {
					Raw      string `json:"raw"`
					ThreadID string `json:"threadId"`
				} `json:"message"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Message.ThreadID != "thread-1" {
				t.Errorf("update threadId = %q", body.Message.ThreadID)
			}
			raw, err := base64.RawURLEncoding.DecodeString(body.Message.Raw)
			if err != nil {
				t.Error(err)
			}
			message, err := stdmail.ReadMessage(bufio.NewReader(bytes.NewReader(raw)))
			if err != nil {
				t.Error(err)
			}
			bcc, err := stdmail.ParseAddress(message.Header.Get("Bcc"))
			if err != nil || bcc.Address != "bcc@example.com" {
				t.Errorf("updated Bcc = %q, %v", message.Header.Get("Bcc"), err)
			}
			content, err := io.ReadAll(message.Body)
			if err != nil || !strings.Contains(string(content), "Latest body") {
				t.Errorf("updated body = %q, %v", content, err)
			}
			writeTestJSON(t, w, apiDraft{ID: "draft-1", Message: apiMessage{ID: "message-1", ThreadID: "thread-1"}})
		case r.URL.Path == "/users/me/drafts/send" && r.Method == http.MethodPost:
			calls = append(calls, "send")
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["id"] != "draft-1" {
				t.Errorf("drafts/send id = %q", body["id"])
			}
			writeTestJSON(t, w, map[string]string{"id": "sent-1", "threadId": "thread-1"})
		case r.URL.Path == "/users/me/messages/send":
			calls = append(calls, "messages/send")
			t.Error("existing draft used messages/send")
		case r.Method == http.MethodDelete:
			calls = append(calls, "delete")
			t.Error("draft send issued an extra DELETE")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := testService(server)
	compose := mail.Compose{To: "to@example.com", Bcc: "bcc@example.com", Text: "Latest body", ThreadID: "thread-1", ThreadingResolved: true}
	if err := service.SendDraft(context.Background(), "draft-1", compose); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"update", "send"}) {
		t.Fatalf("draft send calls = %v, want update then drafts/send", calls)
	}
	calls = nil
	if err := service.SendDraft(context.Background(), "draft-1", mail.Compose{}); err == nil {
		t.Fatal("empty draft send was accepted")
	}
	if len(calls) != 0 {
		t.Fatalf("invalid draft send made network calls: %v", calls)
	}
}

func assertEmptyDraftRequest(t *testing.T, r *http.Request) {
	t.Helper()
	var body struct {
		Message struct {
			Raw string `json:"raw"`
		} `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(body.Message.Raw)
	if err != nil {
		t.Fatal(err)
	}
	message, err := stdmail.ReadMessage(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if message.Header.Get("To") != "" || message.Header.Get("Bcc") != "" {
		t.Fatalf("empty draft gained recipients: %v", message.Header)
	}
	content, err := io.ReadAll(message.Body)
	if err != nil || len(content) != 0 {
		t.Fatalf("empty draft body = %q, %v", content, err)
	}
}
