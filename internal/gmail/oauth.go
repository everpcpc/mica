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
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"golang.org/x/oauth2"
)

const (
	googleAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL = "https://oauth2.googleapis.com/token"
	oauthTokenKey  = "gmail-oauth2-token"
	activeEmailKey = "gmail-active-account"
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
	projectID    string
	loginMu      sync.Mutex
}

type secretStore interface {
	Get(string) ([]byte, error)
	Set(string, []byte) error
	Delete(string) error
}

type credentialState struct {
	mu     sync.RWMutex
	closed bool
}

var credentialStates sync.Map

func credentialStateFor(key string) *credentialState {
	state, _ := credentialStates.LoadOrStore(key, &credentialState{})
	return state.(*credentialState)
}

func accountTokenKey(email string) string {
	return oauthTokenKey + ":" + strings.ToLower(strings.TrimSpace(email))
}

var credentialLifecycle struct {
	mu         sync.RWMutex
	generation uint64
}

func CredentialGeneration() uint64 {
	credentialLifecycle.mu.RLock()
	defer credentialLifecycle.mu.RUnlock()
	return credentialLifecycle.generation
}

func InvalidateCredentials() {
	credentialLifecycle.mu.Lock()
	credentialLifecycle.generation++
	credentialLifecycle.mu.Unlock()
}

func AllowAccount(email string, generation uint64) bool {
	credentialLifecycle.mu.Lock()
	defer credentialLifecycle.mu.Unlock()
	if generation != credentialLifecycle.generation {
		return false
	}
	credentialStateFor(accountTokenKey(email)).allow()
	credentialStateFor(activeEmailKey).allow()
	return true
}

func (s *credentialState) allow() {
	s.mu.Lock()
	s.closed = false
	s.mu.Unlock()
}

type keychainTokenStore struct {
	secrets secretStore
	key     string
}

func NewOAuth(clientID, clientSecret string) *OAuth {
	return NewOAuthWithStore(clientID, clientSecret, &keychainTokenStore{secrets: mygo.Secrets})
}

func NewOAuthWithProject(clientID, clientSecret, projectID string) *OAuth {
	return NewOAuthWithProjectAndStore(clientID, clientSecret, projectID, &keychainTokenStore{secrets: mygo.Secrets})
}

func NewOAuthForAccount(clientID, clientSecret, projectID, email string) *OAuth {
	return NewOAuthWithProjectAndStore(clientID, clientSecret, projectID, &keychainTokenStore{secrets: mygo.Secrets, key: accountTokenKey(email)})
}

func NewOAuthWithProjectAndStore(clientID, clientSecret, projectID string, store TokenStore) *OAuth {
	a := NewOAuthWithStore(clientID, clientSecret, store)
	a.projectID = projectID
	return a
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
				"openid",
				"profile",
				"email",
				"https://www.googleapis.com/auth/gmail.modify",
				"https://www.googleapis.com/auth/gmail.send",
				"https://www.googleapis.com/auth/pubsub",
			},
			Endpoint: oauth2.Endpoint{AuthURL: googleAuthURL, TokenURL: googleTokenURL},
		},
		store:        store,
		openExternal: mygo.Shell.OpenExternal,
		httpClient:   http.DefaultClient,
		random:       rand.Reader,
		projectID:    EmbeddedProjectID(),
	}
}

func (s *keychainTokenStore) tokenKey() string {
	if s.key != "" {
		return s.key
	}
	return oauthTokenKey
}

func (s *keychainTokenStore) Load() (*oauth2.Token, error) {
	state := credentialStateFor(s.tokenKey())
	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.closed {
		return nil, ErrNoSession
	}
	value, err := s.secrets.Get(s.tokenKey())
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
	state := credentialStateFor(s.tokenKey())
	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.closed {
		return errors.New("gmail: account is signed out")
	}
	value, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("gmail: encode OAuth token: %w", err)
	}
	if err := s.secrets.Set(s.tokenKey(), value); err != nil {
		return fmt.Errorf("gmail: save OAuth token: %w", err)
	}
	return nil
}

func (s *keychainTokenStore) Delete() error {
	state := credentialStateFor(s.tokenKey())
	state.mu.Lock()
	defer state.mu.Unlock()
	state.closed = true
	if err := s.secrets.Delete(s.tokenKey()); err != nil {
		return fmt.Errorf("gmail: delete OAuth token: %w", err)
	}
	return nil
}

type activeAccountStore struct {
	secrets secretStore
}

func NewActiveAccountStore() *activeAccountStore {
	return &activeAccountStore{secrets: mygo.Secrets}
}

func (s *activeAccountStore) Load() (string, error) {
	state := credentialStateFor(activeEmailKey)
	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.closed {
		return "", nil
	}
	value, err := s.secrets.Get(activeEmailKey)
	if errors.Is(err, mygo.ErrSecretNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("gmail: read active account: %w", err)
	}
	return string(value), nil
}

func (s *activeAccountStore) Save(email string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return errors.New("gmail: empty account email")
	}
	state := credentialStateFor(activeEmailKey)
	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.closed {
		return errors.New("gmail: account is signed out")
	}
	if err := s.secrets.Set(activeEmailKey, []byte(email)); err != nil {
		return fmt.Errorf("gmail: save active account: %w", err)
	}
	return nil
}

func (s *activeAccountStore) Delete() error {
	state := credentialStateFor(activeEmailKey)
	state.mu.Lock()
	defer state.mu.Unlock()
	state.closed = true
	if err := s.secrets.Delete(activeEmailKey); err != nil {
		return fmt.Errorf("gmail: delete active account: %w", err)
	}
	return nil
}

func (a *OAuth) SaveToken(token *oauth2.Token) error {
	return a.store.Save(token)
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
	return oauth2.NewClient(ctx, a.tokenSource(ctx, token))
}

func (a *OAuth) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	token, err := a.store.Load()
	if err != nil {
		return nil, err
	}
	if token == nil || (token.AccessToken == "" && token.RefreshToken == "") {
		return nil, ErrNoSession
	}
	return a.tokenSource(context.WithoutCancel(a.httpContext(ctx)), token), nil
}

func (a *OAuth) Service(client *http.Client) (*Service, error) {
	source, err := a.TokenSource(context.Background())
	if err != nil {
		return nil, err
	}
	return NewWithProject(client, a.projectID, source), nil
}

func (a *OAuth) tokenSource(ctx context.Context, token *oauth2.Token) oauth2.TokenSource {
	return &savingTokenSource{
		source:  a.config.TokenSource(ctx, token),
		store:   a.store,
		access:  token.AccessToken,
		refresh: token.RefreshToken,
		expiry:  token.Expiry,
	}
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
