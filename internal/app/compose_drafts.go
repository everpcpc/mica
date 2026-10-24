package app

import (
	"context"
	"errors"
	"strings"
	"time"

	mailmodel "mica/internal/mail"
	"mica/locales"
)

const draftSaveDelay = 700 * time.Millisecond

type draftSaveState int

const (
	draftSaveIdle draftSaveState = iota
	draftSaveScheduled
	draftSaveSaving
	draftSaveSaved
	draftSaveFailed
)

type composerPendingAction int

const (
	composerActionNone composerPendingAction = iota
	composerActionClose
	composerActionDiscard
	composerActionSend
	composerActionSchedule
)

func (a *App) draftManager(m *composer) (mailmodel.DraftManager, bool) {
	owner := a.composerOwner(m)
	service, _ := a.ownerService(owner)
	manager, ok := service.(mailmodel.DraftManager)
	if !ok || !a.composeOwnerVerified(owner) {
		return nil, false
	}
	if a.composeOwnerIsActive(owner) && a.storeAccount != "" && !strings.EqualFold(a.account.Email, a.storeAccount) {
		return nil, false
	}
	if m != nil && m.recoveryAccount != "" && !strings.EqualFold(m.recoveryAccount, owner) {
		return nil, false
	}
	return manager, true
}

func (a *App) markComposerEdited(m *composer) {
	if a.compose != m || m.sending || m.pendingAction == composerActionSend || m.pendingAction == composerActionSchedule || m.pendingAction == composerActionDiscard {
		return
	}
	if m.outboxID != "" {
		m.outboxID = ""
		m.outboxAttempted = false
		m.messageID = ""
		m.draftID = ""
	}
	m.revision++
	m.saveState = draftSaveScheduled
	m.err = ""
	a.persistDraftSnapshot(m)
	a.scheduleDraftSave(m)
}

func (a *App) scheduleDraftSave(m *composer) {
	generation, revision := m.generation, m.revision
	a.after(draftSaveDelay, func() {
		a.publish(func() {
			if a.compose == m && m.generation == generation && m.revision == revision {
				a.pumpDraftSave(m)
			}
		})
	})
}

func (a *App) pumpDraftSave(m *composer) {
	if a.compose != m || m.sending || m.pendingAction == composerActionSend || m.pendingAction == composerActionSchedule || m.pendingAction == composerActionDiscard || m.draftSaving {
		return
	}
	manager, ok := a.draftManager(m)
	if !ok {
		if m.pendingAction == composerActionClose {
			m.pendingAction = composerActionNone
			m.saveState = draftSaveFailed
			m.err = locales.DraftSaveFailedReason("drafts are unavailable")
		}
		return
	}
	if m.revision == m.savedRevision {
		if m.pendingAction == composerActionClose {
			m.pendingAction = composerActionNone
			a.finishCloseCompose(m)
		}
		return
	}
	compose, ready := a.composeSnapshot(m)
	if !ready {
		_, failed := m.composeAttachmentsState()
		if failed {
			m.saveState = draftSaveFailed
			if m.pendingAction == composerActionClose {
				m.pendingAction = composerActionNone
			}
		}
		return
	}
	if m.draftID == "" && draftComposeEmpty(compose) {
		m.savedRevision = m.revision
		m.saveState = draftSaveSaved
		a.clearDraftSnapshot(m)
		if m.pendingAction == composerActionClose {
			m.pendingAction = composerActionNone
			a.finishCloseCompose(m)
		}
		return
	}
	m.draftSaving = true
	m.saveState = draftSaveSaving
	generation, revision, draftID := m.generation, m.revision, m.draftID
	owner := a.composerOwner(m)
	_, ownerGeneration := a.ownerService(owner)
	a.schedule(func() {
		var draft mailmodel.DraftRecord
		var err error
		if draftID == "" {
			draft, err = manager.CreateDraft(context.Background(), compose)
		} else {
			draft, err = manager.UpdateDraft(context.Background(), draftID, compose)
		}
		a.publish(func() {
			a.finishDraftSave(m, generation, revision, draftID, ownerGeneration, draft, err)
		})
	})
}

