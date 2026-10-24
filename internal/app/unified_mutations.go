package app

import (
	"context"
	"sort"

	"mica/internal/mail"
)

func (a *App) unifiedStreamForKey(key string) (*unifiedState, *unifiedAccountStream, threadRef, bool) {
	if !a.compositeThreads || a.unified == nil {
		return nil, nil, threadRef{}, false
	}
	ref := parseThreadRef(key)
	if ref.AccountEmail == "" {
		ref.AccountEmail = a.storeAccount
	}
	ref.AccountEmail = normalizedEmail(ref.AccountEmail)
	stream := a.unified.accounts[ref.AccountEmail]
	if stream == nil || ref.ID == "" {
		return nil, nil, threadRef{}, false
	}
	return a.unified, stream, ref, true
}

func rememberUnifiedSummary(state *unifiedState, summary mail.ThreadSummary) {
	if state.modified == nil {
		state.modified = make(map[string]mail.ThreadSummary)
	}
	summary.LabelIDs = append([]string(nil), summary.LabelIDs...)
	state.modified[summaryKey(summary)] = summary
}

func (a *App) syncUnifiedSummary(id string) {
	state, stream, ref, ok := a.unifiedStreamForKey(id)
	if !ok {
		return
	}
	for _, summary := range a.threads {
		if a.summaryKey(summary) != id {
			continue
		}
		summary.AccountEmail = stream.email
		for i := range stream.threads {
			if stream.threads[i].ID == ref.ID {
				stream.threads[i] = summary
				stream.threads[i].LabelIDs = append([]string(nil), summary.LabelIDs...)
				rememberUnifiedSummary(state, summary)
				sortUnifiedSummaries(stream.threads)
				stream.syncCursor()
				return
			}
		}
		if !state.removed[id] {
			stream.threads = append(stream.threads, summary)
			stream.emitted[ref.ID] = true
			rememberUnifiedSummary(state, summary)
			sortUnifiedSummaries(stream.threads)
			stream.syncCursor()
		}
		return
	}
}

func (a *App) removeUnifiedThread(id string) {
	state, stream, ref, ok := a.unifiedStreamForKey(id)
	if !ok {
		return
	}
	if state.removed == nil {
		state.removed = make(map[string]bool)
	}
	key := threadRefForAccount(stream.email, ref.ID).key()
	delete(state.modified, key)
	state.removed[key] = true
	for i := range stream.threads {
		if stream.threads[i].ID == ref.ID {
			stream.threads = append(stream.threads[:i], stream.threads[i+1:]...)
			break
		}
	}
	stream.syncCursor()
}

func (a *App) restoreUnifiedSummary(summary mail.ThreadSummary) {
	key := summaryKey(summary)
	state, stream, _, ok := a.unifiedStreamForKey(key)
	if !ok {
		return
	}
	delete(state.removed, key)
	summary.AccountEmail = stream.email
	summary.LabelIDs = append([]string(nil), summary.LabelIDs...)
	streamFound := false
	for i := range stream.threads {
		if stream.threads[i].ID == summary.ID {
			stream.threads[i] = summary
			streamFound = true
			break
		}
	}
	if !streamFound {
		stream.threads = append(stream.threads, summary)
	}
	stream.emitted[summary.ID] = true
	sortUnifiedSummaries(stream.threads)
	stream.syncCursor()
	visibleFound := false
	for i := range a.threads {
		if a.summaryKey(a.threads[i]) == key {
			a.threads[i] = summary
			visibleFound = true
			break
		}
	}
	if !visibleFound {
		a.threads = append(a.threads, summary)
	}
	sort.SliceStable(a.threads, func(i, j int) bool {
		return unifiedSummaryLess(a.threads[i], a.threads[j])
	})
}

func (a *App) restoreUnifiedUndo(action *undoAction) {
	hidden := make(map[string]bool)
	for _, item := range action.items {
		if item.summary.ID != "" {
			restored := item.summary
			restored.LabelIDs = modifyLabelIDs(restored.LabelIDs, item.add, item.remove)
			if historyListContains(a.unified.query, restored) {
				a.restoreUnifiedSummary(item.summary)
			} else {
				hidden[item.id] = true
				a.removeUnifiedThread(item.id)
			}
		}
		a.updateThreadLabels(item.id, item.add, item.remove)
	}
	visible := make([]mail.ThreadSummary, 0, len(a.threads))
	for _, summary := range a.threads {
		if !hidden[a.summaryKey(summary)] {
			visible = append(visible, summary)
		}
	}
	a.applyThreadList(visible, len(hidden) > 0)
	a.persistUnifiedLists()
}

func unifiedSnapshotThreads(state *unifiedState, stream *unifiedAccountStream) []mail.ThreadSummary {
	threads := make([]mail.ThreadSummary, 0, len(stream.threads))
	for _, summary := range stream.threads {
		if state.removed[threadRefForAccount(stream.email, summary.ID).key()] {
			continue
		}
		summary.AccountEmail = stream.email
		summary.LabelIDs = append([]string(nil), summary.LabelIDs...)
		threads = append(threads, summary)
	}
	return threads
}

func unifiedAvailableThreads(state *unifiedState, stream *unifiedAccountStream, threads []mail.ThreadSummary) []mail.ThreadSummary {
	available := make([]mail.ThreadSummary, 0, len(threads))
	for _, summary := range threads {
		key := threadRefForAccount(stream.email, summary.ID).key()
		if state.removed[key] {
			continue
		}
		if modified, ok := state.modified[key]; ok {
			summary = modified
		}
		available = append(available, summary)
	}
	return available
}

func resetUnifiedStream(state *unifiedState, stream *unifiedAccountStream, threads []mail.ThreadSummary, nextPageToken string) {
	available := unifiedAvailableThreads(state, stream, threads)
	for key, modified := range state.modified {
		ref := parseThreadRef(key)
		if normalizedEmail(ref.AccountEmail) != stream.email || state.removed[key] {
			continue
		}
		found := false
		for _, summary := range available {
			if summary.ID == ref.ID {
				found = true
				break
			}
		}
		if !found {
			available = append(available, modified)
		}
	}
	stream.resetThreads(available, nextPageToken)
}

func appendUnifiedPage(state *unifiedState, stream *unifiedAccountStream, page mail.ThreadPage) {
	page.Threads = unifiedAvailableThreads(state, stream, page.Threads)
	stream.appendPage(page)
}

func (a *App) persistUnifiedLists() {
	if a.unified == nil {
		return
	}
	for _, stream := range unifiedStreams(a.unified) {
		a.persistUnifiedStream(a.unified, stream)
	}
}

func (a *App) persistUnifiedStream(state *unifiedState, stream *unifiedAccountStream) {
	threads := unifiedSnapshotThreads(state, stream)
	version, revision := a.nextListCacheVersion(stream.email, state.query)
	a.persistForAccount(stream.email, func(store mailStore, email string) error {
		if version.Load() != revision {
			return nil
		}
		return store.SaveThreadList(context.Background(), email, state.query, threads)
	})
}
