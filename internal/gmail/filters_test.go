package gmail

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mica/internal/mail"
)

func TestFiltersListUsesSingularResponseAndPreservesFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/users/me/settings/filters" {
			http.NotFound(w, r)
			return
		}
		writeTestJSON(t, w, map[string]any{
			"filter": []map[string]any{{
				"id": "filter-server-1",
				"criteria": map[string]any{
					"from": "sender@example.com", "to": "recipient@example.com", "subject": "Launch",
					"query": "is:unread", "negatedQuery": "label:archive", "hasAttachment": true,
					"excludeChats": true, "size": 2097152, "sizeComparison": "larger",
				},
				"action": map[string]any{
					"addLabelIds":    []string{"Label_7", "STARRED"},
					"removeLabelIds": []string{"INBOX", "UNREAD"},
					"forward":        "archive@example.com",
				},
			}},
			"filters": []map[string]any{{"id": "wrong-plural-field"}},
		})
	}))
	defer server.Close()

	filters, err := testService(server).Filters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []mail.Filter{{
		ID: "filter-server-1",
		Criteria: mail.FilterCriteria{
			From: "sender@example.com", To: "recipient@example.com", Subject: "Launch",
			Query: "is:unread", NegatedQuery: "label:archive", HasAttachment: true,
			ExcludeChats: true, Size: 2097152, SizeComparison: "larger",
		},
		Action: mail.FilterAction{
			AddLabelIDs: []string{"Label_7", "STARRED"}, RemoveLabelIDs: []string{"INBOX", "UNREAD"},
			Forward: "archive@example.com",
		},
	}}
	if !reflect.DeepEqual(filters, want) {
		t.Fatalf("filters = %#v, want %#v", filters, want)
	}
}

func TestFiltersEmptyList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, map[string]any{})
	}))
	defer server.Close()
	filters, err := testService(server).Filters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if filters == nil || len(filters) != 0 {
		t.Fatalf("empty filters = %#v, want non-nil empty slice", filters)
	}
}

func TestCreateFilterRequestAndServerID(t *testing.T) {
	input := mail.Filter{
		ID: "local-id-must-not-be-sent",
		Criteria: mail.FilterCriteria{
			From: "Team <team@example.com>", To: "alias@example.com", Subject: "Release", Query: "has:drive",
		},
		Action: mail.FilterAction{AddLabelIDs: []string{"Label_9", "STARRED"}, RemoveLabelIDs: []string{"INBOX", "UNREAD"}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/users/me/settings/filters" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, found := body["id"]; found {
			t.Fatalf("create request contains a local ID: %v", body)
		}
		criteria := body["criteria"].(map[string]any)
		for key, want := range map[string]any{"from": "Team <team@example.com>", "to": "alias@example.com", "subject": "Release", "query": "has:drive"} {
			if criteria[key] != want {
				t.Errorf("criteria[%q] = %#v, want %#v", key, criteria[key], want)
			}
		}
		for _, key := range []string{"negatedQuery", "hasAttachment", "excludeChats", "size", "sizeComparison"} {
			if _, found := criteria[key]; found {
				t.Errorf("empty criteria field %q was sent: %v", key, criteria)
			}
		}
		action := body["action"].(map[string]any)
		if !reflect.DeepEqual(action["addLabelIds"], []any{"Label_9", "STARRED"}) || !reflect.DeepEqual(action["removeLabelIds"], []any{"INBOX", "UNREAD"}) {
			t.Errorf("action = %#v", action)
		}
		if _, found := action["forward"]; found {
			t.Errorf("empty forward was sent: %v", action)
		}
		writeTestJSON(t, w, apiFilter{ID: "filter-from-server", Criteria: bodyCriteria(criteria), Action: &apiFilterAction{
			AddLabelIDs: []string{"Label_9", "STARRED"}, RemoveLabelIDs: []string{"INBOX", "UNREAD"},
		}})
	}))
	defer server.Close()

	created, err := testService(server).CreateFilter(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "filter-from-server" || created.Criteria.Query != "has:drive" {
		t.Fatalf("created filter = %+v", created)
	}
	if input.ID != "local-id-must-not-be-sent" {
		t.Fatalf("CreateFilter mutated its input: %+v", input)
	}
}

func bodyCriteria(value map[string]any) *apiFilterCriteria {
	encoded, _ := json.Marshal(value)
	var criteria apiFilterCriteria
	_ = json.Unmarshal(encoded, &criteria)
	return &criteria
}

func TestDeleteFilterEscapesExactID(t *testing.T) {
	const id = "filter/id ?%"
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		if r.Method != http.MethodDelete {
			t.Errorf("delete method = %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	if err := testService(server).DeleteFilter(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if want := "/users/me/settings/filters/" + urlPathEscapeForTest(id); path != want {
		t.Fatalf("delete path = %q, want %q", path, want)
	}
}

func urlPathEscapeForTest(value string) string {
	replacer := strings.NewReplacer("%", "%25", "/", "%2F", " ", "%20", "?", "%3F")
	return replacer.Replace(value)
}

func TestFilterAPIErrorIsPreserved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"message":"Filter settings scope required","errors":[{"reason":"insufficientPermissions"}]}}`)
	}))
	defer server.Close()
	_, err := testService(server).CreateFilter(context.Background(), mail.Filter{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden || apiErr.Reason != "insufficientPermissions" || apiErr.Message != "Filter settings scope required" {
		t.Fatalf("create error = %#v", err)
	}
}

func TestCreateFilterDoesNotRetryTransientFailure(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/users/me/settings/filters" {
			http.NotFound(w, r)
			return
		}
		posts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"temporary failure","errors":[{"reason":"backendError"}]}}`)
	}))
	defer server.Close()
	service := testService(server)
	service.sleep = func(context.Context, time.Duration) error {
		t.Fatal("CreateFilter slept before an automatic retry")
		return nil
	}
	_, err := service.CreateFilter(context.Background(), mail.Filter{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("create error = %#v", err)
	}
	if got := posts.Load(); got != 1 {
		t.Fatalf("CreateFilter POSTs = %d, want 1", got)
	}
}

func TestFilterIDValidation(t *testing.T) {
	t.Run("create requires a server ID", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/users/me/settings/filters" {
				http.NotFound(w, r)
				return
			}
			writeTestJSON(t, w, apiFilter{})
		}))
		defer server.Close()
		if _, err := testService(server).CreateFilter(context.Background(), mail.Filter{}); err == nil {
			t.Fatal("CreateFilter accepted a response without a server ID")
		}
	})
	t.Run("delete requires an ID", func(t *testing.T) {
		requests := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()
		if err := testService(server).DeleteFilter(context.Background(), ""); err == nil {
			t.Fatal("DeleteFilter accepted an empty ID")
		}
		if requests != 0 {
			t.Fatalf("DeleteFilter sent %d requests for an empty ID", requests)
		}
	})
}
