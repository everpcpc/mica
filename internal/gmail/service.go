package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mica/internal/mail"
	"net/http"
	stdmail "net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/sync/singleflight"
)

const (
	gmailAPIURL       = "https://gmail.googleapis.com/gmail/v1"
	googleUserinfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
	threadListSize    = 50
	listParallel      = 4
)

type Service struct {
	client       *http.Client
	baseURL      string
	userinfoURL  string
	userID       string
	maxParallel  int
	projectID    string
	tokenSource  oauth2.TokenSource
	deactivate   func()
	sleep        func(context.Context, time.Duration) error
	requestGroup singleflight.Group
}

type APIError struct {
	StatusCode int
	Reason     string
	Message    string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = http.StatusText(e.StatusCode)
	}
	if e.Reason != "" {
		return fmt.Sprintf("gmail: %s (%s)", message, e.Reason)
	}
	return fmt.Sprintf("gmail: %s (status %d)", message, e.StatusCode)
}

type apiThread struct {
	ID       string       `json:"id"`
	Snippet  string       `json:"snippet"`
	Messages []apiMessage `json:"messages"`
}

type apiMessage struct {
	ID           string   `json:"id"`
	ThreadID     string   `json:"threadId"`
	Snippet      string   `json:"snippet"`
	LabelIDs     []string `json:"labelIds"`
	InternalDate string   `json:"internalDate"`
	Payload      apiPart  `json:"payload"`
}

type apiPart struct {
	PartID   string      `json:"partId"`
	MIMEType string      `json:"mimeType"`
	Filename string      `json:"filename"`
	Headers  []apiHeader `json:"headers"`
	Body     apiBody     `json:"body"`
	Parts    []apiPart   `json:"parts"`
}

type apiHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type apiBody struct {
	AttachmentID string `json:"attachmentId"`
	Size         int64  `json:"size"`
	Data         string `json:"data"`
}

type apiLabelColor struct {
	BackgroundColor string `json:"backgroundColor"`
	TextColor       string `json:"textColor"`
}

type apiLabel struct {
	ID                  string         `json:"id"`
	Name                string         `json:"name"`
	Type                string         `json:"type"`
	LabelListVisibility string         `json:"labelListVisibility"`
	MessagesTotal       int            `json:"messagesTotal"`
	MessagesUnread      int            `json:"messagesUnread"`
	Color               *apiLabelColor `json:"color"`
}

func New(client *http.Client) *Service {
	return NewWithProject(client, EmbeddedProjectID(), nil)
}

func NewWithProject(client *http.Client, projectID string, source oauth2.TokenSource) *Service {
	if client == nil {
		client = http.DefaultClient
	}
	return &Service{client: client, baseURL: gmailAPIURL, userinfoURL: googleUserinfoURL, userID: "me", maxParallel: listParallel, projectID: projectID, tokenSource: source, sleep: sleepContext}
}

func (s *Service) Deactivate() {
	if s.deactivate != nil {
		s.deactivate()
		return
	}
	if source, ok := s.tokenSource.(interface{ Deactivate() }); ok {
		source.Deactivate()
	}
}

func (s *Service) Account(ctx context.Context) (mail.Account, error) {
	var profile struct {
		EmailAddress string `json:"emailAddress"`
	}
	if err := s.doJSON(ctx, http.MethodGet, "/profile", nil, nil, &profile); err != nil {
		return mail.Account{}, err
	}
	if profile.EmailAddress == "" {
		return mail.Account{}, errors.New("gmail: profile has no email address")
	}
	account := mail.Account{Name: profile.EmailAddress, Email: profile.EmailAddress}
	if s.userinfoURL == "" {
		return account, nil
	}
	var userinfo struct {
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	if err := s.doJSON(ctx, http.MethodGet, s.userinfoURL, nil, nil, &userinfo); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden) {
			account.ProfileUnauthorized = true
		}
		return account, nil
	}
	if userinfo.Name != "" {
		account.Name = userinfo.Name
	}
	account.PhotoURL = userinfo.Picture
	return account, nil
}

func (s *Service) Labels(ctx context.Context) ([]mail.Label, error) {
	value, err, _ := s.requestGroup.Do("labels", func() (any, error) {
		return s.listLabels(ctx)
	})
	if err != nil {
		return nil, err
	}
	return value.([]mail.Label), nil
}

