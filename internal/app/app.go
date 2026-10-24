package app

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"mica/internal/gmail"
	"mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const threadPageSize = 25

type App struct {
	service                  mail.Service
	store                    mailStore
	outbox                   outboxStore
	snooze                   snoozeStore
	snoozeRuntime            snoozeRuntime
	outboxRecords            map[string]mail.OutboxMessage
	outboxBusy               string
	outboxDeleteID           string
	outboxDeleteOpen         bool
	outboxReviewed           map[string]bool
	outboxReviewOpen         bool
	outboxReviewID           string
	cacheState               cacheState
	accountClients           map[string]*accountClient
	accountGenerationCounter uint64
	accountGenerations       map[string]uint64
	accountClientLoads       map[string]uint64
	accountClientPending     map[string][]func(error)
	unified                  *unifiedState
	listVersions             listCacheVersions
	threadVersions           threadCacheVersions
	accountCount             int
	storeAccount             string
	account                  mail.Account
	accountPhoto             *ui.Bitmap
	labels                   []mail.Label
	threads                  []mail.ThreadSummary
	compositeThreads         bool
	thread                   mail.Thread
	threadCache              map[string]mail.Thread
	profileReauthAttempted   bool

	selectedLabel        string
	selectedThread       string
	selectedRow          int
	threadSelection      ui.Selection[string]
	startupSelectionDone bool
	sidebarWidth         float32
	listWidth            float32
	query                string
	unreadOnly           bool

	loading          bool
	loadingMore      bool
	nextPageToken    string
	threadLoading    bool
	mutationQueue    []mutationJob
	mutationSequence uint64
	mutationRunning  bool
	mutationRefresh  bool
	mutationLabels   bool
	err              string
	threadErr        string
	notice           string
	noticeID         uint64
	noticeToastID    string
	expiredNoticeID  string

	list                 ui.ListState
	search               ui.Handle
	searchFocused        bool
	shortcutMenu         func(bool, string, string)
	shortcutMenuDisabled bool
	shortcutMenuTrash    string
	shortcutMenuSpam     string
	emailResources       *emailResourceStore

	remoteImagesAutomatic  bool
	remoteImagePermissions map[string]bool

	emailPages           map[string]*emailPage
	emailHeights         map[string]float32
	attachmentDownloads  map[string]attachmentDownload
	openPath             func(string) error
	openDialog           func(mygo.OpenDialogOptions) ([]string, error)
	saveDialog           func(mygo.SaveDialogOptions) (string, error)
	newEmailView         func() (*mygo.WebView, error)
	documents            map[string]*emailDocument
	expandedMessages     map[string]bool
	compose              *composer
	draftRestoreAccount  string
	labelDialog          *labelDialogState
	settings             *settingsState
	accountTransition    *accountTransition
	accountService       func(context.Context, string) (mail.Service, error)
	saveActiveAccount    func(string) error
	deleteActiveAccount  func() error
	removeCredentials    func(context.Context, string) error
	removeLegacy         func(context.Context, string) error
	verifyLegacy         func(context.Context, string) (bool, error)
	deleteCheckpoint     func(string) error
	mutationWaiters      []func()
	appearance           string
	helpOpen             bool
	chord                string
	chordAt              time.Time
	focusedMessage       int
	messageScroll        int
	readerScroll         ui.ScrollState
	undo                 *undoAction
	request              int
	threadCall           int
	listFreshRequest     int
	threadFreshCall      int
	accountVerified      bool
	labelsSynced         bool
	serviceVersion       int
	restoreAttempted     bool
	authCancel           context.CancelFunc
	authID               uint64
	update               func(func())
	schedule             func(func())
	after                func(time.Duration, func())
	setBadge             func(int)
	notify               func(string, string, string)
	notificationsStarted bool
	notification         *notificationWorker
	notificationDone     <-chan struct{}
	unreadSeen           map[string]time.Time
	pendingThread        notificationTarget
	loadCheckpoint       func(string) (uint64, bool, error)
	saveCheckpoint       func(string, uint64) error

	startupPending        bool
	draftRestorePending   bool
	pendingMailto         string
	registerURLScheme     func(string) error
	isURLSchemeRegistered func(string) bool
	defaultMailApp        bool
	loadActiveAccount     func() (string, error)
	loginGmail            gmailLoginFunc
	unifiedAccounts       []mail.Account
	unifiedExpanded       bool
	accountInboxPending   string
}

