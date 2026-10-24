package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"mica/internal/mail"
)

var ErrSnoozeConflict = errors.New("store: snooze changed")

func (s *Store) SaveSnooze(ctx context.Context, email string, record mail.SnoozeRecord) error {
	email = normalizeAccountEmail(email)
	if email == "" || record.ThreadID == "" || record.WakeAt.IsZero() {
		return errors.New("store: invalid snooze record")
	}
	record.AccountEmail = email
	record.WakeAt = record.WakeAt.UTC()
	record.Summary.AccountEmail = email
	record.Summary.SnoozeAt = record.WakeAt
	if record.Summary.ID == "" {
		record.Summary.ID = record.ThreadID
	}
	record.Summary.LabelIDs = append([]string(nil), record.Summary.LabelIDs...)
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("store: encode snooze: %w", err)
	}
	_, err = s.db.Execute(ctx, `INSERT INTO snoozed_threads(account_email, thread_id, wake_at, data)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(account_email, thread_id) DO UPDATE SET wake_at = excluded.wake_at, data = excluded.data`,
		email, record.ThreadID, record.WakeAt.UnixNano(), data)
	if err != nil {
		return fmt.Errorf("store: save snooze: %w", err)
	}
	return nil
}

func (s *Store) SnoozeRecords(ctx context.Context, email string) ([]mail.SnoozeRecord, error) {
	email = normalizeAccountEmail(email)
	if email == "" {
		return nil, errors.New("store: snooze account is required")
	}
	rows, err := s.db.Query(ctx, "SELECT data FROM snoozed_threads WHERE account_email = ? COLLATE NOCASE ORDER BY wake_at, thread_id", email)
	if err != nil {
		return nil, err
	}
	records := make([]mail.SnoozeRecord, 0, len(rows.Values))
	for _, values := range rows.Values {
		record, err := decodeSnoozeRecord(values)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func (s *Store) SnoozeRecord(ctx context.Context, email, threadID string, wakeAt time.Time) (mail.SnoozeRecord, bool, error) {
	rows, err := s.db.Query(ctx, "SELECT data FROM snoozed_threads WHERE account_email = ? COLLATE NOCASE AND thread_id = ? AND wake_at = ?",
		normalizeAccountEmail(email), threadID, wakeAt.UTC().UnixNano())
	if err != nil {
		return mail.SnoozeRecord{}, false, err
	}
	if len(rows.Values) == 0 {
		return mail.SnoozeRecord{}, false, nil
	}
	record, err := decodeSnoozeRecord(rows.Values[0])
	if err != nil {
		return mail.SnoozeRecord{}, false, err
	}
	return record, true, nil
}

func (s *Store) DeleteSnooze(ctx context.Context, email, threadID string, wakeAt time.Time) error {
	result, err := s.db.Execute(ctx, "DELETE FROM snoozed_threads WHERE account_email = ? COLLATE NOCASE AND thread_id = ? AND wake_at = ?",
		normalizeAccountEmail(email), threadID, wakeAt.UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("store: delete snooze: %w", err)
	}
	if result.Changes == 0 {
		return ErrSnoozeConflict
	}
	return nil
}

func (s *Store) CancelSnooze(ctx context.Context, email, threadID string) error {
	_, err := s.db.Execute(ctx, "DELETE FROM snoozed_threads WHERE account_email = ? COLLATE NOCASE AND thread_id = ?", normalizeAccountEmail(email), threadID)
	if err != nil {
		return fmt.Errorf("store: cancel snooze: %w", err)
	}
	return nil
}

func decodeSnoozeRecord(values []any) (mail.SnoozeRecord, error) {
	if len(values) != 1 {
		return mail.SnoozeRecord{}, errors.New("store: invalid snooze result")
	}
	var data []byte
	switch stored := values[0].(type) {
	case []byte:
		data = stored
	case string:
		data = []byte(stored)
	default:
		return mail.SnoozeRecord{}, fmt.Errorf("store: invalid snooze data type %T", stored)
	}
	var record mail.SnoozeRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return mail.SnoozeRecord{}, fmt.Errorf("store: decode snooze: %w", err)
	}
	return record, nil
}
