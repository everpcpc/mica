package app

import (
	"context"
	"log"
	"strings"
	"sync"

	mailmodel "mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo/ui/richtext"
)

type draftStore interface {
	DraftSnapshot(context.Context, string) (mailmodel.DraftSnapshot, bool, error)
	SaveDraftSnapshot(context.Context, string, mailmodel.DraftSnapshot) error
	DeleteDraftSnapshot(context.Context, string) error
}

type draftSnapshotWriteKey struct {
	app   *App
	email string
}

var draftSnapshotWrites = struct {
	sync.Mutex
	latest  map[draftSnapshotWriteKey]uint64
	pending map[*App]int
}{
	latest:  make(map[draftSnapshotWriteKey]uint64),
	pending: make(map[*App]int),
}

func trackDraftSnapshotWrite(a *App, email string, write uint64) {
	draftSnapshotWrites.Lock()
	defer draftSnapshotWrites.Unlock()
	draftSnapshotWrites.latest[draftSnapshotWriteKey{app: a, email: email}] = write
	draftSnapshotWrites.pending[a]++
}

func draftSnapshotWriteCurrent(a *App, email string, write uint64) bool {
	draftSnapshotWrites.Lock()
	defer draftSnapshotWrites.Unlock()
	return draftSnapshotWrites.latest[draftSnapshotWriteKey{app: a, email: email}] == write
}

func untrackDraftSnapshotWrite(a *App) {
	draftSnapshotWrites.Lock()
	defer draftSnapshotWrites.Unlock()
	draftSnapshotWrites.pending[a]--
	if draftSnapshotWrites.pending[a] > 0 {
		return
	}
	delete(draftSnapshotWrites.pending, a)
	for key := range draftSnapshotWrites.latest {
		if key.app == a {
			delete(draftSnapshotWrites.latest, key)
		}
	}
}

func (a *App) restoreDraftSnapshot() {
	store, ok := a.store.(draftStore)
	email := a.storeAccount
	if a.accountTransition != nil || !ok || email == "" || a.draftRestoreAccount == email || !strings.EqualFold(a.account.Email, email) {
		return
	}
	a.draftRestoreAccount = email
	version := a.serviceVersion
	a.schedule(func() {
		snapshot, found, err := store.DraftSnapshot(context.Background(), email)
		logCacheError(err)
		a.publish(func() {
			if a.accountTransition != nil || version != a.serviceVersion || !strings.EqualFold(a.account.Email, email) || email != a.storeAccount || a.compose != nil || err != nil || !found {
				return
			}
			a.openRecoveredDraft(snapshot, email)
		})
	})
}

func (a *App) draftSnapshotAccount(m *composer) (string, bool) {
	email := a.composerOwner(m)
	if email == "" {
		return "", false
	}
	if m != nil && m.recoveryAccount != "" && !strings.EqualFold(m.recoveryAccount, email) {
		return "", false
	}
	return email, true
}

func (a *App) persistDraftSnapshot(m *composer) {
	email, ok := a.draftSnapshotAccount(m)
	if !ok {
		return
	}
	snapshot := mailmodel.DraftSnapshot{DraftID: m.draftID, Compose: composeSnapshotContent(m)}
	for i, item := range m.attachments {
		if item.loading || item.err != "" {
			snapshot.AttachmentErrors = append(snapshot.AttachmentErrors, i)
		}
	}
	a.writeDraftSnapshot(email, &snapshot)
}

func (a *App) clearDraftSnapshot(m *composer) {
	email, ok := a.draftSnapshotAccount(m)
	if !ok {
		return
	}
	a.writeDraftSnapshot(email, nil)
}

func (a *App) writeDraftSnapshot(email string, snapshot *mailmodel.DraftSnapshot) {
	store, ok := a.store.(draftStore)
	email = normalizedEmail(email)
	if !ok || email == "" || a.accountTransition != nil {
		return
	}
	version := a.serviceVersion
	_, generation := a.ownerService(email)
	ownerClient := a.accountClients[email]
	a.cacheState.mu.Lock()
	a.cacheState.draftWrite++
	write := a.cacheState.draftWrite
	trackDraftSnapshotWrite(a, email, write)
	a.cacheState.mu.Unlock()
	a.cacheState.writes.Add(1)
	a.schedule(func() {
		defer a.cacheState.writes.Done()
		defer untrackDraftSnapshotWrite(a)
		a.cacheState.mu.Lock()
		defer a.cacheState.mu.Unlock()
		if version != a.cacheState.version || !draftSnapshotWriteCurrent(a, email, write) || ownerClient != nil && ownerClient.generation.Load() != generation {
			return
		}
		var err error
		if snapshot == nil {
			err = store.DeleteDraftSnapshot(context.Background(), email)
		} else {
			err = store.SaveDraftSnapshot(context.Background(), email, *snapshot)
		}
		if err != nil {
			log.Printf("mica: write draft snapshot: %v", err)
		}
	})
}