func New(service mail.Service) *App {
	a := &App{
		service:              service,
		emailResources:       newEmailResourceStore(service),
		accountClients:       make(map[string]*accountClient),
		accountGenerations:   make(map[string]uint64),
		accountClientLoads:   make(map[string]uint64),
		accountClientPending: make(map[string][]func(error)),
		openPath:             mygo.Shell.OpenPath,
		openDialog:           mygo.Dialog.Open,
		saveDialog:           mygo.Dialog.Save,
		account:              mail.Account{Name: "Mica", Demo: true},
		threadCache:          make(map[string]mail.Thread),
		selectedLabel:        mail.Inbox,
		unifiedExpanded:      true,
		selectedRow:          -1,
		sidebarWidth:         204,
		listWidth:            342,
		appearance:           "system",
		focusedMessage:       -1,
		messageScroll:        -1,
		schedule:             func(fn func()) { go fn() },
		after:                func(d time.Duration, fn func()) { time.AfterFunc(d, fn) },
		loadCheckpoint:       loadHistoryCheckpoint,
		saveCheckpoint:       saveHistoryCheckpoint,
	}
	a.resetMessageViews()
	a.registerURLScheme = mygo.App.RegisterURLScheme
	a.isURLSchemeRegistered = mygo.App.IsURLSchemeRegistered
	a.loadActiveAccount = gmail.NewActiveAccountStore().Load
	a.loginGmail = loginGmailService
	a.list.Selected = &a.selectedRow
	a.list.Selection = &a.threadSelection
	return a
}

func (a *App) SetUpdate(update func(func())) {
	a.update = update
}

func (a *App) Start() {
	a.resetSnoozeRuntime()
	a.startupPending = true
	a.refresh()
	service, version := a.service, a.serviceVersion
	store, email := a.store, a.storeAccount
	restore, restoreOK := a.prepareSnoozeRestore(email)
	a.schedule(func() {
		if restoreOK {
			a.runSnoozeRestore(restore)
		}
		if store != nil && email != "" {
			cachedAccount, accountFound, accountCacheErr := store.Account(context.Background(), email)
			cachedLabels, labelsFound, labelsCacheErr := store.Labels(context.Background(), email)
			cachedAccounts, accountsErr := store.Accounts(context.Background())
			logCacheError(accountCacheErr)
			logCacheError(labelsCacheErr)
			logCacheError(accountsErr)
			a.publish(func() {
				if version != a.serviceVersion || email != a.storeAccount {
					return
				}
				if accountsErr == nil {
					a.accountCount = len(cachedAccounts)
					a.unifiedAccounts = cachedAccounts
				}
				if accountCacheErr == nil && accountFound && normalizedEmail(cachedAccount.Email) == normalizedEmail(email) && !a.accountVerified {
					a.account = cachedAccount
					a.configureMessageCache()
					a.loadAccountPhoto(cachedAccount)
					a.restoreDraftSnapshot()
				}
				if labelsCacheErr == nil && labelsFound && !a.labelsSynced {
					a.setLabels(cachedLabels)
				}
			})
		}
		account, accountErr := service.Account(context.Background())
		labels, labelsErr := service.Labels(context.Background())
		a.publish(func() {
			if version != a.serviceVersion {
				return
			}
			if accountErr == nil {
				a.accountVerified = true
				a.account = account
				a.persistAccount(account)
				a.restoreDraftSnapshot()
				if a.compose != nil && a.compose.recoveryAccount == account.Email {
					a.pumpDraftSave(a.compose)
				}
				if a.labelsSynced {
					a.persistLabels(a.labels)
				}
				if a.listFreshRequest == a.request {
					a.persistList(a.currentQuery(), a.threads)
				}
				a.loadAccountPhoto(account)
				if account.ProfileUnauthorized && !a.profileReauthAttempted {
					a.profileReauthAttempted = true
					a.reauthorizeGmail()
				}
				a.startNotifications()
			}
			if labelsErr == nil {
				a.labelsSynced = true
				a.setLabels(labels)
				if a.accountVerified {
					a.persistLabels(labels)
				}
			}
			if accountErr != nil {
				a.notice = accountErr.Error()
			} else if labelsErr != nil {
				a.notice = labelsErr.Error()
			}
			a.startupPending = false
			a.openPendingMailto()
		})
	})
	a.restoreGmail()
	a.openPendingThread()
	a.restoreDraftSnapshot()
	a.openPendingMailto()
}

func (a *App) loadAccountPhoto(account mail.Account) {
	a.accountPhoto = nil
	if account.PhotoURL == "" {
		return
	}
	load, version := newMessageImages(a.service), a.serviceVersion
	a.schedule(func() {
		source, err := load(account.PhotoURL)
		a.publish(func() {
			if version != a.serviceVersion || err != nil {
				return
			}
			if bitmap, ok := source.(*ui.Bitmap); ok {
				a.accountPhoto = bitmap
			}
		})
	})
}

func (a *App) publish(fn func()) {
	if a.update != nil {
		a.update(fn)
		return
	}
	fn()
}

func (a *App) refresh() {
	if a.mutationsPending() {
		a.mutationRefresh = true
		return
	}
	a.threadCache = make(map[string]mail.Thread)
	if a.selectedThread != "" {
		id := a.selectedThread
		a.selectedThread = ""
		a.selectThread(id)
	}
	a.refreshList()
}

