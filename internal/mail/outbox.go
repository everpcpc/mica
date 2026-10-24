package mail

import (
	"context"
	"time"
)

type OutboxState string

const (
	OutboxSending   OutboxState = "sending"
	OutboxUncertain OutboxState = "uncertain"
)

type OutboxMessage struct {
	ID           string
	AccountEmail string
	DraftID      string
	Compose      Compose
	State        OutboxState
	Attempt      uint64
	Created      time.Time
	Updated      time.Time
	LastError    string
}

type SentMessageFinder interface {
	SentMessageExists(context.Context, string) (bool, error)
}