func (a *App) finishDraftSave(m *composer, generation, revision uint64, previousDraftID string, ownerGeneration uint64, draft mailmodel.DraftRecord, err error) {
	if a.compose == m && m.generation == generation {
		m.draftSaving = false
	}
	if a.compose != m || m.generation != generation || !a.ownerOperationCurrent(a.composerOwner(m), ownerGeneration) {
		return
	}
	if err != nil {
		m.saveState = draftSaveFailed
		m.err = locales.DraftSaveFailedReason(err.Error())
		action := m.pendingAction
		m.pendingAction = composerActionNone
		m.sending = false
		if action == composerActionDiscard && m.draftID == "" {
			a.clearDraftSnapshot(m)
			a.compose = nil
		}
		return
	}
	if previousDraftID == "" {
		m.draftID = draft.ID
	}
	m.savedRevision = revision
	if m.revision == revision {
		m.saveState = draftSaveSaved
		a.clearDraftSnapshot(m)
	} else {
		m.saveState = draftSaveScheduled
		a.persistDraftSnapshot(m)
	}
	action := m.pendingAction
	m.pendingAction = composerActionNone
	switch action {
	case composerActionClose:
		if m.revision == m.savedRevision {
			a.finishCloseCompose(m)
		} else {
			m.pendingAction = composerActionClose
			a.pumpDraftSave(m)
		}
	case composerActionDiscard:
		a.beginDiscardCompose(m)
	case composerActionSend:
		a.beginSendCompose(m)
	case composerActionSchedule:
		a.beginScheduleCompose(m, m.scheduledAt)
	default:
		if m.revision > m.savedRevision {
			a.pumpDraftSave(m)
		}
	}
}

func (a *App) composeSnapshot(m *composer) (mailmodel.Compose, bool) {
	if loading, failed := m.composeAttachmentsState(); loading || failed {
		return mailmodel.Compose{}, false
	}
	return composeSnapshotContent(m), true
}

func composeSnapshotContent(m *composer) mailmodel.Compose {
	text := m.doc.Text().PlainText()
	htmlContent := m.doc.Text().HTML()
	if m.quote != nil {
		text += "\n\n" + replyQuoteText(*m.quote)
		htmlContent += replyQuoteHTML(*m.quote)
	}
	compose := mailmodel.Compose{
		To:                m.to,
		Cc:                m.cc,
		Bcc:               m.bcc,
		Subject:           m.subject,
		Text:              text,
		HTML:              htmlContent,
		MessageID:         m.messageID,
		ThreadID:          m.threadID,
		InReplyTo:         m.inReplyTo,
		References:        m.references,
		ThreadingResolved: m.threadID != "",
		Attachments:       m.outgoingAttachments(),
	}
	return mailmodel.CloneCompose(compose)
}

func draftComposeEmpty(compose mailmodel.Compose) bool {
	return strings.TrimSpace(compose.To) == "" && strings.TrimSpace(compose.Cc) == "" && strings.TrimSpace(compose.Bcc) == "" &&
		strings.TrimSpace(compose.Subject) == "" && strings.TrimSpace(compose.Text) == "" && strings.TrimSpace(emailMarkdown(compose.HTML)) == "" && len(compose.Attachments) == 0
}

func (a *App) finishCloseCompose(m *composer) {
	if a.compose != m {
		return
	}
	draftKey := threadRefForAccount(a.composerOwner(m), m.draftID).key()
	if m.draftID != "" && a.selectedLabel == mailmodel.Draft && (a.selectedThread == m.draftID || a.selectedThread == draftKey) {
		a.clearThread()
	}
	a.compose = nil
}

func (a *App) closeCompose() {
	m := a.compose
	if m == nil || m.sending || m.pendingAction != composerActionNone {
		return
	}
	service, _ := a.ownerService(a.composerOwner(m))
	if _, ok := service.(mailmodel.DraftManager); !ok && m.recoveryAccount == "" {
		a.clearDraftSnapshot(m)
		a.compose = nil
		return
	}
	compose, ready := a.composeSnapshot(m)
	if ready && m.draftID == "" && draftComposeEmpty(compose) {
		a.clearDraftSnapshot(m)
		a.compose = nil
		return
	}
	if m.revision == m.savedRevision && !m.draftSaving {
		a.finishCloseCompose(m)
		return
	}
	m.pendingAction = composerActionClose
	m.err = ""
	a.pumpDraftSave(m)
}

func (a *App) discardCompose() {
	m := a.compose
	if m == nil || m.sending || m.pendingAction != composerActionNone {
		return
	}
	manager, ok := a.draftManager(m)
	if !ok {
		if m.draftID != "" {
			m.err = locales.DraftDeleteFailedReason("drafts are unavailable")
			return
		}
		if m.outboxID == "" {
			a.clearDraftSnapshot(m)
			a.compose = nil
			return
		}
	}
	if m.draftSaving && m.draftID != "" {
		m.pendingAction = composerActionDiscard
		m.err = ""
		return
	}
	if m.draftID == "" && m.outboxID == "" {
		m.generation++
		a.clearDraftSnapshot(m)
		a.compose = nil
		return
	}
	m.pendingAction = composerActionDiscard
	m.err = ""
	m.generation++
	owner := a.composerOwner(m)
	draftID, outboxID, generation := m.draftID, m.outboxID, m.generation
	_, ownerGeneration := a.ownerService(owner)
	if outboxID != "" {
		a.invalidateDraftSnapshotWrites(owner)
	}
	store := a.outbox
	a.cacheState.writes.Add(1)
	a.schedule(func() {
		defer a.cacheState.writes.Done()
		err := discardOutboxDraft(store, owner, outboxID, manager, draftID)
		a.publish(func() {
			if a.compose != m || m.generation != generation || !a.ownerOperationCurrent(owner, ownerGeneration) {
				return
			}
			if err != nil {
				m.pendingAction = composerActionNone
				m.err = locales.DraftDeleteFailedReason(err.Error())
				return
			}
			a.clearDraftSnapshot(m)
			a.compose = nil
			if a.composeOwnerIsActive(owner) {
				a.refresh()
			}
		})
	})
}