func (a *App) currentQuery() mail.Query {
	if a.selectedLabel == mail.Unified {
		if search := strings.TrimSpace(a.query); search != "" {
			return mail.Query{Search: search, Limit: threadPageSize}
		}
		return mail.Query{LabelID: mail.Inbox, Limit: threadPageSize}
	}
	labelID, search := mailboxQuery(a.selectedLabel)
	if userSearch := strings.TrimSpace(a.query); userSearch != "" {
		if a.selectedLabel == mail.Draft {
			search = userSearch
		} else {
			labelID, search = "", userSearch
		}
	}
	return mail.Query{LabelID: labelID, Search: search, Limit: threadPageSize}
}

func listThreadPage(ctx context.Context, service mail.Service, query mail.Query) (mail.ThreadPage, error) {
	if query.LabelID == mail.Draft {
		manager, ok := service.(mail.DraftManager)
		if !ok {
			return mail.ThreadPage{}, errors.New("mail: drafts are unavailable")
		}
		page, err := manager.Drafts(ctx, query)
		return draftThreadPage(page), err
	}
	if pager, ok := service.(mail.ThreadPager); ok {
		return pager.ListThreadPage(ctx, query)
	}
	threads, err := service.ListThreads(ctx, query)
	if err != nil {
		return mail.ThreadPage{}, err
	}
	return mail.ThreadPage{Threads: threads}, nil
}

func (a *App) refreshList() {
	if a.selectedLabel == mail.Outbox {
		a.refreshOutbox()
		return
	}
	if a.selectedLabel == mail.Unified {
		a.refreshUnified()
		return
	}
	a.request++
	request := a.request
	a.nextPageToken = ""
	a.loadingMore = false
	a.loading = true
	a.err = ""
	service, version := a.service, a.serviceVersion
	store, email := a.store, a.storeAccount
	if email == "" {
		email = normalizedEmail(a.account.Email)
	}
	query := a.currentQuery()
	a.schedule(func() {
		if store != nil && email != "" {
			cached, found, cacheErr := store.ThreadList(context.Background(), email, query)
			logCacheError(cacheErr)
			a.publish(func() {
				if version != a.serviceVersion || request != a.request || email != a.storeAccount || a.listFreshRequest >= request {
					return
				}
				if cacheErr == nil && found {
					a.applyThreadList(cached, false)
				}
			})
		}
		page, err := listThreadPage(context.Background(), service, query)
		var cached []mail.ThreadSummary
		var cacheErr error
		if err != nil {
			if searchStore, ok := store.(offlineSearchStore); ok && email != "" {
				cached, cacheErr = searchStore.SearchThreads(context.Background(), email, query)
			}
		}
		if a.selectedLabel == mail.Snoozed {
			var records []mail.SnoozeRecord
			if cache := a.snoozeCache(email); cache != nil {
				if err := cache(func(store snoozeStore) error {
					var err error
					records, err = store.SnoozeRecords(context.Background(), email)
					return err
				}); err != nil {
					logCacheError(err)
				}
			}
			if err == nil {
				page.Threads = mergeSnoozedSummaries(page.Threads, records)
			} else {
				cached = mergeSnoozedSummaries(cached, records)
				if len(cached) > 0 {
					cacheErr = nil
				}
			}
		}
		a.publish(func() {
			if version != a.serviceVersion || request != a.request {
				return
			}
			a.loading = false
			if err != nil {
				if cacheErr == nil && len(cached) > 0 {
					a.applyThreadList(cached, true)
					a.startupSelectionDone = true
					a.err = locales.OfflineResults()
				} else {
					a.err = err.Error()
				}
				return
			}
			a.listFreshRequest = request
			a.nextPageToken = page.NextPageToken
			a.applyThreadList(page.Threads, true)
			a.startupSelectionDone = true
			if a.accountVerified {
				a.persistList(query, page.Threads)
			}
		})
	})
}

func (a *App) loadMoreThreads() {
	if a.selectedLabel == mail.Unified {
		a.loadMoreUnified()
		return
	}
	if a.nextPageToken == "" || a.loading || a.loadingMore || a.err != "" {
		return
	}
	a.loadingMore = true
	service, version := a.service, a.serviceVersion
	request := a.request
	query := a.currentQuery()
	query.PageToken = a.nextPageToken
	a.schedule(func() {
		page, err := listThreadPage(context.Background(), service, query)
		a.publish(func() {
			if version != a.serviceVersion || request != a.request || query.PageToken != a.nextPageToken {
				return
			}
			a.loadingMore = false
			if err != nil {
				a.err = err.Error()
				return
			}
			a.nextPageToken = page.NextPageToken
			a.applyHistoryChanges(page.Threads, nil)
		})
	})
}

func (a *App) visibleThreads() []mail.ThreadSummary {
	if !a.unreadOnly {
		return a.threads
	}
	visible := make([]mail.ThreadSummary, 0, len(a.threads))
	for _, thread := range a.threads {
		if thread.Unread {
			visible = append(visible, thread)
		}
	}
	return visible
}

