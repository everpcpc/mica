package app

import (
	"context"
	"log"
	"strings"

	mailmodel "mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo/ui/richtext"
)

type draftStore interface {
	DraftSnapshot(context.Context, string) (mailmodel.DraftSnapshot, bool, error)
	SaveDraftSnapshot(context.Context, string, mailmodel.DraftSnapshot) error
	DeleteDraftSnapshot(context.Context, string) error
}

func (a *App) restoreDraftSnapshot() {
	store, ok := a.store.(draftStore)
	email := a.storeAccount
	if !ok || email == "" || a.draftRestoreAccount == email || !strings.EqualFold(a.account.Email, email) {
		return
	}
	a.draftRestoreAccount = email
	version := a.serviceVersion
	a.schedule(func() {
		snapshot, found, err := store.DraftSnapshot(context.Background(), email)
		logCacheError(err)
		a.publish(func() {
			if version != a.serviceVersion || !strings.EqualFold(a.account.Email, email) || email != a.storeAccount || a.compose != nil || err != nil || !found {
				return
			}
			a.openRecoveredDraft(snapshot, email)
		})
	})
}

func (a *App) draftSnapshotAccount(m *composer) (string, bool) {
	email := a.storeAccount
	if email == "" {
		return "", false
	}
	if m != nil && m.recoveryAccount != "" && (!strings.EqualFold(m.recoveryAccount, a.account.Email) || !strings.EqualFold(m.recoveryAccount, email)) {
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
	if !ok || email == "" {
		return
	}
	version := a.serviceVersion
	a.cacheState.mu.Lock()
	a.cacheState.draftWrite++
	write := a.cacheState.draftWrite
	a.cacheState.mu.Unlock()
	a.schedule(func() {
		a.cacheState.mu.Lock()
		defer a.cacheState.mu.Unlock()
		if version != a.cacheState.version || write != a.cacheState.draftWrite {
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
	if a.compose != nil {
		return
	}
	compose := mailmodel.Compose{
		To: message.To, Cc: message.Cc, Bcc: message.Bcc, Subject: message.Subject, Text: message.Text, HTML: message.HTML,
		ThreadID: message.ThreadID, InReplyTo: message.InReplyTo, References: message.References, ThreadingResolved: message.ThreadID != "",
	}
	m := newDraftComposer(compose)
	m.draftID = draftID
	a.compose = m
	a.addMessageAttachments(message)
}

func (a *App) openRecoveredDraft(snapshot mailmodel.DraftSnapshot, email string) {
	if a.compose != nil {
		return
	}
	m := newDraftComposer(snapshot.Compose)
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

func newDraftComposer(compose mailmodel.Compose) *composer {
	compose = mailmodel.CloneCompose(compose)
	doc := richtext.NewDocument(richtext.NewText(compose.Text))
	if compose.HTML != "" {
		doc = richtext.NewDocument(richtext.ParseHTML(compose.HTML))
	}
	m := &composer{
		open: true, generation: 1, saveState: draftSaveSaved,
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
	a.thread = mailmodel.Thread{}
	a.threadLoading = true
	manager, ok := a.service.(mailmodel.DraftManager)
	version := a.serviceVersion
	if !ok {
		a.threadLoading = false
		a.threadErr = "drafts are unavailable"
		return
	}
	a.schedule(func() {
		draft, err := manager.Draft(context.Background(), id)
		a.publish(func() {
			if version != a.serviceVersion || call != a.threadCall {
				return
			}
			a.threadLoading = false
			if err != nil {
				if a.thread.ID != id {
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
		ID: draft.ID, Subject: subject, Snippet: snippet, Senders: addressName(message.From), Updated: message.Date,
		MessageCount: 1, LabelIDs: labels, Unread: containsLabel(labels, mailmodel.Unread),
	}
}

func draftThread(draft mailmodel.DraftRecord) mailmodel.Thread {
	summary := draftSummary(draft)
	message := draft.Message
	if !containsLabel(message.LabelIDs, mailmodel.Draft) {
		message.LabelIDs = append(append([]string(nil), message.LabelIDs...), mailmodel.Draft)
	}
	return mailmodel.Thread{ID: draft.ID, Subject: summary.Subject, LabelIDs: message.LabelIDs, Messages: []mailmodel.Message{message}}
}