func (a *App) beginDiscardCompose(m *composer) {
	if a.compose != m || m.draftID == "" && m.outboxID == "" {
		if a.compose == m {
			a.clearDraftSnapshot(m)
			a.compose = nil
		}
		return
	}
	m.pendingAction = composerActionNone
	m.saveState = draftSaveIdle
	a.discardCompose()
}

func (a *App) prepareComposeForSend(m *composer) bool {
	if m == nil || m.sending || m.pendingAction != composerActionNone {
		return false
	}
	if loading, failed := m.composeAttachmentsState(); loading || failed {
		if loading {
			m.err = locales.AttachmentsReading()
		} else {
			m.err = locales.AttachmentsReadFailed()
		}
		return false
	}
	if err := validateRecipients(m.to, m.cc, m.bcc); err != nil {
		m.err = err.Error()
		return false
	}
	if strings.TrimSpace(m.doc.Text().PlainText()) == "" {
		m.err = locales.BodyRequired()
		return false
	}
	return true
}

func (a *App) sendComposeWithDraft() {
	m := a.compose
	if !a.prepareComposeForSend(m) {
		return
	}
	if m.draftSaving {
		m.pendingAction = composerActionSend
		m.sending = true
		m.err = ""
		return
	}
	a.beginSendCompose(m)
}

func (a *App) scheduleCompose(sendAt time.Time) {
	m := a.compose
	if !a.prepareComposeForSend(m) {
		return
	}
	if m.outboxAttempted {
		m.err = locales.ScheduleReviewRequired()
		return
	}
	if !sendAt.After(a.currentTime()) {
		m.err = locales.ScheduleTimeInvalid()
		return
	}
	if m.draftSaving {
		m.pendingAction = composerActionSchedule
		m.scheduledAt = sendAt
		m.sending = true
		m.err = ""
		return
	}
	a.beginScheduleCompose(m, sendAt)
}

func (a *App) beginScheduleCompose(m *composer, sendAt time.Time) {
	if a.compose != m {
		return
	}
	if !sendAt.After(a.currentTime()) {
		m.sending = false
		m.pendingAction = composerActionNone
		m.err = locales.ScheduleTimeInvalid()
		return
	}
	owner := a.composerOwner(m)
	if m.outboxAttempted {
		m.sending = false
		m.pendingAction = composerActionNone
		m.err = locales.ScheduleReviewRequired()
		return
	}
	if m.recoveryAccount != "" && (!a.composeOwnerVerified(owner) || !strings.EqualFold(m.recoveryAccount, owner)) {
		m.sending = false
		m.pendingAction = composerActionNone
		m.err = locales.DraftSaveFailedReason("account is not verified")
		return
	}
	_, hasDrafts := a.draftManager(m)
	service, ownerGeneration := a.ownerService(owner)
	if service == nil || m.draftID != "" && !hasDrafts {
		m.sending = false
		m.pendingAction = composerActionNone
		m.err = locales.DraftSaveFailedReason("drafts are unavailable")
		return
	}
	store := a.outbox
	if store == nil {
		m.sending = false
		m.pendingAction = composerActionNone
		m.err = locales.ScheduleSendFailed(errOutboxUnavailable.Error())
		return
	}
	a.invalidateDraftSnapshotWrites(owner)
	if err := ensureOutboxIdentity(m, owner); err != nil {
		m.sending = false
		m.pendingAction = composerActionNone
		m.err = locales.ScheduleSendFailed(err.Error())
		return
	}
	compose, ready := a.composeSnapshot(m)
	if !ready {
		m.sending = false
		m.pendingAction = composerActionNone
		return
	}
	draftID, outboxID := m.draftID, m.outboxID
	m.generation++
	m.pendingAction = composerActionSchedule
	m.saveState = draftSaveIdle
	m.sending = true
	m.err = ""
	a.cacheState.writes.Add(1)
	a.schedule(func() {
		defer a.cacheState.writes.Done()
		previous, found, err := store.OutboxMessage(context.Background(), owner, outboxID)
		var record mailmodel.OutboxMessage
		if err == nil {
			switch {
			case found && previous.State != mailmodel.OutboxScheduled:
				err = errors.New("mail: scheduled send requires an unsent draft")
			default:
				record = outboxRecordForCompose(outboxID, owner, draftID, compose, previous, found, false)
				record.State = mailmodel.OutboxScheduled
				record.Attempt = 1
				record.SendAt = sendAt.UTC()
				record.LastError = ""
				snapshot := mailmodel.DraftSnapshot{DraftID: draftID, OutboxID: outboxID, Compose: compose}
				err = store.EnqueueOutbox(context.Background(), owner, record, snapshot)
			}
		}
		a.publish(func() {
			if a.compose != m || !a.ownerOperationCurrent(owner, ownerGeneration) {
				return
			}
			m.sending = false
			m.pendingAction = composerActionNone
			if err != nil {
				m.err = locales.ScheduleSendFailed(err.Error())
				if _, ok := a.draftManager(m); ok && m.revision > m.savedRevision {
					m.saveState = draftSaveScheduled
					a.scheduleDraftSave(m)
				}
				return
			}
			a.armScheduledOutbox(record)
			a.compose = nil
			a.notice = locales.ScheduleSendUntil(snoozeTime(record.SendAt))
			if a.composeOwnerIsActive(owner) {
				a.refresh()
			}
		})
	})
}

