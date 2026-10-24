package gmail

import (
	"context"
	"encoding/base64"
	"errors"
	"mica/internal/mail"
	"net/http"
	"net/url"
	"strconv"
)

type apiDraft struct {
	ID      string     `json:"id"`
	Message apiMessage `json:"message"`
}

type apiDraftList struct {
	Drafts        []apiDraft `json:"drafts"`
	NextPageToken string     `json:"nextPageToken"`
}

func (s *Service) Drafts(ctx context.Context, query mail.Query) (mail.DraftPage, error) {
	values := make(url.Values)
	if query.Limit > 0 {
		values.Set("maxResults", strconv.Itoa(query.Limit))
	}
	if query.PageToken != "" {
		values.Set("pageToken", query.PageToken)
	}
	if query.Search != "" {
		values.Set("q", query.Search)
	}
	var response apiDraftList
	if err := s.doJSON(ctx, http.MethodGet, "/drafts", values, nil, &response); err != nil {
		return mail.DraftPage{}, err
	}
	page := mail.DraftPage{Drafts: make([]mail.DraftRecord, len(response.Drafts)), NextPageToken: response.NextPageToken}
	for i, draft := range response.Drafts {
		full, err := s.Draft(ctx, draft.ID)
		if err != nil {
			return mail.DraftPage{}, err
		}
		page.Drafts[i] = full
	}
	return page, nil
}

func (s *Service) Draft(ctx context.Context, id string) (mail.DraftRecord, error) {
	if id == "" {
		return mail.DraftRecord{}, errors.New("gmail: empty draft ID")
	}
	values := make(url.Values)
	values.Set("format", "full")
	var response apiDraft
	if err := s.doJSON(ctx, http.MethodGet, "/drafts/"+url.PathEscape(id), values, nil, &response); err != nil {
		return mail.DraftRecord{}, err
	}
	return makeDraft(response)
}

func (s *Service) CreateDraft(ctx context.Context, compose mail.Compose) (mail.DraftRecord, error) {
	body, err := draftRequest(compose)
	if err != nil {
		return mail.DraftRecord{}, err
	}
	var response apiDraft
	if err := s.doJSON(ctx, http.MethodPost, "/drafts", nil, body, &response); err != nil {
		return mail.DraftRecord{}, err
	}
	return makeDraft(response)
}

func (s *Service) UpdateDraft(ctx context.Context, id string, compose mail.Compose) (mail.DraftRecord, error) {
	if id == "" {
		return mail.DraftRecord{}, errors.New("gmail: empty draft ID")
	}
	body, err := draftRequest(compose)
	if err != nil {
		return mail.DraftRecord{}, err
	}
	var response apiDraft
	if err := s.doJSON(ctx, http.MethodPut, "/drafts/"+url.PathEscape(id), nil, body, &response); err != nil {
		return mail.DraftRecord{}, err
	}
	return makeDraft(response)
}

func (s *Service) DeleteDraft(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("gmail: empty draft ID")
	}
	return s.doJSON(ctx, http.MethodDelete, "/drafts/"+url.PathEscape(id), nil, nil, nil)
}

func (s *Service) SendDraft(ctx context.Context, id string, compose mail.Compose) error {
	if err := mail.ValidateComposeForSend(compose); err != nil {
		return err
	}
	if _, err := s.UpdateDraft(ctx, id, compose); err != nil {
		return err
	}
	body := struct {
		ID string `json:"id"`
	}{ID: id}
	return s.doJSON(ctx, http.MethodPost, "/drafts/send", nil, body, nil)
}

func draftRequest(compose mail.Compose) (any, error) {
	raw, err := buildRFC5322(compose)
	if err != nil {
		return nil, err
	}
	return struct {
		Message struct {
			Raw      string `json:"raw"`
			ThreadID string `json:"threadId,omitempty"`
		} `json:"message"`
	}{
		Message: struct {
			Raw      string `json:"raw"`
			ThreadID string `json:"threadId,omitempty"`
		}{Raw: base64.RawURLEncoding.EncodeToString(raw), ThreadID: compose.ThreadID},
	}, nil
}

func makeDraft(value apiDraft) (mail.DraftRecord, error) {
	message, err := makeMessage(value.Message.ThreadID, value.Message)
	if err != nil {
		return mail.DraftRecord{}, err
	}
	return mail.DraftRecord{ID: value.ID, Message: message}, nil
}
