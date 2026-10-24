package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mica/internal/mail"

	"github.com/egoist/mygo/plugins/sqlite"
)

var ErrNewerDatabase = errors.New("store: database schema is newer than this version")

var migrations = [][]string{{
	`CREATE TABLE accounts (
		email TEXT PRIMARY KEY,
		data BLOB NOT NULL
	)`,
	`CREATE TABLE last_account (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		email TEXT NOT NULL REFERENCES accounts(email)
	)`,
	`CREATE TABLE labels (
		account_email TEXT NOT NULL,
		data BLOB NOT NULL,
		PRIMARY KEY (account_email)
	)`,
	`CREATE TABLE thread_lists (
		account_email TEXT NOT NULL,
		query_key TEXT NOT NULL,
		data BLOB NOT NULL,
		PRIMARY KEY (account_email, query_key)
	)`,
	`CREATE TABLE threads (
		account_email TEXT NOT NULL,
		thread_id TEXT NOT NULL,
		data BLOB NOT NULL,
		PRIMARY KEY (account_email, thread_id)
	)`,
}, {
	`CREATE TABLE preferences (
		key TEXT PRIMARY KEY,
		data TEXT NOT NULL
	)`,
}, {
	`CREATE TABLE draft_snapshots (
			account_email TEXT PRIMARY KEY,
			data BLOB NOT NULL
		)`,
}}

type Store struct {
	db *sqlite.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("store: database path is required")
	}
	if path != ":memory:" {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("store: create database directory: %w", err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return nil, fmt.Errorf("store: secure database directory: %w", err)
		}
	}
	db, err := sqlite.Open(ctx, path, sqlite.OpenOptions{})
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.init(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) init(ctx context.Context) error {
	rows, err := s.db.Query(ctx, "PRAGMA user_version")
	if err != nil {
		return err
	}
	if len(rows.Values) != 1 || len(rows.Values[0]) != 1 {
		return errors.New("store: invalid user_version result")
	}
	version, ok := rows.Values[0][0].(int64)
	if !ok {
		return fmt.Errorf("store: invalid user_version type %T", rows.Values[0][0])
	}
	if version > int64(len(migrations)) {
		return fmt.Errorf("%w (have %d, know %d)", ErrNewerDatabase, version, len(migrations))
	}
	for _, pragma := range []string{"PRAGMA foreign_keys = ON", "PRAGMA journal_mode = WAL"} {
		if _, err := s.db.Query(ctx, pragma); err != nil {
			return err
		}
	}
	for next := version + 1; next <= int64(len(migrations)); next++ {
		migration := migrations[next-1]
		statements := make([]sqlite.Statement, 0, len(migration)+1)
		for _, sql := range migration {
			statements = append(statements, sqlite.Statement{SQL: sql})
		}
		statements = append(statements, sqlite.Statement{SQL: fmt.Sprintf("PRAGMA user_version = %d", next)})
		if _, err := s.db.Transaction(ctx, statements); err != nil {
			return fmt.Errorf("store: migration %d: %w", next, err)
		}
	}
	return nil
}

func normalizeAccountEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (s *Store) Accounts(ctx context.Context) ([]mail.Account, error) {
	rows, err := s.db.Query(ctx, "SELECT data FROM accounts ORDER BY email")
	if err != nil {
		return nil, err
	}
	accounts := make([]mail.Account, 0, len(rows.Values))
	for _, values := range rows.Values {
		if len(values) != 1 {
			return nil, errors.New("store: invalid account result")
		}
		var data []byte
		switch stored := values[0].(type) {
		case []byte:
			data = stored
		case string:
			data = []byte(stored)
		default:
			return nil, fmt.Errorf("store: invalid account data type %T", stored)
		}
		var account mail.Account
		if err := json.Unmarshal(data, &account); err != nil {
			return nil, fmt.Errorf("store: decode account: %w", err)
		}
		accounts = append(accounts, account)
	}
	return accounts, nil
}

func (s *Store) Account(ctx context.Context, email string) (mail.Account, bool, error) {
	return readSnapshot[mail.Account](ctx, s.db,
		"SELECT data FROM accounts WHERE email = ? COLLATE NOCASE", normalizeAccountEmail(email))
}

func (s *Store) SaveAccount(ctx context.Context, account mail.Account) error {
	account.Email = normalizeAccountEmail(account.Email)
	data, err := json.Marshal(account)
	if err != nil {
		return fmt.Errorf("store: encode account: %w", err)
	}
	_, err = s.db.Execute(ctx, `INSERT INTO accounts(email, data) VALUES(?, ?)
		ON CONFLICT(email) DO UPDATE SET data = excluded.data`, account.Email, data)
	if err != nil {
		return fmt.Errorf("store: save account: %w", err)
	}
	return nil
}

func (s *Store) SetLastAccount(ctx context.Context, email string) error {
	result, err := s.db.Execute(ctx, `INSERT INTO last_account(id, email)
		SELECT 1, email FROM accounts WHERE email = ? COLLATE NOCASE
		ON CONFLICT(id) DO UPDATE SET email = excluded.email`, normalizeAccountEmail(email))
	if err != nil {
		return fmt.Errorf("store: set last account: %w", err)
	}
	if result.Changes == 0 {
		return errors.New("store: set last account: account not found")
	}
	return nil
}

func (s *Store) LastAccount(ctx context.Context) (mail.Account, bool, error) {
	return readSnapshot[mail.Account](ctx, s.db, `SELECT accounts.data
		FROM last_account JOIN accounts ON accounts.email = last_account.email
		WHERE last_account.id = 1`)
}

