package app

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

func TestRemoteImageBlockedByDefault(t *testing.T) {
	data := testPNG(t)
	requests := 0
	client := &http.Client{Transport: imageTransport(func(request *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(data)),
		}, nil
	})}
	resources := newEmailResourceStore(newFakeService())
	resources.loader = &messageImages{service: newFakeService(), client: client}
	token := resources.register(mail.Message{}, "https://images.example/tracker.png")
	response := httptest.NewRecorder()
	resources.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "mica-img://"+token, nil))
	if response.Code != http.StatusNotFound || requests != 0 {
		t.Fatalf("blocked remote image: status=%d requests=%d", response.Code, requests)
	}
}

func TestRemoteImageActionIsRendered(t *testing.T) {
	a := newTestApp(newFakeService())
	a.selectThread("thread-1")
	a.thread.Messages[0].HTML = `<p>Hello</p><img src="https://images.example/banner.png">`
	tt := ui.NewTester(a.View, 1280, 800)
	tt.SetLocale("en")
	if !tt.HasText("Load images") {
		t.Fatal("message with remote images has no per-message loading action")
	}
	if err := tt.Click("Load images"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.remoteImagesAllowed(a.thread.Messages[0]) || tt.HasText("Load images") {
		t.Fatal("per-message action did not enable only this message")
	}
}

func TestRemoteImagePreferenceIsRendered(t *testing.T) {
	a := newTestApp(newFakeService())
	store := newFakeMailStore()
	a.SetMailStore(store)
	a.settings = &settingsState{open: true}
	tt := ui.NewTester(a.View, 1280, 800)
	tt.SetLocale("en")
	if !tt.HasText("Remote images") {
		t.Fatal("settings do not expose remote image privacy")
	}
	if err := tt.Click("Load automatically"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.remoteImagesAutomatic || store.preferences[remoteImagesPreference] != "auto" {
		t.Fatalf("automatic preference was not applied and saved: enabled=%v preference=%q", a.remoteImagesAutomatic, store.preferences[remoteImagesPreference])
	}
}

func TestRemoteImagePerMessageAndGlobalAllow(t *testing.T) {
	data := testPNG(t)
	requests := 0
	client := &http.Client{Transport: imageTransport(func(request *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(data)),
		}, nil
	})}
	resources := newEmailResourceStore(newFakeService())
	resources.loader = &messageImages{service: newFakeService(), client: client}
	a := New(newFakeService())
	a.emailResources = resources
	messageA := mail.Message{ID: "shared", ThreadID: "shared-thread", AccountEmail: "a@example.com"}
	messageB := mail.Message{ID: "shared", ThreadID: "shared-thread", AccountEmail: "b@example.com"}
	tokenA := resources.register(messageA, "https://images.example/a.png")
	tokenB := resources.register(messageB, "https://images.example/b.png")
	if response := serveOwnerResource(resources, tokenA); response.Code != http.StatusNotFound || requests != 0 {
		t.Fatalf("default policy allowed A: status=%d requests=%d", response.Code, requests)
	}
	a.allowRemoteImages(messageA)
	bodyA := prepareEmailHTML(resources, mail.Message{ID: messageA.ID, ThreadID: messageA.ThreadID, AccountEmail: messageA.AccountEmail, HTML: `<img src="https://images.example/a.png">`})
	allowedTokens := ownerResourceTokens(t, bodyA)
	if len(allowedTokens) != 1 {
		t.Fatalf("reloaded A tokens = %v", allowedTokens)
	}
	if response := serveOwnerResource(resources, allowedTokens[0]); response.Code != http.StatusOK || requests != 1 {
		t.Fatalf("A was not allowed: status=%d requests=%d", response.Code, requests)
	}
	if response := serveOwnerResource(resources, tokenB); response.Code != http.StatusNotFound || requests != 1 {
		t.Fatalf("A permission leaked to B: status=%d requests=%d", response.Code, requests)
	}
	a.setRemoteImagesAutomatic(true)
	if response := serveOwnerResource(resources, tokenB); response.Code != http.StatusOK || requests != 2 {
		t.Fatalf("global allow did not enable B: status=%d requests=%d", response.Code, requests)
	}
}

func TestRemoteImagePreferencePersistsAndRestores(t *testing.T) {
	store := newFakeMailStore()
	a := New(newFakeService())
	a.schedule = func(fn func()) { fn() }
	a.SetMailStore(store)
	if a.remoteImagesAutomatic {
		t.Fatal("remote images default to automatic loading")
	}
	a.setRemoteImagesAutomatic(true)
	if got := store.preferences[remoteImagesPreference]; got != "auto" {
		t.Fatalf("saved automatic preference = %q", got)
	}
	restarted := New(newFakeService())
	restarted.schedule = func(fn func()) { fn() }
	restarted.SetMailStore(store)
	restarted.RestoreRemoteImagesPreference()
	if !restarted.remoteImagesAutomatic || !restarted.emailResources.automaticRemote {
		t.Fatal("automatic preference did not survive restart")
	}
	restarted.setRemoteImagesAutomatic(false)
	if got := store.preferences[remoteImagesPreference]; got != "blocked" {
		t.Fatalf("saved blocked preference = %q", got)
	}
	blocked := New(newFakeService())
	blocked.SetMailStore(store)
	blocked.RestoreRemoteImagesPreference()
	if blocked.remoteImagesAutomatic || blocked.emailResources.automaticRemote {
		t.Fatal("blocked preference did not survive restart")
	}
}

