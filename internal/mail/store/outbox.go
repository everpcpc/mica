package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"mica/internal/mail"

	"github.com/egoist/mygo/plugins/sqlite"
)

var ErrOutboxConflict = errors.New("store: outbox message changed")

const upsertOutboxSQL = `INSERT INTO outbox_messages(account_email, id, state, attempt, updated, data)
VALUES(?, ?, ?, ?, ?, ?)
ON CONFLICT(account_email, id) DO UPDATE SET
	state = excluded.state,
	attempt = excluded.attempt,
	updated = excluded.updated,
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
	default:
		return nil, errors.New("store: invalid outbox state")
	}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("store: encode outbox message: %w", err)
	}
	return []any{email, record.ID, record.State, record.Attempt, record.Updated.UnixNano(), data}, nil
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
		var data []byte
		switch stored := values[0].(type) {
		case []byte:
			data = stored
		case string:
			data = []byte(stored)
		default:
			return nil, fmt.Errorf("store: invalid outbox data type %T", stored)
		}
		var message mail.OutboxMessage
		if err := json.Unmarshal(data, &message); err != nil {
			return nil, fmt.Errorf("store: decode outbox message: %w", err)
		}
		messages = append(messages, message)
	}
	return messages, nil
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
