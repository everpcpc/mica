package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo/plugins/sqlite"
)

var ErrOutboxConflict = errors.New("store: outbox message changed")

const upsertOutboxSQL = `INSERT INTO outbox_messages(account_email, id, state, attempt, updated, send_at, data)
VALUES(?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(account_email, id) DO UPDATE SET
	state = excluded.state,
	attempt = excluded.attempt,
	updated = excluded.updated,
	send_at = excluded.send_at,
	data = excluded.data`

func outboxValues(email string, record mail.OutboxMessage) ([]any, error) {
	email = normalizeAccountEmail(email)
	record.AccountEmail = email
	record.Compose = mail.CloneCompose(record.Compose)
	if email == "" || record.ID == "" || record.Compose.MessageID == "" || record.Attempt == 0 {
		return nil, errors.New("store: invalid outbox message")
	}
	switch record.State {
	case mail.OutboxSending, mail.OutboxUncertain:
	case mail.OutboxScheduled:
		if record.SendAt.IsZero() {
			return nil, errors.New("store: scheduled outbox time is required")
		}
	default:
		return nil, errors.New("store: invalid outbox state")
	}
	var sendAt int64
	if !record.SendAt.IsZero() {
		record.SendAt = record.SendAt.UTC()
		sendAt = record.SendAt.UnixNano()
	}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("store: encode outbox message: %w", err)
	}
	return []any{email, record.ID, record.State, record.Attempt, record.Updated.UnixNano(), sendAt, data}, nil
}

func outboxWriteError(err error) error {
	if err != nil && strings.Contains(err.Error(), "stale outbox attempt") {
		return fmt.Errorf("%w: %v", ErrOutboxConflict, err)
	}
	return err
}

func (s *Store) EnqueueOutbox(ctx context.Context, email string, record mail.OutboxMessage, snapshot mail.DraftSnapshot) error {
	values, err := outboxValues(email, record)
	if err != nil {
		return err
	}
	snapshot.DraftID = record.DraftID
	snapshot.OutboxID = record.ID
	snapshot.Compose = mail.CloneCompose(record.Compose)
	snapshotData, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("store: encode draft snapshot: %w", err)
	}
	_, err = s.db.Transaction(ctx, []sqlite.Statement{
		{SQL: upsertOutboxSQL, Args: values},
		{SQL: `INSERT INTO draft_snapshots(account_email, data) VALUES(?, ?)
			ON CONFLICT(account_email) DO UPDATE SET data = excluded.data`, Args: []any{values[0], snapshotData}},
	})
	if err != nil {
		return outboxWriteError(fmt.Errorf("store: enqueue outbox: %w", err))
	}
	return nil
}

func (s *Store) SaveOutboxMessage(ctx context.Context, email string, record mail.OutboxMessage) error {
	values, err := outboxValues(email, record)
	if err != nil {
		return err
	}
	_, err = s.db.Execute(ctx, upsertOutboxSQL, values...)
	if err != nil {
		return outboxWriteError(fmt.Errorf("store: save outbox: %w", err))
	}
	return nil
}

func (s *Store) OutboxMessage(ctx context.Context, email, id string) (mail.OutboxMessage, bool, error) {
	return readSnapshot[mail.OutboxMessage](ctx, s.db,
		"SELECT data FROM outbox_messages WHERE account_email = ? AND id = ?", normalizeAccountEmail(email), id)
}