func (a *App) syncThreadSelection(clearMissing bool) {
	threads := a.visibleThreads()
	visible := make(map[string]bool, len(threads))
	for _, thread := range threads {
		visible[a.summaryKey(thread)] = true
	}
	if !a.compositeThreads {
		for id := range a.threadSelection.All() {
			if !visible[id] {
				a.threadSelection.Remove(id)
			}
		}
	}
	if a.selectedThread == "" {
		if a.selectedRow >= len(threads) {
			a.selectedRow = -1
		}
		return
	}
	for i := range threads {
		if a.summaryKey(threads[i]) == a.selectedThread {
			a.selectedRow = i
			return
		}
	}
	a.selectedRow = -1
	if !clearMissing {
		return
	}
	for i := range a.threads {
		if a.summaryKey(a.threads[i]) == a.selectedThread {
			return
		}
	}
	a.clearThread()
}

func (a *App) applyThreadList(threads []mail.ThreadSummary, clearMissing bool) {
	if !a.compositeThreads {
		for i := range threads {
			if threads[i].AccountEmail == "" {
				threads[i].AccountEmail = a.storeAccount
			}
		}
	}
	a.threads = threads
	a.syncThreadSelection(clearMissing)
	if !a.startupSelectionDone && a.selectedThread == "" {
		if visible := a.visibleThreads(); len(visible) > 0 {
			a.selectThread(a.summaryKey(visible[0]))
		}
	}
}

func (a *App) historyThreadKey(id string) string {
	if a.compositeThreads {
		return threadRefForAccount(a.storeAccount, id).key()
	}
	return id
}

func (a *App) applyHistoryChanges(changed []mail.ThreadSummary, deleted []string) {
	if a.compositeThreads {
		a.refreshUnified()
		return
	}
	changes := make(map[string]mail.ThreadSummary, len(changed))

	for _, summary := range changed {
		changes[a.summaryKey(summary)] = summary
	}
	removed := make(map[string]bool, len(deleted))
	for _, id := range deleted {
		removed[a.historyThreadKey(id)] = true
	}
	query := a.currentQuery()
	updated := make([]mail.ThreadSummary, 0, len(a.threads)+len(changes))
	for _, existing := range a.threads {
		key := a.summaryKey(existing)
		if removed[key] {
			continue
		}
		if replacement, ok := changes[key]; ok {
			existing = replacement
			delete(changes, key)
		}
		if historyListContains(query, existing) {
			updated = append(updated, existing)
		}
	}
	for key, summary := range changes {
		if !removed[key] && historyListContains(query, summary) {
			updated = append(updated, summary)
		}
	}
	sort.SliceStable(updated, func(i, j int) bool {
		if a.compositeThreads && updated[i].Updated.Equal(updated[j].Updated) {
			return a.summaryKey(updated[i]) < a.summaryKey(updated[j])
		}
		return updated[i].Updated.After(updated[j].Updated)
	})
	a.applyThreadList(updated, true)
	if a.accountVerified || a.compositeThreads {
		a.persistList(query, updated)
	}
}

func historyListContains(query mail.Query, summary mail.ThreadSummary) bool {
	if query.LabelID == "" || query.LabelID == mail.All {
		return true
	}
	return containsLabel(summary.LabelIDs, query.LabelID)
}

func (a *App) selectLabel() {
	if a.accountInboxPending != "" {
		a.selectedLabel = a.accountInboxPending
		return
	}
	activate := ""
	if email := accountInboxLabel(a.selectedLabel); email != "" {
		if !a.accountInboxAvailable(email) {
			a.selectedLabel = mail.Inbox
		} else if email != normalizedEmail(a.storeAccount) {
			activate = email
			a.accountInboxPending = a.selectedLabel
		}
	}
	viewChanged := a.compositeThreads != (a.selectedLabel == mail.Unified) || activate != "" || a.selectedLabel == mail.Outbox || a.outboxRecords != nil
	if viewChanged {
		a.outboxRecords = nil
		a.request++
		a.threads = nil
		a.threadCache = nil
		a.nextPageToken = ""
		a.loadingMore = false
		a.loading = true
		a.err = ""
		a.undo = nil
		if a.noticeToastID != "" {
			a.expiredNoticeID = a.noticeToastID
			a.noticeToastID = ""
		}
	}
	if a.compositeThreads {
		a.compositeThreads = false
		a.unified = nil
	}
	a.query = ""
	a.clearThread()
	if activate != "" {
		a.switchAccount(activate)
		if a.accountTransition == nil && a.authCancel == nil && a.accountInboxPending != "" {
			a.resetFailedAccountInbox()
		}
		return
	}
	a.refresh()
}

func (a *App) resetFailedAccountInbox() {
	a.accountInboxPending = ""
	if email := accountInboxLabel(a.selectedLabel); email != "" && email != normalizedEmail(a.storeAccount) {
		a.selectedLabel = mail.Inbox
		a.selectLabel()
	}
}

func (a *App) cancelAccountInboxRequest() {
	a.cancelPendingAuthentication()
	a.resetFailedAccountInbox()
}

func (a *App) clearThread() {
	a.startupSelectionDone = true
	a.closeEmailViews()
	a.threadCall++
	a.selectedRow = -1
	a.selectedThread = ""
	a.threadSelection.Clear()
	a.thread = mail.Thread{}
	a.threadErr = ""
	a.threadLoading = false
}

