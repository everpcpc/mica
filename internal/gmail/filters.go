package gmail

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"mica/internal/mail"
)

type apiFilter struct {
	ID       string             `json:"id,omitempty"`
	Criteria *apiFilterCriteria `json:"criteria,omitempty"`
	Action   *apiFilterAction   `json:"action,omitempty"`
}

type apiFilterCriteria struct {
	From           string `json:"from,omitempty"`
	To             string `json:"to,omitempty"`
	Subject        string `json:"subject,omitempty"`
	Query          string `json:"query,omitempty"`
	NegatedQuery   string `json:"negatedQuery,omitempty"`
	HasAttachment  bool   `json:"hasAttachment,omitempty"`
	ExcludeChats   bool   `json:"excludeChats,omitempty"`
	Size           int64  `json:"size,omitempty"`
	SizeComparison string `json:"sizeComparison,omitempty"`
}

type apiFilterAction struct {
	AddLabelIDs    []string `json:"addLabelIds,omitempty"`
	RemoveLabelIDs []string `json:"removeLabelIds,omitempty"`
	Forward        string   `json:"forward,omitempty"`
}

func (s *Service) Filters(ctx context.Context) ([]mail.Filter, error) {
	var response struct {
		Filters []apiFilter `json:"filter"`
	}
	if err := s.doJSON(ctx, http.MethodGet, "/settings/filters", nil, nil, &response); err != nil {
		return nil, err
	}
	filters := make([]mail.Filter, 0, len(response.Filters))
	for _, item := range response.Filters {
		filters = append(filters, makeFilter(item))
	}
	return filters, nil
}

func (s *Service) CreateFilter(ctx context.Context, filter mail.Filter) (mail.Filter, error) {
	body := makeAPIFilter(filter)
	body.ID = ""
	var created apiFilter
	if err := s.doJSON(ctx, http.MethodPost, "/settings/filters", nil, body, &created); err != nil {
		return mail.Filter{}, err
	}
	if created.ID == "" {
		return mail.Filter{}, errors.New("gmail: created filter has no ID")
	}
	return makeFilter(created), nil
}

func (s *Service) DeleteFilter(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("gmail: empty filter ID")
	}
	return s.doJSON(ctx, http.MethodDelete, "/settings/filters/"+url.PathEscape(id), nil, nil, nil)
}

func makeFilter(item apiFilter) mail.Filter {
	filter := mail.Filter{ID: item.ID}
	if item.Criteria != nil {
		filter.Criteria = mail.FilterCriteria{
			From: item.Criteria.From, To: item.Criteria.To, Subject: item.Criteria.Subject,
			Query: item.Criteria.Query, NegatedQuery: item.Criteria.NegatedQuery,
			HasAttachment: item.Criteria.HasAttachment, ExcludeChats: item.Criteria.ExcludeChats,
			Size: item.Criteria.Size, SizeComparison: item.Criteria.SizeComparison,
		}
	}
	if item.Action != nil {
		filter.Action = mail.FilterAction{
			AddLabelIDs:    append([]string(nil), item.Action.AddLabelIDs...),
			RemoveLabelIDs: append([]string(nil), item.Action.RemoveLabelIDs...),
			Forward:        item.Action.Forward,
		}
	}
	return filter
}

func makeAPIFilter(filter mail.Filter) apiFilter {
	return apiFilter{
		ID: filter.ID,
		Criteria: &apiFilterCriteria{
			From: filter.Criteria.From, To: filter.Criteria.To, Subject: filter.Criteria.Subject,
			Query: filter.Criteria.Query, NegatedQuery: filter.Criteria.NegatedQuery,
			HasAttachment: filter.Criteria.HasAttachment, ExcludeChats: filter.Criteria.ExcludeChats,
			Size: filter.Criteria.Size, SizeComparison: filter.Criteria.SizeComparison,
		},
		Action: &apiFilterAction{
			AddLabelIDs:    append([]string(nil), filter.Action.AddLabelIDs...),
			RemoveLabelIDs: append([]string(nil), filter.Action.RemoveLabelIDs...),
			Forward:        filter.Action.Forward,
		},
	}
}
