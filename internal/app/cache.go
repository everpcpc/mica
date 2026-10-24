package app

import (
	"context"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mica/internal/mail"
)

type listCacheKey struct {
	email string
	query mail.Query
}

type listCacheVersions map[listCacheKey]*atomic.Uint64

func (a *App) nextListCacheVersion(email string, query mail.Query) (*atomic.Uint64, uint64) {
	a.cacheState.versionsMu.Lock()
	defer a.cacheState.versionsMu.Unlock()
	if a.listVersions == nil {
		a.listVersions = make(listCacheVersions)
	}
	key := listCacheKey{email: normalizedEmail(email), query: query}
	version := a.listVersions[key]
	if version == nil {
		version = &atomic.Uint64{}
		a.listVersions[key] = version
	}
	return version, version.Add(1)
}

type threadCacheKey struct {
	email string
	id    string
}

type threadCacheVersions map[threadCacheKey]*atomic.Uint64

func (a *App) nextThreadCacheVersion(email, id string) (*atomic.Uint64, uint64) {
	a.cacheState.versionsMu.Lock()
	defer a.cacheState.versionsMu.Unlock()
	if a.threadVersions == nil {
		a.threadVersions = make(threadCacheVersions)
	}
	email = normalizedEmail(email)
	if email == "" {
		email = a.storeAccount
	}
	key := threadCacheKey{email: email, id: id}
	version := a.threadVersions[key]
	if version == nil {
		version = &atomic.Uint64{}
		a.threadVersions[key] = version
	}
	return version, version.Add(1)
}

type cacheState struct {
	mu         sync.Mutex
	versionsMu sync.Mutex
	writes     sync.WaitGroup
	version    int
	widthSave  int
	draftWrite uint64

	remoteImagesSave        uint64
	attachmentWritesBlocked bool
}

type mailStore interface {
	Accounts(context.Context) ([]mail.Account, error)
	Account(context.Context, string) (mail.Account, bool, error)
	LastAccount(context.Context) (mail.Account, bool, error)
	SaveAccount(context.Context, mail.Account) error
	SetLastAccount(context.Context, string) error
	DeleteAccount(context.Context, string) error
	Labels(context.Context, string) ([]mail.Label, bool, error)
	SaveLabels(context.Context, string, []mail.Label) error
	ThreadList(context.Context, string, mail.Query) ([]mail.ThreadSummary, bool, error)
	SaveThreadList(context.Context, string, mail.Query, []mail.ThreadSummary) error
	Thread(context.Context, string, string) (mail.Thread, bool, error)
	SaveThread(context.Context, string, mail.Thread) error
	DeleteThread(context.Context, string, string) error
	Preference(context.Context, string) (string, bool, error)
	SavePreference(context.Context, string, string) error
}

type offlineSearchStore interface {
	SearchThreads(context.Context, string, mail.Query) ([]mail.ThreadSummary, error)
}

type attachmentStore interface {
	SaveAttachment(context.Context, string, string, string, string, []byte) error
	Attachment(context.Context, string, string, string, string) ([]byte, bool, error)
}

type attachmentCacheWriter func(threadID, messageID, attachmentID string, data []byte, tracked ...bool)
type attachmentCacheBegin func() func()

func (a *App) attachmentCacheBegin(owner string, generation uint64) attachmentCacheBegin {
	owner = normalizedEmail(owner)
	if _, ok := a.store.(attachmentStore); !ok || owner == "" {
		return nil
	}
	client := a.accountClients[owner]
	version := a.serviceVersion
	return func() func() {
		a.cacheState.mu.Lock()
		defer a.cacheState.mu.Unlock()
		if a.cacheState.attachmentWritesBlocked || version != a.cacheState.version || client != nil && client.generation.Load() != generation {
			return nil
		}
		a.cacheState.writes.Add(1)
		return a.cacheState.writes.Done
	}
}

func (a *App) attachmentCacheWriter(owner string, generation uint64) attachmentCacheWriter {
	owner = normalizedEmail(owner)
	store, ok := a.store.(attachmentStore)
	if !ok || owner == "" {
		return nil
	}
	client := a.accountClients[owner]
	version := a.serviceVersion
	return func(threadID, messageID, attachmentID string, data []byte, tracked ...bool) {
		a.cacheState.mu.Lock()
		defer a.cacheState.mu.Unlock()
		trackedOperation := len(tracked) > 0 && tracked[0]
		if !trackedOperation && a.cacheState.attachmentWritesBlocked || version != a.cacheState.version || client != nil && client.generation.Load() != generation {
			return
		}
		if !trackedOperation {
			a.cacheState.writes.Add(1)
			defer a.cacheState.writes.Done()
		}
		if err := store.SaveAttachment(context.Background(), owner, threadID, messageID, attachmentID, data); err != nil {
			logCacheError(err)
		}
	}
}

func normalizedEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (a *App) SetMailStore(store mailStore) {
	a.cacheState.mu.Lock()
	a.store = store
	a.outbox = nil
	if outbox, ok := store.(outboxStore); ok {
		a.outbox = outbox
	}
	a.cacheState.version = a.serviceVersion
	a.cacheState.mu.Unlock()
	a.resetMessageViews()
}

func (a *App) currentCacheVersion() int {
	a.cacheState.mu.Lock()
	defer a.cacheState.mu.Unlock()
	if generation, ok := a.accountGenerations[a.activeGenerationKey()]; ok {
		return int(generation)
	}
	return a.serviceVersion
}

func (a *App) persistAccount(account mail.Account) {
	a.configureMessageCache()
	if account.Demo || account.Email == "" {
		return
	}
	account.Email = normalizedEmail(account.Email)
	a.storeAccount = account.Email
	if a.compose != nil && a.compose.revision > a.compose.savedRevision {
		a.persistDraftSnapshot(a.compose)
	}
	a.persist(func(store mailStore, email string) error {
		return store.SaveAccount(context.Background(), account)
	})
}

func (a *App) persistLabels(labels []mail.Label) {
	a.persistLabelsForAccount(a.storeAccount, labels)
}

func (a *App) persistLabelsForAccount(accountEmail string, labels []mail.Label) {
	labels = slices.Clone(labels)
	a.persistForAccount(accountEmail, func(store mailStore, email string) error {
		return store.SaveLabels(context.Background(), email, labels)
	})
}

func (a *App) persistList(query mail.Query, threads []mail.ThreadSummary) {
	if a.compositeThreads && a.unified != nil {
		a.persistUnifiedLists()
		return
	}
	threads = cloneThreadSummaries(threads)
	groups := make(map[string][]mail.ThreadSummary)
	for _, thread := range threads {
		email := normalizedEmail(thread.AccountEmail)
		if email == "" {
			email = a.storeAccount
		}
		groups[email] = append(groups[email], thread)
	}
	owners := []string{a.storeAccount}
	if a.compositeThreads {
		for email := range a.accountClients {
			owners = append(owners, email)
		}
	}
	slices.Sort(owners)
	seen := make(map[string]bool, len(owners))
	for _, email := range owners {
		if email == "" || seen[email] {
			continue
		}
		seen[email] = true
		version, revision := a.nextListCacheVersion(email, query)
		a.persistForAccount(email, func(store mailStore, email string) error {
			if version.Load() != revision {
				return nil
			}
			return store.SaveThreadList(context.Background(), email, query, groups[email])
		})
	}
}

func (a *App) WaitForCacheWrites() {
	a.cacheState.mu.Lock()
	a.cacheState.attachmentWritesBlocked = true
	a.cacheState.mu.Unlock()
	a.cacheState.writes.Wait()
}

func (a *App) persistThread(thread mail.Thread) {
	thread = cloneMailThread(thread)
	version, revision := a.nextThreadCacheVersion(thread.AccountEmail, thread.ID)
	a.persistForAccount(thread.AccountEmail, func(store mailStore, email string) error {
		if version.Load() != revision {
			return nil
		}
		return store.SaveThread(context.Background(), email, thread)
	})
}

func (a *App) restoreUnread(id string, query mail.Query) {
	ref := parseThreadRef(id)
	owner := normalizedEmail(ref.AccountEmail)
	if owner == "" {
		owner = a.storeAccount
	}
	version, revision := a.nextListCacheVersion(owner, query)
	detailVersion, detailRevision := a.nextThreadCacheVersion(owner, ref.ID)
	a.persistForAccount(owner, func(store mailStore, email string) error {
		if version.Load() == revision {
			threads, found, err := store.ThreadList(context.Background(), email, query)
			if err != nil {
				return err
			}
			if found {
				changed := false
				for i := range threads {
					if threads[i].ID != ref.ID {
						continue
					}
					threads[i].AccountEmail = email
					threads[i].LabelIDs = modifyLabelIDs(threads[i].LabelIDs, []string{mail.Unread}, nil)
					threads[i].Unread = true
					changed = true
					break
				}
				if changed {
					if err := store.SaveThreadList(context.Background(), email, query, threads); err != nil {
						return err
					}
				}
			}
		}
		if detailVersion.Load() != detailRevision {
			return nil
		}
		thread, found, err := store.Thread(context.Background(), email, ref.ID)
		if err != nil || !found {
			return err
		}
		thread.AccountEmail = email
		thread.LabelIDs = modifyLabelIDs(thread.LabelIDs, []string{mail.Unread}, nil)
		for i := range thread.Messages {
			thread.Messages[i].AccountEmail = email
			thread.Messages[i].LabelIDs = modifyLabelIDs(thread.Messages[i].LabelIDs, []string{mail.Unread}, nil)
		}
		if detailVersion.Load() != detailRevision {
			return nil
		}
		return store.SaveThread(context.Background(), email, thread)
	})
}

