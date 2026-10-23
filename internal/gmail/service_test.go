package gmail

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mica/internal/mail"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/http/httptest"
	stdmail "net/mail"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAccountAndLabels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/me/profile":
			if r.Method != http.MethodGet {
				t.Errorf("profile method = %s", r.Method)
			}
			writeTestJSON(t, w, map[string]string{"emailAddress": "alex@example.com"})
		case "/users/me/labels":
			writeTestJSON(t, w, map[string]any{"labels": []map[string]any{
				{"id": "INBOX", "name": "Inbox", "type": "system", "messagesTotal": 12, "messagesUnread": 3},
				{"id": "Label_7", "name": "Launch", "type": "user", "labelListVisibility": "labelShowIfUnread", "messagesTotal": 4, "messagesUnread": 1},
			}})
		case "/userinfo":
			writeTestJSON(t, w, map[string]string{"name": "Alex Example", "picture": "https://images.example/alex.png"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := testService(server)

	service.userinfoURL = server.URL + "/userinfo"
	account, err := service.Account(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if account.Email != "alex@example.com" || account.Demo {
		t.Fatalf("unexpected account: %+v", account)
	}
	if account.Name != "Alex Example" || account.PhotoURL != "https://images.example/alex.png" || account.ProfileUnauthorized {
		t.Fatalf("unexpected userinfo: %+v", account)
	}
	labels, err := service.Labels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []mail.Label{
		{ID: "INBOX", Name: "Inbox", Messages: 12, Unread: 3, System: true},
		{ID: "Label_7", Name: "Launch", Messages: 4, Unread: 1, ShowIfUnread: true},
	}
	if !reflect.DeepEqual(labels, want) {
		t.Fatalf("labels = %+v, want %+v", labels, want)
	}
}

func TestAccountProfileUnauthorized(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/users/me/profile":
					writeTestJSON(t, w, map[string]string{"emailAddress": "alex@example.com"})
				case "/userinfo":
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"error":{"message":"Insufficient permissions","errors":[{"reason":"insufficientPermissions"}]}}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			service := testService(server)
			service.userinfoURL = server.URL + "/userinfo"
			account, err := service.Account(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !account.ProfileUnauthorized {
				t.Fatalf("account = %+v, want ProfileUnauthorized", account)
			}
		})
	}
}

func TestAttachment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/me/messages/message-1/attachments/attachment-1" {
			http.NotFound(w, r)
			return
		}
		writeTestJSON(t, w, apiBody{Data: base64.RawURLEncoding.EncodeToString([]byte("image data"))})
	}))
	defer server.Close()
	service := testService(server)
	data, err := service.Attachment(context.Background(), "message-1", "attachment-1")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "image data" {
		t.Fatalf("attachment = %q", data)
	}
}

