package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"mica/internal/mail"
)

func outboxFixture(email, id string, attempt uint64) mail.OutboxMessage {
	updated := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC).Add(time.Duration(attempt) * time.Second)
	return mail.OutboxMessage{
		ID: id, AccountEmail: email, DraftID: "draft-" + id, State: mail.OutboxSending, Attempt: attempt,
		Created: updated, Updated: updated,
		Compose: mail.Compose{
			To: "to@example.com", Subject: "Outgoing", Text: "body", MessageID: "<" + id + "@example.com>",
			Attachments: []mail.OutgoingAttachment{{Name: "payload.bin", MIMEType: "application/octet-stream", Data: []byte{0, 1, '\r', '\n', 255}}},
		},
	}
}

func TestOutboxRoundTripPreservesAttachmentsAndSnapshot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	s := openTestStore(t, path)
	record := outboxFixture("A@EXAMPLE.COM", "outbox-a", 1)
	record.AccountEmail = normalizeAccountEmail(record.AccountEmail)
	if err := s.EnqueueOutbox(ctx, "A@EXAMPLE.COM", record, mail.DraftSnapshot{}); err != nil {
		t.Fatal(err)
	}
	other := outboxFixture("b@example.com", "outbox-b", 1)
	if err := s.EnqueueOutbox(ctx, other.AccountEmail, other, mail.DraftSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, path)
	got, found, err := s.OutboxMessage(ctx, "a@example.com", record.ID)
	if err != nil || !found || !reflect.DeepEqual(got, record) {
		t.Fatalf("outbox round trip: found=%v err=%v\ngot=%+v\nwant=%+v", found, err, got, record)
	}
	if !reflect.DeepEqual(got.Compose.Attachments[0].Data, record.Compose.Attachments[0].Data) {
		t.Fatalf("attachment bytes = %v", got.Compose.Attachments[0].Data)
	}
	snapshot, found, err := s.DraftSnapshot(ctx, "a@example.com")
	if err != nil || !found || snapshot.OutboxID != record.ID || snapshot.Compose.MessageID != record.Compose.MessageID {
		t.Fatalf("linked snapshot = %+v found=%v err=%v", snapshot, found, err)
	}
	for _, email := range []string{"a@example.com", "b@example.com"} {
		messages, err := s.OutboxMessages(ctx, email)
		if err != nil || len(messages) != 1 || messages[0].AccountEmail != email {
			t.Fatalf("outbox account %q = %+v err=%v", email, messages, err)
		}
	}
}

