package app

import (
	"context"
	"testing"
	"time"

	"mica/internal/gmail"
)

func TestPushOnlySchedulesHistoryForActiveAccount(t *testing.T) {
	for _, tc := range []struct {
		name        string
		email       string
		wantBatches int
	}{
		{name: "active account", email: "demo@example.com", wantBatches: 1},
		{name: "other account", email: "other@example.com", wantBatches: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newNotificationHarness()
			var delays []time.Duration
			h.app.after = func(delay time.Duration, fn func()) {
				delays = append(delays, delay)
			}
			h.service.stream = func(_ context.Context, fn func(gmail.PushNotification) error) error {
				if err := fn(gmail.PushNotification{EmailAddress: tc.email, HistoryID: 9}); err != nil {
					return err
				}
				return context.Canceled
			}
			h.app.Start()
			batches := 0
			for _, delay := range delays {
				if delay == historyBatchDelay {
					batches++
				}
			}
			if batches != tc.wantBatches {
				t.Fatalf("history batches = %d, want %d; delays=%v", batches, tc.wantBatches, delays)
			}
		})
	}
}
