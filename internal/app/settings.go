package app

import (
	"context"
	"errors"
	"strings"

	"mica/internal/gmail"
	"mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
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
	s := &settingsState{open: true, language: mygo.App.Locale()}
	a.settings = s
	a.schedule(func() {
		clientID, clientSecret, err := loadCustomOAuthCredentials()
		projectID, projectErr := oauthSecret(oauthProjectIDKey)
		if err == nil {
			err = projectErr
		}
		a.publish(func() {
			if a.settings != s {
				return
			}
			s.clientID = clientID
			s.clientSecret = clientSecret
			s.projectID = projectID
			if err != nil {
				s.err = locales.OauthConfigReadFailed(err.Error())
			}
		})
	})
}

func (a *App) settingsView(c *ui.Context) {
	s := a.settings
	t := c.Theme()
	ui.Modal(c, &s.open, func() {
		ui.Column(c).Width(600).Gap(15).Children(func() {
			ui.Text(c, locales.Settings()).FontSize(19).Bold()
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
				options := []string{locales.LanguageSystem(), locales.LanguageEnglish(), locales.LanguageChinese()}
				if ui.Select(c, &s.language, options).Label(locales.Language()).Width(170).Changed() {
					mygo.App.SetLocale(languageCode(s.language))
				}
			})
			ui.Divider(c)
			ui.Text(c, locales.Gmail()).FontSize(16).Bold()
			if a.account.Demo {
				ui.Text(c, locales.GmailSetup()).FontSize(12).LineHeight(1.45).TextColor(t.TextMuted)
				ui.Row(c).Justify(ui.End).Children(func() {
					ui.PrimaryButton(c, locales.ConnectGmail()).Disabled(s.busy).OnClick(a.connectGmail)
				})
				ui.Collapsible(c, locales.CustomOauthClient(), &s.customOAuth, func() {
					ui.Link(c, locales.GmailCloudCredentials(), "https://console.cloud.google.com/apis/credentials")
					settingsField(c, "Client ID", &s.clientID, "…apps.googleusercontent.com", false, s.busy)
					settingsField(c, "Client Secret", &s.clientSecret, locales.ClientSecretPlaceholder(), true, s.busy)
					settingsField(c, "Project ID", &s.projectID, "my-project", false, s.busy)
					ui.Row(c).Justify(ui.End).Children(func() {
						ui.Button(c, locales.ConnectGmail()).Disabled(s.busy).OnClick(a.connectCustomGmail)
					})
				})
			} else {
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Avatar(c, a.account.Name, a.accountPhoto)
					ui.Column(c).Grow(1).Children(func() {
						ui.Text(c, a.account.Email).Bold()
						ui.Text(c, locales.Connected()).FontSize(12).TextColor(t.Success)
					})
					ui.Button(c, locales.LogoutGmail()).Disabled(s.busy).OnClick(a.logoutGmail)
				})
			}
			if s.err != "" {
				ui.Text(c, s.err).FontSize(12).LineHeight(1.4).TextColor(t.Danger)
			}
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
	}
	a.settings = nil
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

func (a *App) connectGmailWith(clientID, clientSecret, projectID string, custom bool) {
	s := a.settings
	if s == nil || s.busy {
		return
	}
	if clientID == "" || clientSecret == "" {
		s.err = locales.OauthConfigRequired()
		return
	}
	s.busy = true
	s.err = ""
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	a.schedule(func() {
		if custom {
			if err := saveOAuthCredentials(clientID, clientSecret, projectID); err != nil {
				a.publish(func() {
					if a.settings == s {
						s.busy = false
						s.cancel = nil
						s.err = locales.OauthConfigSaveFailed(err.Error())
					}
				})
				return
			}
		}
		oauth := gmail.NewOAuthWithProject(clientID, clientSecret, projectID)
		client, err := oauth.Login(ctx)
		var service mail.Service
		if err == nil {
			service, err = oauth.Service(client)
		}
		a.publish(func() {
			if a.settings != s {
				return
			}
			s.busy = false
			s.cancel = nil
			if err != nil {
				s.err = locales.GmailConnectFailed(err.Error())
				return
			}
			a.useService(service)
			a.settings = nil
			a.notice = locales.GmailConnected()
		})
	})
}

