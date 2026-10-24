package app

import (
	"strings"

	"mica/internal/mail"
)

type threadRef struct {
	AccountEmail string
	ID           string
}

func (r threadRef) key() string {
	email := normalizedEmail(r.AccountEmail)
	if email == "" {
		return r.ID
	}
	return email + "\x00" + r.ID
}

func parseThreadRef(key string) threadRef {
	email, id, found := strings.Cut(key, "\x00")
	if !found {
		return threadRef{ID: key}
	}
	return threadRef{AccountEmail: email, ID: id}
}

func summaryRef(summary mail.ThreadSummary) threadRef {
	return threadRef{AccountEmail: summary.AccountEmail, ID: summary.ID}
}

func summaryKey(summary mail.ThreadSummary) string {
	return summaryRef(summary).key()
}

func (a *App) summaryKey(summary mail.ThreadSummary) string {
	if !a.compositeThreads {
		return summary.ID
	}
	return summaryRef(summary).key()
}

func mailThreadKey(thread mail.Thread) string {
	return threadRefForAccount(thread.AccountEmail, thread.ID).key()
}

func (a *App) threadKey(thread mail.Thread) string {
	if !a.compositeThreads {
		return thread.ID
	}
	return mailThreadKey(thread)
}

func threadRefForAccount(email, id string) threadRef {
	return threadRef{AccountEmail: email, ID: id}
}

func withThreadOwner(thread mail.Thread, email string) mail.Thread {
	email = normalizedEmail(email)
	if email == "" {
		return thread
	}
	thread.AccountEmail = email
	for i := range thread.Messages {
		thread.Messages[i].AccountEmail = email
	}
	return thread
}

type messageRef struct {
	AccountEmail string
	ThreadID     string
	MessageID    string
}

func (r messageRef) key() string {
	return threadRefForAccount(r.AccountEmail, r.ThreadID).key() + "\x00" + r.MessageID
}

func messageRefForAccount(email, threadID, messageID string) messageRef {
	return messageRef{AccountEmail: email, ThreadID: threadID, MessageID: messageID}
}
