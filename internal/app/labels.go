package app

import (
	"strings"

	"mica/internal/mail"
)

func hiddenSystemLabel(id string) bool {
	switch id {
	case mail.Inbox, mail.Starred, mail.Sent, mail.Trash, mail.Unread,
		"ALL", "CHAT", "DRAFT", "IMPORTANT", "SPAM":
		return true
	}
	return strings.HasPrefix(id, "CATEGORY_")
}
