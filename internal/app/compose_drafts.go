package app

import (
	"context"
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
)

func (a *App) draftManager(m *composer) (mailmodel.DraftManager, bool) {
	manager, ok := a.service.(mailmodel.DraftManager)
	if !ok || !a.accountVerified || a.storeAccount != "" && !strings.EqualFold(a.account.Email, a.storeAccount) {
		return nil, false
	}
	if m != nil && m.recoveryAccount != "" && !strings.EqualFold(m.recoveryAccount, a.account.Email) {
		return nil, false
	}
	return manager, true
}

func (a *App) markComposerEdited(m *composer) {
	if a.compose != m || m.sending || m.pendingAction == composerActionSend || m.pendingAction == composerActionDiscard {
		return
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
	if a.compose != m || m.sending || m.pendingAction == composerActionSend || m.pendingAction == composerActionDiscard || m.draftSaving {
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
	generation, revision, draftID, version := m.generation, m.revision, m.draftID, a.serviceVersion
	a.schedule(func() {
		var draft mailmodel.DraftRecord
		var err error
		if draftID == "" {
			draft, err = manager.CreateDraft(context.Background(), compose)
		} else {
			draft, err = manager.UpdateDraft(context.Background(), draftID, compose)
		}
		a.publish(func() {
			a.finishDraftSave(m, generation, revision, draftID, version, draft, err)
		})
	})
}

func (a *App) finishDraftSave(m *composer, generation, revision uint64, previousDraftID string, version int, draft mailmodel.DraftRecord, err error) {
	if a.compose == m && m.generation == generation {
		m.draftSaving = false
	}
	if a.compose != m || m.generation != generation || a.serviceVersion != version {
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
	if m.draftID != "" && a.selectedLabel == mailmodel.Draft && a.selectedThread == m.draftID {
		a.clearThread()
	}
	a.compose = nil
}

func (a *App) closeCompose() {
	m := a.compose
	if m == nil || m.sending || m.pendingAction != composerActionNone {
		return
	}
	if _, ok := a.service.(mailmodel.DraftManager); !ok && m.recoveryAccount == "" {
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
		a.clearDraftSnapshot(m)
		a.compose = nil
		return
	}
	if m.draftSaving {
		m.pendingAction = composerActionDiscard
		m.err = ""
		return
	}
	if m.draftID == "" {
		m.generation++
		a.clearDraftSnapshot(m)
		a.compose = nil
		return
	}
	m.pendingAction = composerActionDiscard
	m.err = ""
	m.generation++
	draftID, generation, version := m.draftID, m.generation, a.serviceVersion
	a.schedule(func() {
		err := manager.DeleteDraft(context.Background(), draftID)
		a.publish(func() {
			if a.compose != m || m.generation != generation || a.serviceVersion != version {
				return
			}
			if err != nil {
				m.pendingAction = composerActionNone
				m.err = locales.DraftDeleteFailedReason(err.Error())
				return
			}
			a.clearDraftSnapshot(m)
			a.compose = nil
			a.refresh()
		})
	})
}

func (a *App) beginDiscardCompose(m *composer) {
	if a.compose != m || m.draftID == "" {
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

func (a *App) sendComposeWithDraft() {
	m := a.compose
	if m == nil || m.sending || m.pendingAction != composerActionNone {
		return
	}
	if loading, failed := m.composeAttachmentsState(); loading || failed {
		if loading {
			m.err = locales.AttachmentsReading()
		} else {
			m.err = locales.AttachmentsReadFailed()
		}
		return
	}
	if err := validateRecipients(m.to, m.cc, m.bcc); err != nil {
		m.err = err.Error()
		return
	}
	if strings.TrimSpace(m.doc.Text().PlainText()) == "" {
		m.err = locales.BodyRequired()
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

func (a *App) beginSendCompose(m *composer) {
	if a.compose != m {
		return
	}
	if m.recoveryAccount != "" && (!a.accountVerified || !strings.EqualFold(m.recoveryAccount, a.account.Email)) {
		m.sending = false
		m.pendingAction = composerActionNone
		m.err = locales.DraftSaveFailedReason("account is not verified")
		return
	}
	compose, ready := a.composeSnapshot(m)
	if !ready {
		m.sending = false
		m.pendingAction = composerActionNone
		return
	}
	manager, hasDrafts := a.draftManager(m)
	if m.draftID != "" && !hasDrafts {
		m.sending = false
		m.pendingAction = composerActionNone
		m.err = locales.DraftSaveFailedReason("drafts are unavailable")
		return
	}
	draftID := m.draftID
	m.generation++
	m.pendingAction = composerActionSend
	m.saveState = draftSaveIdle
	m.sending = true
	m.err = ""
	service, version := a.service, a.serviceVersion
	a.schedule(func() {
		var err error
		if hasDrafts && draftID != "" {
			err = manager.SendDraft(context.Background(), draftID, compose)
		} else {
			err = service.Send(context.Background(), compose)
		}
		a.publish(func() {
			if a.compose != m || a.serviceVersion != version {
				return
			}
			if err != nil {
				m.sending = false
				m.pendingAction = composerActionNone
				m.err = locales.SendFailed(err.Error())
				if _, ok := a.draftManager(m); ok && m.revision > m.savedRevision {
					m.saveState = draftSaveScheduled
					a.scheduleDraftSave(m)
				}
				return
			}
			a.clearDraftSnapshot(m)
			a.compose = nil
			a.notice = locales.SendSucceeded()
			a.refresh()
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
