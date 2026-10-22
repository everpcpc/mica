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
)

const (
	gmailAPIURL    = "https://gmail.googleapis.com/gmail/v1"
	threadListSize = 50
	listParallel   = 4
)

type Service struct {
	client      *http.Client
	baseURL     string
	userID      string
	maxParallel int
}

type APIError struct {
	StatusCode int
	Reason     string
	Message    string
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

func New(client *http.Client) *Service {
	if client == nil {
		client = http.DefaultClient
	}
	return &Service{client: client, baseURL: gmailAPIURL, userID: "me", maxParallel: listParallel}
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
	return mail.Account{Name: profile.EmailAddress, Email: profile.EmailAddress}, nil
}

func (s *Service) Labels(ctx context.Context) ([]mail.Label, error) {
	var response struct {
		Labels []struct {
			ID                  string `json:"id"`
			Name                string `json:"name"`
			Type                string `json:"type"`
			LabelListVisibility string `json:"labelListVisibility"`
			MessagesTotal       int    `json:"messagesTotal"`
			MessagesUnread      int    `json:"messagesUnread"`
		} `json:"labels"`
	}
	if err := s.doJSON(ctx, http.MethodGet, "/labels", nil, nil, &response); err != nil {
		return nil, err
	}
	labels := make([]mail.Label, len(response.Labels))
	for i, label := range response.Labels {
		labels[i] = mail.Label{
			ID: label.ID, Name: label.Name, Messages: label.MessagesTotal, Unread: label.MessagesUnread,
			System: label.Type == "system", Hide: label.LabelListVisibility == "labelHide",
			ShowIfUnread: label.LabelListVisibility == "labelShowIfUnread",
		}
	}
	return labels, nil
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
	limit := query.Limit
	if limit <= 0 {
		limit = threadListSize
	}
	var ids []string
	pageToken := ""
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
			return nil, err
		}
		for _, thread := range page.Threads {
			if thread.ID != "" {
				ids = append(ids, thread.ID)
			}
		}
		if page.NextPageToken == "" || page.NextPageToken == pageToken {
			break
		}
		pageToken = page.NextPageToken
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	if len(ids) == 0 {
		return []mail.ThreadSummary{}, nil
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
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return summaries, nil
}

func (s *Service) Thread(ctx context.Context, id string) (mail.Thread, error) {
	if id == "" {
		return mail.Thread{}, errors.New("gmail: empty thread ID")
	}
	thread, err := s.getThread(ctx, id, "full")
	if err != nil {
		return mail.Thread{}, err
	}
	return makeThread(thread)
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

func (s *Service) Send(ctx context.Context, compose mail.Compose) error {
	if compose.ThreadID != "" && (compose.InReplyTo == "" || compose.References == "") {
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
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("gmail: encode request: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}
	requestURL := strings.TrimRight(s.baseURL, "/") + "/users/" + url.PathEscape(s.userID) + path
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, payload)
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
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readAPIError(resp)
	}
	if result == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("gmail: decode response: %w", err)
	}
	return nil
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
