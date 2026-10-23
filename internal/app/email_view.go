package app

import (
	"context"
	"errors"
	"math"
	"net/url"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const emailInitialHeight float32 = 24

type emailPage struct {
	messageID string
	view      *mygo.WebView
	height    float32
	failed    bool
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

func (a *App) estimatedEmailHeight(id string) float32 {
	if height := a.emailHeights[id]; height > 0 {
		return height
	}
	return emailInitialHeight
}

func (a *App) setEmailHeight(page *emailPage, height float32) {
	page.height = height
	page.failed = false
	if a.emailHeights == nil {
		a.emailHeights = make(map[string]float32)
	}
	a.emailHeights[page.messageID] = height
}

func (a *App) emailView(c *ui.Context, message mail.Message) ui.Element {
	page := a.emailPage(c, message)
	if page == nil {
		return ui.Text(c, a.emailDocument(message).preview).Selectable().Padding(20, 24)
	}
	return ui.Column(c.Key("email-body-" + message.ID)).Height(page.height).Transition(ui.ElementTransition{
		Size: true, Duration: 160 * time.Millisecond,
	}).Children(func() {
		if page.failed {
			ui.Text(c, a.emailDocument(message).preview).Selectable().Padding(20, 24)
			return
		}
		ui.WebView(c, page.view).Height(page.height).Transition(ui.ElementTransition{
			Enter: &ui.Motion{Opacity: 0}, Duration: 160 * time.Millisecond,
		})
	})
}

func (a *App) emailPage(c *ui.Context, message mail.Message) *emailPage {
	if page := a.emailPages[message.ID]; page != nil {
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
	height := a.estimatedEmailHeight(message.ID)
	page := &emailPage{messageID: message.ID, view: view, height: height}
	if a.emailPages == nil {
		a.emailPages = make(map[string]*emailPage)
	}
	a.emailPages[message.ID] = page
	services := c.Services()
	view.Page().OnDidFinishLoad(func() {
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
	view.Page().LoadHTML(emailDocumentHTML(body, c.Theme().Dark), "")
	return page
}

func (a *App) closeEmailView(id string) {
	if page := a.emailPages[id]; page != nil {
		page.view.Destroy()
		delete(a.emailPages, id)
	}
	if a.emailResources != nil {
		a.emailResources.revokeMessage(id)
	}
}

func (a *App) closeEmailViews() {
	for id := range a.emailPages {
		a.closeEmailView(id)
	}
}

func (a *App) retainEmailViews(thread mail.Thread) {
	messages := make(map[string]bool, len(thread.Messages))
	for _, message := range thread.Messages {
		messages[message.ID] = true
	}
	for id := range a.emailPages {
		if !messages[id] {
			a.closeEmailView(id)
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

func (b *emailBridge) Wheel(ctx context.Context, dy float64) error {
	if math.IsNaN(dy) || math.IsInf(dy, 0) {
		return errors.New("mail: invalid wheel delta")
	}
	page := mygo.CallerPage(ctx)
	b.app.publish(func() {
		for _, candidate := range b.app.emailPages {
			if candidate.view.Page() == page {
				scroll := &b.app.readerScroll
				scroll.Y = min(max(scroll.Y+float32(dy), 0), scroll.MaxY)
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
  let pending = 0, queued = false;
  addEventListener("wheel", event => {
    if (event.ctrlKey || Math.abs(event.deltaY) <= Math.abs(event.deltaX)) return;
    event.preventDefault();
    const unit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? innerHeight : 1;
    pending += event.deltaY * unit;
    if (queued) return;
    queued = true;
    requestAnimationFrame(() => {
      queued = false;
      const dy = pending;
      pending = 0;
      if (dy) mygo.call("MicaMail.Wheel", dy).catch(() => {});
    });
  }, {passive: false});
  const keyShortcuts = {
    c: "compose", j: "next", k: "previous", o: "open", u: "close", e: "archive",
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
