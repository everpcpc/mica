package gmail

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"mica/internal/mail"
)

func (s *Service) SentMessageExists(ctx context.Context, messageID string) (bool, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return false, errors.New("gmail: empty message ID")
	}
	query := make(url.Values)
	query.Set("q", "rfc822msgid:"+messageID)
	query.Set("labelIds", mail.Sent)
	query.Set("maxResults", "1")
	var page struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := s.doJSON(ctx, http.MethodGet, "/messages", query, nil, &page); err != nil {
		return false, err
	}
	for _, candidate := range page.Messages {
		metadata := make(url.Values)
		metadata.Set("format", "metadata")
		metadata.Set("metadataHeaders", "Message-ID")
		var message apiMessage
		if err := s.doJSON(ctx, http.MethodGet, "/messages/"+url.PathEscape(candidate.ID), metadata, nil, &message); err != nil {
			return false, err
		}
		if decodedHeader(message.Payload, "Message-ID") != messageID {
			continue
		}
		for _, label := range message.LabelIDs {
			if label == mail.Sent {
				return true, nil
			}
		}
	}
	return false, nil
}
