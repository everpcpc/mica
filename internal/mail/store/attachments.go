package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
)

const (
	maxAttachmentBytes      = 32 << 20
	maxAttachmentCacheBytes = 128 << 20
)

func (s *Store) SaveAttachment(ctx context.Context, email, threadID, messageID, attachmentID string, data []byte) error {
	email = normalizeAccountEmail(email)
	if len(data) > maxAttachmentBytes {
		return nil
	}
	if data == nil {
		data = []byte{}
	}
	_, err := s.db.Transaction(ctx, []sqlite.Statement{
		{
			SQL: `WITH ranked AS (
				SELECT rowid, SUM(length(data)) OVER (
					ORDER BY updated DESC, thread_id DESC, message_id DESC, attachment_id DESC
					ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
				) AS retained
				FROM attachments
				WHERE account_email = ? AND NOT (thread_id = ? AND message_id = ? AND attachment_id = ?)
			)
			DELETE FROM attachments WHERE rowid IN (SELECT rowid FROM ranked WHERE retained > ?)`,
			Args: []any{email, threadID, messageID, attachmentID, maxAttachmentCacheBytes - int64(len(data))},
		},
		{
			SQL: `INSERT INTO attachments(account_email, thread_id, message_id, attachment_id, data, updated)
				VALUES(?, ?, ?, ?, ?, ?)
				ON CONFLICT(account_email, thread_id, message_id, attachment_id) DO UPDATE SET data = excluded.data, updated = excluded.updated`,
			Args: []any{email, threadID, messageID, attachmentID, data, time.Now().UnixNano()},
		},
	})
	if err != nil {
		return fmt.Errorf("store: save attachment: %w", err)
	}
	return nil
}

func (s *Store) Attachment(ctx context.Context, email, threadID, messageID, attachmentID string) ([]byte, bool, error) {
	rows, err := s.db.Query(ctx, "SELECT data FROM attachments WHERE account_email = ? AND thread_id = ? AND message_id = ? AND attachment_id = ?",
		normalizeAccountEmail(email), threadID, messageID, attachmentID)
	if err != nil {
		return nil, false, err
	}
	if len(rows.Values) == 0 {
		return nil, false, nil
	}
	if len(rows.Values) != 1 || len(rows.Values[0]) != 1 {
		return nil, false, errors.New("store: invalid attachment result")
	}
	switch data := rows.Values[0][0].(type) {
	case []byte:
		return append([]byte(nil), data...), true, nil
	case string:
		return []byte(data), true, nil
	default:
		return nil, false, fmt.Errorf("store: invalid attachment data type %T", data)
	}
}