func (a *App) restoreGmail() {
	if a.restoreAttempted {
		return
	}
	a.restoreAttempted = true
	version := a.serviceVersion
	store := a.store
	a.schedule(func() {
		clientID, clientSecret, err := loadOAuthCredentials()
		if err != nil || clientID == "" || clientSecret == "" {
			return
		}
		projectID, err := loadOAuthProjectID()
		if err != nil {
			return
		}
		oauth := gmail.NewOAuthWithProject(clientID, clientSecret, projectID)
		client, err := oauth.Restore(context.Background())
		if err != nil {
			if !errors.Is(err, gmail.ErrNoSession) {
				a.publish(func() {
					if version == a.serviceVersion {
						a.notice = locales.OauthRestoreFailed(err.Error())
					}
				})
			}
			return
		}
		service, err := oauth.Service(client)
		cachedEmail := ""
		if err == nil && store != nil {
			account, found, cacheErr := store.LastAccount(context.Background())
			logCacheError(cacheErr)
			if cacheErr == nil && found {
				cachedEmail = account.Email
			}
		}
		a.publish(func() {
			if version == a.serviceVersion {
				if err != nil {
					a.notice = locales.OauthRestoreFailed(err.Error())
					return
				}
				a.useService(service, cachedEmail)
			}
		})
	})
}

func (a *App) reauthorizeGmail() {
	clientID, clientSecret, err := loadOAuthCredentials()
	if err != nil || clientID == "" || clientSecret == "" {
		return
	}
	projectID, err := loadOAuthProjectID()
	if err != nil {
		return
	}
	version := a.serviceVersion
	a.schedule(func() {
		oauth := gmail.NewOAuthWithProject(clientID, clientSecret, projectID)
		client, err := oauth.Login(context.Background())
		var service mail.Service
		if err == nil {
			service, err = oauth.Service(client)
		}
		a.publish(func() {
			if version != a.serviceVersion {
				return
			}
			if err != nil {
				a.notice = locales.GmailConnectFailed(err.Error())
				return
			}
			a.useService(service)
		})
	})
}

func (a *App) logoutGmail() {
	s := a.settings
	if s == nil || s.busy {
		return
	}
	s.busy = true
	s.err = ""
	version := a.serviceVersion
	a.schedule(func() {
		err := gmail.NewOAuth("", "").Logout(context.Background())
		if err == nil {
			err = deleteOAuthCredentials()
		}
		a.publish(func() {
			if version != a.serviceVersion {
				return
			}
			if err != nil {
				if a.settings == s {
					s.busy = false
					s.err = locales.LogoutFailed(err.Error())
				}
				return
			}
			a.useService(mail.NewDemo())
			if a.settings == s {
				a.settings = nil
			}
			a.notice = locales.LoggedOutDemo()
		})
	})
}

func (a *App) useService(service mail.Service, cachedAccount ...string) {
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
	_, connected := service.(*gmail.Service)
	a.storeAccount = ""
	if connected && len(cachedAccount) > 0 {
		a.storeAccount = cachedAccount[0]
	}
	a.accountVerified = false
	a.labelsSynced = false
	a.account = mail.Account{Name: "Mica", Demo: !connected}
	a.accountPhoto = nil
	a.labels = nil
	a.threads = nil
	a.thread = mail.Thread{}
	a.query = ""
	a.unreadOnly = false
	a.selectedLabel = mail.Inbox
	a.err = ""
	a.mutationQueue = nil
	a.mutationRunning = false
	a.mutationRefresh = false
	a.mutationLabels = false
	a.undo = nil
	a.compose = nil
	a.labelDialog = nil
	a.threadCache = nil
	a.resetMessageViews()
	a.clearThread()
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

func deleteOAuthCredentials() error {
	if err := mygo.Secrets.Delete(oauthClientIDKey); err != nil {
		return err
	}
	if err := mygo.Secrets.Delete(oauthClientSecretKey); err != nil {
		return err
	}
	return mygo.Secrets.Delete(oauthProjectIDKey)
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
