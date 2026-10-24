package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"mica/internal/mail"
)

type cachedSearchCandidate struct {
	summary  mail.ThreadSummary
	revision int64
	detail   bool
}

type cachedThreadDetail struct {
	thread   mail.Thread
	revision int64
}

type cachedSearchRow struct {
	data     []byte
	revision int64
	detail   bool
}

func (s *Store) SearchThreads(ctx context.Context, email string, query mail.Query) ([]mail.ThreadSummary, error) {
	email = normalizeAccountEmail(email)
	labelID, search := query.LabelID, strings.ToLower(strings.TrimSpace(query.Search))
	unreadOnly := search == "is:unread"
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
	rows, err := s.db.Query(ctx, `SELECT 0 AS kind, data, updated, query_key AS record_key FROM thread_lists WHERE account_email = ? COLLATE NOCASE
		UNION ALL
		SELECT 1, data, updated, thread_id FROM threads WHERE account_email = ? COLLATE NOCASE
		ORDER BY kind, record_key, updated`, email, email)
	if err != nil {
		return nil, err
	}
	candidates := make(map[string]cachedSearchCandidate)
	details := make(map[string]cachedThreadDetail)
	add := func(summary mail.ThreadSummary, revision int64, detail bool) {
		if current, found := candidates[summary.ID]; found && (current.revision > revision || current.revision == revision && current.detail && !detail) {
			return
		}
		if summary.AccountEmail == "" {
			summary.AccountEmail = email
		}
		summary.Unread = hasCachedLabel(summary.LabelIDs, mail.Unread)
		summary.Starred = hasCachedLabel(summary.LabelIDs, mail.Starred)
		candidates[summary.ID] = cachedSearchCandidate{summary: summary, revision: revision, detail: detail}
	}
	for _, values := range rows.Values {
		row, err := decodeCachedSearchRow(values)
		if err != nil {
			return nil, err
		}
		if !row.detail {
			var summaries []mail.ThreadSummary
			if err := json.Unmarshal(row.data, &summaries); err != nil {
				return nil, fmt.Errorf("store: decode cached thread list: %w", err)
			}
			for _, summary := range summaries {
				add(summary, row.revision, false)
			}
			continue
		}
		var thread mail.Thread
		if err := json.Unmarshal(row.data, &thread); err != nil {
			return nil, fmt.Errorf("store: decode cached thread: %w", err)
		}
		if current, found := details[thread.ID]; !found || current.revision <= row.revision {
			details[thread.ID] = cachedThreadDetail{thread: thread, revision: row.revision}
		}
		add(cachedThreadSummary(thread), row.revision, true)
	}
	result := make([]mail.ThreadSummary, 0, len(candidates))
	for id, candidate := range candidates {
		if cachedThreadMatches(candidate.summary, details[id].thread, labelID, search, unreadOnly) {
			result = append(result, candidate.summary)
		}
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

func decodeCachedSearchRow(values []any) (cachedSearchRow, error) {
	if len(values) != 4 {
		return cachedSearchRow{}, fmt.Errorf("store: invalid cached search row with %d columns", len(values))
	}
	kind, kindOK := values[0].(int64)
	revision, revisionOK := values[2].(int64)
	if !kindOK || kind != 0 && kind != 1 || !revisionOK {
		return cachedSearchRow{}, fmt.Errorf("store: invalid cached search metadata")
	}
	row := cachedSearchRow{revision: revision, detail: kind == 1}
	switch data := values[1].(type) {
	case []byte:
		row.data = data
	case string:
		row.data = []byte(data)
	default:
		return cachedSearchRow{}, fmt.Errorf("store: invalid cached search data type %T", data)
	}
	return row, nil
}

func cachedThreadMatches(summary mail.ThreadSummary, thread mail.Thread, labelID, search string, unreadOnly bool) bool {
	if unreadOnly && !summary.Unread {
		return false
	}
	if labelID != "" && !hasCachedLabel(summary.LabelIDs, labelID) {
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
