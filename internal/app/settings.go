package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"mica/internal/gmail"
	"mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"golang.org/x/oauth2"
)

const (
	oauthClientIDKey     = "gmail-oauth-client-id"
	oauthClientSecretKey = "gmail-oauth-client-secret"
	oauthProjectIDKey    = "gmail-oauth-project-id"
)

type settingsState struct {
	open         bool
	language     string
	clientID     string
	clientSecret string
	projectID    string
	customOAuth  bool
	accounts     []mail.Account
	removeEmail  string
	busy         bool
	err          string
	cancel       context.CancelFunc
}

func (a *App) OpenSettings() {
	a.publish(func() {
		if a.settings != nil && a.settings.open {
			return
		}
		a.openSettings()
	})
}

func (a *App) openSettings() {
	s := &settingsState{open: true, language: mygo.App.Locale(), busy: a.accountTransition != nil}
	a.settings = s
	a.schedule(func() {
		clientID, clientSecret, err := loadCustomOAuthCredentials()
		projectID, projectErr := oauthSecret(oauthProjectIDKey)
		if err == nil {
			err = projectErr
		}
		registered := a.isURLSchemeRegistered != nil && a.isURLSchemeRegistered("mailto")
		a.publish(func() {
			if a.settings != s {
				return
			}
			a.defaultMailApp = registered
			s.clientID = clientID
			s.clientSecret = clientSecret
			s.projectID = projectID
			if err != nil {
				s.err = locales.OauthConfigReadFailed(err.Error())
			}
		})
	})
	a.refreshAccountList()
}

func (a *App) settingsView(c *ui.Context) {
	s := a.settings
	t := c.Theme()
	ui.Modal(c, &s.open, func() {
		ui.Column(c).Width(600).Gap(15).Children(func() {
			ui.Text(c, locales.Settings()).FontSize(19).Bold()
			sectionPanel(c).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					ui.Text(c, locales.Appearance()).Grow(1)
					appearance := a.appearanceLabel()
					options := []string{locales.AppearanceSystem(), locales.AppearanceLight(), locales.AppearanceDark()}
					if ui.Select(c, &appearance, options).Label(locales.Appearance()).Width(170).Changed() {
						a.appearance = appearanceValue(appearance)
						a.applyAppearance()
					}
				})
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					ui.Text(c, locales.Language()).Grow(1)
					language := languageLabel(s.language)
					options := []string{locales.LanguageSystem(), locales.LanguageEnglish(), locales.LanguageChinese()}
					if ui.Select(c, &language, options).Label(locales.Language()).Width(170).Changed() {
						s.language = languageCode(language)
						mygo.App.SetLocale(s.language)
					}
				})
			})
			a.remoteImageSettings(c)
			a.mailtoSettings(c)
			a.gmailSettings(c, s)
			ui.Divider(c)
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				if s.busy {
					ui.Spinner(c).Size(14, 14)
					ui.Text(c, locales.Processing()).FontSize(12).TextColor(t.TextMuted).Grow(1)
				} else {
					ui.Text(c, locales.OauthLocalKeychain()).FontSize(11).TextColor(t.TextMuted).Grow(1)
				}
				closeLabel := locales.Done()
				if s.busy && s.cancel != nil {
					closeLabel = locales.Cancel()
				}
				ui.Button(c, closeLabel).OnClick(a.closeSettings)
			})
		})
	})
	if !s.open {
		a.closeSettings()
	}
}

func (a *App) remoteImageSettings(c *ui.Context) {
	t := c.Theme()
	sectionPanel(c).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, locales.RemoteImages()).Bold()
				ui.Text(c, locales.RemoteImagesHint()).FontSize(11).LineHeight(1.4).TextColor(t.TextMuted)
			})
			toggle := ui.Toggle(c.Key("remote-images"), &a.remoteImagesAutomatic, "").Label(locales.LoadRemoteImages())
			if toggle.Changed() {
				a.setRemoteImagesAutomatic(a.remoteImagesAutomatic)
			}
		})
	})
}

func (a *App) mailtoSettings(c *ui.Context) {
	t := c.Theme()
	sectionPanel(c).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, locales.DefaultMailApp()).Bold()
				ui.Text(c, locales.DefaultMailAppHint()).FontSize(11).LineHeight(1.4).TextColor(t.TextMuted)
			})
			if a.defaultMailAppRegistered() {
				ui.Text(c, locales.DefaultMailAppRegistered()).TextColor(t.Success)
			} else {
				ui.Button(c, locales.SetDefaultMailApp()).OnClick(a.setDefaultMailApp)
			}
		})
	})
}

