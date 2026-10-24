package app

import (
	"context"
	"errors"

	"mica/internal/gmail"
	"mica/internal/mail"
	"mica/locales"
)

var errAccountTransition = errors.New("mail: account transition is in progress")

type accountTransition struct {
	sourceVersion   int
	email           string
	removeEmail     string
	service         mail.Service
	closeSettings   bool
	notice          string
	selectedLabel   string
	draining        bool
	activationReady bool
}

func restoreStoredAccount(ctx context.Context, email string) (mail.Service, error) {
	clientID, clientSecret, err := loadOAuthCredentials()
	if err != nil {
		return nil, err
	}
	projectID, err := loadOAuthProjectID()
	if err != nil {
		return nil, err
	}
	return restoreAccountGmail(ctx, clientID, clientSecret, projectID, email)
}

func (a *App) switchAccount(email string) {
	email = normalizedEmail(email)
	if email == "" || a.accountTransition != nil || a.compose != nil {
		return
	}
	current := normalizedEmail(a.storeAccount)
	if current == "" && !a.account.Demo {
		current = normalizedEmail(a.account.Email)
	}
	if email == current {
		return
	}
	transition := &accountTransition{sourceVersion: a.serviceVersion, email: email, selectedLabel: a.accountInboxPending}
	a.setAccountTransition(transition)
	a.cancelPendingAuthentication()
	store := a.store
	a.schedule(func() {
		var err error
		if store == nil {
			err = errors.New("mail: account catalog is unavailable")
		} else {
			_, found, accountErr := store.Account(context.Background(), email)
			switch {
			case accountErr != nil:
				err = accountErr
			case !found:
				err = errors.New("mail: account is no longer available")
			}
		}
		a.publish(func() {
			if a.accountTransition != transition {
				return
			}
			if err != nil {
				a.failAccountTransition(transition, err)
				return
			}
			a.ensureAccountClient(email, func(err error) {
				if a.accountTransition != transition {
					return
				}
				if err != nil {
					if errors.Is(err, gmail.ErrNoSession) {
						a.setAccountTransition(nil)
						a.authenticateAccount(email)
						return
					}
					a.failAccountTransition(transition, err)
					return
				}
				transition.service, _ = a.ownerService(email)
				a.drainAccountTransition(transition)
			})
		})
	})
}

func (a *App) transitionToService(service mail.Service, email string, closeSettings bool, notice string) {
	if service == nil || a.accountTransition != nil || a.compose != nil {
		return
	}
	transition := &accountTransition{
		sourceVersion: a.serviceVersion, email: normalizedEmail(email), service: service,
		closeSettings: closeSettings, notice: notice, selectedLabel: a.accountInboxPending,
	}
	a.setAccountTransition(transition)
	a.cancelPendingAuthentication()
	a.drainAccountTransition(transition)
}

func (a *App) drainAccountTransition(transition *accountTransition) {
	if a.accountTransition != transition || transition.draining || transition.service == nil {
		return
	}
	transition.draining = true
	a.clearThread()
	notificationsDone := a.stopNotifications()
	a.schedule(func() {
		<-notificationsDone
		a.publish(func() {
			if a.accountTransition != transition || a.serviceVersion != transition.sourceVersion {
				return
			}
			a.waitForMutations(func() {
				a.schedule(func() {
					a.cacheState.writes.Wait()
					err := a.commitAccountTransition(transition)
					a.publish(func() {
						if a.accountTransition != transition || a.serviceVersion != transition.sourceVersion {
							return
						}
						if err != nil && !transition.activationReady {
							a.failAccountTransition(transition, err)
							return
						}
						a.accountTransition = nil
						if transition.selectedLabel != "" {
							a.selectedLabel = transition.selectedLabel
						}
						a.useService(transition.service, transition.email)
						a.removeAccountClient(transition.removeEmail)
						a.allowAttachmentCacheWrites()
						message := transition.notice
						if err != nil {
							message = locales.AccountSwitchFailed(err.Error())
							if transition.removeEmail != "" {
								message = locales.AccountRemoveFailed(err.Error())
							}
						}
						if transition.closeSettings {
							a.settings = nil
						} else if a.settings != nil {
							a.settings.busy = false
							a.settings.removeEmail = ""
							if err != nil {
								a.settings.err = message
							}
							a.refreshAccountList()
						}
						if message != "" {
							a.notice = message
						}
					})
				})
			})
		})
	})
}

func (a *App) setAccountTransition(transition *accountTransition) {
	a.cacheState.mu.Lock()
	a.accountTransition = transition
	a.cacheState.attachmentWritesBlocked = transition != nil
	a.cacheState.mu.Unlock()
}

func (a *App) allowAttachmentCacheWrites() {
	a.cacheState.mu.Lock()
	a.cacheState.attachmentWritesBlocked = false
	a.cacheState.mu.Unlock()
}

func (a *App) failAccountTransition(transition *accountTransition, err error) {
	if a.accountTransition != transition {
		return
	}
	a.setAccountTransition(nil)
	a.draftRestoreAccount = ""
	if a.pendingThread.email == transition.email {
		a.pendingThread = notificationTarget{}
	}
	message := locales.AccountSwitchFailed(err.Error())
	if transition.removeEmail != "" {
		message = locales.AccountRemoveFailed(err.Error())
	}
	if a.settings != nil {
		a.settings.busy = false
		a.settings.err = message
	} else {
		a.notice = message
	}
	a.resetFailedAccountInbox()
	a.startNotifications()
	a.resumePendingMailto()
}
