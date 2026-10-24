package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mica/internal/mail"
)

func TestSendEndpointsDoNotRetryTransientErrors(t *testing.T) {
	for _, endpoint := range []string{"/messages/send", "/drafts/send"} {
		t.Run(endpoint, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case endpoint == "/messages/send" && r.URL.Path == "/users/me/messages/send":
					calls++
					var body struct{ Raw string }
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					raw, err := base64.RawURLEncoding.DecodeString(body.Raw)
					if err != nil || !strings.Contains(string(raw), "Message-ID: <stable@example.com>\r\n") {
						t.Errorf("outgoing Message-ID: raw=%q err=%v", raw, err)
					}
					w.WriteHeader(http.StatusInternalServerError)
				case endpoint == "/drafts/send" && r.URL.Path == "/users/me/drafts/draft-1" && r.Method == http.MethodPut:
					writeTestJSON(t, w, apiDraft{ID: "draft-1", Message: apiMessage{ID: "message-1"}})
				case endpoint == "/drafts/send" && r.URL.Path == "/users/me/drafts/send":
					calls++
					w.WriteHeader(http.StatusInternalServerError)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			service := testService(server)
			service.sleep = func(context.Context, time.Duration) error {
				t.Fatal("send endpoint slept before retrying")
				return nil
			}
			compose := mail.Compose{To: "to@example.com", Text: "body", MessageID: "<stable@example.com>"}
			var err error
			if endpoint == "/messages/send" {
				err = service.Send(context.Background(), compose)
			} else {
				err = service.SendDraft(context.Background(), "draft-1", compose)
			}
			if err == nil || calls != 1 {
				t.Fatalf("send calls = %d err=%v, want one attempt", calls, err)
			}
		})
	}
}

func TestSentMessageExistsUsesRFC822MessageID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/me/threads":
			if got := r.URL.Query().Get("q"); got != "rfc822msgid:<stable@example.com>" {
				t.Errorf("search query = %q", got)
			}
			if got := r.URL.Query().Get("labelIds"); got != mail.Sent {
				t.Errorf("label scope = %q", got)
			}
			writeTestJSON(t, w, map[string]any{"threads": []map[string]string{{"id": "sent-1"}}})
		case "/users/me/threads/sent-1":
			writeTestJSON(t, w, apiThread{ID: "sent-1", Messages: []apiMessage{{ID: "message-1", LabelIDs: []string{mail.Sent}, Payload: apiPart{Headers: []apiHeader{{Name: "Subject", Value: "Outgoing"}}}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := testService(server)
	found, err := service.SentMessageExists(context.Background(), "<stable@example.com>")
	if err != nil || !found {
		t.Fatalf("SentMessageExists = %v, %v", found, err)
	}
}

func TestSentMessageExistsIgnoresNonSentMatches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/me/threads":
			if got := r.URL.Query().Get("labelIds"); got != mail.Sent {
				t.Errorf("label scope = %q", got)
			}
			writeTestJSON(t, w, map[string]any{"threads": []map[string]string{{"id": "draft-1"}}})
		case "/users/me/threads/draft-1":
			writeTestJSON(t, w, apiThread{ID: "draft-1", Messages: []apiMessage{{ID: "message-1", LabelIDs: []string{mail.Draft}, Payload: apiPart{Headers: []apiHeader{{Name: "Subject", Value: "Outgoing"}}}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := testService(server)
	found, err := service.SentMessageExists(context.Background(), "<stable@example.com>")
	if err != nil || found {
		t.Fatalf("DRAFT-only match was accepted as Sent: found=%v err=%v", found, err)
	}
}