func (a *App) selectThread(id string) {
	if id == "" {
		return
	}
	a.threadSelection.Clear()
	a.threadSelection.Add(id)
	a.loadThread(id)
}

func (a *App) openThreadFromList(id string) {
	a.loadThread(id)
}

func (a *App) loadThread(id string) {
	if id == "" || a.accountTransition != nil {
		return
	}
	if id == a.selectedThread {
		a.syncThreadSelection(false)
		return
	}
	a.startupSelectionDone = true
	a.threadCall++
	call := a.threadCall
	a.selectedThread = id
	a.syncThreadSelection(false)
	a.threadErr = ""
	if a.selectedLabel == mail.Outbox {
		record, found := a.outboxRecords[outboxRecordKey(id)]
		if !found {
			a.threadLoading = false
			a.threadErr = errOutboxUnavailable.Error()
			return
		}
		a.showThread(id, outboxThread(record))
		return
	}
	if a.selectedLabel == mail.Draft {
		a.selectDraft(id, call)
		return
	}
	if thread, ok := a.threadCache[id]; ok {
		a.showThread(id, thread)
		return
	}
	ref := parseThreadRef(id)
	service, generation := a.ownerService(ref.AccountEmail)
	if service == nil && ref.AccountEmail != "" {
		a.threadLoading = true
		a.ensureAccountClient(ref.AccountEmail, func(err error) {
			if err != nil {
				if call == a.threadCall {
					a.threadLoading = false
					a.threadErr = err.Error()
				}
				return
			}
			if call != a.threadCall {
				return
			}
			a.selectedThread = ""
			a.loadThread(id)
		})
		return
	}
	if service == nil {
		a.threadLoading = false
		a.threadErr = "mail: account is unavailable"
		return
	}
	owner := ref.AccountEmail
	if owner == "" {
		owner = a.storeAccount
	}
	a.thread = mail.Thread{}
	a.threadLoading = true
	store, email := a.store, ref.AccountEmail
	if email == "" {
		email = a.storeAccount
	}
	a.schedule(func() {
		if store != nil && email != "" {
			cached, found, cacheErr := store.Thread(context.Background(), email, ref.ID)
			logCacheError(cacheErr)
			cached = withThreadOwner(cached, owner)
			a.publish(func() {
				if generation != a.generationForThreadKey(id) || call != a.threadCall || a.threadFreshCall >= call || ref.AccountEmail == "" && email != a.storeAccount {
					return
				}
				if cacheErr == nil && found {
					a.showThread(id, cached)
				}
			})
		}
		thread, err := service.Thread(context.Background(), ref.ID)
		thread = withThreadOwner(thread, owner)
		a.publish(func() {
			if generation != a.generationForThreadKey(id) || call != a.threadCall {
				return
			}
			a.threadLoading = false
			if err != nil {
				if a.thread.ID != ref.ID {
					a.threadErr = err.Error()
				}
				return
			}
			if a.threadCache == nil {
				a.threadCache = make(map[string]mail.Thread)
			}
			a.threadFreshCall = call
			a.threadCache[id] = thread
			if a.ownerVerified(ref.AccountEmail) {
				a.persistThread(thread)
			}
			a.showThread(id, thread)
		})
	})
}

func (a *App) showThread(id string, thread mail.Thread) {
	sameThread := a.threadKey(thread) == id
	if sameThread {
		a.retainEmailViews(thread)
	} else {
		a.closeEmailViews()
	}
	a.thread = thread
	a.threadLoading = false
	if !sameThread {
		a.focusedMessage = len(thread.Messages) - 1
		a.messageScroll = -1
		a.readerScroll.Y = 0
		a.readerScroll.MaxY = 0
	}
	if a.selectedLabel != mail.Draft {
		for i := range a.threads {
			if a.summaryKey(a.threads[i]) == id && a.threads[i].Unread {
				a.threads[i].Unread = false
				a.markRead(id)
				break
			}
		}
	}
	a.syncThreadSelection(false)
}

func (a *App) selectedSummary() *mail.ThreadSummary {
	for i := range a.threads {
		if a.summaryKey(a.threads[i]) == a.selectedThread {
			return &a.threads[i]
		}
	}
	return nil
}

func (a *App) markRead(id string) {
	if a.accountTransition != nil || !a.canMutateThread(id) {
		return
	}
	a.updateThreadLabels(id, nil, []string{mail.Unread})
	a.enqueueMutation(mutationJob{
		service:    a.serviceForThreadKey(id),
		id:         id,
		remove:     []string{mail.Unread},
		readStatus: true,
		readQuery:  a.currentQuery(),
		failure:    locales.ReadStatusFailed,
	})
}

func (a *App) reloadLabels() {
	service, version := a.service, a.serviceVersion
	a.schedule(func() {
		labels, err := service.Labels(context.Background())
		a.publish(func() {
			if version == a.serviceVersion && err == nil {
				a.labelsSynced = true
				a.setLabels(labels)
				if a.accountVerified {
					a.persistLabels(labels)
				}
			}
		})
	})
}