func (a *App) beginSendCompose(m *composer) {
	if a.compose != m {
		return
	}
	owner := a.composerOwner(m)
	if m.recoveryAccount != "" && (!a.composeOwnerVerified(owner) || !strings.EqualFold(m.recoveryAccount, owner)) {
		m.sending = false
		m.pendingAction = composerActionNone
		m.err = locales.DraftSaveFailedReason("account is not verified")
		return
	}
	manager, hasDrafts := a.draftManager(m)
	service, ownerGeneration := a.ownerService(owner)
	if service == nil || m.draftID != "" && !hasDrafts {
		m.sending = false
		m.pendingAction = composerActionNone
		m.err = locales.DraftSaveFailedReason("drafts are unavailable")
		return
	}
	store := a.outbox
	if store == nil {
		m.sending = false
		m.pendingAction = composerActionNone
		m.err = locales.SendFailed(errOutboxUnavailable.Error())
		return
	}
	a.invalidateDraftSnapshotWrites(owner)
	if err := ensureOutboxIdentity(m, owner); err != nil {
		m.sending = false
		m.pendingAction = composerActionNone
		m.err = locales.SendFailed(err.Error())
		return
	}
	compose, ready := a.composeSnapshot(m)
	if !ready {
		m.sending = false
		m.pendingAction = composerActionNone
		return
	}
	draftID, outboxID := m.draftID, m.outboxID
	attempted := m.outboxAttempted
	allowUncertain := m.outboxReviewed
	m.outboxAttempted = true
	m.outboxReviewed = false
	m.generation++
	m.pendingAction = composerActionSend
	m.saveState = draftSaveIdle
	m.sending = true
	m.err = ""
	a.cacheState.writes.Add(1)
	a.schedule(func() {
		defer a.cacheState.writes.Done()
		previous, found, err := store.OutboxMessage(context.Background(), owner, outboxID)
		result := outboxAttemptResult{err: err}
		if err == nil {
			record := outboxRecordForCompose(outboxID, owner, draftID, compose, previous, found, attempted)
			snapshot := mailmodel.DraftSnapshot{DraftID: draftID, OutboxID: outboxID, Compose: compose}
			result = attemptOutboxSend(store, owner, record, &snapshot, service, manager, hasDrafts, allowUncertain)
		}
		a.publish(func() {
			if a.compose != m || !a.ownerOperationCurrent(owner, ownerGeneration) {
				return
			}
			m.sending = false
			m.pendingAction = composerActionNone
			if result.err != nil {
				if errors.Is(result.err, errOutboxReviewRequired) {
					m.outboxReviewed = true
					m.err = locales.OutboxReviewRequired()
					return
				}
				if result.sent {
					m.err = locales.OutboxCleanupFailed(result.err.Error())
				} else {
					m.err = locales.SendFailed(result.err.Error())
					if _, ok := a.draftManager(m); ok && m.revision > m.savedRevision {
						m.saveState = draftSaveScheduled
						a.scheduleDraftSave(m)
					}
				}
				return
			}
			a.clearDraftSnapshot(m)
			a.compose = nil
			a.notice = locales.SendSucceeded()
			if a.composeOwnerIsActive(owner) {
				a.refresh()
			}
		})
	})
}

func (a *App) composeSaveStatus(m *composer) string {
	if _, ok := a.draftManager(m); !ok {
		return ""
	}
	switch m.saveState {
	case draftSaveScheduled, draftSaveSaving:
		return locales.DraftSaving()
	case draftSaveSaved:
		return locales.DraftSaved()
	case draftSaveFailed:
		return locales.DraftSaveFailed()
	default:
		return ""
	}
}
