package app

import (
	"context"
	"errors"
	"math"
	"net/url"

	"mica/internal/mail"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type emailPage struct {
	view   *mygo.WebView
	height float32
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

func (a *App) emailView(c *ui.Context, message mail.Message) ui.Element {
	page := a.emailPage(c, message)
	if page == nil {
		return ui.Text(c, a.emailDocument(message).preview).Selectable()
	}
	return ui.WebView(c, page.view).Height(page.height)
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
	page := &emailPage{view: view, height: 200}
	if a.emailPages == nil {
		a.emailPages = make(map[string]*emailPage)
	}
	a.emailPages[message.ID] = page
	services := c.Services()
	view.Page().OnDidFinishLoad(func() {
		_, _ = view.Page().Eval(emailHeightScript)
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
				candidate.height = float32(px)
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
  let last = 0;
  const measure = () => {
    const height = Math.ceil(body.getBoundingClientRect().height);
    if (height > 0 && height !== last) {
      last = height;
      mygo.call("MicaMail.Height", height).catch(() => {});
    }
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
  measure();
})()
`
