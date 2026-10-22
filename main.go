package main

import (
	"log"

	"mica/internal/app"
	"mica/internal/mail"
	_ "mica/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"
)

func appMenu() *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Mica", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout},
			updater.MenuItem(),
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
	mygo.App.OnLocaleChanged(func(string) { mygo.App.SetMenu(appMenu()) })
	a := app.New(mail.NewDemo())
	var win *mygo.Window
	quitting := false
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { quitting = true })
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows && win != nil {
			win.Show()
		}
	})
	mygo.App.WhenReady(func() {
		mygo.App.SetMenu(appMenu())
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
		a.SetUpdate(win.Update)
		a.Start()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
