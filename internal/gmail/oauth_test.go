package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"golang.org/x/oauth2"
)

func TestOAuthLoginLoopbackStateAndPKCE(t *testing.T) {
	store := newMemoryTokenStore()
	stateBytes := bytes.Repeat([]byte{0x11}, 32)
	verifierBytes := bytes.Repeat([]byte{0x22}, 32)
	wantState := base64.RawURLEncoding.EncodeToString(stateBytes)
	wantVerifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	wantChallenge := oauth2.S256ChallengeFromVerifier(wantVerifier)
	var tokenCalls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenCalls++
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if got := r.Form.Get("grant_type"); got != "authorization_code" {
				t.Errorf("grant_type = %q", got)
			}
			if got := r.Form.Get("code"); got != "offline-code" {
				t.Errorf("code = %q", got)
			}
			if got := r.Form.Get("code_verifier"); got != wantVerifier {
				t.Errorf("code_verifier = %q, want %q", got, wantVerifier)
			}
			if got := r.Form.Get("redirect_uri"); !strings.HasPrefix(got, "http://127.0.0.1:") || !strings.HasSuffix(got, oauthCallback) {
				t.Errorf("redirect_uri = %q", got)
			}
			writeTestJSON(t, w, map[string]any{
				"access_token":  "login-access",
				"refresh_token": "login-refresh",
				"token_type":    "Bearer",
				"expires_in":    3600,
			})
		case "/resource":
			if got := r.Header.Get("Authorization"); got != "Bearer login-access" {
				t.Errorf("Authorization = %q", got)
			}
			writeTestJSON(t, w, map[string]bool{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	auth := NewOAuthWithStore("desktop-client", "desktop-secret", store)
	auth.config.Endpoint = oauth2.Endpoint{AuthURL: "https://accounts.example/authorize", TokenURL: server.URL + "/token"}
	auth.httpClient = server.Client()
	auth.random = bytes.NewReader(append(stateBytes, verifierBytes...))
	auth.openExternal = func(rawURL string) error {
		authorization, err := url.Parse(rawURL)
		if err != nil {
			return err
		}
		query := authorization.Query()
		if query.Get("client_id") != "desktop-client" || query.Get("response_type") != "code" {
			t.Errorf("authorization query = %v", query)
		}
		if query.Get("state") != wantState {
			t.Errorf("state = %q, want %q", query.Get("state"), wantState)
		}
		if query.Get("code_challenge") != wantChallenge || query.Get("code_challenge_method") != "S256" {
			t.Errorf("PKCE query = %v", query)
		}
		if query.Get("access_type") != "offline" || query.Get("prompt") != "consent" {
			t.Errorf("offline query = %v", query)
		}
		if !strings.Contains(query.Get("scope"), "gmail.modify") || !strings.Contains(query.Get("scope"), "gmail.send") {
			t.Errorf("scope = %q", query.Get("scope"))
		}
		if !strings.Contains(query.Get("scope"), "openid") || !strings.Contains(query.Get("scope"), "profile") || !strings.Contains(query.Get("scope"), "email") {
			t.Errorf("profile scopes are missing: %q", query.Get("scope"))
		}
		if !strings.Contains(query.Get("scope"), "pubsub") {
			t.Errorf("Pub/Sub scope is missing: %q", query.Get("scope"))
		}
		callback := query.Get("redirect_uri") + "?code=offline-code&state=" + url.QueryEscape(query.Get("state"))
		resp, err := http.Get(callback)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("callback status = %s", resp.Status)
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := auth.Login(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tokenCalls != 1 {
		t.Fatalf("token calls = %d, want 1", tokenCalls)
	}
	token, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "login-access" || token.RefreshToken != "login-refresh" {
		t.Fatalf("saved token = %+v", token)
	}
	resp, err := client.Get(server.URL + "/resource")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resource status = %s", resp.Status)
	}

	restored, err := auth.Restore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	resp, err = restored.Get(server.URL + "/resource")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if err := auth.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Restore(context.Background()); !errors.Is(err, ErrNoSession) {
		t.Fatalf("Restore after Logout error = %v", err)
	}
}

func TestOAuthRejectsStateMismatch(t *testing.T) {
	store := newMemoryTokenStore()
	var tokenCalls int
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls++
		writeTestJSON(t, w, map[string]string{"access_token": "must-not-be-used"})
	}))
	defer tokenServer.Close()
	auth := NewOAuthWithStore("desktop-client", "", store)
	auth.config.Endpoint.TokenURL = tokenServer.URL
	auth.httpClient = tokenServer.Client()
	auth.openExternal = func(rawURL string) error {
		authorization, err := url.Parse(rawURL)
		if err != nil {
			return err
		}
		callback := authorization.Query().Get("redirect_uri") + "?code=untrusted-code&state=wrong-state"
		resp, err := http.Get(callback)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("callback status = %s", resp.Status)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := auth.Login(ctx); err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("Login error = %v", err)
	}
	if tokenCalls != 0 {
		t.Fatalf("token endpoint called %d times", tokenCalls)
	}
	if _, err := store.Load(); !errors.Is(err, ErrNoSession) {
		t.Fatalf("saved token after state mismatch: %v", err)
	}
}

