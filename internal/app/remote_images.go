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
	a.schedule(func() {
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
	parsed, err := url.Parse(strings.TrimSpace(source))
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")
}

func messageHasRemoteImages(message mail.Message) bool {
	root, err := xhtml.Parse(strings.NewReader(message.HTML))
	if err != nil {
		return false
	}
	found := false
	walkEmailNodes(root, func(node *xhtml.Node) {
		if found || node.Type != xhtml.ElementNode {
			return
		}
		if remoteImageSource(imageAttribute(node, "src")) {
			found = true
			return
		}
		for _, attribute := range node.Attr {
			if attribute.Key != "style" && attribute.Key != "data-bg" {
				continue
			}
			value := strings.ToLower(attribute.Val)
			if strings.Contains(value, "http://") || strings.Contains(value, "https://") {
				found = true
				return
			}
		}
	})
	return found
}
