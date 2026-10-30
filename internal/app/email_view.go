package app

import (
	"context"
	"errors"
	"net/url"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const emailInitialHeight float32 = 24

type emailPage struct {
	messageKey string
	view       *mygo.WebView
	height     float32
	failed     bool
}

func readerMessageKey(message mail.Message) string {
	if normalizedEmail(message.AccountEmail) == "" {
		return message.ID
	}
	return messageRefForAccount(message.AccountEmail, message.ThreadID, message.ID).key()
}

func RegisterEmailRenderer(a *App) error {
	if err := mygo.Protocol.Handle("mica-img", a.emailResources); err != nil {
		return err
	}
	mygo.BindAs("MicaMail", &emailBridge{app: a})
	return nil
}

func (a *App) SetEmailViewFactory(factory func() (*mygo.WebView, error)) {
	a.newEmailView = factory
}

func (a *App) estimatedEmailHeight(key string) float32 {
	return a.emailHeights[key]
}

func (a *App) setEmailHeight(page *emailPage, height float32) {
	page.height = height
	page.failed = false
	if a.emailHeights == nil {
		a.emailHeights = make(map[string]float32)
	}
	a.emailHeights[page.messageKey] = height
}

func (a *App) emailView(c *ui.Context, message mail.Message) ui.Element {
	page := a.emailPage(c, message)
	if page == nil {
		return ui.Text(c, a.readerEmailDocument(message).preview).Selectable().Padding(20, 24)
	}
	height := page.height
	if height <= 0 {
		_, viewportHeight := c.Size()
		height = max(emailInitialHeight, viewportHeight-titleBarHeight)
	}
	return ui.Column(c.Key("email-body-" + readerMessageKey(message))).Height(height).Transition(ui.ElementTransition{
		Size: true, Duration: 160 * time.Millisecond,
	}).Children(func() {
		if page.failed {
			ui.Text(c, a.readerEmailDocument(message).preview).Selectable().Padding(20, 24)
			return
		}
		ui.WebView(c, page.view).Height(height).Transition(ui.ElementTransition{
			Enter: &ui.Motion{Opacity: 0}, Duration: 160 * time.Millisecond,
		})
	})
}

func (a *App) emailPage(c *ui.Context, message mail.Message) *emailPage {
	key := readerMessageKey(message)
	if page := a.emailPages[key]; page != nil {
		return page
	}
	if a.newEmailView == nil {
		return nil
	}
	body := prepareEmailHTML(a.emailResources, message)
	if body == "" {
		return nil
	}
	view, err := a.newEmailView()
	if err != nil {
		return nil
	}
	height := a.estimatedEmailHeight(key)
	page := &emailPage{messageKey: key, view: view, height: height}
	if a.emailPages == nil {
		a.emailPages = make(map[string]*emailPage)
	}
	a.emailPages[key] = page
	services := c.Services()
	view.Page().OnDOMReady(func() {
		_, _ = view.Page().Eval(emailHeightScript)
	})
	view.Page().OnDidFailLoad(func(*mygo.LoadError) {
		a.publish(func() {
			page.failed = true
			page.height = 80
		})
	})
	view.Page().OnWillNavigate(func(event *mygo.NavigateEvent) {
		if emailFragmentURL(event.URL) {
			return
		}
		event.PreventDefault()
		if event.UserInitiated && ui.SafeToOpen(event.URL) {
			services.OpenURL(event.URL)
		}
	})
	view.Page().LoadHTML(emailDocumentHTML(body), "")
	return page
}

func (a *App) closeEmailView(key string) {
	if page := a.emailPages[key]; page != nil {
		page.view.Destroy()
		delete(a.emailPages, key)
	}
	if a.emailResources != nil {
		a.emailResources.revokeMessage(key)
	}
}

func (a *App) closeEmailViews() {
	for key := range a.emailPages {
		a.closeEmailView(key)
	}
}

func (a *App) retainEmailViews(thread mail.Thread) {
	messages := make(map[string]bool, len(thread.Messages))
	for _, message := range thread.Messages {
		messages[readerMessageKey(message)] = true
	}
	for key := range a.emailPages {
		if !messages[key] {
			a.closeEmailView(key)
		}
	}
}

func emailFragmentURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Fragment != "" && (u.Scheme == "" || u.Scheme == "about")
}

type emailBridge struct {
	app *App
}

func (b *emailBridge) Height(ctx context.Context, px int) error {
	if px <= 0 || px > 1<<20 {
		return errors.New("mail: invalid body height")
	}
	page := mygo.CallerPage(ctx)
	b.app.publish(func() {
		for _, candidate := range b.app.emailPages {
			if candidate.view.Page() == page {
				b.app.setEmailHeight(candidate, float32(px))
				return
			}
		}
	})
	return nil
}

func (b *emailBridge) Shortcut(ctx context.Context, name string, shift bool) error {
	binding, ok := emailShortcutBinding(name)
	if !ok {
		return errors.New("mail: invalid shortcut")
	}
	page := mygo.CallerPage(ctx)
	mods := ui.Modifiers(0)
	if shift {
		mods = ui.Shift
	}
	b.app.publish(func() {
		for _, candidate := range b.app.emailPages {
			if candidate.view.Page() == page {
				b.app.performShortcut(binding.key, mods)
				return
			}
		}
	})
	return nil
}

const emailHeightScript = `
(() => {
  const body = document.getElementById("godwit-body");
  if (!body) return;
  let last = 0, measureQueued = false;
  const measure = () => {
    if (measureQueued) return;
    measureQueued = true;
    requestAnimationFrame(() => {
      measureQueued = false;
      const height = Math.ceil(body.getBoundingClientRect().height);
      if (height > 0 && height !== last) {
        last = height;
        mygo.call("MicaMail.Height", height).catch(() => {});
      }
    });
  };
  new ResizeObserver(measure).observe(body);
  addEventListener("resize", measure);
  for (const image of document.images) {
    image.addEventListener("load", measure);
    image.addEventListener("error", measure);
  }
  const keyShortcuts = {
    c: "compose", j: "next", k: "previous", o: "open", u: "close", e: "archive", b: "snooze",
    s: "star", r: "reply", a: "replyAll", f: "forward", z: "undo",
    n: "nextMessage", p: "previousMessage", g: "go", i: "inbox", t: "sent"
  };
  const namedShortcuts = {Enter: "open", "?": "help", "#": "trash", "!": "spam"};
  addEventListener("keydown", event => {
    if (event.isComposing || event.ctrlKey || event.metaKey || event.altKey) return;
    if (event.target instanceof Element && (event.target.isContentEditable || event.target.closest("input, textarea"))) return;
    const name = namedShortcuts[event.key] || keyShortcuts[event.key.toLowerCase()];
    if (!name) return;
    event.preventDefault();
    mygo.call("MicaMail.Shortcut", name, event.shiftKey).catch(() => {});
  });
  measure();
})()
`
