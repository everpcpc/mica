package gmail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mica/internal/mail"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/pubsub/v2"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
)

const (
	pushTopicID         = "mica-gmail"
	pushSubscriptionID  = "mica-gmail-stream"
	pubsubAPIURL        = "https://pubsub.googleapis.com/v1"
	gmailPushPublisher  = "serviceAccount:gmail-api-push@system.gserviceaccount.com"
	pubsubPublisherRole = "roles/pubsub.publisher"
)

type WatchState struct {
	HistoryID  uint64
	Expiration time.Time
}

type pubsubBinding struct {
	Role      string          `json:"role"`
	Members   []string        `json:"members"`
	Condition json.RawMessage `json:"condition,omitempty"`
}

type pubsubPolicy struct {
	Version      int               `json:"version,omitempty"`
	Bindings     []pubsubBinding   `json:"bindings"`
	AuditConfigs []json.RawMessage `json:"auditConfigs,omitempty"`
	Etag         string            `json:"etag,omitempty"`
}

func (s *Service) EnsurePush(ctx context.Context) error {
	topicName, err := s.pushTopicName()
	if err != nil {
		return err
	}
	projectID := url.PathEscape(s.projectID)
	topicPath := pubsubAPIURL + "/projects/" + projectID + "/topics/" + pushTopicID
	subscriptionPath := pubsubAPIURL + "/projects/" + projectID + "/subscriptions/" + pushSubscriptionID
	if err := s.ensurePubSubResource(ctx, topicPath, struct{}{}); err != nil {
		return err
	}
	subscription := struct {
		Topic string `json:"topic"`
	}{Topic: topicName}
	if err := s.ensurePubSubResource(ctx, subscriptionPath, subscription); err != nil {
		return err
	}
	var policy pubsubPolicy
	if err := s.doJSON(ctx, http.MethodGet, topicPath+":getIamPolicy", nil, nil, &policy); err != nil {
		return err
	}
	if !policy.ensurePublisher() {
		return nil
	}
	body := struct {
		Policy pubsubPolicy `json:"policy"`
	}{Policy: policy}
	return s.doJSON(ctx, http.MethodPost, topicPath+":setIamPolicy", nil, body, nil)
}

func (s *Service) Watch(ctx context.Context) (WatchState, error) {
	topicName, err := s.pushTopicName()
	if err != nil {
		return WatchState{}, err
	}
	body := struct {
		LabelFilterBehavior string   `json:"labelFilterBehavior"`
		LabelIDs            []string `json:"labelIds"`
		TopicName           string   `json:"topicName"`
	}{
		LabelFilterBehavior: "include",
		LabelIDs:            []string{mail.Inbox},
		TopicName:           topicName,
	}
	var response struct {
		HistoryID  string `json:"historyId"`
		Expiration string `json:"expiration"`
	}
	if err := s.doJSON(ctx, http.MethodPost, "/watch", nil, body, &response); err != nil {
		return WatchState{}, err
	}
	historyID, err := strconv.ParseUint(response.HistoryID, 10, 64)
	if err != nil {
		return WatchState{}, fmt.Errorf("gmail: parse watch history id: %w", err)
	}
	expiration, err := strconv.ParseInt(response.Expiration, 10, 64)
	if err != nil {
		return WatchState{}, fmt.Errorf("gmail: parse watch expiration: %w", err)
	}
	return WatchState{HistoryID: historyID, Expiration: time.UnixMilli(expiration)}, nil
}

func (s *Service) History(ctx context.Context, start uint64, fn func([]mail.ThreadSummary) error) (uint64, error) {
	if start == 0 {
		return 0, errors.New("gmail: start history id is required")
	}
	pageToken := ""
	var latest uint64
	for {
		values := url.Values{
			"startHistoryId": []string{strconv.FormatUint(start, 10)},
			"labelId":        []string{mail.Inbox},
			"maxResults":     []string{"500"},
		}
		if pageToken != "" {
			values.Set("pageToken", pageToken)
		}
		var page struct {
			HistoryID     string `json:"historyId"`
			NextPageToken string `json:"nextPageToken"`
			History       []struct {
				MessagesAdded []struct {
					Message apiMessage `json:"message"`
				} `json:"messagesAdded"`
			} `json:"history"`
		}
		if err := s.doJSON(ctx, http.MethodGet, "/history", values, nil, &page); err != nil {
			return 0, err
		}
		historyID, err := strconv.ParseUint(page.HistoryID, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("gmail: parse history id: %w", err)
		}
		latest = historyID
		summaries := make([]mail.ThreadSummary, 0)
		for _, entry := range page.History {
			for _, added := range entry.MessagesAdded {
				if added.Message.ID == "" {
					continue
				}
				message, err := s.getMessageMetadata(ctx, added.Message.ID)
				if err != nil {
					return 0, err
				}
				if message.ThreadID == "" {
					message.ThreadID = added.Message.ThreadID
				}
				thread := apiThread{ID: message.ThreadID, Snippet: message.Snippet, Messages: []apiMessage{message}}
				summaries = append(summaries, makeSummary(thread))
			}
		}
		if err := fn(summaries); err != nil {
			return 0, err
		}
		if page.NextPageToken == "" || page.NextPageToken == pageToken {
			break
		}
		pageToken = page.NextPageToken
	}
	return latest, nil
}

