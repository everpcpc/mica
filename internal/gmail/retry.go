package gmail

import (
	"context"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

const (
	maxAttempts    = 4
	retryBaseDelay = 500 * time.Millisecond
	retryMaxDelay  = 5 * time.Second
)

func (e *APIError) Transient() bool {
	return e.Reason == "rateLimitExceeded" || e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

func retryDelay(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	delay := retryBaseDelay << (attempt - 1)
	if delay > retryMaxDelay {
		delay = retryMaxDelay
	}
	return delay + time.Duration(rand.Int63n(100))*time.Millisecond
}

func parseRetryAfter(value string) time.Duration {
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if delay := time.Until(at); delay > 0 {
			return delay
		}
	}
	return 0
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
