package gmail

import (
	"context"
	"errors"
	"strings"

	"mica/internal/mail"
)

func (s *Service) SentMessageExists(ctx context.Context, messageID string) (bool, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return false, errors.New("gmail: empty message ID")
	}
	threads, err := s.ListThreads(ctx, mail.Query{LabelID: mail.Sent, Search: "rfc822msgid:" + messageID, Limit: 1})
	if err != nil {
		return false, err
	}
	for _, thread := range threads {
		for _, label := range thread.LabelIDs {
			if label == mail.Sent {
				return true, nil
			}
		}
	}
	return false, nil
}
