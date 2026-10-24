package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"mica/internal/mail"
)

func (s *Store) SearchThreads(ctx context.Context, email string, query mail.Query) ([]mail.ThreadSummary, error) {
	email = normalizeAccountEmail(email)
	labelID, search := query.LabelID, strings.ToLower(strings.TrimSpace(query.Search))
	switch search {
	case "in:snoozed":
		labelID, search = mail.Snoozed, ""
	case "category:purchases":
		labelID, search = mail.CategoryPurchases, ""
	case "category:reservations":
		labelID, search = mail.CategoryReservations, ""
	case "is:unread":
		search = ""
	}
	rows, err := s.db.Query(ctx, "SELECT data FROM thread_lists WHERE account_email = ?", email)
	if err != nil {
		return nil, err
	}
	details := make(map[string]mail.Thread)
	detailRows, err := s.db.Query(ctx, "SELECT data FROM threads WHERE account_email = ?", email)
	if err != nil {
		return nil, err
	}
	for _, values := range detailRows.Values {
		var thread mail.Thread
		if err := json.Unmarshal(values[0].([]byte), &thread); err != nil {
			return nil, fmt.Errorf("store: decode cached thread: %w", err)
		}
		details[thread.ID] = thread
	}
	candidates := make(map[string]mail.ThreadSummary)
	for _, values := range rows.Values {
		var summaries []mail.ThreadSummary
		if err := json.Unmarshal(values[0].([]byte), &summaries); err != nil {
			return nil, fmt.Errorf("store: decode cached thread list: %w", err)
		}
		for _, summary := range summaries {
			if !cachedThreadMatches(summary, details[summary.ID], labelID, search, query.Search == "is:unread") {
				continue
			}
			if summary.AccountEmail == "" {
				summary.AccountEmail = email
			}
			if current, found := candidates[summary.ID]; !found || current.Updated.Before(summary.Updated) {
				candidates[summary.ID] = summary
			}
		}
	}
	for id, thread := range details {
		if _, found := candidates[id]; found {
			continue
		}
		summary := cachedThreadSummary(thread)
		if cachedThreadMatches(summary, thread, labelID, search, query.Search == "is:unread") {
			if summary.AccountEmail == "" {
				summary.AccountEmail = email
			}
			candidates[id] = summary
		}
	}
	result := make([]mail.ThreadSummary, 0, len(candidates))
	for _, summary := range candidates {
		result = append(result, summary)
	}
	sortThreadSummaries(result)
	limit := query.Limit
	if limit <= 0 {
		limit = 25
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func cachedThreadMatches(summary mail.ThreadSummary, thread mail.Thread, labelID, search string, unreadOnly bool) bool {
	if unreadOnly && !summary.Unread {
		return false
	}
	if labelID != "" && !hasCachedLabel(summary.LabelIDs, labelID) && !hasCachedLabel(thread.LabelIDs, labelID) {
		return false
	}
	if search == "" {
		return true
	}
	if strings.Contains(strings.ToLower(summary.Subject+" "+summary.Senders+" "+summary.Snippet), search) {
		return true
	}
	for _, message := range thread.Messages {
		if strings.Contains(strings.ToLower(message.From+" "+message.To+" "+message.Subject+" "+message.Text+" "+message.HTML), search) {
			return true
		}
	}
	return false
}

func hasCachedLabel(labels []string, target string) bool {
	for _, label := range labels {
		if label == target {
			return true
		}
	}
	return false
}

func cachedThreadSummary(thread mail.Thread) mail.ThreadSummary {
	summary := mail.ThreadSummary{ID: thread.ID, AccountEmail: thread.AccountEmail, Subject: thread.Subject, MessageCount: len(thread.Messages), LabelIDs: thread.LabelIDs}
	for _, message := range thread.Messages {
		summary.Senders = message.From
		if message.Date.After(summary.Updated) {
			summary.Updated = message.Date
		}
		if text := strings.Join(strings.Fields(message.Text), " "); text != "" {
			runes := []rune(text)
			if len(runes) > 120 {
				text = string(runes[:119]) + "…"
			}
			summary.Snippet = text
		}
	}
	summary.Unread = hasCachedLabel(summary.LabelIDs, mail.Unread)
	summary.Starred = hasCachedLabel(summary.LabelIDs, mail.Starred)
	return summary
}

func sortThreadSummaries(summaries []mail.ThreadSummary) {
	for i := 1; i < len(summaries); i++ {
		for j := i; j > 0; j-- {
			left, right := summaries[j-1], summaries[j]
			if left.Updated.After(right.Updated) || left.Updated.Equal(right.Updated) && left.ID <= right.ID {
				break
			}
			summaries[j-1], summaries[j] = right, left
		}
	}
}
