package store

import (
	"context"
	"encoding/json"
	"fmt"
	"mica/internal/mail"
)

func (s *Store) DraftSnapshot(ctx context.Context, email string) (mail.DraftSnapshot, bool, error) {
	return readSnapshot[mail.DraftSnapshot](ctx, s.db, "SELECT data FROM draft_snapshots WHERE account_email = ?", email)
}

func (s *Store) SaveDraftSnapshot(ctx context.Context, email string, snapshot mail.DraftSnapshot) error {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("store: encode draft snapshot: %w", err)
	}
	_, err = s.db.Execute(ctx, `INSERT INTO draft_snapshots(account_email, data) VALUES(?, ?)
		ON CONFLICT(account_email) DO UPDATE SET data = excluded.data`, email, data)
	if err != nil {
		return fmt.Errorf("store: save draft snapshot: %w", err)
	}
	return nil
}

func (s *Store) DeleteDraftSnapshot(ctx context.Context, email string) error {
	_, err := s.db.Execute(ctx, "DELETE FROM draft_snapshots WHERE account_email = ?", email)
	if err != nil {
		return fmt.Errorf("store: delete draft snapshot: %w", err)
	}
	return nil
}
