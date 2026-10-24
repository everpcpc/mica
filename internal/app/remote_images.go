package app

import (
	"context"
	"log"
	"net/url"
	"strings"

	"mica/internal/mail"

	xhtml "golang.org/x/net/html"
)

const remoteImagesPreference = "remote-images"

func (a *App) syncRemoteImagePolicy() {
	if a.emailResources == nil {
		return
	}
	allowed := make(map[string]bool, len(a.remoteImagePermissions))
	for key, value := range a.remoteImagePermissions {
		allowed[key] = value
	}
	a.emailResources.mu.Lock()
	a.emailResources.automaticRemote = a.remoteImagesAutomatic
	a.emailResources.allowedRemote = allowed
	a.emailResources.mu.Unlock()
}

func (a *App) remoteImagesAllowed(message mail.Message) bool {
	return a.remoteImagesAutomatic || a.remoteImagePermissions[readerMessageKey(message)]
}

func (a *App) showRemoteImagesAction(message mail.Message) bool {
	return message.HTML != "" && !a.remoteImagesAllowed(message) && messageHasRemoteImages(message)
}

func (a *App) allowRemoteImages(message mail.Message) {
	key := readerMessageKey(message)
	if a.remoteImagePermissions == nil {
		a.remoteImagePermissions = make(map[string]bool)
	}
	a.remoteImagePermissions[key] = true
	a.syncRemoteImagePolicy()
	a.closeEmailView(key)
}

func (a *App) setRemoteImagesAutomatic(allowed bool) {
	a.remoteImagesAutomatic = allowed
	a.syncRemoteImagePolicy()
	a.saveRemoteImagesPreference()
	a.closeEmailViews()
}

func (a *App) RestoreRemoteImagesPreference() {
	if a.store == nil {
		return
	}
	value, found, err := a.store.Preference(context.Background(), remoteImagesPreference)
	logCacheError(err)
	if err == nil && found {
		a.remoteImagesAutomatic = value == "auto"
		a.syncRemoteImagePolicy()
	}
}

func (a *App) saveRemoteImagesPreference() {
	value := "blocked"
	if a.remoteImagesAutomatic {
		value = "auto"
	}
	a.cacheState.mu.Lock()
	a.cacheState.remoteImagesSave++
	generation := a.cacheState.remoteImagesSave
	store := a.store
	a.cacheState.mu.Unlock()
	if store == nil {
		return
	}
	a.cacheState.writes.Add(1)
	a.schedule(func() {
		defer a.cacheState.writes.Done()
		a.cacheState.mu.Lock()
		defer a.cacheState.mu.Unlock()
		if generation != a.cacheState.remoteImagesSave {
			return
		}
		if err := store.SavePreference(context.Background(), remoteImagesPreference, value); err != nil {
			log.Printf("mica: save remote image preference: %v", err)
		}
	})
}

func remoteImageSource(source string) bool {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(source, "//") {
		return true
	}
	parsed, err := url.Parse(source)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")
}

func remoteImageStyle(value string) bool {
	value = strings.ToLower(value)
	for {
		start := strings.Index(value, "url(")
		if start < 0 {
			return false
		}
		value = value[start+4:]
		end := strings.Index(value, ")")
		if end < 0 {
			return false
		}
		target := strings.Trim(strings.TrimSpace(value[:end]), `"'`)
		if remoteImageSource(target) {
			return true
		}
		value = value[end+1:]
	}
}

func messageHasRemoteImages(message mail.Message) bool {
	root, err := xhtml.Parse(strings.NewReader(message.HTML))
	if err != nil {
		return false
	}
	found := false
	var visit func(*xhtml.Node)
	visit = func(node *xhtml.Node) {
		if found {
			return
		}
		if node.Type == xhtml.ElementNode {
			if emailHidden(node) || trackingImageNode(node) {
				return
			}
			if remoteImageSource(imageAttribute(node, "src")) {
				found = true
				return
			}
			for _, attribute := range node.Attr {
				if attribute.Key == "data-bg" && remoteImageSource(attribute.Val) {
					found = true
					return
				}
				if attribute.Key == "style" && remoteImageStyle(attribute.Val) {
					found = true
					return
				}
			}
		}
		for child := node.FirstChild; child != nil && !found; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	return found
}
