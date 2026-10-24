package app

import (
	"context"
	"errors"

	"mica/internal/gmail"
	"mica/internal/mail"
	"mica/locales"
)

func (a *App) commitAccountTransition(transition *accountTransition) error {
	saveActive := a.saveActiveAccount
	if saveActive == nil {
		saveActive = gmail.NewActiveAccountStore().Save
	}
	deleteActive := a.deleteActiveAccount
	if deleteActive == nil {
		deleteActive = gmail.NewActiveAccountStore().Delete
	}
	legacyMatches := false
	if transition.removeEmail != "" {
		var err error
		legacyMatches, err = a.legacyAccountMatches(transition.removeEmail)
		if err != nil {
			return err
		}
	}
	commitEmail := transition.email
	if commitEmail == "" {
		commitEmail = transition.removeEmail
	}
	commit, ok := gmail.BeginAccountCommit(commitEmail, gmail.CredentialGeneration())
	if !ok {
		return errors.New("gmail: authentication was cancelled")
	}
	defer commit()
	if transition.email != "" {
		if err := saveActive(transition.email); err != nil {
			return err
		}
		transition.activationReady = true
		var activationErr error
		if a.store != nil {
			activationErr = a.store.SetLastAccount(context.Background(), transition.email)
		}
		if transition.removeEmail != "" {
			if err := a.deleteAccountArtifacts(transition.removeEmail, legacyMatches); err != nil {
				return errors.Join(activationErr, err)
			}
		}
		return activationErr
	}
	if transition.removeEmail != "" {
		if err := a.deleteAccountArtifacts(transition.removeEmail, legacyMatches); err != nil {
			return err
		}
		transition.activationReady = true
		if err := deleteActive(); err != nil {
			return err
		}
	}
	return nil
}

func verifyLegacyAccount(ctx context.Context, email string) (bool, error) {
	clientID, clientSecret, err := loadOAuthCredentials()
	if err != nil {
		return false, err
	}
	projectID, err := loadOAuthProjectID()
	if err != nil {
		return false, err
	}
	legacy := gmail.NewOAuthWithProject(clientID, clientSecret, projectID)
	client, err := legacy.Restore(ctx)
	if errors.Is(err, gmail.ErrNoSession) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	service, err := legacy.Service(client)
	if err != nil {
		return false, err
	}
	account, err := service.Account(ctx)
	if err != nil {
		return false, err
	}
	return normalizedEmail(account.Email) == normalizedEmail(email), nil
}

func (a *App) legacyAccountMatches(email string) (bool, error) {
	verify := a.verifyLegacy
	if verify == nil {
		verify = verifyLegacyAccount
	}
	return verify(context.Background(), email)
}

func (a *App) deleteAccountArtifacts(email string, legacyMatches bool) error {
	if legacyMatches {
		removeLegacy := a.removeLegacy
		if removeLegacy == nil {
			removeLegacy = func(ctx context.Context, _ string) error {
				return gmail.NewOAuthWithProject("", "", "").Logout(ctx)
			}
		}
		if err := removeLegacy(context.Background(), email); err != nil {
			return err
		}
	}
	removeCredentials := a.removeCredentials
	if removeCredentials == nil {
		removeCredentials = func(ctx context.Context, email string) error {
			return gmail.NewOAuthForAccount("", "", "", email).Logout(ctx)
		}
	}
	deleteCheckpoint := a.deleteCheckpoint
	if deleteCheckpoint == nil {
		deleteCheckpoint = deleteHistoryCheckpoint
	}
	if err := removeCredentials(context.Background(), email); err != nil {
		return err
	}
	if err := deleteCheckpoint(email); err != nil {
		return err
	}
	if a.store != nil {
		return a.store.DeleteAccount(context.Background(), email)
	}
	return nil
}

func (a *App) requestRemoveAccount(email string) {
	if a.settings == nil || a.settings.busy {
		return
	}
	email = normalizedEmail(email)
	a.settings.err = ""
	if a.settings.removeEmail == email {
		a.settings.removeEmail = ""
	} else {
		a.settings.removeEmail = email
	}
}

func (a *App) removeAccount(email string) {
	s := a.settings
	email = normalizedEmail(email)
	if s == nil || s.busy || email == "" {
		return
	}
	if a.compose != nil || a.accountTransition != nil {
		s.err = locales.AccountComposerRequired()
		return
	}
	current := normalizedEmail(a.storeAccount)
	if current == "" && !a.account.Demo {
		current = normalizedEmail(a.account.Email)
	}
	if email == current && a.account.Email != "" && normalizedEmail(a.account.Email) != current {
		s.err = locales.AccountRemoveFailed("account identity changed")
		return
	}
	s.busy = true
	s.err = ""
	transition := &accountTransition{sourceVersion: a.serviceVersion, removeEmail: email}
	a.accountTransition = transition
	a.cancelPendingAuthentication()
	store, restore := a.store, a.accountService
	if restore == nil {
		restore = restoreStoredAccount
	}
	a.waitForMutations(func() {
		a.schedule(func() {
			a.cacheState.writes.Wait()
			var snapshotFound bool
			var next mail.Service
			var nextEmail string
			var err error
			if store == nil {
				err = errors.New("mail: account catalog is unavailable")
			} else {
				if drafts, ok := store.(draftStore); ok {
					_, snapshotFound, err = drafts.DraftSnapshot(context.Background(), email)
				}
				if err == nil && !snapshotFound && email == current {
					var accounts []mail.Account
					accounts, err = store.Accounts(context.Background())
					for _, account := range accounts {
						if err != nil || normalizedEmail(account.Email) == email {
							continue
						}
						candidate := normalizedEmail(account.Email)
						next, err = restore(context.Background(), candidate)
						if err == nil {
							nextEmail = candidate
							break
						}
						if errors.Is(err, gmail.ErrNoSession) {
							err = nil
							next = nil
						}
					}
				}
			}
			if err == nil && !snapshotFound && email != current {
				legacyMatches, verifyErr := a.legacyAccountMatches(email)
				if verifyErr != nil {
					err = verifyErr
				} else {
					commit, ok := gmail.BeginAccountCommit(email, gmail.CredentialGeneration())
					if !ok {
						err = errors.New("gmail: authentication was cancelled")
					} else {
						err = a.deleteAccountArtifacts(email, legacyMatches)
						commit()
					}
				}
			}
			a.publish(func() {
				if a.accountTransition != transition || a.serviceVersion != transition.sourceVersion {
					return
				}
				if snapshotFound {
					a.accountTransition = nil
					if a.settings == s {
						s.busy = false
						s.err = locales.AccountDraftRemaining(email)
					}
					a.resumePendingMailto()
					return
				}
				if err != nil {
					a.failAccountTransition(transition, err)
					return
				}
				if email != current {
					a.accountTransition = nil
					a.removeAccountClient(email)
					if a.settings == s {
						s.busy = false
						s.removeEmail = ""
					}
					a.notice = locales.AccountRemoved(email)
					a.refreshAccountList()
					a.refreshUnifiedAfterAccountRemoval(email)
					a.resumePendingMailto()
					return
				}
				if next == nil {
					next = mail.NewDemo()
				}
				transition.email = nextEmail
				transition.service = next
				transition.notice = locales.AccountRemoved(email)
				a.drainAccountTransition(transition)
			})
		})
	})
}
