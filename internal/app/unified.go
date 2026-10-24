package app

import (
	"sort"

	"mica/internal/mail"
)

type unifiedAccountStream struct {
	email         string
	threads       []mail.ThreadSummary
	nextPageToken string
	cursor        int
	emitted       map[string]bool
	err           error
}

func newUnifiedAccountStream(email string, threads []mail.ThreadSummary) *unifiedAccountStream {
	stream := &unifiedAccountStream{email: normalizedEmail(email)}
	stream.resetThreads(threads, "")
	return stream
}

func (s *unifiedAccountStream) resetThreads(threads []mail.ThreadSummary, nextPageToken string) {
	s.threads = cloneThreadSummaries(threads)
	s.nextPageToken = nextPageToken
	s.cursor = 0
	s.emitted = make(map[string]bool)
	sortUnifiedSummaries(s.threads)
}

func (s *unifiedAccountStream) appendPage(page mail.ThreadPage) {
	s.threads = append(s.threads, cloneThreadSummaries(page.Threads)...)
	s.nextPageToken = page.NextPageToken
	sortUnifiedSummaries(s.threads)
	s.syncCursor()
}

func (s *unifiedAccountStream) syncCursor() {
	s.cursor = 0
	for s.cursor < len(s.threads) && s.emitted[s.threads[s.cursor].ID] {
		s.cursor++
	}
}

func sortUnifiedSummaries(threads []mail.ThreadSummary) {
	sort.SliceStable(threads, func(i, j int) bool {
		return unifiedSummaryLess(threads[i], threads[j])
	})
}

func unifiedSummaryLess(left, right mail.ThreadSummary) bool {
	if !left.Updated.Equal(right.Updated) {
		return left.Updated.After(right.Updated)
	}
	if normalizedEmail(left.AccountEmail) != normalizedEmail(right.AccountEmail) {
		return normalizedEmail(left.AccountEmail) < normalizedEmail(right.AccountEmail)
	}
	return left.ID < right.ID
}

func (s *unifiedAccountStream) next() (mail.ThreadSummary, bool) {
	s.syncCursor()
	if s.cursor >= len(s.threads) {
		return mail.ThreadSummary{}, false
	}
	summary := s.threads[s.cursor]
	s.emitted[summary.ID] = true
	summary.AccountEmail = s.email
	s.syncCursor()
	return summary, true
}

func mergeUnifiedAccounts(accounts []*unifiedAccountStream, limit int) []mail.ThreadSummary {
	if limit <= 0 {
		return nil
	}
	merged := make([]mail.ThreadSummary, 0, limit)
	for len(merged) < limit {
		best := -1
		var candidate mail.ThreadSummary
		for i, stream := range accounts {
			if stream.cursor >= len(stream.threads) {
				continue
			}
			current := stream.threads[stream.cursor]
			current.AccountEmail = stream.email
			if best < 0 || unifiedSummaryLess(current, candidate) {
				best = i
				candidate = current
			}
		}
		if best < 0 {
			break
		}
		summary, ok := accounts[best].next()
		if !ok {
			continue
		}
		merged = append(merged, summary)
	}
	return merged
}

func unifiedHasMore(accounts []*unifiedAccountStream) bool {
	for _, stream := range accounts {
		if stream.err != nil || stream.cursor < len(stream.threads) || stream.nextPageToken != "" {
			return true
		}
	}
	return false
}