func (a *App) openDraftMessage(draftID string, message mailmodel.Message) {
	if a.compose != nil || a.accountTransition != nil {
		return
	}
	ref := parseThreadRef(draftID)
	owner := normalizedEmail(ref.AccountEmail)
	if owner == "" {
		owner = a.storedMessageOwner(message)
	}
	if owner == "" {
		owner = a.activeComposeOwner()
	}
	message.AccountEmail = owner
	compose := mailmodel.Compose{
		To: message.To, Cc: message.Cc, Bcc: message.Bcc, Subject: message.Subject, Text: message.Text, HTML: message.HTML,
		ThreadID: message.ThreadID, InReplyTo: message.InReplyTo, References: message.References, ThreadingResolved: message.ThreadID != "",
	}
	m := newDraftComposer(compose, owner)
	m.draftID = ref.ID
	a.compose = m
	a.addMessageAttachments(message)
}

func (a *App) openRecoveredDraft(snapshot mailmodel.DraftSnapshot, email string) {
	if a.compose != nil || a.accountTransition != nil {
		return
	}
	email = normalizedEmail(email)
	m := newDraftComposer(snapshot.Compose, email)
	m.draftID = snapshot.DraftID
	m.recoveryAccount = email
	m.revision = 1
	m.savedRevision = 0
	m.saveState = draftSaveScheduled
	for _, index := range snapshot.AttachmentErrors {
		if index >= 0 && index < len(m.attachments) {
			m.attachments[index].attachment.Data = nil
			m.attachments[index].err = locales.AttachmentRestoreFailed()
			m.saveState = draftSaveFailed
		}
	}
	a.compose = m
	a.scheduleDraftSave(m)
}

func newDraftComposer(compose mailmodel.Compose, owner string) *composer {
	compose = mailmodel.CloneCompose(compose)
	doc := richtext.NewDocument(richtext.NewText(compose.Text))
	if compose.HTML != "" {
		doc = richtext.NewDocument(richtext.ParseHTML(compose.HTML))
	}
	m := &composer{
		open: true, accountEmail: normalizedEmail(owner), generation: 1, saveState: draftSaveSaved,
		to: compose.To, cc: compose.Cc, bcc: compose.Bcc, subject: compose.Subject, doc: doc,
		showCc: compose.Cc != "", showBcc: compose.Bcc != "",
		threadID: compose.ThreadID, inReplyTo: compose.InReplyTo, references: compose.References,
	}
	for _, attachment := range compose.Attachments {
		m.nextAttachmentID++
		m.attachments = append(m.attachments, composerAttachment{id: m.nextAttachmentID, attachment: attachment})
	}
	return m
}

func (a *App) selectDraft(id string, call int) {
	ref := parseThreadRef(id)
	owner := normalizedEmail(ref.AccountEmail)
	if owner == "" {
		owner = normalizedEmail(a.storeAccount)
	}
	a.thread = mailmodel.Thread{}
	a.threadLoading = true
	service, generation := a.ownerService(owner)
	manager, ok := service.(mailmodel.DraftManager)
	if !ok {
		a.threadLoading = false
		a.threadErr = "drafts are unavailable"
		return
	}
	a.schedule(func() {
		draft, err := manager.Draft(context.Background(), ref.ID)
		draft.Message.AccountEmail = owner
		a.publish(func() {
			if !a.ownerOperationCurrent(owner, generation) || call != a.threadCall {
				return
			}
			a.threadLoading = false
			if err != nil {
				if a.thread.ID != ref.ID {
					a.threadErr = err.Error()
				}
				return
			}
			a.showThread(id, draftThread(draft))
		})
	})
}

func (a *App) openSelectedDraft() {
	if a.selectedLabel != mailmodel.Draft || a.selectedThread == "" || len(a.thread.Messages) == 0 {
		return
	}
	a.openDraftMessage(a.selectedThread, a.thread.Messages[0])
}

func draftThreadPage(page mailmodel.DraftPage) mailmodel.ThreadPage {
	result := mailmodel.ThreadPage{Threads: make([]mailmodel.ThreadSummary, len(page.Drafts)), NextPageToken: page.NextPageToken}
	for i, draft := range page.Drafts {
		result.Threads[i] = draftSummary(draft)
	}
	return result
}

func draftSummary(draft mailmodel.DraftRecord) mailmodel.ThreadSummary {
	message := draft.Message
	subject := message.Subject
	if strings.TrimSpace(subject) == "" {
		subject = locales.UntitledDraft()
	}
	snippet := strings.Join(strings.Fields(message.Text), " ")
	const limit = 120
	runes := []rune(snippet)
	if len(runes) > limit {
		snippet = string(runes[:limit-1]) + "…"
	}
	labels := append([]string(nil), message.LabelIDs...)
	if !containsLabel(labels, mailmodel.Draft) {
		labels = append(labels, mailmodel.Draft)
	}
	return mailmodel.ThreadSummary{
		ID: draft.ID, AccountEmail: message.AccountEmail, Subject: subject, Snippet: snippet, Senders: addressName(message.From), Updated: message.Date,
		MessageCount: 1, LabelIDs: labels, Unread: containsLabel(labels, mailmodel.Unread),
	}
}

func draftThread(draft mailmodel.DraftRecord) mailmodel.Thread {
	summary := draftSummary(draft)
	message := draft.Message
	if !containsLabel(message.LabelIDs, mailmodel.Draft) {
		message.LabelIDs = append(append([]string(nil), message.LabelIDs...), mailmodel.Draft)
	}
	return mailmodel.Thread{ID: draft.ID, AccountEmail: message.AccountEmail, Subject: summary.Subject, LabelIDs: message.LabelIDs, Messages: []mailmodel.Message{message}}
}
