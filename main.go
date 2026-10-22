package main

import (
	"context"
	"log"
	"path/filepath"

	"mica/internal/app"
	"mica/internal/mail"
	mailstore "mica/internal/mail/store"
	"mica/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"
)

func appMenu(openSettings func()) *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Mica", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout},
			updater.MenuItem(),
			mygo.Separator(),
			{Label: locales.Settings() + "…", Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { openSettings() }},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		}},
		{Role: mygo.RoleFileMenu},
		{Role: mygo.RoleEditMenu},
		{Role: mygo.RoleFormatMenu},
		{Role: mygo.RoleWindowMenu},
	})
}

func main() {
	mygo.Use(native.Plugin)
	a := app.New(mail.NewDemo())
	if err := app.RegisterEmailRenderer(a); err != nil {
		log.Fatal(err)
	}
	var win *mygo.Window
	openSettings := func() {
		if win != nil {
			win.Show()
		}
		a.OpenSettings()
	}
	mygo.App.OnLocaleChanged(func(string) { mygo.App.SetMenu(appMenu(openSettings)) })
	a.SetNotificationHandlers(mygo.App.SetBadgeCount, func(id, title, body string) {
		go func() {
			err := mygo.NewNotification(mygo.NotificationOptions{ID: id, Group: "mail", Title: title, Body: body}).Show()
			if err != nil {
				log.Printf("show notification: %v", err)
			}
		}()
	})
	mygo.App.OnNotificationClick(func(id string) {
		a.OpenThread(id)
		if win != nil {
			win.Show()
		}
	})
	quitting := false
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { quitting = true })
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows && win != nil {
			win.Show()
		}
	})
	mygo.App.WhenReady(func() {
		mygo.App.SetMenu(appMenu(openSettings))
		win = mygo.NewWindow(mygo.WindowOptions{
			Title:          "Mica",
			Width:          1280,
			Height:         800,
			MinWidth:       980,
			MinHeight:      600,
			StateKey:       "main",
			TitleBarStyle:  mygo.TitleBarHiddenInset,
			TitleBarHeight: 52,
			Vibrancy:       mygo.VibrancySidebar,
			Content:        ui.View(a.View),
		})
		win.OnClose(func(event *mygo.CloseEvent) {
			if !quitting {
				event.PreventDefault()
				win.Hide()
			}
		})
		a.SetEmailViewFactory(func() (*mygo.WebView, error) {
			return win.NewWebView(mygo.WebViewOptions{})
		})
		a.SetUpdate(win.Update)
		dir, err := mygo.App.Path(mygo.PathUserData)
		if err != nil {
			log.Printf("locate mail cache: %v", err)
		} else if store, err := mailstore.Open(context.Background(), filepath.Join(dir, "mail.db")); err != nil {
			log.Printf("open mail cache: %v", err)
		} else {
			a.SetMailStore(store)
			a.RestorePaneWidths()
			mygo.App.OnQuit(func() {
				if err := store.Close(); err != nil {
					log.Printf("close mail cache: %v", err)
				}
			})
		}
		a.Start()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
