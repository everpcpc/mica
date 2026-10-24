package app

import (
	"context"

	"mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo/ui"
)

func (a *App) refreshAccountList() {
	s, store := a.settings, a.store
	if store == nil {
		var accounts []mail.Account
		if email := normalizedEmail(a.account.Email); !a.account.Demo && email != "" {
			accounts = []mail.Account{a.account}
		}
		if s != nil {
			s.accounts = accounts
		}
		a.accountCount = len(accounts)
		a.unifiedAccounts = accounts
		return
	}
	a.schedule(func() {
		accounts, err := store.Accounts(context.Background())
		a.publish(func() {
			if err != nil {
				if s != nil && a.settings == s {
					s.err = locales.AccountSwitchFailed(err.Error())
				}
				return
			}
			if s != nil && a.settings == s {
				s.accounts = accounts
			}
			a.accountCount = len(accounts)
			a.unifiedAccounts = accounts
		})
	})
}

func (a *App) gmailSettings(c *ui.Context, s *settingsState) {
	t := c.Theme()
	sectionPanel(c).Children(func() {
		ui.Text(c, locales.Gmail()).FontSize(16).Bold()
		if len(s.accounts) == 0 {
			ui.Text(c, locales.GmailSetup()).FontSize(12).LineHeight(1.45).TextColor(t.TextMuted)
		}
		for _, account := range s.accounts {
			a.accountSettingsRow(c, s, account)
		}
		if len(s.accounts) > 0 {
			ui.Divider(c)
			ui.Text(c, locales.GmailSetup()).FontSize(12).LineHeight(1.45).TextColor(t.TextMuted)
		}
		ui.Row(c).Justify(ui.End).Children(func() {
			primaryAction(c, locales.AddAccount()).Disabled(s.busy || a.compose != nil).OnClick(a.connectGmail)
		})
		ui.Collapsible(c, locales.CustomOauthClient(), &s.customOAuth, func() {
			ui.Link(c, locales.GmailCloudCredentials(), "https://console.cloud.google.com/apis/credentials")
			settingsField(c, "Client ID", &s.clientID, "…apps.googleusercontent.com", false, s.busy)
			settingsField(c, "Client Secret", &s.clientSecret, locales.ClientSecretPlaceholder(), true, s.busy)
			settingsField(c, "Project ID", &s.projectID, "my-project", false, s.busy)
			ui.Row(c).Justify(ui.End).Children(func() {
				ui.Button(c, locales.ConnectGmail()).Disabled(s.busy || a.compose != nil).OnClick(a.connectCustomGmail)
			})
		})
		if a.compose != nil {
			ui.Text(c, locales.AccountComposerRequired()).FontSize(12).LineHeight(1.4).TextColor(t.TextMuted)
		}
		if s.err != "" {
			ui.Text(c, s.err).FontSize(12).LineHeight(1.4).TextColor(t.Danger)
		}
	})
}

func (a *App) accountSettingsRow(c *ui.Context, s *settingsState, account mail.Account) {
	t := c.Theme()
	email := normalizedEmail(account.Email)
	current := normalizedEmail(a.storeAccount)
	if current == "" && !a.account.Demo {
		current = normalizedEmail(a.account.Email)
	}
	active := email != "" && email == current
	name := account.Name
	if name == "" {
		name = email
	}
	ui.Column(c.Key("account-" + email)).Gap(8).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			var photo *ui.Bitmap
			if active {
				photo = a.accountPhoto
			}
			ui.Avatar(c, name, photo)
			ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
				ui.Text(c, name).Bold().SingleLine()
				ui.Text(c, email).FontSize(11).TextColor(t.TextMuted).SingleLine()
			})
			if active {
				ui.Text(c, locales.Connected()).FontSize(12).TextColor(t.Success)
				ui.Button(c, locales.ReauthorizeAccount()).Disabled(s.busy).OnClick(func() { a.reauthorizeAccountFromSettings(email) })
			} else {
				ui.Button(c, locales.SwitchAccount()).Disabled(s.busy).OnClick(func() { a.switchAccountFromSettings(email) })
			}
			ui.Button(c, locales.RemoveAccount()).Disabled(s.busy).OnClick(func() { a.requestRemoveAccount(email) })
		})
		if s.removeEmail == email {
			ui.Text(c, locales.RemoveAccountWarning(email)).FontSize(12).LineHeight(1.4).TextColor(t.TextMuted)
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				ui.Button(c, locales.Cancel()).Disabled(s.busy).OnClick(func() { a.requestRemoveAccount(email) })
				ui.Button(c, locales.ConfirmRemoveAccount()).TextColor(t.Danger).Disabled(s.busy).OnClick(func() { a.removeAccount(email) })
			})
		}
	})
}

func (a *App) switchAccountFromSettings(email string) {
	s := a.settings
	if s == nil || s.busy {
		return
	}
	if a.compose != nil {
		s.err = locales.AccountComposerRequired()
		return
	}
	s.busy = true
	s.err = ""
	s.removeEmail = ""
	a.switchAccount(email)
	if a.accountTransition == nil {
		s.busy = false
	}
}

func (a *App) reauthorizeAccountFromSettings(email string) {
	s := a.settings
	if s == nil || s.busy {
		return
	}
	if a.compose != nil {
		s.err = locales.AccountComposerRequired()
		return
	}
	s.busy = true
	s.err = ""
	a.authenticateAccount(email)
}