func (a *App) toggleStar() {
	summaries := a.actionSummaries()
	if len(summaries) == 0 {
		return
	}
	starred := !summaries[0].Starred
	targets := make([]mail.ThreadSummary, 0, len(summaries))
	for _, summary := range summaries {
		if summary.Starred != starred {
			targets = append(targets, summary)
		}
	}
	if len(targets) == 0 {
		return
	}
	add, remove := []string{mail.Starred}, []string(nil)
	if !starred {
		add, remove = remove, add
	}
	if len(targets) == 1 {
		summary := targets[0]
		a.recordUndo(a.summaryKey(summary), remove, add)
		a.mutate(a.summaryKey(summary), add, remove, false, func() string {
			if starred {
				return locales.StarAdded()
			}
			return locales.StarRemoved()
		})
		return
	}
	message := locales.ConversationsUnstarred
	if starred {
		message = locales.ConversationsStarred
	}
	a.mutateThreads(targets, batchMutation{
		add: add, remove: remove, inverseAdd: remove, inverseRemove: add,
		message: message, failure: locales.ActionFailed,
	})
}

func (a *App) toggleUnread() {
	summaries := a.actionSummaries()
	if len(summaries) == 0 {
		return
	}
	a.setUnread(!summaries[0].Unread)
}

func (a *App) actionSummaries() []mail.ThreadSummary {
	if a.accountTransition != nil || a.selectedLabel == mail.Outbox {
		return nil
	}
	threads := a.visibleThreads()
	summaries := make([]mail.ThreadSummary, 0, a.threadSelection.Len())
	for _, thread := range threads {
		if a.threadSelection.Has(a.summaryKey(thread)) {
			summaries = append(summaries, thread)
		}
	}
	if len(summaries) > 0 {
		return summaries
	}
	if summary := a.selectedSummary(); summary != nil {
		return []mail.ThreadSummary{*summary}
	}
	return nil
}

func (a *App) inboxView() bool {
	return a.selectedLabel == mail.Inbox || a.selectedLabel == mail.Unified || accountInboxLabel(a.selectedLabel) != ""
}

func (a *App) archive() {
	summaries := a.actionSummaries()
	if len(summaries) == 0 {
		return
	}
	if len(summaries) == 1 {
		summary := summaries[0]
		a.recordUndo(a.summaryKey(summary), []string{mail.Inbox}, nil)
		a.mutate(a.summaryKey(summary), nil, []string{mail.Inbox}, a.inboxView(), locales.Archived)
		return
	}
	a.archiveThreads(summaries)
}

type batchRemoval int

const (
	batchRemoveNever batchRemoval = iota
	batchRemoveInbox
	batchRemoveAll
	batchRemoveHidden
)

type batchMutation struct {
	kind          threadMutation
	add           []string
	remove        []string
	inverseKind   threadMutation
	inverseAdd    []string
	inverseRemove []string
	removal       batchRemoval
	message       func(int) string
	failure       func(string) string
}

func (a *App) mutateThreads(summaries []mail.ThreadSummary, mutation batchMutation) {
	if len(summaries) == 0 {
		return
	}
	for _, summary := range summaries {
		if !a.canMutateThread(a.summaryKey(summary)) {
			return
		}
	}
	action := &undoAction{items: make([]undoItem, 0, len(summaries))}
	a.undo = action
	ids := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		action.items = append(action.items, undoItem{
			id: a.summaryKey(summary), kind: mutation.inverseKind, add: mutation.inverseAdd, remove: mutation.inverseRemove,
		})
		ids = append(ids, a.summaryKey(summary))
	}
	for _, id := range ids {
		a.updateThreadLabels(id, mutation.add, mutation.remove)
		a.captureUndoSummary(id)
	}
	var removeIDs []string
	switch mutation.removal {
	case batchRemoveInbox:
		if a.inboxView() {
			removeIDs = ids
		}
	case batchRemoveAll:
		removeIDs = ids
	case batchRemoveHidden:
		query := a.currentQuery()
		if query.Search != "" {
			removeIDs = ids
			break
		}
		for _, summary := range summaries {
			summary.LabelIDs = modifyLabelIDs(summary.LabelIDs, mutation.add, mutation.remove)
			if !historyListContains(query, summary) {
				removeIDs = append(removeIDs, a.summaryKey(summary))
			}
		}
	}
	if len(removeIDs) > 0 {
		a.removeThreads(removeIDs)
	}
	a.notice = mutation.message(len(ids))
	failure := mutation.failure
	if failure == nil {
		failure = locales.ActionFailed
	}
	for _, id := range ids {
		a.enqueueMutation(mutationJob{
			service: a.serviceForThreadKey(id),
			id:      id,
			kind:    mutation.kind,
			add:     mutation.add,
			remove:  mutation.remove,
			undo:    action,
			failure: failure,
		})
	}
}