func TestRemoteImagePreferenceLatestWriteWins(t *testing.T) {
	store := newFakeMailStore()
	a := New(newFakeService())
	a.SetMailStore(store)
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.setRemoteImagesAutomatic(true)
	a.setRemoteImagesAutomatic(false)
	if len(jobs) != 2 {
		t.Fatalf("preference writes = %d, want 2", len(jobs))
	}
	jobs[1]()
	jobs[0]()
	if got := store.preferences[remoteImagesPreference]; got != "blocked" {
		t.Fatalf("out-of-order preference writes saved %q", got)
	}
}

func TestMessageRemoteImageDetection(t *testing.T) {
	for _, tc := range []struct {
		name string
		html string
		want bool
	}{
		{name: "remote image", html: `<img src="https://images.example/a.png">`, want: true},
		{name: "remote background", html: `<div style="background:url('https://images.example/bg.png')">Background</div>`, want: true},
		{name: "protocol relative image", html: `<img src="//cdn.example/a.png">`, want: true},
		{name: "protocol relative background", html: `<div style="background-image:url(//cdn.example/bg.png)">Background</div>`, want: true},
		{name: "inline image", html: `<img src="data:image/png;base64,AAAA">`},
		{name: "hidden remote image", html: `<div style="display:none"><img src="https://images.example/hidden.png"></div>`},
		{name: "tracking pixel", html: `<img width="1" height="1" src="https://images.example/pixel.png">`},
		{name: "small background", html: `<div width="1" height="1" style="background:url(https://cdn.example/bg.png)">Text</div>`, want: true},
		{name: "plain text", html: `<p>Hello</p>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := messageHasRemoteImages(mail.Message{HTML: tc.html}); got != tc.want {
				t.Fatalf("messageHasRemoteImages() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProtocolRelativeRemoteImage(t *testing.T) {
	data := testPNG(t)
	requested := ""
	client := &http.Client{Transport: imageTransport(func(request *http.Request) (*http.Response, error) {
		requested = request.URL.String()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(data)),
		}, nil
	})}
	resources := newEmailResourceStore(newFakeService())
	resources.loader = &messageImages{service: newFakeService(), client: client}
	resources.automaticRemote = true
	token := resources.register(mail.Message{}, "//cdn.example/a.png")
	response := serveOwnerResource(resources, token)
	if response.Code != http.StatusOK || requested != "https://cdn.example/a.png" {
		t.Fatalf("protocol-relative image: status=%d request=%q", response.Code, requested)
	}
}

func TestRemoteImagePreferenceQuitWaitsForWrites(t *testing.T) {
	store := newFakeMailStore()
	store.preferences[remoteImagesPreference] = "auto"
	a := New(newFakeService())
	a.SetMailStore(store)
	var jobs []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.setRemoteImagesAutomatic(false)
	if len(jobs) != 1 {
		t.Fatalf("preference jobs = %d, want 1", len(jobs))
	}
	done := make(chan struct{})
	go func() {
		a.cacheState.writes.Wait()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("cache wait returned before the preference was saved")
	case <-time.After(20 * time.Millisecond):
	}
	jobs[0]()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cache wait did not finish after saving")
	}
	if got := store.preferences[remoteImagesPreference]; got != "blocked" {
		t.Fatalf("preference after quit wait = %q", got)
	}
}

func TestRemoteImagePermissionDoesNotLeakWithinAccount(t *testing.T) {
	resources := newEmailResourceStore(newFakeService())
	a := New(newFakeService())
	a.emailResources = resources
	first := mail.Message{ID: "first", ThreadID: "shared-thread", AccountEmail: "a@example.com"}
	second := mail.Message{ID: "second", ThreadID: "shared-thread", AccountEmail: "a@example.com"}
	a.allowRemoteImages(first)
	if !resources.remoteAllowed(readerMessageKey(first), "https://images.example/a.png") {
		t.Fatal("first message was not allowed")
	}
	if resources.remoteAllowed(readerMessageKey(second), "https://images.example/b.png") {
		t.Fatal("permission leaked to another message in the same account")
	}
}

func TestCaptureRemoteImages(t *testing.T) {
	if os.Getenv("MICA_CAPTURE") == "" {
		t.Skip("capture is opt-in")
	}
	a := newTestApp(newFakeService())
	a.selectThread("thread-1")
	a.thread.Messages[0].HTML = `<p>Hello</p><img src="https://images.example/banner.png">`
	tt := ui.NewTester(a.View, 1280, 800)
	tt.SetLocale("en")
	if !tt.HasText("Load images") {
		t.Fatal("remote image action is missing from capture")
	}
	captureFrame(t, tt, "remote-images")
}
