package store

import (
	"context"
	"errors"
	"fmt"
	"time"
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
	for {
		total, err := s.attachmentCacheBytes(ctx, email, threadID, messageID, attachmentID)
		if err != nil {
			return err
		}
		if total+int64(len(data)) <= maxAttachmentCacheBytes {
			break
		}
		oldest, found, err := s.oldestAttachment(ctx, email, threadID, messageID, attachmentID)
		if err != nil {
			return err
		}
		if !found {
			break
		}
		if _, err := s.db.Execute(ctx, "DELETE FROM attachments WHERE account_email = ? AND thread_id = ? AND message_id = ? AND attachment_id = ?", email, oldest[0], oldest[1], oldest[2]); err != nil {
			return fmt.Errorf("store: evict attachment: %w", err)
		}
	}
	_, err := s.db.Execute(ctx, `INSERT INTO attachments(account_email, thread_id, message_id, attachment_id, data, updated)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_email, thread_id, message_id, attachment_id) DO UPDATE SET data = excluded.data, updated = excluded.updated`,
		email, threadID, messageID, attachmentID, data, time.Now().UnixNano())
	if err != nil {
		return fmt.Errorf("store: save attachment: %w", err)
	}
	return nil
}

func (s *Store) oldestAttachment(ctx context.Context, email, threadID, messageID, attachmentID string) ([3]string, bool, error) {
	rows, err := s.db.Query(ctx, `SELECT thread_id, message_id, attachment_id FROM attachments
		WHERE account_email = ? AND NOT (thread_id = ? AND message_id = ? AND attachment_id = ?)
		ORDER BY updated ASC LIMIT 1`, email, threadID, messageID, attachmentID)
	if err != nil {
		return [3]string{}, false, err
	}
	if len(rows.Values) == 0 {
		return [3]string{}, false, nil
	}
	if len(rows.Values) != 1 || len(rows.Values[0]) != 3 {
		return [3]string{}, false, errors.New("store: invalid oldest attachment result")
	}
	thread, threadOK := rows.Values[0][0].(string)
	message, messageOK := rows.Values[0][1].(string)
	attachment, attachmentOK := rows.Values[0][2].(string)
	if !threadOK || !messageOK || !attachmentOK {
		return [3]string{}, false, errors.New("store: invalid oldest attachment key")
	}
	return [3]string{thread, message, attachment}, true, nil
}

func (s *Store) attachmentCacheBytes(ctx context.Context, email, threadID, messageID, attachmentID string) (int64, error) {
	rows, err := s.db.Query(ctx, `SELECT COALESCE(SUM(length(data)), 0) FROM attachments
		WHERE account_email = ? AND NOT (thread_id = ? AND message_id = ? AND attachment_id = ?)`,
		email, threadID, messageID, attachmentID)
	if err != nil {
		return 0, err
	}
	if len(rows.Values) != 1 || len(rows.Values[0]) != 1 {
		return 0, errors.New("store: invalid attachment size result")
	}
	total, ok := rows.Values[0][0].(int64)
	if !ok {
		return 0, fmt.Errorf("store: invalid attachment size type %T", rows.Values[0][0])
	}
	return total, nil
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