func (a *App) archiveThreads(summaries []mail.ThreadSummary) {
	a.mutateThreads(summaries, batchMutation{
		remove: []string{mail.Inbox}, inverseAdd: []string{mail.Inbox}, removal: batchRemoveInbox,
		message: locales.ConversationsArchived, failure: locales.ActionFailed,
	})
}

func (a *App) toggleTrash() {
	summaries := a.actionSummaries()
	if len(summaries) == 0 {
		return
	}
	if containsLabel(summaries[0].LabelIDs, mail.Trash) {
		a.untrash()
		return
	}
	a.trash()
}

func (a *App) trash() {
	summaries := a.actionSummaries()
	targets := make([]mail.ThreadSummary, 0, len(summaries))
	for _, summary := range summaries {
		if !containsLabel(summary.LabelIDs, mail.Trash) {
			targets = append(targets, summary)
		}
	}
	if len(targets) == 0 {
		return
	}
	if len(targets) == 1 {
		summary := targets[0]
		id := a.summaryKey(summary)
		if !a.canMutateThread(id) {
			return
		}
		a.recordUndoKind(id, mutationUntrash, nil, []string{mail.Trash})
		a.updateThreadLabels(id, []string{mail.Trash}, []string{mail.Inbox})
		a.captureUndoSummary(id)
		a.removeThread(id)
		a.notice = locales.Trashed()
		a.enqueueMutation(mutationJob{
			service: a.serviceForThreadKey(id),
			id:      id,
			kind:    mutationTrash,
			undo:    a.undo,
			failure: locales.TrashFailed,
		})
		return
	}
	a.mutateThreads(targets, batchMutation{
		kind: mutationTrash, add: []string{mail.Trash}, remove: []string{mail.Inbox},
		inverseKind: mutationUntrash, inverseRemove: []string{mail.Trash}, removal: batchRemoveAll,
		message: locales.ConversationsTrashed, failure: locales.TrashFailed,
	})
}

func (a *App) untrash() {
	summaries := a.actionSummaries()
	targets := make([]mail.ThreadSummary, 0, len(summaries))
	for _, summary := range summaries {
		if containsLabel(summary.LabelIDs, mail.Trash) {
			targets = append(targets, summary)
		}
	}
	if len(targets) == 0 {
		return
	}
	if len(targets) == 1 {
		summary := targets[0]
		id := a.summaryKey(summary)
		if !a.canMutateThread(id) {
			return
		}
		a.recordUndoKind(id, mutationTrash, []string{mail.Trash}, []string{mail.Inbox})
		a.updateThreadLabels(id, nil, []string{mail.Trash})
		a.captureUndoSummary(id)
		a.removeThreadIfHidden(id)
		a.notice = locales.Untrashed()
		a.enqueueMutation(mutationJob{
			service: a.serviceForThreadKey(id),
			id:      id,
			kind:    mutationUntrash,
			undo:    a.undo,
			failure: locales.UntrashFailed,
		})
		return
	}
	a.mutateThreads(targets, batchMutation{
		kind: mutationUntrash, remove: []string{mail.Trash},
		inverseKind: mutationTrash, inverseAdd: []string{mail.Trash}, inverseRemove: []string{mail.Inbox}, removal: batchRemoveHidden,
		message: locales.ConversationsUntrashed, failure: locales.UntrashFailed,
	})
}

func (a *App) mutate(id string, add, remove []string, removeFromList bool, message func() string) {
	if !a.canMutateThread(id) {
		a.undo = nil
		return
	}
	a.updateThreadLabels(id, add, remove)
	a.captureUndoSummary(id)
	if removeFromList {
		a.removeThread(id)
	}
	a.notice = message()
	a.enqueueMutation(mutationJob{
		service: a.serviceForThreadKey(id),
		id:      id,
		add:     add,
		remove:  remove,
		undo:    a.undo,
		failure: locales.ActionFailed,
	})
}

func (a *App) removeThreadIfHidden(id string) {
	query := a.currentQuery()
	if query.Search == "" {
		for _, summary := range a.threads {
			if a.summaryKey(summary) == id && historyListContains(query, summary) {
				return
			}
		}
	}
	a.removeThread(id)
}

func (a *App) removeThreads(ids []string) {
	removed := make(map[string]bool, len(ids))
	for _, id := range ids {
		removed[id] = true
		a.removeUnifiedThread(id)
		delete(a.threadCache, id)
		a.threadSelection.Remove(id)
	}
	readerRemoved := removed[a.selectedThread]
	nextReader := ""
	if readerRemoved {
		threads := a.visibleThreads()
		for i, thread := range threads {
			if a.summaryKey(thread) != a.selectedThread {
				continue
			}
			for j := i + 1; j < len(threads) && nextReader == ""; j++ {
				if !removed[a.summaryKey(threads[j])] {
					nextReader = a.summaryKey(threads[j])
				}
			}
			for j := i - 1; j >= 0 && nextReader == ""; j-- {
				if !removed[a.summaryKey(threads[j])] {
					nextReader = a.summaryKey(threads[j])
				}
			}
			break
		}
	}
	remaining := make([]mail.ThreadSummary, 0, len(a.threads))
	for _, thread := range a.threads {
		if !removed[a.summaryKey(thread)] {
			remaining = append(remaining, thread)
		}
	}
	a.threads = remaining
	if a.accountVerified || a.compositeThreads {
		a.persistList(a.currentQuery(), a.threads)
	}
	if !readerRemoved {
		a.syncThreadSelection(false)
		return
	}
	a.selectedThread = ""
	if nextReader == "" {
		a.clearThread()
		return
	}
	a.selectThread(nextReader)
}

