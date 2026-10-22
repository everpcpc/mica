package gmail

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"golang.org/x/oauth2"
)

const (
	googleAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL = "https://oauth2.googleapis.com/token"
	oauthTokenKey  = "gmail-oauth2-token"
	oauthCallback  = "/oauth/callback"
)

var ErrNoSession = errors.New("gmail: no saved OAuth session")

type TokenStore interface {
	Load() (*oauth2.Token, error)
	Save(*oauth2.Token) error
	Delete() error
}

type OAuth struct {
	config       oauth2.Config
	store        TokenStore
	openExternal func(string) error
	httpClient   *http.Client
	random       io.Reader
	loginMu      sync.Mutex
}

type secretStore interface {
	Get(string) ([]byte, error)
	Set(string, []byte) error
	Delete(string) error
}

type keychainTokenStore struct {
	secrets secretStore
}

func NewOAuth(clientID, clientSecret string) *OAuth {
	return NewOAuthWithStore(clientID, clientSecret, &keychainTokenStore{secrets: mygo.Secrets})
}

func NewOAuthWithStore(clientID, clientSecret string, store TokenStore) *OAuth {
	if store == nil {
		store = &keychainTokenStore{secrets: mygo.Secrets}
	}
	return &OAuth{
		config: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Scopes: []string{
				"https://www.googleapis.com/auth/gmail.modify",
				"https://www.googleapis.com/auth/gmail.send",
			},
			Endpoint: oauth2.Endpoint{AuthURL: googleAuthURL, TokenURL: googleTokenURL},
		},
		store:        store,
		openExternal: mygo.Shell.OpenExternal,
		httpClient:   http.DefaultClient,
		random:       rand.Reader,
	}
}

func (s *keychainTokenStore) Load() (*oauth2.Token, error) {
	value, err := s.secrets.Get(oauthTokenKey)
	if errors.Is(err, mygo.ErrSecretNotFound) {
		return nil, ErrNoSession
	}
	if err != nil {
		return nil, fmt.Errorf("gmail: read OAuth token: %w", err)
	}
	var token oauth2.Token
	if err := json.Unmarshal(value, &token); err != nil {
		return nil, fmt.Errorf("gmail: decode OAuth token: %w", err)
	}
	return &token, nil
}

func (s *keychainTokenStore) Save(token *oauth2.Token) error {
	value, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("gmail: encode OAuth token: %w", err)
	}
	if err := s.secrets.Set(oauthTokenKey, value); err != nil {
		return fmt.Errorf("gmail: save OAuth token: %w", err)
	}
	return nil
}

func (s *keychainTokenStore) Delete() error {
	if err := s.secrets.Delete(oauthTokenKey); err != nil {
		return fmt.Errorf("gmail: delete OAuth token: %w", err)
	}
	return nil
}

func (a *OAuth) Restore(ctx context.Context) (*http.Client, error) {
	token, err := a.store.Load()
	if err != nil {
		return nil, err
	}
	if token == nil || (token.AccessToken == "" && token.RefreshToken == "") {
		return nil, ErrNoSession
	}
	return a.authenticatedClient(ctx, token), nil
}

func (a *OAuth) Login(ctx context.Context) (*http.Client, error) {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	if a.config.ClientID == "" {
		return nil, errors.New("gmail: OAuth client ID is required")
	}
	state, err := a.randomValue()
	if err != nil {
		return nil, err
	}
	verifier, err := a.randomValue()
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("gmail: listen for OAuth callback: %w", err)
	}
	config := a.config
	config.RedirectURL = "http://" + listener.Addr().String() + oauthCallback

	type callbackResult struct {
		code string
		err  error
	}
	results := make(chan callbackResult, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != oauthCallback {
			http.NotFound(w, r)
			return
		}
		query := r.URL.Query()
		result := callbackResult{}
		switch {
		case subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(state)) != 1:
			result.err = errors.New("gmail: OAuth state mismatch")
			http.Error(w, "OAuth state mismatch", http.StatusBadRequest)
		case query.Get("error") != "":
			result.err = errors.New("gmail: OAuth authorization was denied")
			http.Error(w, "OAuth authorization was denied", http.StatusBadRequest)
		case query.Get("code") == "":
			result.err = errors.New("gmail: OAuth callback has no authorization code")
			http.Error(w, "OAuth callback has no authorization code", http.StatusBadRequest)
		default:
			result.code = query.Get("code")
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "Mica sign-in complete. You can return to Mica.\n")
		}
		select {
		case results <- result:
		default:
		}
	})
	server := &http.Server{Handler: handler}
	serveDone := make(chan struct{})
	go func() {
		_ = server.Serve(listener)
		close(serveDone)
	}()
	defer func() {
		_ = server.Close()
		<-serveDone
	}()

	authorizationURL := config.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.ApprovalForce,
		oauth2.S256ChallengeOption(verifier),
	)
	if a.openExternal == nil {
		return nil, errors.New("gmail: no external URL opener")
	}
	if err := a.openExternal(authorizationURL); err != nil {
		return nil, fmt.Errorf("gmail: open OAuth authorization: %w", err)
	}

	var code string
	select {
	case result := <-results:
		if result.err != nil {
			return nil, result.err
		}
		code = result.code
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-serveDone:
		return nil, errors.New("gmail: OAuth callback server stopped")
	}
	token, err := config.Exchange(a.httpContext(ctx), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("gmail: exchange OAuth code: %w", err)
	}
	if err := a.store.Save(token); err != nil {
		return nil, err
	}
	return a.authenticatedClient(ctx, token), nil
}

func (a *OAuth) Logout(context.Context) error {
	return a.store.Delete()
}

func (a *OAuth) randomValue() (string, error) {
	value := make([]byte, 32)
	if _, err := io.ReadFull(a.random, value); err != nil {
		return "", fmt.Errorf("gmail: generate OAuth random value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func (a *OAuth) httpContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, a.httpClient)
}

func (a *OAuth) authenticatedClient(ctx context.Context, token *oauth2.Token) *http.Client {
	ctx = context.WithoutCancel(a.httpContext(ctx))
	source := &savingTokenSource{
		source:  a.config.TokenSource(ctx, token),
		store:   a.store,
		access:  token.AccessToken,
		refresh: token.RefreshToken,
		expiry:  token.Expiry,
	}
	return oauth2.NewClient(ctx, source)
}

type savingTokenSource struct {
	mu      sync.Mutex
	source  oauth2.TokenSource
	store   TokenStore
	access  string
	refresh string
	expiry  time.Time
}

func (s *savingTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, err := s.source.Token()
	if err != nil {
		return nil, err
	}
	if token.RefreshToken == "" {
		token.RefreshToken = s.refresh
	}
	if token.AccessToken == s.access && token.RefreshToken == s.refresh && token.Expiry.Equal(s.expiry) {
		return token, nil
	}
	if err := s.store.Save(token); err != nil {
		return nil, err
	}
	s.access = token.AccessToken
	s.refresh = token.RefreshToken
	s.expiry = token.Expiry
	return token, nil
}