func (s *Store) DeleteAccount(ctx context.Context, email string) error {
	email = normalizeAccountEmail(email)
	_, err := s.db.Transaction(ctx, []sqlite.Statement{
		{SQL: "DELETE FROM labels WHERE account_email = ? COLLATE NOCASE", Args: []any{email}},
		{SQL: "DELETE FROM thread_lists WHERE account_email = ? COLLATE NOCASE", Args: []any{email}},
		{SQL: "DELETE FROM threads WHERE account_email = ? COLLATE NOCASE", Args: []any{email}},
		{SQL: "DELETE FROM draft_snapshots WHERE account_email = ? COLLATE NOCASE", Args: []any{email}},
		{SQL: "DELETE FROM last_account WHERE id = 1 AND email = ? COLLATE NOCASE", Args: []any{email}},
		{SQL: "DELETE FROM accounts WHERE email = ? COLLATE NOCASE", Args: []any{email}},
	})
	if err != nil {
		return fmt.Errorf("store: delete account: %w", err)
	}
	return nil
}

func (s *Store) Labels(ctx context.Context, email string) ([]mail.Label, bool, error) {
	return readSnapshot[[]mail.Label](ctx, s.db,
		"SELECT data FROM labels WHERE account_email = ?", email)
}

func (s *Store) SaveLabels(ctx context.Context, email string, labels []mail.Label) error {
	data, err := json.Marshal(labels)
	if err != nil {
		return fmt.Errorf("store: encode labels: %w", err)
	}
	_, err = s.db.Execute(ctx, `INSERT INTO labels(account_email, data) VALUES(?, ?)
		ON CONFLICT(account_email) DO UPDATE SET data = excluded.data`, email, data)
	if err != nil {
		return fmt.Errorf("store: save labels: %w", err)
	}
	return nil
}

func (s *Store) ThreadList(ctx context.Context, email string, query mail.Query) ([]mail.ThreadSummary, bool, error) {
	key, err := json.Marshal(query)
	if err != nil {
		return nil, false, fmt.Errorf("store: encode thread query: %w", err)
	}
	return readSnapshot[[]mail.ThreadSummary](ctx, s.db,
		"SELECT data FROM thread_lists WHERE account_email = ? AND query_key = ?", email, string(key))
}

func (s *Store) SaveThreadList(ctx context.Context, email string, query mail.Query, threads []mail.ThreadSummary) error {
	key, err := json.Marshal(query)
	if err != nil {
		return fmt.Errorf("store: encode thread query: %w", err)
	}
	data, err := json.Marshal(threads)
	if err != nil {
		return fmt.Errorf("store: encode thread list: %w", err)
	}
	_, err = s.db.Execute(ctx, `INSERT INTO thread_lists(account_email, query_key, data) VALUES(?, ?, ?)
		ON CONFLICT(account_email, query_key) DO UPDATE SET data = excluded.data`, email, string(key), data)
	if err != nil {
		return fmt.Errorf("store: save thread list: %w", err)
	}
	return nil
}

func (s *Store) Thread(ctx context.Context, email, id string) (mail.Thread, bool, error) {
	return readSnapshot[mail.Thread](ctx, s.db,
		"SELECT data FROM threads WHERE account_email = ? AND thread_id = ?", email, id)
}

func (s *Store) SaveThread(ctx context.Context, email string, thread mail.Thread) error {
	data, err := json.Marshal(thread)
	if err != nil {
		return fmt.Errorf("store: encode thread: %w", err)
	}
	_, err = s.db.Execute(ctx, `INSERT INTO threads(account_email, thread_id, data) VALUES(?, ?, ?)
		ON CONFLICT(account_email, thread_id) DO UPDATE SET data = excluded.data`, email, thread.ID, data)
	if err != nil {
		return fmt.Errorf("store: save thread: %w", err)
	}
	return nil
}

func (s *Store) DeleteThread(ctx context.Context, email, id string) error {
	_, err := s.db.Execute(ctx, "DELETE FROM threads WHERE account_email = ? AND thread_id = ?", email, id)
	if err != nil {
		return fmt.Errorf("store: delete thread: %w", err)
	}
	return nil
}

func (s *Store) Preference(ctx context.Context, key string) (string, bool, error) {
	rows, err := s.db.Query(ctx, "SELECT data FROM preferences WHERE key = ?", key)
	if err != nil {
		return "", false, err
	}
	if len(rows.Values) == 0 {
		return "", false, nil
	}
	if len(rows.Values) != 1 || len(rows.Values[0]) != 1 {
		return "", false, errors.New("store: invalid preference result")
	}
	value, ok := rows.Values[0][0].(string)
	if !ok {
		return "", false, errors.New("store: invalid preference type")
	}
	return value, true, nil
}

func (s *Store) SavePreference(ctx context.Context, key, value string) error {
	_, err := s.db.Execute(ctx, `INSERT INTO preferences(key, data) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET data = excluded.data`, key, value)
	if err != nil {
		return fmt.Errorf("store: save preference: %w", err)
	}
	return nil
}

func readSnapshot[T any](ctx context.Context, db *sqlite.DB, sql string, args ...any) (T, bool, error) {
	var value T
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return value, false, err
	}
	if len(rows.Values) == 0 {
		return value, false, nil
	}
	if len(rows.Values) != 1 || len(rows.Values[0]) != 1 {
		return value, false, errors.New("store: invalid snapshot result")
	}
	var data []byte
	switch stored := rows.Values[0][0].(type) {
	case []byte:
		data = stored
	case string:
		data = []byte(stored)
	default:
		return value, false, fmt.Errorf("store: invalid snapshot type %T", stored)
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, false, fmt.Errorf("store: decode snapshot: %w", err)
	}
	return value, true, nil
}