func (a *App) removeThread(id string) {
	delete(a.threadCache, id)
	removed := -1
	for i := range a.threads {
		if a.summaryKey(a.threads[i]) == id {
			removed = i
			a.threads = append(a.threads[:i], a.threads[i+1:]...)
			break
		}
	}
	if removed < 0 {
		return
	}
	a.removeUnifiedThread(id)
	if a.accountVerified || a.compositeThreads {
		a.persistList(a.currentQuery(), a.threads)
	}
	if a.selectedThread == id {
		a.selectedThread = ""
		threads := a.visibleThreads()
		if len(threads) == 0 {
			a.clearThread()
			return
		}
		row := max(a.selectedRow, 0)
		a.selectedRow = min(row, len(threads)-1)
		a.selectThread(a.summaryKey(threads[a.selectedRow]))
		return
	}
	a.syncThreadSelection(false)
}

func (a *App) updateThreadLabels(id string, add, remove []string) {
	found := false
	for i := range a.threads {
		if a.summaryKey(a.threads[i]) == id {
			a.threads[i].LabelIDs = modifyLabelIDs(a.threads[i].LabelIDs, add, remove)
			a.threads[i].Unread = containsLabel(a.threads[i].LabelIDs, mail.Unread)
			a.threads[i].Starred = containsLabel(a.threads[i].LabelIDs, mail.Starred)
			found = true
			break
		}
	}
	if thread, ok := a.threadCache[id]; ok {
		thread.LabelIDs = modifyLabelIDs(thread.LabelIDs, add, remove)
		for i := range thread.Messages {
			thread.Messages[i].LabelIDs = modifyLabelIDs(thread.Messages[i].LabelIDs, add, remove)
		}
		a.threadCache[id] = thread
		found = true
	}
	if a.threadKey(a.thread) == id {
		a.thread.LabelIDs = modifyLabelIDs(a.thread.LabelIDs, add, remove)
		for i := range a.thread.Messages {
			a.thread.Messages[i].LabelIDs = modifyLabelIDs(a.thread.Messages[i].LabelIDs, add, remove)
		}
		found = true
	}
	if !found {
		return
	}
	a.syncUnifiedSummary(id)
	a.syncThreadSelection(false)
	if !a.ownerVerified(parseThreadRef(id).AccountEmail) && !a.compositeThreads {
		return
	}
	a.persistList(a.currentQuery(), a.threads)
	if thread, ok := a.threadCache[id]; ok {
		a.persistThread(thread)
	} else if a.threadKey(a.thread) == id {
		a.persistThread(a.thread)
	}
}

func modifyLabelIDs(labels, add, remove []string) []string {
	updated := make([]string, 0, len(labels)+len(add))
	for _, label := range labels {
		if !containsLabel(remove, label) {
			updated = append(updated, label)
		}
	}
	for _, label := range add {
		if !containsLabel(updated, label) {
			updated = append(updated, label)
		}
	}
	return updated
}

func containsLabel(labels []string, target string) bool {
	for _, label := range labels {
		if label == target {
			return true
		}
	}
	return false
}

func (a *App) labelName(id string) string {
	if accountInboxLabel(id) != "" {
		return locales.Inbox()
	}
	switch id {
	case mail.Unified:
		return locales.UnifiedInbox()
	case mail.Inbox:
		return locales.Inbox()
	case mail.Starred:
		return locales.Starred()
	case mail.Snoozed:
		return locales.Snoozed()
	case mail.Sent:
		return locales.Sent()
	case mail.Draft:
		return locales.Drafts()
	case mail.Outbox:
		return locales.Outbox()
	case mail.Important:
		return locales.Important()
	case mail.All:
		return locales.AllMail()
	case mail.Spam:
		return locales.Spam()
	case mail.Trash:
		return locales.Trash()
	case mail.CategorySocial:
		return locales.CategorySocial()
	case mail.CategoryPromotions:
		return locales.CategoryPromotions()
	case mail.CategoryUpdates:
		return locales.CategoryUpdates()
	case mail.CategoryForums:
		return locales.CategoryForums()
	case mail.CategoryPurchases:
		return locales.CategoryPurchases()
	case mail.CategoryReservations:
		return locales.CategoryReservations()
	}
	for _, label := range a.labels {
		if label.ID == id {
			return label.Name
		}
	}
	return id
}

func (a *App) errorText() string {
	if a.err != "" {
		return a.err
	}
	if a.loading || a.loadingMore {
		return locales.LoadingMail()
	}
	return locales.ConversationCount(len(a.visibleThreads()))
}