func TestOutboxRejectsStaleAttemptAtomically(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	record := outboxFixture("a@example.com", "outbox-a", 1)
	if err := s.EnqueueOutbox(ctx, record.AccountEmail, record, mail.DraftSnapshot{}); err != nil {
		t.Fatal(err)
	}
	newer := record
	newer.Attempt = 2
	newer.Compose.Text = "newer"
	if err := s.EnqueueOutbox(ctx, newer.AccountEmail, newer, mail.DraftSnapshot{}); err != nil {
		t.Fatal(err)
	}
	stale := record
	stale.State = mail.OutboxUncertain
	stale.Compose.Text = "stale"
	if err := s.EnqueueOutbox(ctx, stale.AccountEmail, stale, mail.DraftSnapshot{}); !errors.Is(err, ErrOutboxConflict) {
		t.Fatalf("stale enqueue error = %v", err)
	}
	got, _, _ := s.OutboxMessage(ctx, record.AccountEmail, record.ID)
	if got.Attempt != 2 || got.Compose.Text != "newer" {
		t.Fatalf("stale attempt replaced current record: %+v", got)
	}
	snapshot, _, _ := s.DraftSnapshot(ctx, record.AccountEmail)
	if snapshot.Compose.Text != "newer" {
		t.Fatalf("conflicting enqueue changed the snapshot: %+v", snapshot)
	}
	if err := s.DeleteOutboxMessage(ctx, record.AccountEmail, record.ID, 1); !errors.Is(err, ErrOutboxConflict) {
		t.Fatalf("stale delete error = %v", err)
	}
	if err := s.DeleteOutboxMessage(ctx, record.AccountEmail, record.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.OutboxMessage(ctx, record.AccountEmail, record.ID); found {
		t.Fatal("current attempt did not delete the record")
	}
}

func TestCompleteOutboxRemovesSnapshotAndRejectsStaleAttempt(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	record := outboxFixture("a@example.com", "outbox-a", 1)
	if err := s.EnqueueOutbox(ctx, record.AccountEmail, record, mail.DraftSnapshot{}); err != nil {
		t.Fatal(err)
	}
	newer := record
	newer.Attempt = 2
	if err := s.EnqueueOutbox(ctx, newer.AccountEmail, newer, mail.DraftSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteOutbox(ctx, record.AccountEmail, record.ID, 1); !errors.Is(err, ErrOutboxConflict) {
		t.Fatalf("stale completion error = %v", err)
	}
	if _, found, _ := s.DraftSnapshot(ctx, record.AccountEmail); !found {
		t.Fatal("stale completion removed the linked snapshot")
	}
	if err := s.CompleteOutbox(ctx, record.AccountEmail, record.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.OutboxMessage(ctx, record.AccountEmail, record.ID); found {
		t.Fatal("completion retained the Outbox row")
	}
	if _, found, _ := s.DraftSnapshot(ctx, record.AccountEmail); found {
		t.Fatal("completion retained the linked snapshot")
	}
	if err := s.CompleteOutbox(ctx, record.AccountEmail, record.ID, 2); err != nil {
		t.Fatalf("missing completion was not idempotent: %v", err)
	}
}

func TestDeleteAccountRemovesOutbox(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	account := mail.Account{Email: "a@example.com"}
	if err := s.SaveAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	record := outboxFixture(account.Email, "outbox-a", 1)
	if err := s.EnqueueOutbox(ctx, account.Email, record, mail.DraftSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, account.Email); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.OutboxMessage(ctx, account.Email, record.ID); found {
		t.Fatal("account deletion retained the outbox record")
	}
}

func TestOutboxMigrationFromDraftSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	s := openTestStore(t, path)
	for _, query := range []string{
		"DROP TRIGGER IF EXISTS outbox_attempt_guard",
		"DROP TABLE IF EXISTS outbox_messages",
		"DROP TABLE IF EXISTS attachments",
		"DROP TABLE IF EXISTS cache_revision",
		"DROP TABLE IF EXISTS snoozed_threads",
		"ALTER TABLE thread_lists DROP COLUMN updated",
		"ALTER TABLE threads DROP COLUMN updated",
		"PRAGMA user_version = 3",
	} {
		if _, err := s.db.Execute(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, path)
	record := outboxFixture("a@example.com", "outbox-a", 1)
	if err := s.EnqueueOutbox(ctx, record.AccountEmail, record, mail.DraftSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.OutboxMessage(ctx, record.AccountEmail, record.ID); !found {
		t.Fatal("migrated outbox table is unavailable")
	}
}

func scheduledOutboxFixture(email, id string, attempt uint64, sendAt time.Time) mail.OutboxMessage {
	record := outboxFixture(email, id, attempt)
	record.State = mail.OutboxScheduled
	record.SendAt = sendAt
	return record
}

func TestScheduledOutboxRestartPreservesUTCAndAttachments(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	s := openTestStore(t, path)
	zone := time.FixedZone("UTC+8", 8*60*60)
	sendAt := time.Date(2026, 10, 26, 18, 19, 20, 123456789, zone)
	record := scheduledOutboxFixture(" A@EXAMPLE.COM ", "scheduled-a", 2, sendAt)
	if err := s.EnqueueOutbox(ctx, record.AccountEmail, record, mail.DraftSnapshot{}); err != nil {
		t.Fatal(err)
	}
	record.AccountEmail = "a@example.com"
	record.SendAt = sendAt.UTC()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = openTestStore(t, path)
	got, found, err := s.OutboxMessage(ctx, "a@example.com", record.ID)
	if err != nil || !found || !reflect.DeepEqual(got, record) {
		t.Fatalf("scheduled round trip: found=%v err=%v\ngot=%#v\nwant=%#v", found, err, got, record)
	}
	if got.SendAt.Location() != time.UTC || !reflect.DeepEqual(got.Compose.Attachments[0].Data, record.Compose.Attachments[0].Data) {
		t.Fatalf("scheduled time or attachments changed: %+v", got)
	}
	rows, err := s.db.Query(ctx, "SELECT send_at, typeof(data) FROM outbox_messages WHERE account_email = ? AND id = ?", record.AccountEmail, record.ID)
	if err != nil || len(rows.Values) != 1 || len(rows.Values[0]) != 2 {
		t.Fatalf("stored schedule = %+v, %v", rows.Values, err)
	}
	if rows.Values[0][0] != sendAt.UnixNano() || rows.Values[0][1] != "blob" {
		t.Fatalf("stored send_at/type = %v/%v", rows.Values[0][0], rows.Values[0][1])
	}
	if _, err := s.db.Execute(ctx, "UPDATE outbox_messages SET data = CAST(data AS TEXT) WHERE account_email = ? AND id = ?", record.AccountEmail, record.ID); err != nil {
		t.Fatal(err)
	}
	got, found, err = s.OutboxMessage(ctx, record.AccountEmail, record.ID)
	if err != nil || !found || !reflect.DeepEqual(got, record) {
		t.Fatalf("TEXT scheduled round trip: found=%v err=%v\ngot=%#v\nwant=%#v", found, err, got, record)
	}
	messages, err := s.ScheduledOutboxMessages(ctx)
	if err != nil || len(messages) != 1 || !reflect.DeepEqual(messages[0], record) {
		t.Fatalf("scheduled messages = %+v, %v", messages, err)
	}
}

func TestScheduledOutboxValidationAndOrdinaryZeroTime(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		name   string
		mutate func(*mail.OutboxMessage)
	}{
		{name: "attempt", mutate: func(record *mail.OutboxMessage) { record.Attempt = 0 }},
		{name: "time", mutate: func(record *mail.OutboxMessage) { record.SendAt = time.Time{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
			record := scheduledOutboxFixture("a@example.com", "scheduled-a", 1, time.Now())
			test.mutate(&record)
			if err := s.SaveOutboxMessage(ctx, record.AccountEmail, record); err == nil {
				t.Fatal("invalid scheduled record was saved")
			}
			if _, found, err := s.OutboxMessage(ctx, record.AccountEmail, record.ID); err != nil || found {
				t.Fatalf("invalid scheduled record exists: found=%v err=%v", found, err)
			}
		})
	}

	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	ordinary := outboxFixture("a@example.com", "ordinary", 1)
	if err := s.SaveOutboxMessage(ctx, ordinary.AccountEmail, ordinary); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.OutboxMessage(ctx, ordinary.AccountEmail, ordinary.ID)
	if err != nil || !found || !got.SendAt.IsZero() {
		t.Fatalf("ordinary SendAt = %v, found=%v err=%v", got.SendAt, found, err)
	}
	rows, err := s.db.Query(ctx, "SELECT send_at FROM outbox_messages WHERE account_email = ? AND id = ?", ordinary.AccountEmail, ordinary.ID)
	if err != nil || len(rows.Values) != 1 || rows.Values[0][0] != int64(0) {
		t.Fatalf("ordinary stored send_at = %+v, %v", rows.Values, err)
	}
}

func TestOutboxMigrationFromVersion8(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	s := openTestStore(t, path)
	legacy := outboxFixture("a@example.com", "legacy", 2)
	legacy.LastError = "legacy error"
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "SendAt")
	data, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Execute(ctx, "ALTER TABLE outbox_messages DROP COLUMN send_at"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Execute(ctx, `INSERT INTO outbox_messages(account_email, id, state, attempt, updated, data)
		VALUES(?, ?, ?, ?, ?, ?)`, legacy.AccountEmail, legacy.ID, legacy.State, legacy.Attempt, legacy.Updated.UnixNano(), string(data)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Execute(ctx, "PRAGMA user_version = 8"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = openTestStore(t, path)
	got, found, err := s.OutboxMessage(ctx, legacy.AccountEmail, legacy.ID)
	if err != nil || !found || !reflect.DeepEqual(got, legacy) {
		t.Fatalf("legacy outbox after upgrade: found=%v err=%v\ngot=%#v\nwant=%#v", found, err, got, legacy)
	}
	rows, err := s.db.Query(ctx, "SELECT send_at, typeof(data) FROM outbox_messages WHERE account_email = ? AND id = ?", legacy.AccountEmail, legacy.ID)
	if err != nil || len(rows.Values) != 1 || rows.Values[0][0] != int64(0) || rows.Values[0][1] != "text" {
		t.Fatalf("upgraded legacy row = %+v, %v", rows.Values, err)
	}
	messages, err := s.ScheduledOutboxMessages(ctx)
	if err != nil || len(messages) != 0 {
		t.Fatalf("legacy ordinary message became scheduled: %+v, %v", messages, err)
	}
	newer := legacy
	newer.Attempt = 3
	newer.Compose.Text = "newer"
	if err := s.SaveOutboxMessage(ctx, newer.AccountEmail, newer); err != nil {
		t.Fatal(err)
	}
	stale := legacy
	stale.State = mail.OutboxUncertain
	if err := s.SaveOutboxMessage(ctx, stale.AccountEmail, stale); !errors.Is(err, ErrOutboxConflict) {
		t.Fatalf("stale attempt after migration: %v", err)
	}
	got, _, _ = s.OutboxMessage(ctx, legacy.AccountEmail, legacy.ID)
	if got.Attempt != 3 || got.Compose.Text != "newer" {
		t.Fatalf("migration lost attempt guard: %+v", got)
	}
}

func TestScheduledOutboxMessagesAcrossAccounts(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	base := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	earlier := scheduledOutboxFixture(" B@EXAMPLE.COM ", "z", 1, base)
	recovery := outboxFixture(" A@EXAMPLE.COM ", "recovery", 2)
	recovery.SendAt = base.Add(time.Hour)
	second := scheduledOutboxFixture("B@EXAMPLE.COM", "a", 1, base.Add(2*time.Hour))
	third := scheduledOutboxFixture("A@EXAMPLE.COM", "z", 1, base.Add(2*time.Hour))
	uncertain := outboxFixture("a@example.com", "uncertain", 1)
	uncertain.State = mail.OutboxUncertain
	uncertain.SendAt = base.Add(-time.Hour)
	ordinary := outboxFixture("b@example.com", "ordinary", 1)
	for _, record := range []mail.OutboxMessage{third, second, ordinary, uncertain, recovery, earlier} {
		if err := s.EnqueueOutbox(ctx, record.AccountEmail, record, mail.DraftSnapshot{}); err != nil {
			t.Fatal(err)
		}
	}

	messages, err := s.ScheduledOutboxMessages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		email string
		id    string
		state mail.OutboxState
	}{
		{email: "b@example.com", id: "z", state: mail.OutboxScheduled},
		{email: "a@example.com", id: "recovery", state: mail.OutboxSending},
		{email: "b@example.com", id: "a", state: mail.OutboxScheduled},
		{email: "a@example.com", id: "z", state: mail.OutboxScheduled},
	}
	if len(messages) != len(want) {
		t.Fatalf("scheduled messages = %+v, want %d", messages, len(want))
	}
	for i, message := range messages {
		if message.AccountEmail != want[i].email || message.ID != want[i].id || message.State != want[i].state {
			t.Fatalf("scheduled message %d = %+v, want %+v", i, message, want[i])
		}
	}
}

func TestClaimScheduledOutboxDueForceAndStale(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	due := scheduledOutboxFixture(" A@EXAMPLE.COM ", "due", 2, now)
	future := scheduledOutboxFixture("b@example.com", "future", 1, now.Add(time.Hour))
	for _, record := range []mail.OutboxMessage{due, future} {
		if err := s.EnqueueOutbox(ctx, record.AccountEmail, record, mail.DraftSnapshot{}); err != nil {
			t.Fatal(err)
		}
	}
	reject := func(email string, record mail.OutboxMessage) {
		t.Helper()
		claimed, ok, err := s.ClaimScheduledOutbox(ctx, email, record, now, false)
		if err != nil || ok || !reflect.DeepEqual(claimed, mail.OutboxMessage{}) {
			t.Fatalf("claim should be rejected: claimed=%+v ok=%v err=%v", claimed, ok, err)
		}
	}
	reject(future.AccountEmail, future)
	staleAttempt := due
	staleAttempt.Attempt++
	reject(staleAttempt.AccountEmail, staleAttempt)
	staleTime := due
	staleTime.SendAt = staleTime.SendAt.Add(time.Nanosecond)
	reject(staleTime.AccountEmail, staleTime)
	reject("other@example.com", due)
	missing := scheduledOutboxFixture("a@example.com", "missing", 1, now)
	reject(missing.AccountEmail, missing)
	if _, found, err := s.OutboxMessage(ctx, missing.AccountEmail, missing.ID); err != nil || found {
		t.Fatalf("rejected claim inserted a record: found=%v err=%v", found, err)
	}

	claimed, ok, err := s.ClaimScheduledOutbox(ctx, due.AccountEmail, due, now, false)
	if err != nil || !ok {
		t.Fatalf("due claim: ok=%v err=%v", ok, err)
	}
	want := due
	want.AccountEmail = "a@example.com"
	want.State = mail.OutboxSending
	want.Updated = now
	want.SendAt = want.SendAt.UTC()
	if !reflect.DeepEqual(claimed, want) {
		t.Fatalf("claimed record = %#v, want %#v", claimed, want)
	}
	got, found, err := s.OutboxMessage(ctx, want.AccountEmail, want.ID)
	if err != nil || !found || !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted claim: found=%v err=%v\ngot=%#v\nwant=%#v", found, err, got, want)
	}
	reject(due.AccountEmail, due)
	reject(claimed.AccountEmail, claimed)

	claimed, ok, err = s.ClaimScheduledOutbox(ctx, future.AccountEmail, future, now, true)
	if err != nil || !ok {
		t.Fatalf("forced claim: ok=%v err=%v", ok, err)
	}
	want = future
	want.State = mail.OutboxSending
	want.Updated = now
	want.SendAt = want.SendAt.UTC()
	if !reflect.DeepEqual(claimed, want) {
		t.Fatalf("forced record = %#v, want %#v", claimed, want)
	}
}

func TestClaimScheduledOutboxConcurrentSingleWinner(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.db")
	first := openTestStore(t, path)
	second := openTestStore(t, path)
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	record := scheduledOutboxFixture("a@example.com", "shared", 3, now)
	if err := first.EnqueueOutbox(ctx, record.AccountEmail, record, mail.DraftSnapshot{}); err != nil {
		t.Fatal(err)
	}
	type claimResult struct {
		record mail.OutboxMessage
		ok     bool
		err    error
	}
	runClaims := func() []claimResult {
		start := make(chan struct{})
		results := make(chan claimResult, 2)
		var workers sync.WaitGroup
		for _, s := range []*Store{first, second} {
			workers.Add(1)
			go func() {
				defer workers.Done()
				<-start
				claimed, ok, err := s.ClaimScheduledOutbox(ctx, record.AccountEmail, record, now, false)
				results <- claimResult{record: claimed, ok: ok, err: err}
			}()
		}
		close(start)
		workers.Wait()
		close(results)
		var claimed []claimResult
		for result := range results {
			claimed = append(claimed, result)
		}
		return claimed
	}

	results := runClaims()
	winners := 0
	for _, result := range results {
		if result.err != nil {
			t.Fatalf("concurrent claim error = %v", result.err)
		}
		if result.ok {
			winners++
			if result.record.State != mail.OutboxSending || result.record.Attempt != record.Attempt {
				t.Fatalf("winner = %+v", result.record)
			}
		} else if !reflect.DeepEqual(result.record, mail.OutboxMessage{}) {
			t.Fatalf("loser received a record: %+v", result.record)
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent winners = %d, want 1; results=%+v", winners, results)
	}
	for _, result := range runClaims() {
		if result.err != nil || result.ok || !reflect.DeepEqual(result.record, mail.OutboxMessage{}) {
			t.Fatalf("repeated claim acquired send eligibility: %+v", result)
		}
	}
	got, found, err := first.OutboxMessage(ctx, record.AccountEmail, record.ID)
	if err != nil || !found || got.State != mail.OutboxSending || got.Attempt != record.Attempt || !got.SendAt.Equal(record.SendAt) {
		t.Fatalf("record after concurrent claims: found=%v err=%v record=%+v", found, err, got)
	}
}

func TestCancelScheduledOutboxConflictPreservesRecordAndSnapshot(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	record := scheduledOutboxFixture("a@example.com", "scheduled-a", 2, time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC))
	if err := s.EnqueueOutbox(ctx, record.AccountEmail, record, mail.DraftSnapshot{AttachmentErrors: []int{2}}); err != nil {
		t.Fatal(err)
	}
	before, found, err := s.DraftSnapshot(ctx, record.AccountEmail)
	if err != nil || !found {
		t.Fatalf("original snapshot: found=%v err=%v", found, err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*mail.OutboxMessage)
	}{
		{name: "send time", mutate: func(stale *mail.OutboxMessage) { stale.SendAt = stale.SendAt.Add(time.Nanosecond) }},
		{name: "attempt", mutate: func(stale *mail.OutboxMessage) { stale.Attempt++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			stale := record
			test.mutate(&stale)
			if err := s.CancelScheduledOutbox(ctx, stale.AccountEmail, stale); !errors.Is(err, ErrOutboxConflict) {
				t.Fatalf("conflicting cancellation error = %v", err)
			}
			got, found, err := s.OutboxMessage(ctx, record.AccountEmail, record.ID)
			if err != nil || !found || !reflect.DeepEqual(got, record) {
				t.Fatalf("record changed after conflict: found=%v err=%v got=%+v want=%+v", found, err, got, record)
			}
			snapshot, found, err := s.DraftSnapshot(ctx, record.AccountEmail)
			if err != nil || !found || !reflect.DeepEqual(snapshot, before) {
				t.Fatalf("snapshot changed after conflict: found=%v err=%v got=%+v want=%+v", found, err, snapshot, before)
			}
		})
	}
}

func TestCancelScheduledOutboxDetachesTargetSnapshot(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	record := scheduledOutboxFixture("a@example.com", "scheduled-a", 1, time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC))
	if err := s.EnqueueOutbox(ctx, record.AccountEmail, record, mail.DraftSnapshot{AttachmentErrors: []int{1}}); err != nil {
		t.Fatal(err)
	}
	before, found, err := s.DraftSnapshot(ctx, record.AccountEmail)
	if err != nil || !found {
		t.Fatalf("original snapshot: found=%v err=%v", found, err)
	}
	if err := s.CancelScheduledOutbox(ctx, record.AccountEmail, record); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.OutboxMessage(ctx, record.AccountEmail, record.ID); err != nil || found {
		t.Fatalf("cancelled record remains: found=%v err=%v", found, err)
	}
	want := before
	want.OutboxID = ""
	snapshot, found, err := s.DraftSnapshot(ctx, record.AccountEmail)
	requireSnapshot(t, snapshot, found, err, want)
	if snapshot.DraftID != record.DraftID || !reflect.DeepEqual(snapshot.Compose, record.Compose) {
		t.Fatalf("cancellation discarded draft content: %+v", snapshot)
	}
}

func TestCancelScheduledOutboxLeavesDifferentSnapshot(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	record := scheduledOutboxFixture("a@example.com", "scheduled-a", 1, time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC))
	if err := s.EnqueueOutbox(ctx, record.AccountEmail, record, mail.DraftSnapshot{}); err != nil {
		t.Fatal(err)
	}
	other := mail.DraftSnapshot{
		DraftID: "other-draft", OutboxID: "other-outbox",
		Compose: mail.Compose{Text: "other", MessageID: "<other@example.com>"},
	}
	if err := s.SaveDraftSnapshot(ctx, record.AccountEmail, other); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelScheduledOutbox(ctx, record.AccountEmail, record); err != nil {
		t.Fatal(err)
	}
	snapshot, found, err := s.DraftSnapshot(ctx, record.AccountEmail)
	requireSnapshot(t, snapshot, found, err, other)
	if _, found, err := s.OutboxMessage(ctx, record.AccountEmail, record.ID); err != nil || found {
		t.Fatalf("cancelled record remains: found=%v err=%v", found, err)
	}
}

func TestRecoverScheduledOutboxCASAndCompletion(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, filepath.Join(t.TempDir(), "mail.db"))
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	record := scheduledOutboxFixture("a@example.com", "scheduled-a", 2, now)
	if err := s.EnqueueOutbox(ctx, record.AccountEmail, record, mail.DraftSnapshot{}); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := s.ClaimScheduledOutbox(ctx, record.AccountEmail, record, now, false)
	if err != nil || !ok {
		t.Fatalf("claim before recovery: ok=%v err=%v", ok, err)
	}
	claimed.LastError = "connection interrupted"
	claimed.Updated = now.Add(time.Minute)
	staleTime := claimed
	staleTime.SendAt = staleTime.SendAt.Add(time.Nanosecond)
	if recovered, err := s.RecoverScheduledOutbox(ctx, staleTime.AccountEmail, staleTime); err != nil || recovered {
		t.Fatalf("stale-time recovery = %v, %v", recovered, err)
	}
	staleAttempt := claimed
	staleAttempt.Attempt++
	if recovered, err := s.RecoverScheduledOutbox(ctx, staleAttempt.AccountEmail, staleAttempt); err != nil || recovered {
		t.Fatalf("stale-attempt recovery = %v, %v", recovered, err)
	}
	if recovered, err := s.RecoverScheduledOutbox(ctx, claimed.AccountEmail, claimed); err != nil || !recovered {
		t.Fatalf("current recovery = %v, %v", recovered, err)
	}
	want := claimed
	want.State = mail.OutboxUncertain
	got, found, err := s.OutboxMessage(ctx, claimed.AccountEmail, claimed.ID)
	if err != nil || !found || !reflect.DeepEqual(got, want) {
		t.Fatalf("recovered record: found=%v err=%v\ngot=%#v\nwant=%#v", found, err, got, want)
	}
	repeated := claimed
	repeated.LastError = "late callback"
	repeated.Updated = repeated.Updated.Add(time.Minute)
	if recovered, err := s.RecoverScheduledOutbox(ctx, repeated.AccountEmail, repeated); err != nil || recovered {
		t.Fatalf("repeated recovery = %v, %v", recovered, err)
	}
	got, _, _ = s.OutboxMessage(ctx, claimed.AccountEmail, claimed.ID)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repeated recovery changed record: got=%+v want=%+v", got, want)
	}
	messages, err := s.ScheduledOutboxMessages(ctx)
	if err != nil || len(messages) != 0 {
		t.Fatalf("uncertain record returned for retry: %+v, %v", messages, err)
	}
	if err := s.CompleteOutbox(ctx, claimed.AccountEmail, claimed.ID, claimed.Attempt); err != nil {
		t.Fatal(err)
	}
	if recovered, err := s.RecoverScheduledOutbox(ctx, claimed.AccountEmail, claimed); err != nil || recovered {
		t.Fatalf("completed record recovered = %v, %v", recovered, err)
	}
	if _, found, err := s.OutboxMessage(ctx, claimed.AccountEmail, claimed.ID); err != nil || found {
		t.Fatalf("completed record resurrected: found=%v err=%v", found, err)
	}
}