func TestListThreadsQueryPaginationAndSummaries(t *testing.T) {
	var mu sync.Mutex
	var listCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/me/threads":
			mu.Lock()
			listCalls++
			call := listCalls
			mu.Unlock()
			if got := r.URL.Query().Get("q"); got != "is:unread from:maya" {
				t.Errorf("q = %q", got)
			}
			if got := r.URL.Query().Get("labelIds"); got != "INBOX" {
				t.Errorf("labelIds = %q", got)
			}
			if call == 1 {
				if got := r.URL.Query().Get("maxResults"); got != "2" {
					t.Errorf("first maxResults = %q", got)
				}
				writeTestJSON(t, w, map[string]any{
					"threads":       []map[string]string{{"id": "thread-1"}},
					"nextPageToken": "page-2",
				})
				return
			}
			if got := r.URL.Query().Get("pageToken"); got != "page-2" {
				t.Errorf("pageToken = %q", got)
			}
			if got := r.URL.Query().Get("maxResults"); got != "1" {
				t.Errorf("second maxResults = %q", got)
			}
			writeTestJSON(t, w, map[string]any{"threads": []map[string]string{{"id": "thread-2"}}})
		case "/users/me/threads/thread-1", "/users/me/threads/thread-2":
			if got := r.URL.Query().Get("format"); got != "metadata" {
				t.Errorf("format = %q", got)
			}
			if got := r.URL.Query()["metadataHeaders"]; !reflect.DeepEqual(got, []string{"Subject", "From", "Date"}) {
				t.Errorf("metadataHeaders = %v", got)
			}
			id := strings.TrimPrefix(r.URL.Path, "/users/me/threads/")
			writeTestJSON(t, w, apiThread{
				ID:      id,
				Snippet: "Latest snippet for " + id,
				Messages: []apiMessage{
					{
						ID:       id + "-1",
						LabelIDs: []string{"INBOX", "UNREAD"},
						Payload: apiPart{Headers: []apiHeader{
							{Name: "Subject", Value: "Launch plan"},
							{Name: "From", Value: "Maya Chen <maya@example.com>"},
							{Name: "Date", Value: "Mon, 20 Oct 2025 10:00:00 +0000"},
						}},
					},
					{
						ID:           id + "-2",
						LabelIDs:     []string{"SENT", "STARRED"},
						InternalDate: "1760954400000",
						Payload: apiPart{Headers: []apiHeader{
							{Name: "From", Value: "Alex Morgan <alex@example.com>"},
						}},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := testService(server)

	threads, err := service.ListThreads(context.Background(), mail.Query{LabelID: "INBOX", Search: "is:unread from:maya", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 2 {
		t.Fatalf("got %d threads, want 2", len(threads))
	}
	for i, thread := range threads {
		if thread.ID != fmt.Sprintf("thread-%d", i+1) || thread.Subject != "Launch plan" {
			t.Errorf("unexpected thread summary: %+v", thread)
		}
		if thread.MessageCount != 2 || !thread.Unread || !thread.Starred {
			t.Errorf("summary flags were not combined: %+v", thread)
		}
		if thread.Senders != "Maya Chen, Alex Morgan" || thread.Snippet == "" {
			t.Errorf("summary display fields = %+v", thread)
		}
		wantDate := time.Date(2025, 10, 20, 10, 0, 0, 0, time.UTC)
		if !thread.Updated.Equal(wantDate) {
			t.Errorf("updated = %s, want %s", thread.Updated, wantDate)
		}
	}
}

func TestThreadParsesNestedMIME(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/me/threads/thread-mime" || r.URL.Query().Get("format") != "full" {
			http.NotFound(w, r)
			return
		}
		writeTestJSON(t, w, apiThread{
			ID: "thread-mime",
			Messages: []apiMessage{{
				ID:       "message-mime",
				ThreadID: "thread-mime",
				LabelIDs: []string{"INBOX", "UNREAD"},
				Payload: apiPart{
					MIMEType: "multipart/mixed",
					Headers: []apiHeader{
						{Name: "From", Value: "=?UTF-8?Q?Maya_Chen?= <maya@example.com>"},
						{Name: "To", Value: "Alex <alex@example.com>"},
						{Name: "Cc", Value: "Leo <leo@example.com>"},
						{Name: "Subject", Value: "=?UTF-8?Q?Launch_=E8=AE=A1=E5=88=92?="},
						{Name: "Message-ID", Value: "<message-2@example.com>"},
						{Name: "References", Value: "<message-1@example.com>"},
						{Name: "Date", Value: "Tue, 21 Oct 2025 11:30:00 +0000"},
					},
					Parts: []apiPart{
						{
							MIMEType: "multipart/alternative",
							Parts: []apiPart{
								{MIMEType: "text/plain", Body: apiBody{Data: base64.RawURLEncoding.EncodeToString([]byte("Plain body\n第二行"))}},
								{MIMEType: "text/html", Body: apiBody{Data: base64.URLEncoding.EncodeToString([]byte("<p>HTML body</p>"))}},
							},
						},
						{
							MIMEType: "application/pdf", Filename: "launch-plan.pdf",
							Headers: []apiHeader{{Name: "Content-ID", Value: "<launch-plan>"}},
							Body:    apiBody{AttachmentID: "attachment-1", Size: 2048},
						},
					},
				},
			}},
		})
	}))
	defer server.Close()
	service := testService(server)

	thread, err := service.Thread(context.Background(), "thread-mime")
	if err != nil {
		t.Fatal(err)
	}
	if thread.ID != "thread-mime" || thread.Subject != "Launch 计划" || len(thread.Messages) != 1 {
		t.Fatalf("unexpected thread: %+v", thread)
	}
	message := thread.Messages[0]
	if message.From != "Maya Chen <maya@example.com>" || message.To == "" || message.Cc == "" {
		t.Errorf("address headers were not parsed: %+v", message)
	}
	if message.Text != "Plain body\n第二行" || message.HTML != "<p>HTML body</p>" {
		t.Errorf("bodies were not parsed: text %q, HTML %q", message.Text, message.HTML)
	}
	if message.MessageID != "<message-2@example.com>" || message.References != "<message-1@example.com>" {
		t.Errorf("threading headers were not parsed: %+v", message)
	}
	wantAttachments := []mail.Attachment{{ID: "attachment-1", Name: "launch-plan.pdf", MIMEType: "application/pdf", Size: 2048, ContentID: "launch-plan"}}
	if !reflect.DeepEqual(message.Attachments, wantAttachments) {
		t.Errorf("attachments = %+v, want %+v", message.Attachments, wantAttachments)
	}
	if message.Date.IsZero() {
		t.Error("message date was not parsed")
	}
}

func TestModifyAndTrashThread(t *testing.T) {
	var modifyCalled, trashCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/me/threads/thread-1/modify":
			modifyCalled = true
			if r.Method != http.MethodPost {
				t.Errorf("modify method = %s", r.Method)
			}
			var body map[string][]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if !reflect.DeepEqual(body["addLabelIds"], []string{"STARRED"}) || !reflect.DeepEqual(body["removeLabelIds"], []string{"UNREAD"}) {
				t.Errorf("modify body = %v", body)
			}
			writeTestJSON(t, w, map[string]string{"id": "thread-1"})
		case "/users/me/threads/thread-1/trash":
			trashCalled = true
			if r.Method != http.MethodPost {
				t.Errorf("trash method = %s", r.Method)
			}
			writeTestJSON(t, w, map[string]string{"id": "thread-1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := testService(server)
	ctx := context.Background()
	if err := service.ModifyThread(ctx, "thread-1", []string{"STARRED"}, []string{"UNREAD"}); err != nil {
		t.Fatal(err)
	}
	if err := service.TrashThread(ctx, "thread-1"); err != nil {
		t.Fatal(err)
	}
	if !modifyCalled || !trashCalled {
		t.Fatalf("modify called %v, trash called %v", modifyCalled, trashCalled)
	}
}

func TestSendRawPlainText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/me/messages/send" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Raw      string `json:"raw"`
			ThreadID string `json:"threadId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.ThreadID != "" {
			t.Errorf("new message threadId = %q", body.ThreadID)
		}
		raw, err := base64.RawURLEncoding.DecodeString(body.Raw)
		if err != nil {
			t.Fatalf("raw is not base64url: %v", err)
		}
		message, err := stdmail.ReadMessage(bufio.NewReader(bytes.NewReader(raw)))
		if err != nil {
			t.Fatalf("raw is not RFC 5322: %v", err)
		}
		for name, want := range map[string]string{"To": "maya@example.com", "Cc": "leo@example.com", "Bcc": "sam@example.com"} {
			address, err := stdmail.ParseAddress(message.Header.Get(name))
			if err != nil || address.Address != want {
				t.Errorf("%s = %q, %v", name, message.Header.Get(name), err)
			}
		}
		decoded, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
		if err != nil || decoded != "发布 计划" {
			t.Errorf("decoded Subject = %q, %v", decoded, err)
		}
		content, err := io.ReadAll(quotedprintable.NewReader(message.Body))
		if err != nil {
			t.Error(err)
		}
		if string(content) != "Plain launch notes\r\n第二行" {
			t.Errorf("body = %q", content)
		}
		writeTestJSON(t, w, map[string]string{"id": "sent-1"})
	}))
	defer server.Close()
	service := testService(server)
	if err := service.Send(context.Background(), mail.Compose{
		To:      "maya@example.com",
		Cc:      "leo@example.com",
		Bcc:     "sam@example.com",
		Subject: "发布 计划",
		Text:    "Plain launch notes\n第二行",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSendReplyRawAlternative(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/me/threads/thread-reply":
			writeTestJSON(t, w, apiThread{ID: "thread-reply", Messages: []apiMessage{{
				ID: "last-message",
				Payload: apiPart{Headers: []apiHeader{
					{Name: "Subject", Value: "Launch plan"},
					{Name: "Message-ID", Value: "<last@example.com>"},
					{Name: "References", Value: "<first@example.com>"},
				}},
			}}})
		case "/users/me/messages/send":
			var body struct {
				Raw      string `json:"raw"`
				ThreadID string `json:"threadId"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.ThreadID != "thread-reply" {
				t.Errorf("threadId = %q", body.ThreadID)
			}
			raw, err := base64.RawURLEncoding.DecodeString(body.Raw)
			if err != nil {
				t.Error(err)
			}
			message, err := stdmail.ReadMessage(bufio.NewReader(bytes.NewReader(raw)))
			if err != nil {
				t.Error(err)
			}
			if got := message.Header.Get("In-Reply-To"); got != "<last@example.com>" {
				t.Errorf("In-Reply-To = %q", got)
			}
			if got := message.Header.Get("References"); got != "<first@example.com> <last@example.com>" {
				t.Errorf("References = %q", got)
			}
			mediaType, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
			if err != nil || mediaType != "multipart/alternative" {
				t.Fatalf("Content-Type = %q, %v", message.Header.Get("Content-Type"), err)
			}
			reader := multipart.NewReader(message.Body, params["boundary"])
			var bodies []string
			for {
				part, err := reader.NextRawPart()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				content, err := io.ReadAll(quotedprintable.NewReader(part))
				if err != nil {
					t.Fatal(err)
				}
				bodies = append(bodies, string(content))
			}
			if len(bodies) != 2 || bodies[0] != "Plain reply" || bodies[1] != "<p>HTML reply</p>" {
				t.Errorf("alternative bodies = %q", bodies)
			}
			writeTestJSON(t, w, map[string]string{"id": "sent-reply", "threadId": "thread-reply"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := testService(server)
	if err := service.Send(context.Background(), mail.Compose{
		To:       "maya@example.com",
		ThreadID: "thread-reply",
		Text:     "Plain reply",
		HTML:     "<p>HTML reply</p>",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAPIErrorDoesNotExposeAuthorization(t *testing.T) {
	const token = "test-access-token-not-for-production"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"Invalid Credentials","errors":[{"reason":"authError"}]}}`)
	}))
	defer server.Close()
	client := server.Client()
	transport := client.Transport
	client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		r.Header.Set("Authorization", "Bearer "+token)
		return transport.RoundTrip(r)
	})
	service := New(client)
	service.baseURL = server.URL

	_, err := service.Labels(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized || apiErr.Reason != "authError" {
		t.Fatalf("error = %#v", err)
	}
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "Authorization") {
		t.Fatalf("error exposes credentials: %v", err)
	}
}

func TestDoJSONClosesResponseBody(t *testing.T) {
	body := &trackingBody{Reader: strings.NewReader(`{"error":{"message":"denied"}}`)}
	service := New(&http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: body}, nil
	})})
	service.baseURL = "https://gmail.invalid"
	_, err := service.Labels(context.Background())
	if err == nil {
		t.Fatal("expected an API error")
	}
	if !body.closed {
		t.Fatal("response body was not closed")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

type trackingBody struct {
	io.Reader
	closed bool
}

func (b *trackingBody) Close() error {
	b.closed = true
	return nil
}

func testService(server *httptest.Server) *Service {
	service := New(server.Client())
	service.baseURL = server.URL
	return service
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}
