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
	threads, err := s.ListThreads(ctx, mail.Query{Search: "rfc822msgid:" + messageID, Limit: 1})
	if err != nil {
		return false, err
	}
	return len(threads) > 0, nil
}
