package gmail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mica/internal/mail"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestEnsurePushCreatesResourcesAndAppendsPublisher(t *testing.T) {
	var setBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/project-1/topics/mica-gmail":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && r.URL.Path == "/v1/projects/project-1/topics/mica-gmail":
			writeTestJSON(t, w, map[string]string{"name": "projects/project-1/topics/mica-gmail"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/project-1/subscriptions/mica-gmail-stream":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && r.URL.Path == "/v1/projects/project-1/subscriptions/mica-gmail-stream":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			want := map[string]any{"topic": "projects/project-1/topics/mica-gmail"}
			if !reflect.DeepEqual(body, want) {
				t.Errorf("subscription body = %#v, want %#v", body, want)
			}
			writeTestJSON(t, w, map[string]string{"name": "projects/project-1/subscriptions/mica-gmail-stream"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/project-1/topics/mica-gmail:getIamPolicy":
			writeTestJSON(t, w, map[string]any{
				"version": 3,
				"etag":    "etag-1",
				"bindings": []map[string]any{
					{
						"role":    "roles/viewer",
						"members": []string{"user:alex@example.com"},
						"condition": map[string]any{
							"title":      "retain",
							"expression": "request.time < timestamp(\"2027-01-01T00:00:00Z\")",
						},
					},
					{
						"role":    "roles/pubsub.publisher",
						"members": []string{"serviceAccount:existing@example.com"},
					},
				},
				"auditConfigs": []map[string]any{{"service": "allServices"}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/projects/project-1/topics/mica-gmail:setIamPolicy":
			if err := json.NewDecoder(r.Body).Decode(&setBody); err != nil {
				t.Error(err)
			}
			writeTestJSON(t, w, setBody)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := pubsubTestService(server, "project-1")

	if err := service.EnsurePush(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"policy": map[string]any{
			"version": float64(3),
			"etag":    "etag-1",
			"bindings": []any{
				map[string]any{
					"role":    "roles/viewer",
					"members": []any{"user:alex@example.com"},
					"condition": map[string]any{
						"title":      "retain",
						"expression": "request.time < timestamp(\"2027-01-01T00:00:00Z\")",
					},
				},
				map[string]any{
					"role":    "roles/pubsub.publisher",
					"members": []any{"serviceAccount:existing@example.com", "serviceAccount:gmail-api-push@system.gserviceaccount.com"},
				},
			},
			"auditConfigs": []any{map[string]any{"service": "allServices"}},
		},
	}
	if !reflect.DeepEqual(setBody, want) {
		t.Fatalf("setIamPolicy body = %#v, want %#v", setBody, want)
	}
}

func TestEnsurePushLeavesExistingResourcesAndPolicyUnchanged(t *testing.T) {
	setCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/project-1/topics/mica-gmail":
			writeTestJSON(t, w, map[string]string{"name": "projects/project-1/topics/mica-gmail"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/project-1/subscriptions/mica-gmail-stream":
			writeTestJSON(t, w, map[string]string{
				"name":  "projects/project-1/subscriptions/mica-gmail-stream",
				"topic": "projects/project-1/topics/mica-gmail",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/project-1/topics/mica-gmail:getIamPolicy":
			writeTestJSON(t, w, map[string]any{
				"etag": "etag-2",
				"bindings": []map[string]any{{
					"role": "roles/pubsub.publisher",
					"members": []string{
						"serviceAccount:existing@example.com",
						"serviceAccount:gmail-api-push@system.gserviceaccount.com",
					},
				}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/projects/project-1/topics/mica-gmail:setIamPolicy":
			setCalled = true
			t.Error("setIamPolicy called for an existing publisher binding")
			writeTestJSON(t, w, map[string]any{})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := pubsubTestService(server, "project-1")

	if err := service.EnsurePush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if setCalled {
		t.Fatal("existing IAM policy was rewritten")
	}
}

func TestEnsurePushRequiresProject(t *testing.T) {
	service := NewWithProject(nil, "", nil)
	err := service.EnsurePush(context.Background())
	if err == nil || !strings.Contains(err.Error(), "project id") {
		t.Fatalf("error = %v, want missing project id", err)
	}
}

func TestWatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/users/me/watch" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		want := map[string]any{
			"labelFilterBehavior": "include",
			"labelIds":            []any{"INBOX"},
			"topicName":           "projects/project-1/topics/mica-gmail",
		}
		if !reflect.DeepEqual(body, want) {
			t.Errorf("watch body = %#v, want %#v", body, want)
		}
		writeTestJSON(t, w, map[string]string{"historyId": "9876543210", "expiration": "1760954400123"})
	}))
	defer server.Close()
	service := NewWithProject(server.Client(), "project-1", nil)
	service.baseURL = server.URL

	state, err := service.Watch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.HistoryID != 9876543210 {
		t.Errorf("history ID = %d", state.HistoryID)
	}
	if want := time.UnixMilli(1760954400123); !state.Expiration.Equal(want) {
		t.Errorf("expiration = %s, want %s", state.Expiration, want)
	}
}

func TestHistoryPaginationAndMetadata(t *testing.T) {
	historyCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/me/history":
			historyCalls++
			if r.Method != http.MethodGet {
				t.Errorf("history method = %s", r.Method)
			}
			query := r.URL.Query()
			for key, want := range map[string]string{
				"startHistoryId": "100",
				"labelId":        "INBOX",
				"maxResults":     "500",
			} {
				if got := query.Get(key); got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
			if historyCalls == 1 {
				if got := query.Get("pageToken"); got != "" {
					t.Errorf("first pageToken = %q", got)
				}
				writeTestJSON(t, w, map[string]any{
					"historyId":     "110",
					"nextPageToken": "page-2",
					"history": []map[string]any{{
						"messagesAdded": []map[string]any{{
							"message": map[string]string{"id": "message-1", "threadId": "thread-1"},
						}},
					}},
				})
				return
			}
			if got := query.Get("pageToken"); got != "page-2" {
				t.Errorf("second pageToken = %q", got)
			}
			writeTestJSON(t, w, map[string]any{
				"historyId": "120",
				"history": []map[string]any{{
					"messagesAdded": []map[string]any{{
						"message": map[string]string{"id": "message-2", "threadId": "thread-2"},
					}},
				}},
			})
		case "/users/me/messages/message-1", "/users/me/messages/message-2":
			if r.Method != http.MethodGet {
				t.Errorf("metadata method = %s", r.Method)
			}
			query := r.URL.Query()
			if got := query.Get("format"); got != "metadata" {
				t.Errorf("format = %q", got)
			}
			if got := query["metadataHeaders"]; !reflect.DeepEqual(got, []string{"Subject", "From", "Date"}) {
				t.Errorf("metadataHeaders = %v", got)
			}
			id := strings.TrimPrefix(r.URL.Path, "/users/me/messages/message-")
			writeTestJSON(t, w, apiMessage{
				ID:       "message-" + id,
				ThreadID: "thread-" + id,
				Snippet:  "Snippet " + id,
				LabelIDs: []string{"INBOX", "UNREAD"},
				Payload: apiPart{Headers: []apiHeader{
					{Name: "Subject", Value: "Launch " + id},
					{Name: "From", Value: "Maya Chen <maya@example.com>"},
					{Name: "Date", Value: "Mon, 20 Oct 2025 10:00:00 +0000"},
				}},
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := NewWithProject(server.Client(), "project-1", nil)
	service.baseURL = server.URL

	var pages [][]mail.ThreadSummary
	latest, err := service.History(context.Background(), 100, func(summaries []mail.ThreadSummary) error {
		pages = append(pages, append([]mail.ThreadSummary(nil), summaries...))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if latest != 120 {
		t.Errorf("latest history ID = %d, want 120", latest)
	}
	updated := time.Date(2025, 10, 20, 10, 0, 0, 0, time.UTC)
	want := [][]mail.ThreadSummary{
		{{
			ID: "thread-1", Subject: "Launch 1", Snippet: "Snippet 1", Senders: "Maya Chen",
			Updated: updated, MessageCount: 1, LabelIDs: []string{"INBOX", "UNREAD"}, Unread: true,
		}},
		{{
			ID: "thread-2", Subject: "Launch 2", Snippet: "Snippet 2", Senders: "Maya Chen",
			Updated: updated, MessageCount: 1, LabelIDs: []string{"INBOX", "UNREAD"}, Unread: true,
		}},
	}
	for i := range pages {
		for j := range pages[i] {
			pages[i][j].Updated = pages[i][j].Updated.UTC()
		}
	}
	if !reflect.DeepEqual(pages, want) {
		t.Fatalf("pages = %+v, want %+v", pages, want)
	}
	if historyCalls != 2 {
		t.Errorf("history calls = %d, want 2", historyCalls)
	}
}

func TestHistoryCallbackError(t *testing.T) {
	callbackErr := errors.New("stop history")
	historyCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/me/history":
			historyCalls++
			writeTestJSON(t, w, map[string]any{
				"historyId":     "110",
				"nextPageToken": "page-2",
				"history": []map[string]any{{
					"messagesAdded": []map[string]any{{
						"message": map[string]string{"id": "message-1", "threadId": "thread-1"},
					}},
				}},
			})
		case "/users/me/messages/message-1":
			writeTestJSON(t, w, apiMessage{ID: "message-1", ThreadID: "thread-1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := NewWithProject(server.Client(), "project-1", nil)
	service.baseURL = server.URL

	_, err := service.History(context.Background(), 100, func([]mail.ThreadSummary) error {
		return callbackErr
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("error = %v, want %v", err, callbackErr)
	}
	if historyCalls != 1 {
		t.Errorf("history calls = %d, want 1", historyCalls)
	}
}

func TestHistoryRequiresStart(t *testing.T) {
	service := New(nil)
	called := false
	_, err := service.History(context.Background(), 0, func([]mail.ThreadSummary) error {
		called = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "start history id") {
		t.Fatalf("error = %v, want missing start history id", err)
	}
	if called {
		t.Fatal("callback called without a start history ID")
	}
}

func TestStreamAcksSuccessAndNacksFailure(t *testing.T) {
	callbackErr := errors.New("stop stream")
	var acked, nacked []uint64
	receiver := &fakeStreamReceiver{}
	receiver.receive = func(_ context.Context, subscription string, settings streamSettings, fn func(streamMessage)) error {
		if subscription != "mica-gmail-stream" {
			t.Errorf("subscription = %q", subscription)
		}
		wantSettings := streamSettings{numGoroutines: 1, maxOutstandingMessages: 10}
		if settings != wantSettings {
			t.Errorf("settings = %+v, want %+v", settings, wantSettings)
		}
		fn(streamMessage{
			data: []byte(`{"emailAddress":"alex@example.com","historyId":"123"}`),
			ack:  func() { acked = append(acked, 123) },
			nack: func() { nacked = append(nacked, 123) },
		})
		fn(streamMessage{
			data: []byte(`{"emailAddress":"alex@example.com","historyId":124}`),
			ack:  func() { acked = append(acked, 124) },
			nack: func() { nacked = append(nacked, 124) },
		})
		fn(streamMessage{
			data: []byte(`{"emailAddress":"alex@example.com","historyId":"456"}`),
			ack:  func() { acked = append(acked, 456) },
			nack: func() { nacked = append(nacked, 456) },
		})
		return nil
	}
	service := NewWithProject(nil, "project-1", pushTokenSource{})
	installStreamReceiver(t, service, receiver)

	var historyIDs []uint64
	err := service.Stream(context.Background(), func(historyID uint64) error {
		historyIDs = append(historyIDs, historyID)
		if historyID == 456 {
			return callbackErr
		}
		return nil
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("error = %v, want %v", err, callbackErr)
	}
	if !reflect.DeepEqual(historyIDs, []uint64{123, 124, 456}) {
		t.Errorf("history IDs = %v", historyIDs)
	}
	if !reflect.DeepEqual(acked, []uint64{123, 124}) {
		t.Errorf("acked = %v, want [123 124]", acked)
	}
	if !reflect.DeepEqual(nacked, []uint64{456}) {
		t.Errorf("nacked = %v, want [456]", nacked)
	}
	if receiver.closed != 1 {
		t.Errorf("close calls = %d, want 1", receiver.closed)
	}
}

func TestStreamCancellationClosesClient(t *testing.T) {
	started := make(chan struct{})
	receiver := &fakeStreamReceiver{}
	receiver.receive = func(ctx context.Context, _ string, _ streamSettings, _ func(streamMessage)) error {
		close(started)
		<-ctx.Done()
		return nil
	}
	service := NewWithProject(nil, "project-1", pushTokenSource{})
	installStreamReceiver(t, service, receiver)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- service.Stream(ctx, func(uint64) error { return nil })
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("Receive did not start")
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Stream error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stream did not stop after cancellation")
	}
	if receiver.closed != 1 {
		t.Errorf("close calls = %d, want 1", receiver.closed)
	}
}

func TestStreamRequiresTokenSource(t *testing.T) {
	service := NewWithProject(nil, "project-1", nil)
	err := service.Stream(context.Background(), func(uint64) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "token source") {
		t.Fatalf("error = %v, want missing token source", err)
	}
}

type fakeStreamReceiver struct {
	receive func(context.Context, string, streamSettings, func(streamMessage)) error
	closed  int
}

func (r *fakeStreamReceiver) Receive(ctx context.Context, subscription string, settings streamSettings, fn func(streamMessage)) error {
	return r.receive(ctx, subscription, settings, fn)
}

func (r *fakeStreamReceiver) Close() error {
	r.closed++
	return nil
}

type pushTokenSource struct{}

func (pushTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: "test-token"}, nil
}

func installStreamReceiver(t *testing.T, service *Service, receiver streamReceiver) {
	t.Helper()
	previous := newStreamReceiver
	newStreamReceiver = func(_ context.Context, projectID string, source oauth2.TokenSource) (streamReceiver, error) {
		if projectID != service.projectID {
			t.Errorf("project ID = %q, want %q", projectID, service.projectID)
		}
		if source != service.tokenSource {
			t.Error("token source was not forwarded")
		}
		return receiver, nil
	}
	t.Cleanup(func() {
		newStreamReceiver = previous
	})
}

func pubsubTestService(server *httptest.Server, projectID string) *Service {
	client := server.Client()
	transport := client.Transport
	client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "pubsub.googleapis.com" {
			return nil, fmt.Errorf("unexpected request host: %s", r.URL.Host)
		}
		request := r.Clone(r.Context())
		request.URL.Scheme = "http"
		request.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return transport.RoundTrip(request)
	})
	return NewWithProject(client, projectID, nil)
}