func (s *Store) OutboxMessages(ctx context.Context, email string) ([]mail.OutboxMessage, error) {
	rows, err := s.db.Query(ctx, "SELECT data FROM outbox_messages WHERE account_email = ? ORDER BY updated DESC, id", normalizeAccountEmail(email))
	if err != nil {
		return nil, err
	}
	messages := make([]mail.OutboxMessage, 0, len(rows.Values))
	for _, values := range rows.Values {
		if len(values) != 1 {
			return nil, errors.New("store: invalid outbox result")
		}
		message, err := decodeOutboxMessage(values[0])
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func decodeOutboxMessage(stored any) (mail.OutboxMessage, error) {
	var data []byte
	switch stored := stored.(type) {
	case []byte:
		data = stored
	case string:
		data = []byte(stored)
	default:
		return mail.OutboxMessage{}, fmt.Errorf("store: invalid outbox data type %T", stored)
	}
	var message mail.OutboxMessage
	if err := json.Unmarshal(data, &message); err != nil {
		return mail.OutboxMessage{}, fmt.Errorf("store: decode outbox message: %w", err)
	}
	return message, nil
}

func (s *Store) ScheduledOutboxMessages(ctx context.Context) ([]mail.OutboxMessage, error) {
	rows, err := s.db.Query(ctx, `SELECT account_email, data FROM outbox_messages
		WHERE state = ? OR (state = ? AND send_at > 0)
		ORDER BY send_at, id, account_email`, mail.OutboxScheduled, mail.OutboxSending)
	if err != nil {
		return nil, err
	}
	messages := make([]mail.OutboxMessage, 0, len(rows.Values))
	for _, values := range rows.Values {
		if len(values) != 2 {
			return nil, errors.New("store: invalid scheduled outbox result")
		}
		email, ok := values[0].(string)
		if !ok {
			return nil, fmt.Errorf("store: invalid scheduled outbox owner type %T", values[0])
		}
		message, err := decodeOutboxMessage(values[1])
		if err != nil {
			return nil, err
		}
		message.AccountEmail = normalizeAccountEmail(email)
		messages = append(messages, message)
	}
	return messages, nil
}

func (s *Store) ClaimScheduledOutbox(ctx context.Context, email string, record mail.OutboxMessage, now time.Time, force bool) (mail.OutboxMessage, bool, error) {
	email = normalizeAccountEmail(email)
	if record.State != mail.OutboxScheduled {
		return mail.OutboxMessage{}, false, nil
	}
	if _, err := outboxValues(email, record); err != nil {
		return mail.OutboxMessage{}, false, err
	}
	claimed := record
	claimed.AccountEmail = email
	claimed.State = mail.OutboxSending
	claimed.Updated = now
	claimed.SendAt = claimed.SendAt.UTC()
	values, err := outboxValues(email, claimed)
	if err != nil {
		return mail.OutboxMessage{}, false, err
	}
	query := `UPDATE outbox_messages SET state = ?, updated = ?, data = ?
		WHERE account_email = ? AND id = ? AND state = ? AND attempt = ? AND send_at = ?`
	args := []any{values[2], values[4], values[6], email, record.ID, mail.OutboxScheduled, record.Attempt, record.SendAt.UTC().UnixNano()}
	if !force {
		query += " AND send_at <= ?"
		args = append(args, now.UnixNano())
	}
	result, err := s.db.Execute(ctx, query, args...)
	if err != nil {
		return mail.OutboxMessage{}, false, fmt.Errorf("store: claim scheduled outbox: %w", err)
	}
	if result.Changes == 0 {
		return mail.OutboxMessage{}, false, nil
	}
	return claimed, true, nil
}

func (s *Store) CancelScheduledOutbox(ctx context.Context, email string, record mail.OutboxMessage) error {
	email = normalizeAccountEmail(email)
	if record.State != mail.OutboxScheduled {
		return ErrOutboxConflict
	}
	if _, err := outboxValues(email, record); err != nil {
		return err
	}
	sendAt := record.SendAt.UTC().UnixNano()
	results, err := s.db.Transaction(ctx, []sqlite.Statement{
		{SQL: `UPDATE draft_snapshots SET data = CAST(json_remove(data, '$.OutboxID') AS BLOB)
			WHERE account_email = ? AND json_extract(data, '$.OutboxID') = ?
			AND EXISTS (SELECT 1 FROM outbox_messages
				WHERE account_email = ? AND id = ? AND state = ? AND attempt = ? AND send_at = ?)`,
			Args: []any{email, record.ID, email, record.ID, mail.OutboxScheduled, record.Attempt, sendAt}},
		{SQL: `DELETE FROM outbox_messages
			WHERE account_email = ? AND id = ? AND state = ? AND attempt = ? AND send_at = ?`,
			Args: []any{email, record.ID, mail.OutboxScheduled, record.Attempt, sendAt}},
	})
	if err != nil {
		return fmt.Errorf("store: cancel scheduled outbox: %w", err)
	}
	if results[len(results)-1].Changes == 0 {
		return ErrOutboxConflict
	}
	return nil
}

func (s *Store) RecoverScheduledOutbox(ctx context.Context, email string, record mail.OutboxMessage) (bool, error) {
	email = normalizeAccountEmail(email)
	if record.State != mail.OutboxSending {
		return false, nil
	}
	recovered := record
	recovered.State = mail.OutboxUncertain
	values, err := outboxValues(email, recovered)
	if err != nil {
		return false, err
	}
	result, err := s.db.Execute(ctx, `UPDATE outbox_messages SET state = ?, updated = ?, data = ?
		WHERE account_email = ? AND id = ? AND state = ? AND attempt = ? AND send_at = ?`,
		values[2], values[4], values[6], email, record.ID, mail.OutboxSending, record.Attempt, record.SendAt.UTC().UnixNano())
	if err != nil {
		return false, fmt.Errorf("store: recover scheduled outbox: %w", err)
	}
	return result.Changes > 0, nil
}

func (s *Store) DeleteOutboxMessage(ctx context.Context, email, id string, attempt uint64) error {
	result, err := s.db.Execute(ctx, "DELETE FROM outbox_messages WHERE account_email = ? AND id = ? AND attempt = ?", normalizeAccountEmail(email), id, attempt)
	if err != nil {
		return fmt.Errorf("store: delete outbox: %w", err)
	}
	if result.Changes == 0 {
		return ErrOutboxConflict
	}
	return nil
}

func (s *Store) CompleteOutbox(ctx context.Context, email, id string, attempt uint64) error {
	email = normalizeAccountEmail(email)
	results, err := s.db.Transaction(ctx, []sqlite.Statement{
		{SQL: `DELETE FROM draft_snapshots
			WHERE account_email = ? AND json_extract(data, '$.OutboxID') = ?
			AND EXISTS (SELECT 1 FROM outbox_messages WHERE account_email = ? AND id = ? AND attempt = ?)`,
			Args: []any{email, id, email, id, attempt}},
		{SQL: "DELETE FROM outbox_messages WHERE account_email = ? AND id = ? AND attempt = ?", Args: []any{email, id, attempt}},
	})
	if err != nil {
		return fmt.Errorf("store: complete outbox: %w", err)
	}
	if results[len(results)-1].Changes > 0 {
		return nil
	}
	_, found, err := s.OutboxMessage(ctx, email, id)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	return ErrOutboxConflict
}