func TestOAuthRestoreRefreshesAndPersistsToken(t *testing.T) {
	store := newMemoryTokenStore()
	store.token = &oauth2.Token{
		AccessToken:  "expired-access",
		RefreshToken: "saved-refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(-time.Hour),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if got := r.Form.Get("grant_type"); got != "refresh_token" {
				t.Errorf("grant_type = %q", got)
			}
			if got := r.Form.Get("refresh_token"); got != "saved-refresh" {
				t.Errorf("refresh_token = %q", got)
			}
			writeTestJSON(t, w, map[string]any{
				"access_token": "refreshed-access",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		case "/resource":
			if got := r.Header.Get("Authorization"); got != "Bearer refreshed-access" {
				t.Errorf("Authorization = %q", got)
			}
			_, _ = io.WriteString(w, "ok")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	auth := NewOAuthWithStore("desktop-client", "desktop-secret", store)
	auth.config.Endpoint.TokenURL = server.URL + "/token"
	auth.httpClient = server.Client()
	client, err := auth.Restore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get(server.URL + "/resource")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	token, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "refreshed-access" || token.RefreshToken != "saved-refresh" {
		t.Fatalf("refreshed token = %+v", token)
	}
}

func TestKeychainTokenStoreEncoding(t *testing.T) {
	secrets := newMemorySecrets()
	store := &keychainTokenStore{secrets: secrets}
	want := &oauth2.Token{AccessToken: "access", RefreshToken: "refresh", TokenType: "Bearer", Expiry: time.Unix(1234, 0)}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken || !got.Expiry.Equal(want.Expiry) {
		t.Fatalf("token = %+v, want %+v", got, want)
	}
	if err := store.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrNoSession) {
		t.Fatalf("Load after Delete error = %v", err)
	}
}

type memoryTokenStore struct {
	mu    sync.Mutex
	token *oauth2.Token
}

func newMemoryTokenStore() *memoryTokenStore {
	return &memoryTokenStore{}
}

func (s *memoryTokenStore) Load() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token == nil {
		return nil, ErrNoSession
	}
	token := *s.token
	return &token, nil
}

func (s *memoryTokenStore) Save(token *oauth2.Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *token
	s.token = &copy
	return nil
}

func (s *memoryTokenStore) Delete() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = nil
	return nil
}

type memorySecrets struct {
	mu     sync.Mutex
	values map[string][]byte
}

func newMemorySecrets() *memorySecrets {
	return &memorySecrets{values: make(map[string][]byte)}
}

func (s *memorySecrets) Get(key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[key]
	if !ok {
		return nil, mygo.ErrSecretNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *memorySecrets) Set(key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = append([]byte(nil), value...)
	return nil
}

func (s *memorySecrets) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	return nil
}
