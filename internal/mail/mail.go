package mail

import (
	"context"
	"time"
)

const (
	Inbox     = "INBOX"
	Starred   = "STARRED"
	Snoozed   = "SNOOZED"
	Sent      = "SENT"
	Draft     = "DRAFT"
	Important = "IMPORTANT"
	All       = "ALL"
	Spam      = "SPAM"
	Trash     = "TRASH"
	Unread    = "UNREAD"

	CategorySocial       = "CATEGORY_SOCIAL"
	CategoryPromotions   = "CATEGORY_PROMOTIONS"
	CategoryUpdates      = "CATEGORY_UPDATES"
	CategoryForums       = "CATEGORY_FORUMS"
	CategoryPurchases    = "CATEGORY_PURCHASES"
	CategoryReservations = "CATEGORY_RESERVATIONS"
)

type Account struct {
	Name                string
	Email               string
	Demo                bool
	PhotoURL            string
	ProfileUnauthorized bool
}

type LabelColor struct {
	Background string
	Text       string
}

type Label struct {
	ID           string
	Name         string
	Messages     int
	Unread       int
	System       bool
	Hide         bool
	ShowIfUnread bool
	Color        LabelColor
}

type ThreadSummary struct {
	ID           string
	Subject      string
	Snippet      string
	Senders      string
	Updated      time.Time
	MessageCount int
	LabelIDs     []string
	Unread       bool
	Starred      bool
}

type Thread struct {
	ID       string
	Subject  string
	LabelIDs []string
	Messages []Message
}

type Message struct {
	ID          string
	ThreadID    string
	From        string
	ReplyTo     string
	To          string
	Cc          string
	Subject     string
	MessageID   string
	References  string
	Date        time.Time
	Text        string
	HTML        string
	LabelIDs    []string
	Attachments []Attachment
}

type Attachment struct {
	ID        string
	Name      string
	MIMEType  string
	Size      int64
	ContentID string
}

type Query struct {
	LabelID   string
	Search    string
	Limit     int
	PageToken string `json:",omitempty"`
}

type ThreadPage struct {
	Threads       []ThreadSummary
	NextPageToken string
}

type Compose struct {
	To         string
	Cc         string
	Bcc        string
	Subject    string
	Text       string
	HTML       string
	ThreadID   string
	InReplyTo  string
	References string
}

type Service interface {
	Account(context.Context) (Account, error)
	Labels(context.Context) ([]Label, error)
	ListThreads(context.Context, Query) ([]ThreadSummary, error)
	Thread(context.Context, string) (Thread, error)
	ModifyThread(context.Context, string, []string, []string) error
	TrashThread(context.Context, string) error
	Send(context.Context, Compose) error
}

type ThreadPager interface {
	ListThreadPage(context.Context, Query) (ThreadPage, error)
}

type LabelManager interface {
	CreateLabel(context.Context, string) (Label, error)
	DeleteLabel(context.Context, string) error
	UpdateLabelColor(context.Context, string, LabelColor) error
}

type AttachmentFetcher interface {
	Attachment(context.Context, string, string) ([]byte, error)
}
