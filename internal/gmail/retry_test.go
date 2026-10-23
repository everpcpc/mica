package gmail

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mica/internal/mail"
)

func TestDoJSONRetriesRateLimitWithRetryAfter(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/me/profile" {
			if calls.Add(1) < 3 {
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(http.StatusForbidden)
				writeTestJSON(t, w, map[string]any{"error": map[string]any{"message": "slow down", "errors": []map[string]string{{"reason": "rateLimitExceeded"}}}})
				return
			}
			writeTestJSON(t, w, map[string]string{"emailAddress": "alex@example.com"})
			return
		}
		if r.URL.Path == "/userinfo" {
			writeTestJSON(t, w, map[string]string{"name": "Alex"})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	service := testService(server)
	service.userinfoURL = server.URL + "/userinfo"
	var delays []time.Duration
	service.sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}
	account, err := service.Account(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if account.Email != "alex@example.com" || calls.Load() != 3 {
		t.Fatalf("account = %+v, calls = %d, want success after two retries", account, calls.Load())
	}
	if len(delays) != 2 || delays[0] != 2*time.Second || delays[1] != 2*time.Second {
		t.Fatalf("retry delays = %v, want two Retry-After delays", delays)
	}
}

func TestDoJSONDoesNotRetryOtherForbidden(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusForbidden)
		writeTestJSON(t, w, map[string]any{"error": map[string]any{"message": "forbidden", "errors": []map[string]string{{"reason": "forbidden"}}}})
	}))
	defer server.Close()
	service := testService(server)
	var sleeps atomic.Int32
	service.sleep = func(context.Context, time.Duration) error {
		sleeps.Add(1)
		return nil
	}
	_, err := service.Account(context.Background())
	if err == nil || calls.Load() != 1 || sleeps.Load() != 0 {
		t.Fatalf("error = %v, calls = %d, sleeps = %d, want one failed attempt", err, calls.Load(), sleeps.Load())
	}
}

func TestDoJSONStopsAfterRetryLimit(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	service := testService(server)
	service.sleep = func(context.Context, time.Duration) error { return nil }
	_, err := service.Account(context.Background())
	if err == nil || calls.Load() != maxAttempts {
		t.Fatalf("error = %v, calls = %d, want %d attempts", err, calls.Load(), maxAttempts)
	}
}

func TestLabelsDeduplicatesConcurrentRequests(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		once.Do(func() { close(started) })
		<-release
		writeTestJSON(t, w, map[string]any{"labels": []map[string]any{{"id": "INBOX", "name": "Inbox", "type": "system", "messagesTotal": 1, "messagesUnread": 1}}})
	}))
	defer server.Close()
	service := testService(server)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := service.Labels(context.Background())
		errs <- err
	}()
	<-started
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := service.Labels(context.Background())
		errs <- err
	}()
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("label requests = %d, want one shared request", calls.Load())
	}
}

func TestListThreadsDeduplicatesConcurrentRequests(t *testing.T) {
	var listCalls atomic.Int32
	var metadataCalls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/me/threads":
			listCalls.Add(1)
			once.Do(func() { close(started) })
			<-release
			writeTestJSON(t, w, map[string]any{"threads": []map[string]string{{"id": "thread-1"}}})
		case "/users/me/threads/thread-1":
			metadataCalls.Add(1)
			writeTestJSON(t, w, apiThread{ID: "thread-1", Messages: []apiMessage{{
				ID: "message-1", ThreadID: "thread-1",
				Payload: apiPart{MIMEType: "text/plain", Body: apiBody{Data: base64.RawURLEncoding.EncodeToString([]byte("Body"))}},
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := testService(server)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := service.ListThreads(context.Background(), mail.Query{LabelID: mail.Inbox, Limit: 1})
		errs <- err
	}()
	<-started
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := service.ListThreads(context.Background(), mail.Query{LabelID: mail.Inbox, Limit: 1})
		errs <- err
	}()
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if listCalls.Load() != 1 || metadataCalls.Load() != 1 {
		t.Fatalf("list = %d, metadata = %d, want shared requests", listCalls.Load(), metadataCalls.Load())
	}
}