func settingsField(c *ui.Context, label string, value *string, placeholder string, password, disabled bool) {
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).Width(100).Shrink(0)
		input := ui.TextInput(c.Key(label), value).Label(label).Placeholder(placeholder).Grow(1).Disabled(disabled)
		if password {
			input.Password()
		}
	})
}

func (a *App) closeSettings() {
	if a.settings != nil && a.settings.cancel != nil {
		a.settings.cancel()
		a.cancelAccountInboxRequest()
	}
	a.settings = nil
	a.openPendingMailto()
}

func (a *App) connectGmail() {
	clientID, clientSecret := gmail.EmbeddedCredentials()
	a.connectGmailWith(clientID, clientSecret, gmail.EmbeddedProjectID(), false)
}

func (a *App) connectCustomGmail() {
	if a.settings == nil {
		return
	}
	a.connectGmailWith(strings.TrimSpace(a.settings.clientID), strings.TrimSpace(a.settings.clientSecret), strings.TrimSpace(a.settings.projectID), true)
}

type gmailLoginFunc func(context.Context, mailStore, string, string, string, string) (mail.Service, string, error)

type pendingTokenStore struct {
	token *oauth2.Token
}

func (s *pendingTokenStore) Load() (*oauth2.Token, error) {
	if s.token == nil {
		return nil, gmail.ErrNoSession
	}
	return s.token, nil
}

func (s *pendingTokenStore) Save(token *oauth2.Token) error {
	s.token = token
	return nil
}

func (s *pendingTokenStore) Delete() error {
	s.token = nil
	return nil
}