func (s *Service) Stream(ctx context.Context, fn func(uint64) error) error {
	if s.tokenSource == nil {
		return errors.New("gmail: token source is required")
	}
	if strings.TrimSpace(s.projectID) == "" {
		return errors.New("gmail: project id is required")
	}
	receiver, err := newStreamReceiver(ctx, s.projectID, s.tokenSource)
	if err != nil {
		return err
	}
	defer func() {
		_ = receiver.Close()
	}()
	receiveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var errorOnce sync.Once
	var callbackErr error
	fail := func(err error) {
		errorOnce.Do(func() {
			callbackErr = err
			cancel()
		})
	}
	err = receiver.Receive(receiveCtx, pushSubscriptionID, streamSettings{
		numGoroutines:          1,
		maxOutstandingMessages: 10,
	}, func(message streamMessage) {
		var notification struct {
			EmailAddress string      `json:"emailAddress"`
			HistoryID    json.Number `json:"historyId"`
		}
		if err := json.Unmarshal(message.data, &notification); err != nil {
			message.nack()
			fail(fmt.Errorf("gmail: decode push notification: %w", err))
			return
		}
		historyID, err := strconv.ParseUint(notification.HistoryID.String(), 10, 64)
		if err != nil {
			message.nack()
			fail(fmt.Errorf("gmail: parse push history id: %w", err))
			return
		}
		if err := fn(historyID); err != nil {
			message.nack()
			fail(err)
			return
		}
		message.ack()
	})
	if callbackErr != nil {
		return callbackErr
	}
	return err
}

type streamSettings struct {
	numGoroutines          int
	maxOutstandingMessages int
}

type streamMessage struct {
	data []byte
	ack  func()
	nack func()
}

type streamReceiver interface {
	Receive(context.Context, string, streamSettings, func(streamMessage)) error
	Close() error
}

type pubsubStreamReceiver struct {
	client *pubsub.Client
}

var newStreamReceiver = func(ctx context.Context, projectID string, source oauth2.TokenSource) (streamReceiver, error) {
	client, err := pubsub.NewClient(ctx, projectID, option.WithTokenSource(source))
	if err != nil {
		return nil, err
	}
	return &pubsubStreamReceiver{client: client}, nil
}

func (r *pubsubStreamReceiver) Receive(ctx context.Context, subscription string, settings streamSettings, fn func(streamMessage)) error {
	subscriber := r.client.Subscriber(subscription)
	subscriber.ReceiveSettings.NumGoroutines = settings.numGoroutines
	subscriber.ReceiveSettings.MaxOutstandingMessages = settings.maxOutstandingMessages
	return subscriber.Receive(ctx, func(_ context.Context, message *pubsub.Message) {
		fn(streamMessage{data: message.Data, ack: message.Ack, nack: message.Nack})
	})
}

func (r *pubsubStreamReceiver) Close() error {
	return r.client.Close()
}

func (s *Service) pushTopicName() (string, error) {
	if strings.TrimSpace(s.projectID) == "" {
		return "", errors.New("gmail: project id is required")
	}
	return "projects/" + s.projectID + "/topics/" + pushTopicID, nil
}

func (s *Service) ensurePubSubResource(ctx context.Context, path string, body any) error {
	err := s.doJSON(ctx, http.MethodGet, path, nil, nil, nil)
	if err == nil {
		return nil
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		return err
	}
	return s.doJSON(ctx, http.MethodPut, path, nil, body, nil)
}

func (p *pubsubPolicy) ensurePublisher() bool {
	for i := range p.Bindings {
		if p.Bindings[i].Role != pubsubPublisherRole {
			continue
		}
		for _, member := range p.Bindings[i].Members {
			if member == gmailPushPublisher {
				return false
			}
		}
		p.Bindings[i].Members = append(p.Bindings[i].Members, gmailPushPublisher)
		return true
	}
	p.Bindings = append(p.Bindings, pubsubBinding{Role: pubsubPublisherRole, Members: []string{gmailPushPublisher}})
	return true
}

func (s *Service) getMessageMetadata(ctx context.Context, id string) (apiMessage, error) {
	values := url.Values{"format": []string{"metadata"}}
	for _, name := range []string{"Subject", "From", "Date"} {
		values.Add("metadataHeaders", name)
	}
	var message apiMessage
	err := s.doJSON(ctx, http.MethodGet, "/messages/"+url.PathEscape(id), values, nil, &message)
	return message, err
}