func (s *Service) listLabels(ctx context.Context) ([]mail.Label, error) {
	var response struct {
		Labels []apiLabel `json:"labels"`
	}
	if err := s.doJSON(ctx, http.MethodGet, "/labels", nil, nil, &response); err != nil {
		return nil, err
	}
	labels := make([]mail.Label, len(response.Labels))
	for i, item := range response.Labels {
		labels[i] = makeLabel(item)
	}
	return labels, nil
}

func (s *Service) Label(ctx context.Context, id string) (mail.Label, error) {
	var detail apiLabel
	if err := s.doJSON(ctx, http.MethodGet, "/labels/"+url.PathEscape(id), nil, nil, &detail); err != nil {
		return mail.Label{}, err
	}
	return makeLabel(detail), nil
}

func (s *Service) CreateLabel(ctx context.Context, name string) (mail.Label, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return mail.Label{}, errors.New("gmail: empty label name")
	}
	body := struct {
		Name                  string `json:"name"`
		LabelListVisibility   string `json:"labelListVisibility"`
		MessageListVisibility string `json:"messageListVisibility"`
	}{Name: name, LabelListVisibility: "labelShow", MessageListVisibility: "labelShow"}
	var detail apiLabel
	if err := s.doJSON(ctx, http.MethodPost, "/labels", nil, body, &detail); err != nil {
		return mail.Label{}, err
	}
	return makeLabel(detail), nil
}

func (s *Service) DeleteLabel(ctx context.Context, id string) error {
	return s.doJSON(ctx, http.MethodDelete, "/labels/"+url.PathEscape(id), nil, nil, nil)
}

func (s *Service) UpdateLabelColor(ctx context.Context, id string, color mail.LabelColor) error {
	if id == "" {
		return errors.New("gmail: empty label ID")
	}
	body := struct {
		Color apiLabelColor `json:"color"`
	}{Color: apiLabelColor{BackgroundColor: color.Background, TextColor: color.Text}}
	return s.doJSON(ctx, http.MethodPatch, "/labels/"+url.PathEscape(id), nil, body, nil)
}

func makeLabel(detail apiLabel) mail.Label {
	label := mail.Label{
		ID: detail.ID, Name: detail.Name, Messages: detail.MessagesTotal, Unread: detail.MessagesUnread,
		System: detail.Type == "system", Hide: detail.LabelListVisibility == "labelHide",
		ShowIfUnread: detail.LabelListVisibility == "labelShowIfUnread",
	}
	if detail.Color != nil {
		label.Color = mail.LabelColor{Background: detail.Color.BackgroundColor, Text: detail.Color.TextColor}
	}
	return label
}

func (s *Service) Attachment(ctx context.Context, messageID, attachmentID string) ([]byte, error) {
	if messageID == "" || attachmentID == "" {
		return nil, errors.New("gmail: empty message or attachment ID")
	}
	var response apiBody
	path := "/messages/" + url.PathEscape(messageID) + "/attachments/" + url.PathEscape(attachmentID)
	if err := s.doJSON(ctx, http.MethodGet, path, nil, nil, &response); err != nil {
		return nil, err
	}
	data, err := decodeBase64URL(response.Data)
	if err != nil {
		return nil, fmt.Errorf("gmail: decode attachment: %w", err)
	}
	return data, nil
}

func (s *Service) ListThreads(ctx context.Context, query mail.Query) ([]mail.ThreadSummary, error) {
	page, err := s.ListThreadPage(ctx, query)
	if err != nil {
		return nil, err
	}
	return page.Threads, nil
}

func (s *Service) ListThreadPage(ctx context.Context, query mail.Query) (mail.ThreadPage, error) {
	key := fmt.Sprintf("threads:%v", query)
	value, err, _ := s.requestGroup.Do(key, func() (any, error) {
		return s.listThreadPage(ctx, query)
	})
	if err != nil {
		return mail.ThreadPage{}, err
	}
	return value.(mail.ThreadPage), nil
}