func loginGmailService(ctx context.Context, store mailStore, clientID, clientSecret, projectID, expectedEmail string) (mail.Service, string, error) {
	generation := gmail.CredentialGeneration()
	pending := &pendingTokenStore{}
	oauth := gmail.NewOAuthWithProjectAndStore(clientID, clientSecret, projectID, pending)
	client, err := oauth.Login(ctx)
	if err != nil {
		return nil, "", err
	}
	service, err := oauth.Service(client)
	if err != nil {
		return nil, "", err
	}
	account, err := service.Account(ctx)
	if err != nil {
		return nil, "", err
	}
	if expectedEmail != "" && !strings.EqualFold(account.Email, expectedEmail) {
		return nil, "", fmt.Errorf("gmail: signed in to %s, expected %s", account.Email, expectedEmail)
	}
	if pending.token == nil {
		return nil, "", gmail.ErrNoSession
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if store != nil {
		if err := store.SaveAccount(ctx, account); err != nil {
			return nil, "", err
		}
	}
	commit, ok := gmail.BeginAccountCommit(account.Email, generation)
	if !ok {
		return nil, "", errors.New("gmail: authentication was cancelled")
	}
	defer commit()
	formal := gmail.NewOAuthForAccount(clientID, clientSecret, projectID, account.Email)
	if err := formal.SaveToken(pending.token); err != nil {
		return nil, "", err
	}
	client, err = formal.Restore(ctx)
	if err != nil {
		return nil, "", err
	}
	service, err = formal.Service(client)
	if err != nil {
		return nil, "", err
	}
	return service, account.Email, nil
}

func (a *App) connectGmailWith(clientID, clientSecret, projectID string, custom bool) {
	s := a.settings
	if s == nil || s.busy {
		return
	}
	if a.compose != nil || a.accountTransition != nil {
		s.err = locales.AccountComposerRequired()
		return
	}
	if clientID == "" || clientSecret == "" {
		s.err = locales.OauthConfigRequired()
		return
	}
	s.busy = true
	s.err = ""
	a.cancelAccountInboxRequest()
	auth := a.authID
	version := a.serviceVersion
	ctx, cancel := context.WithCancel(context.Background())
	a.authCancel = cancel
	s.cancel = cancel
	a.schedule(func() {
		defer a.publish(func() {
			if a.authID == auth {
				a.authCancel = nil
				a.openPendingMailto()
			}
		})
		if custom {
			if err := saveOAuthCredentials(clientID, clientSecret, projectID); err != nil {
				a.publish(func() {
					if a.settings == s && a.authID == auth && version == a.serviceVersion {
						s.busy = false
						s.cancel = nil
						s.err = locales.OauthConfigSaveFailed(err.Error())
					}
				})
				return
			}
		}
		login := a.loginGmail
		if login == nil {
			login = loginGmailService
		}
		service, email, err := login(ctx, a.store, clientID, clientSecret, projectID, "")
		a.publish(func() {
			if a.settings != s || a.authID != auth || version != a.serviceVersion {
				return
			}
			s.cancel = nil
			if err != nil {
				s.busy = false
				s.err = locales.GmailConnectFailed(err.Error())
				return
			}
			if a.compose != nil {
				s.busy = false
				s.err = locales.AccountComposerRequired()
				return
			}
			a.transitionToService(service, email, true, locales.GmailConnected())
		})
	})
}

func (a *App) cancelPendingAuthentication() {
	a.authID++
	gmail.InvalidateCredentials()
	if a.authCancel != nil {
		a.authCancel()
		a.authCancel = nil
	}
}

func (a *App) cancelAuthentication() {
	if service, ok := a.service.(interface{ Deactivate() }); ok {
		service.Deactivate()
	}
	a.cancelPendingAuthentication()
}

func (a *App) restoreGmail() {
	if a.restoreAttempted {
		return
	}
	a.restoreAttempted = true
	a.cancelAuthentication()
	auth := a.authID
	ctx, cancel := context.WithCancel(context.Background())
	a.authCancel = cancel
	version := a.serviceVersion
	store := a.store
	a.schedule(func() {
		clientID, clientSecret, err := loadOAuthCredentials()
		if err != nil || clientID == "" || clientSecret == "" {
			a.publish(func() { a.finishRestoreGmail(version, auth, nil, "", err) })
			return
		}
		projectID, err := loadOAuthProjectID()
		if err != nil {
			a.publish(func() { a.finishRestoreGmail(version, auth, nil, "", err) })
			return
		}
		loadActive := a.loadActiveAccount
		if loadActive == nil {
			loadActive = gmail.NewActiveAccountStore().Load
		}
		cachedEmail, err := loadActive()
		if err != nil {
			a.publish(func() { a.finishRestoreGmail(version, auth, nil, "", err) })
			return
		}
		if cachedEmail == "" && store != nil {
			account, found, cacheErr := store.LastAccount(ctx)
			logCacheError(cacheErr)
			if cacheErr == nil && found {
				cachedEmail = account.Email
			}
		}
		var service mail.Service
		if cachedEmail != "" {
			service, err = restoreAccountGmail(ctx, clientID, clientSecret, projectID, cachedEmail)
			if errors.Is(err, gmail.ErrNoSession) {
				service, err = nil, nil
			}
		}
		email := cachedEmail
		if service == nil && err == nil {
			service, email, err = migrateLegacyGmail(ctx, store, clientID, clientSecret, projectID)
		}
		a.publish(func() { a.finishRestoreGmail(version, auth, service, email, err) })
	})
}

func (a *App) finishRestoreGmail(version int, auth uint64, service mail.Service, email string, err error) {
	if version == a.serviceVersion && a.authID == auth {
		a.authCancel = nil
	}
	if version != a.serviceVersion || a.authID != auth {
		return
	}
	if err != nil {
		if !errors.Is(err, gmail.ErrNoSession) {
			a.notice = locales.OauthRestoreFailed(err.Error())
		}
		a.openPendingMailto()
		return
	}
	if service != nil {
		a.useService(service, email)
		return
	}
	a.openPendingMailto()
}

func restoreAccountGmail(ctx context.Context, clientID, clientSecret, projectID, email string) (mail.Service, error) {
	oauth := gmail.NewOAuthForAccount(clientID, clientSecret, projectID, email)
	client, err := oauth.Restore(ctx)
	if err != nil {
		return nil, err
	}
	return oauth.Service(client)
}

func migrateLegacyGmail(ctx context.Context, store mailStore, clientID, clientSecret, projectID string) (mail.Service, string, error) {
	generation := gmail.CredentialGeneration()
	legacy := gmail.NewOAuthWithProject(clientID, clientSecret, projectID)
	client, err := legacy.Restore(ctx)
	if err != nil {
		return nil, "", err
	}
	service, err := legacy.Service(client)
	if err != nil {
		return nil, "", err
	}
	account, err := service.Account(ctx)
	if err != nil {
		return nil, "", err
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if store != nil {
		if err := store.SaveAccount(ctx, account); err != nil {
			return nil, "", err
		}
	}
	source, err := legacy.TokenSource(ctx)
	if err != nil {
		return nil, "", err
	}
	token, err := source.Token()
	if err != nil {
		return nil, "", err
	}
	if token == nil {
		return nil, "", gmail.ErrNoSession
	}
	commit, ok := gmail.BeginAccountCommit(account.Email, generation)
	if !ok {
		return nil, "", errors.New("gmail: authentication was cancelled")
	}
	defer commit()
	formal := gmail.NewOAuthForAccount(clientID, clientSecret, projectID, account.Email)
	if err := formal.SaveToken(token); err != nil {
		return nil, "", err
	}
	client, err = formal.Restore(ctx)
	if err != nil {
		return nil, "", err
	}
	service, err = formal.Service(client)
	if err != nil {
		return nil, "", err
	}
	if err := legacy.Logout(ctx); err != nil {
		return nil, "", err
	}
	if err := gmail.NewActiveAccountStore().Save(account.Email); err != nil {
		return nil, "", err
	}
	if store != nil {
		if err := store.SetLastAccount(ctx, account.Email); err != nil {
			log.Printf("mica: save last account: %v", err)
		}
	}
	return service, account.Email, nil
}

func (a *App) reauthorizeGmail() {
	email := a.storeAccount
	if email == "" {
		email = a.account.Email
	}
	a.authenticateAccount(email)
}

func (a *App) authenticateAccount(expectedEmail string) {
	expectedEmail = normalizedEmail(expectedEmail)
	if expectedEmail == "" || a.accountTransition != nil || a.compose != nil {
		return
	}
	clientID, clientSecret, err := loadOAuthCredentials()
	if err == nil {
		var projectID string
		projectID, err = loadOAuthProjectID()
		if err == nil && clientID != "" && clientSecret != "" {
			s := a.settings
			if s != nil {
				s.busy = true
				s.err = ""
			}
			a.cancelPendingAuthentication()
			auth := a.authID
			version := a.serviceVersion
			ctx, cancel := context.WithCancel(context.Background())
			a.authCancel = cancel
			if s != nil {
				s.cancel = cancel
			}
			a.schedule(func() {
				service, email, loginErr := loginGmailService(ctx, a.store, clientID, clientSecret, projectID, expectedEmail)
				a.publish(func() {
					if version != a.serviceVersion || a.authID != auth {
						return
					}
					a.authCancel = nil
					if a.settings == s && s != nil {
						s.cancel = nil
					}
					if loginErr != nil {
						a.accountAuthenticationFailed(s, expectedEmail, loginErr)
						return
					}
					if a.compose != nil || a.accountTransition != nil {
						a.accountAuthenticationFailed(s, expectedEmail, errors.New(locales.AccountComposerRequired()))
						return
					}
					a.transitionToService(service, email, false, "")
				})
			})
			return
		}
	}
	a.accountAuthenticationFailed(a.settings, expectedEmail, err)
}

func (a *App) accountAuthenticationFailed(s *settingsState, expectedEmail string, err error) {
	if err == nil {
		err = gmail.ErrNoSession
	}
	if a.pendingThread.email == normalizedEmail(expectedEmail) {
		a.pendingThread = notificationTarget{}
	}
	if s != nil && a.settings == s {
		s.busy = false
		s.cancel = nil
		s.err = locales.GmailConnectFailed(err.Error())
	} else {
		a.notice = locales.GmailConnectFailed(err.Error())
	}
	if a.authCancel != nil {
		a.cancelPendingAuthentication()
	}
	a.resetFailedAccountInbox()
	a.openPendingMailto()
}

func (a *App) useService(service mail.Service, cachedAccount ...string) {
	inboxLabel := a.selectedLabel
	a.accountInboxPending = ""
	a.cancelAuthentication()
	recovered := a.compose
	preserveRecovered := recovered != nil && !recovered.sending && recovered.recoveryAccount != "" && len(cachedAccount) > 0 && strings.EqualFold(recovered.recoveryAccount, cachedAccount[0])
	if preserveRecovered && (recovered.draftSaving || recovered.saveState == draftSaveSaving) {
		recovered.draftSaving = false
		recovered.saveState = draftSaveScheduled
	}
	a.serviceVersion++
	a.cacheState.mu.Lock()
	a.cacheState.version = a.serviceVersion
	a.cacheState.mu.Unlock()
	a.service = service
	a.stopNotifications()
	a.unreadSeen = nil
	if a.setBadge != nil {
		a.setBadge(0)
	}
	connected := len(cachedAccount) > 0 && normalizedEmail(cachedAccount[0]) != ""
	a.storeAccount = ""
	if connected {
		a.storeAccount = normalizedEmail(cachedAccount[0])
		if client := a.accountClients[a.storeAccount]; client != nil {
			if client.service != service {
				a.removeAccountClient(a.storeAccount)
			} else {
				client.generation.Store(a.nextAccountGeneration(a.storeAccount))
				delete(a.accountClients, a.storeAccount)
			}
		}
	}
	a.nextAccountGeneration(a.storeAccount)
	a.accountVerified = false
	a.labelsSynced = false
	a.profileReauthAttempted = false
	a.account = mail.Account{Name: "Mica", Demo: !connected}
	a.accountPhoto = nil
	a.labels = nil
	a.threads = nil
	a.compositeThreads = false
	a.unified = nil
	a.accountCount = 0
	a.unifiedAccounts = nil
	a.unifiedExpanded = true
	a.thread = mail.Thread{}
	a.query = ""
	a.unreadOnly = false
	a.nextPageToken = ""
	a.loadingMore = false
	a.selectedLabel = mail.Inbox
	if accountInboxLabel(inboxLabel) == a.storeAccount && a.storeAccount != "" {
		a.selectedLabel = accountInboxPrefix + a.storeAccount
	}
	a.err = ""
	a.mutationQueue = nil
	a.mutationRunning = false
	a.mutationRefresh = false
	a.mutationLabels = false
	a.undo = nil
	if preserveRecovered {
		a.compose = recovered
	} else {
		a.compose = nil
	}
	a.draftRestoreAccount = ""
	a.draftRestorePending = false
	a.labelDialog = nil
	a.threadCache = nil
	a.resetMessageViews()
	a.clearThread()
	a.startupSelectionDone = false
	a.Start()
}

func loadCustomOAuthCredentials() (string, string, error) {
	clientID, err := oauthSecret(oauthClientIDKey)
	if err != nil {
		return "", "", err
	}
	clientSecret, err := oauthSecret(oauthClientSecretKey)
	if err != nil {
		return "", "", err
	}
	return clientID, clientSecret, nil
}

func loadOAuthCredentials() (string, string, error) {
	clientID, clientSecret, err := loadCustomOAuthCredentials()
	if err != nil {
		return "", "", err
	}
	embeddedID, embeddedSecret := gmail.EmbeddedCredentials()
	if clientID == "" {
		clientID = embeddedID
	}
	if clientSecret == "" {
		clientSecret = embeddedSecret
	}
	return clientID, clientSecret, nil
}

func loadOAuthProjectID() (string, error) {
	clientID, _, err := loadCustomOAuthCredentials()
	if err != nil {
		return "", err
	}
	if clientID == "" {
		return gmail.EmbeddedProjectID(), nil
	}
	return oauthSecret(oauthProjectIDKey)
}

func oauthSecret(key string) (string, error) {
	value, err := mygo.Secrets.Get(key)
	if errors.Is(err, mygo.ErrSecretNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(value), nil
}

func saveOAuthCredentials(clientID, clientSecret, projectID string) error {
	if err := mygo.Secrets.Set(oauthClientIDKey, []byte(clientID)); err != nil {
		return err
	}
	if err := mygo.Secrets.Set(oauthClientSecretKey, []byte(clientSecret)); err != nil {
		return err
	}
	return mygo.Secrets.Set(oauthProjectIDKey, []byte(projectID))
}

func (a *App) appearanceLabel() string {
	switch a.appearance {
	case "light":
		return locales.AppearanceLight()
	case "dark":
		return locales.AppearanceDark()
	default:
		return locales.AppearanceSystem()
	}
}

func appearanceValue(label string) string {
	switch label {
	case locales.AppearanceLight():
		return "light"
	case locales.AppearanceDark():
		return "dark"
	default:
		return "system"
	}
}

func languageLabel(code string) string {
	switch code {
	case "en":
		return locales.LanguageEnglish()
	case "zh-Hans":
		return locales.LanguageChinese()
	case "":
		return locales.LanguageSystem()
	default:
		return code
	}
}

func languageCode(label string) string {
	switch label {
	case locales.LanguageEnglish():
		return "en"
	case locales.LanguageChinese():
		return "zh-Hans"
	default:
		return ""
	}
}

func (a *App) applyAppearance() {
	switch a.appearance {
	case "light":
		mygo.Theme.SetSource(mygo.ThemeLight)
	case "dark":
		mygo.Theme.SetSource(mygo.ThemeDark)
	default:
		mygo.Theme.SetSource(mygo.ThemeSystem)
	}
}