func (a *App) persist(write func(mailStore, string) error) {
	a.persistForAccount(a.storeAccount, write)
}

func (a *App) persistForAccount(accountEmail string, write func(mailStore, string) error) {
	email := normalizedEmail(accountEmail)
	if email == "" {
		email = a.storeAccount
	}
	store, version := a.store, a.serviceVersion
	_, ownerGeneration := a.ownerService(email)
	ownerClient := a.accountClients[email]
	if store == nil || email == "" || a.accountTransition != nil {
		return
	}
	a.cacheState.writes.Add(1)
	a.schedule(func() {
		defer a.cacheState.writes.Done()
		a.cacheState.mu.Lock()
		defer a.cacheState.mu.Unlock()
		if version != a.cacheState.version {
			return
		}
		if ownerClient != nil && ownerClient.generation.Load() != ownerGeneration {
			return
		}
		if err := write(store, email); err != nil {
			log.Printf("mica: write mail cache: %v", err)
		}
	})
}

const (
	sidebarWidthPreference = "sidebar-width"
	listWidthPreference    = "list-width"
	paneWidthSaveDelay     = 400 * time.Millisecond
)

func (a *App) RestorePaneWidths() {
	if a.store == nil {
		return
	}
	for _, preference := range []struct {
		key    string
		target *float32
	}{
		{key: sidebarWidthPreference, target: &a.sidebarWidth},
		{key: listWidthPreference, target: &a.listWidth},
	} {
		value, found, err := a.store.Preference(context.Background(), preference.key)
		logCacheError(err)
		if err != nil || !found {
			continue
		}
		width, err := strconv.ParseFloat(value, 32)
		if err == nil && width >= 160 && width <= 600 {
			*preference.target = float32(width)
		}
	}
}

func (a *App) savePaneWidths() {
	a.cacheState.mu.Lock()
	a.cacheState.widthSave++
	generation := a.cacheState.widthSave
	store := a.store
	sidebarWidth, listWidth := a.sidebarWidth, a.listWidth
	a.cacheState.mu.Unlock()
	if store == nil {
		return
	}
	a.after(paneWidthSaveDelay, func() {
		a.cacheState.mu.Lock()
		defer a.cacheState.mu.Unlock()
		if generation != a.cacheState.widthSave {
			return
		}
		for _, preference := range []struct {
			key   string
			value string
		}{
			{key: sidebarWidthPreference, value: strconv.FormatFloat(float64(sidebarWidth), 'f', -1, 32)},
			{key: listWidthPreference, value: strconv.FormatFloat(float64(listWidth), 'f', -1, 32)},
		} {
			if err := store.SavePreference(context.Background(), preference.key, preference.value); err != nil {
				log.Printf("mica: save pane width: %v", err)
			}
		}
	})
}

func logCacheError(err error) {
	if err != nil {
		log.Printf("mica: read mail cache: %v", err)
	}
}

func cloneThreadSummaries(threads []mail.ThreadSummary) []mail.ThreadSummary {
	cloned := slices.Clone(threads)
	for i := range cloned {
		cloned[i].LabelIDs = slices.Clone(cloned[i].LabelIDs)
	}
	return cloned
}

func cloneMailThread(thread mail.Thread) mail.Thread {
	cloned := thread
	cloned.LabelIDs = slices.Clone(thread.LabelIDs)
	cloned.Messages = slices.Clone(thread.Messages)
	for i := range cloned.Messages {
		cloned.Messages[i].LabelIDs = slices.Clone(thread.Messages[i].LabelIDs)
		cloned.Messages[i].Attachments = slices.Clone(thread.Messages[i].Attachments)
		for j := range cloned.Messages[i].Attachments {
			cloned.Messages[i].Attachments[j].Data = slices.Clone(thread.Messages[i].Attachments[j].Data)
		}
	}
	return cloned
}