func (s *Service) listThreadPage(ctx context.Context, query mail.Query) (mail.ThreadPage, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = threadListSize
	}
	var ids []string
	pageToken := query.PageToken
	nextPageToken := ""
	for len(ids) < limit {
		values := make(url.Values)
		values.Set("maxResults", strconv.Itoa(min(500, limit-len(ids))))
		if query.LabelID != "" {
			values.Set("labelIds", query.LabelID)
		}
		if query.Search != "" {
			values.Set("q", query.Search)
		}
		if pageToken != "" {
			values.Set("pageToken", pageToken)
		}
		var page struct {
			Threads []struct {
				ID string `json:"id"`
			} `json:"threads"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := s.doJSON(ctx, http.MethodGet, "/threads", values, nil, &page); err != nil {
			return mail.ThreadPage{}, err
		}
		for _, thread := range page.Threads {
			if thread.ID != "" {
				ids = append(ids, thread.ID)
			}
		}
		nextPageToken = page.NextPageToken
		if nextPageToken == "" || nextPageToken == pageToken {
			nextPageToken = ""
			break
		}
		pageToken = nextPageToken
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	if len(ids) == 0 {
		return mail.ThreadPage{Threads: []mail.ThreadSummary{}, NextPageToken: nextPageToken}, nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	summaries := make([]mail.ThreadSummary, len(ids))
	semaphore := make(chan struct{}, max(1, s.maxParallel))
	var wg sync.WaitGroup
	var errorOnce sync.Once
	var firstErr error
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}
			thread, err := s.getThread(ctx, id, "metadata")
			if err != nil {
				errorOnce.Do(func() {
					firstErr = err
					cancel()
				})
				return
			}
			summaries[i] = makeSummary(thread)
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return mail.ThreadPage{}, firstErr
	}
	if err := ctx.Err(); err != nil {
		return mail.ThreadPage{}, err
	}
	return mail.ThreadPage{Threads: summaries, NextPageToken: nextPageToken}, nil
}

func (s *Service) Thread(ctx context.Context, id string) (mail.Thread, error) {
	_, thread, err := s.ThreadState(ctx, id)
	return thread, err
}

type threadStateResult struct {
	summary mail.ThreadSummary
	thread  mail.Thread
}

func (s *Service) ThreadState(ctx context.Context, id string) (mail.ThreadSummary, mail.Thread, error) {
	value, err, _ := s.requestGroup.Do("thread:"+id, func() (any, error) {
		return s.threadState(ctx, id)
	})
	if err != nil {
		return mail.ThreadSummary{}, mail.Thread{}, err
	}
	result := value.(threadStateResult)
	return result.summary, result.thread, nil
}

func (s *Service) threadState(ctx context.Context, id string) (threadStateResult, error) {
	if id == "" {
		return threadStateResult{}, errors.New("gmail: empty thread ID")
	}
	apiThread, err := s.getThread(ctx, id, "full")
	if err != nil {
		return threadStateResult{}, err
	}
	thread, err := makeThread(apiThread)
	if err != nil {
		return threadStateResult{}, err
	}
	return threadStateResult{summary: makeSummary(apiThread), thread: thread}, nil
}

func (s *Service) getThread(ctx context.Context, id, format string) (apiThread, error) {
	values := url.Values{"format": []string{format}}
	if format == "metadata" {
		for _, name := range []string{"Subject", "From", "Date"} {
			values.Add("metadataHeaders", name)
		}
	}
	var thread apiThread
	err := s.doJSON(ctx, http.MethodGet, "/threads/"+url.PathEscape(id), values, nil, &thread)
	return thread, err
}

func (s *Service) ModifyThread(ctx context.Context, id string, add, remove []string) error {
	if id == "" {
		return errors.New("gmail: empty thread ID")
	}
	body := struct {
		Add    []string `json:"addLabelIds"`
		Remove []string `json:"removeLabelIds"`
	}{Add: nonNil(add), Remove: nonNil(remove)}
	return s.doJSON(ctx, http.MethodPost, "/threads/"+url.PathEscape(id)+"/modify", nil, body, nil)
}

func (s *Service) TrashThread(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("gmail: empty thread ID")
	}
	return s.doJSON(ctx, http.MethodPost, "/threads/"+url.PathEscape(id)+"/trash", nil, nil, nil)
}

func (s *Service) UntrashThread(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("gmail: empty thread ID")
	}
	return s.doJSON(ctx, http.MethodPost, "/threads/"+url.PathEscape(id)+"/untrash", nil, nil, nil)
}

func (s *Service) Send(ctx context.Context, compose mail.Compose) error {
	if err := mail.ValidateComposeForSend(compose); err != nil {
		return err
	}
	if compose.ThreadID != "" && !compose.ThreadingResolved && (compose.InReplyTo == "" || compose.References == "") {
		thread, err := s.Thread(ctx, compose.ThreadID)
		if err != nil {
			return err
		}
		for i := len(thread.Messages) - 1; i >= 0; i-- {
			last := thread.Messages[i]
			if last.MessageID == "" {
				continue
			}
			if compose.InReplyTo == "" {
				compose.InReplyTo = last.MessageID
			}
			if compose.References == "" {
				compose.References = joinReferences(last.References, last.MessageID)
			}
			break
		}
	}
	if compose.InReplyTo != "" && compose.References == "" {
		compose.References = compose.InReplyTo
	}
	raw, err := buildRFC5322(compose)
	if err != nil {
		return err
	}
	body := struct {
		Raw      string `json:"raw"`
		ThreadID string `json:"threadId,omitempty"`
	}{Raw: base64.RawURLEncoding.EncodeToString(raw), ThreadID: compose.ThreadID}
	return s.doJSON(ctx, http.MethodPost, "/messages/send", nil, body, nil)
}

func (s *Service) doJSON(ctx context.Context, method, path string, query url.Values, body, result any) error {
	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("gmail: encode request: %w", err)
		}
		payload = encoded
	}
	requestURL := path
	if !strings.HasPrefix(path, "https://") && !strings.HasPrefix(path, "http://") {
		requestURL = strings.TrimRight(s.baseURL, "/") + "/users/" + url.PathEscape(s.userID) + path
	}
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}
	sleep := s.sleep
	if sleep == nil {
		sleep = sleepContext
	}
	attempts := maxAttempts
	if path == "/messages/send" || path == "/drafts/send" {
		// Send endpoints are not idempotent; the caller must reconcile response failures.
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("gmail: create request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json; charset=utf-8")
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return fmt.Errorf("gmail: request failed: %w", err)
		}
		success, err := s.doJSONAttempt(resp, result)
		if success || err == nil {
			return err
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || !apiErr.Transient() || attempt == attempts {
			return err
		}
		if err := sleep(ctx, retryDelay(attempt, apiErr.RetryAfter)); err != nil {
			return err
		}
	}
	return errors.New("gmail: retry loop exhausted")
}

func (s *Service) doJSONAttempt(resp *http.Response, result any) (bool, error) {
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, readAPIError(resp)
	}
	if result == nil || resp.StatusCode == http.StatusNoContent {
		return true, nil
	}
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return true, fmt.Errorf("gmail: decode response: %w", err)
	}
	return true, nil
}

func readAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var response struct {
		Error struct {
			Message string `json:"message"`
			Errors  []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &response)
	apiErr := &APIError{StatusCode: resp.StatusCode, Message: response.Error.Message}
	if len(response.Error.Errors) > 0 {
		apiErr.Reason = response.Error.Errors[0].Reason
	}
	apiErr.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
	return apiErr
}

func makeSummary(thread apiThread) mail.ThreadSummary {
	summary := mail.ThreadSummary{
		ID:           thread.ID,
		Snippet:      thread.Snippet,
		MessageCount: len(thread.Messages),
		LabelIDs:     messageLabels(thread.Messages),
	}
	summary.Unread = contains(summary.LabelIDs, mail.Unread)
	summary.Starred = contains(summary.LabelIDs, mail.Starred)
	var senders []string
	for _, message := range thread.Messages {
		if summary.Subject == "" {
			summary.Subject = decodedHeader(message.Payload, "Subject")
		}
		if date := messageTime(message); date.After(summary.Updated) {
			summary.Updated = date
		}
		sender := displayName(decodedHeader(message.Payload, "From"))
		if sender != "" && !contains(senders, sender) {
			senders = append(senders, sender)
		}
	}
	if summary.Snippet == "" && len(thread.Messages) > 0 {
		summary.Snippet = thread.Messages[len(thread.Messages)-1].Snippet
	}
	summary.Senders = strings.Join(senders, ", ")
	return summary
}

func makeThread(thread apiThread) (mail.Thread, error) {
	result := mail.Thread{ID: thread.ID, LabelIDs: messageLabels(thread.Messages)}
	result.Messages = make([]mail.Message, 0, len(thread.Messages))
	for _, message := range thread.Messages {
		converted, err := makeMessage(thread.ID, message)
		if err != nil {
			return mail.Thread{}, err
		}
		if result.Subject == "" {
			result.Subject = converted.Subject
		}
		result.Messages = append(result.Messages, converted)
	}
	return result, nil
}

func messageLabels(messages []apiMessage) []string {
	var labels []string
	for _, message := range messages {
		for _, label := range message.LabelIDs {
			if !contains(labels, label) {
				labels = append(labels, label)
			}
		}
	}
	sort.Strings(labels)
	return labels
}

func messageTime(message apiMessage) time.Time {
	if value := header(message.Payload, "Date"); value != "" {
		if date, err := stdmail.ParseDate(value); err == nil {
			return date
		}
	}
	milliseconds, err := strconv.ParseInt(message.InternalDate, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(milliseconds)
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
