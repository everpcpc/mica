package app

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"mica/internal/mail"
)

func fakeAttachmentCacheBegin() func() { return func() {} }

type offlineSearchService struct{ *fakeService }

func (offlineSearchService) ListThreads(context.Context, mail.Query) ([]mail.ThreadSummary, error) {
	return nil, errOffline
}

func TestOfflineSearchFallsBackToCachedBodies(t *testing.T) {
	service := &offlineSearchService{fakeService: newFakeService()}
	a := newTestApp(service)
	store := newFakeMailStore()
	a.SetMailStore(store)
	updated := time.Now()
	if err := store.SaveThreadList(context.Background(), "demo@example.com", mail.Query{LabelID: mail.Inbox, Limit: 25}, []mail.ThreadSummary{{
		ID: "t-offline", Subject: "Summary", Snippet: "summary", Senders: "Alice", Updated: updated, LabelIDs: []string{mail.Inbox},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveThread(context.Background(), "demo@example.com", mail.Thread{ID: "t-offline", Subject: "Summary", LabelIDs: []string{mail.Inbox}, Messages: []mail.Message{{
		ID: "m-offline", From: "Alice", Text: "offline-body-token", Date: updated,
	}}}); err != nil {
		t.Fatal(err)
	}
	a.query = "offline-body-token"
	a.refresh()
	if len(a.threads) != 1 || a.threads[0].ID != "t-offline" || a.err != "Offline: showing cached results" {
		t.Fatalf("offline search did not use cached bodies: threads=%+v err=%q", a.threads, a.err)
	}
}

func TestOfflineAttachmentOpenUsesCache(t *testing.T) {
	service := &fakeAttachmentService{fakeService: newFakeService(), data: []byte("cached")}
	a := newTestApp(service)
	store := newFakeMailStore()
	a.SetMailStore(store)
	var opened int
	a.openPath = func(path string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		opened++
		if string(data) != "cached" {
			t.Fatalf("opened attachment = %q", data)
		}
		return os.Remove(path)
	}
	message := mail.Message{ID: "m1", ThreadID: "t1", AccountEmail: "demo@example.com"}
	attachment := mail.Attachment{ID: "a1", Name: "file.bin"}
	a.downloadAttachment(message, attachment, false, 0)
	service.err = errOffline
	a.downloadAttachment(message, attachment, false, 0)
	if opened != 2 || service.calls != 1 {
		t.Fatalf("offline attachment open: opened=%d fetch calls=%d", opened, service.calls)
	}
}

func TestOfflineEmptyAttachmentUsesCache(t *testing.T) {
	service := &fakeAttachmentService{fakeService: newFakeService(), err: errOffline}
	a := newTestApp(service)
	store := newFakeMailStore()
	a.SetMailStore(store)
	if err := store.SaveAttachment(context.Background(), "demo@example.com", "t1", "m1", "a1", []byte{}); err != nil {
		t.Fatal(err)
	}
	message := mail.Message{ID: "m1", ThreadID: "t1", AccountEmail: "demo@example.com"}
	attachment := mail.Attachment{ID: "a1", Name: "empty.txt"}
	t.Run("open", func(t *testing.T) {
		opened := 0
		a.openPath = func(path string) error {
			data, err := os.ReadFile(path)
			if err == nil && len(data) == 0 {
				opened++
			}
			return os.Remove(path)
		}
		a.downloadAttachment(message, attachment, false, 0)
		if opened != 1 || service.calls != 0 {
			t.Fatalf("empty cached attachment: opened=%d fetch calls=%d", opened, service.calls)
		}
	})
	t.Run("forward", func(t *testing.T) {
		source := messageComposeAttachmentSource{fetcher: service, store: store, begin: fakeAttachmentCacheBegin, message: messageRefForAccount("demo@example.com", "t1", "m1"), attachment: attachment}
		outgoing, err := source.read(context.Background(), 0)
		if err != nil || len(outgoing.Data) != 0 || service.calls != 0 {
			t.Fatalf("empty forward attachment = %+v fetch calls=%d err=%v", outgoing, service.calls, err)
		}
	})
}

func TestOfflineForwardAndInlineImageUseCache(t *testing.T) {
	service := &fakeAttachmentService{fakeService: newFakeService(), data: []byte("cached")}
	a := newTestApp(service)
	store := newFakeMailStore()
	a.SetMailStore(store)
	ref := messageRefForAccount("demo@example.com", "t1", "m1")
	source := messageComposeAttachmentSource{fetcher: service, store: store, begin: fakeAttachmentCacheBegin, message: ref, attachment: mail.Attachment{ID: "doc", Name: "doc.bin"}}
	if _, err := source.read(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	service.err = errOffline
	outgoing, err := source.read(context.Background(), 100)
	if err != nil || string(outgoing.Data) != "cached" {
		t.Fatalf("offline forward attachment = %+v err=%v", outgoing, err)
	}
	image := mail.Message{ID: "m1", ThreadID: "t1", AccountEmail: "demo@example.com", Attachments: []mail.Attachment{{ID: "img", ContentID: "cid1"}}}
	loader := newMessageImageLoader(service, store)
	loader.begin = fakeAttachmentCacheBegin
	sourceURL := messageImageURL(image, "cid:cid1")
	service.err = nil
	if _, err := loader.loadData(sourceURL); err != nil {
		t.Fatal(err)
	}
	service.err = errOffline
	data, err := loader.loadData(sourceURL)
	if err != nil || string(data) != "cached" {
		t.Fatalf("offline inline image = %q err=%v", data, err)
	}
}

func TestOfflineWebViewUsesCachedInlineImage(t *testing.T) {
	data := testPNG(t)
	service := &fakeAttachmentService{fakeService: newFakeService(), data: data}
	a := newTestApp(service)
	a.storeAccount = "a@example.com"
	a.account = mail.Account{Email: a.storeAccount}
	a.SetMailStore(newFakeMailStore())
	message := mail.Message{
		AccountEmail: a.storeAccount, ID: "m1", ThreadID: "t1", HTML: `<img src="cid:logo">`,
		Attachments: []mail.Attachment{{ID: "img", ContentID: "logo"}},
	}
	tokens := ownerResourceTokens(t, prepareEmailHTML(a.emailResources, message))
	if len(tokens) != 1 {
		t.Fatalf("resource tokens = %v", tokens)
	}
	if response := serveOwnerResource(a.emailResources, tokens[0]); response.Code != http.StatusOK {
		t.Fatalf("initial inline image status = %d", response.Code)
	}
	service.err = errOffline
	response := serveOwnerResource(a.emailResources, tokens[0])
	if response.Code != http.StatusOK || string(response.Body.Bytes()) != string(data) || service.calls != 1 {
		t.Fatalf("offline WebView response = %d fetch calls=%d err=%v", response.Code, service.calls, response.Body.Bytes())
	}
}

type offlineCallbackAttachmentService struct {
	*fakeAttachmentService
	afterFetch func()
}

func (s *offlineCallbackAttachmentService) Attachment(ctx context.Context, messageID, attachmentID string) ([]byte, error) {
	data, err := s.fakeAttachmentService.Attachment(ctx, messageID, attachmentID)
	if s.afterFetch != nil {
		s.afterFetch()
	}
	return data, err
}

func TestAttachmentCacheRejectsRemovedOwner(t *testing.T) {
	service := &offlineCallbackAttachmentService{fakeAttachmentService: &fakeAttachmentService{fakeService: newFakeService(), data: []byte("stale")}}
	a := newTestApp(service)
	a.storeAccount = "a@example.com"
	a.account = mail.Account{Email: a.storeAccount}
	a.registerAccountClient(service, mail.Account{Email: "b@example.com"}, nil, true)
	store := newFakeMailStore()
	a.SetMailStore(store)
	a.openPath = func(path string) error { return os.Remove(path) }
	service.afterFetch = func() { a.removeAccountClient("b@example.com") }
	a.downloadAttachment(mail.Message{AccountEmail: "b@example.com", ID: "m1", ThreadID: "t1"}, mail.Attachment{ID: "a1"}, false, 0)
	if len(store.attachments) != 0 {
		t.Fatalf("removed account retained attachments: %v", store.attachments)
	}
}

type blockingAttachmentStore struct {
	*fakeMailStore
	started chan struct{}
	release chan struct{}
}

func (s *blockingAttachmentStore) SaveAttachment(ctx context.Context, email, threadID, messageID, attachmentID string, data []byte) error {
	close(s.started)
	<-s.release
	return s.fakeMailStore.SaveAttachment(ctx, email, threadID, messageID, attachmentID, data)
}

func TestAttachmentCacheWriteJoinsCacheDrain(t *testing.T) {
	service := &fakeAttachmentService{fakeService: newFakeService(), data: []byte("cached")}
	a := newTestApp(service)
	store := &blockingAttachmentStore{fakeMailStore: newFakeMailStore(), started: make(chan struct{}), release: make(chan struct{})}
	a.SetMailStore(store)
	a.openPath = func(path string) error { return os.Remove(path) }
	a.schedule = func(fn func()) { go fn() }
	defer func() {
		select {
		case <-store.release:
		default:
			close(store.release)
		}
	}()
	a.downloadAttachment(mail.Message{AccountEmail: "demo@example.com", ID: "m1", ThreadID: "t1"}, mail.Attachment{ID: "a1"}, false, 0)
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("attachment cache write did not start")
	}
	done := make(chan struct{})
	go func() {
		a.WaitForCacheWrites()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("cache drain returned before the attachment was saved")
	case <-time.After(20 * time.Millisecond):
	}
	close(store.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cache drain did not finish after the attachment was saved")
	}
}

type offlineSearchStoreSpy struct {
	*fakeMailStore
	searches int
}

func (s *offlineSearchStoreSpy) SearchThreads(ctx context.Context, email string, query mail.Query) ([]mail.ThreadSummary, error) {
	s.searches++
	return s.fakeMailStore.SearchThreads(ctx, email, query)
}

func TestOfflineSearchRunsBeforePublish(t *testing.T) {
	a := newTestApp(&offlineSearchService{fakeService: newFakeService()})
	store := &offlineSearchStoreSpy{fakeMailStore: newFakeMailStore()}
	var jobs, updates []func()
	a.schedule = func(fn func()) { jobs = append(jobs, fn) }
	a.update = func(fn func()) { updates = append(updates, fn) }
	a.SetMailStore(store)
	a.refreshList()
	if len(jobs) != 1 {
		t.Fatalf("offline jobs = %d, want 1", len(jobs))
	}
	jobs[0]()
	if store.searches != 1 {
		t.Fatalf("cache searches before publishing = %d, want 1", store.searches)
	}
	if len(updates) != 2 {
		t.Fatalf("offline updates = %d, want cache and network results", len(updates))
	}
}

func TestInlineWebViewUsesMessageBytesAndCache(t *testing.T) {
	service := &fakeAttachmentService{fakeService: newFakeService(), err: errOffline}
	a := newTestApp(service)
	a.storeAccount = "a@example.com"
	a.account = mail.Account{Email: a.storeAccount}
	a.SetMailStore(newFakeMailStore())
	message := mail.Message{
		AccountEmail: a.storeAccount, ID: "m1", ThreadID: "t1", HTML: `<img src="cid:logo">`,
		Attachments: []mail.Attachment{{ContentID: "logo", Data: testPNG(t)}},
	}
	tokens := ownerResourceTokens(t, prepareEmailHTML(a.emailResources, message))
	if len(tokens) != 1 {
		t.Fatalf("resource tokens = %v", tokens)
	}
	if response := serveOwnerResource(a.emailResources, tokens[0]); response.Code != http.StatusOK || service.calls != 0 {
		t.Fatalf("embedded inline image = %d fetch calls=%d", response.Code, service.calls)
	}
	message.Attachments[0].Data = nil
	tokens = ownerResourceTokens(t, prepareEmailHTML(a.emailResources, message))
	if response := serveOwnerResource(a.emailResources, tokens[0]); response.Code != http.StatusOK || service.calls != 0 {
		t.Fatalf("cached inline image = %d fetch calls=%d", response.Code, service.calls)
	}
	message.ID = "missing"
	message.Attachments[0].Data = nil
	tokens = ownerResourceTokens(t, prepareEmailHTML(a.emailResources, message))
	if response := serveOwnerResource(a.emailResources, tokens[0]); response.Code != http.StatusNotFound || service.calls != 0 {
		t.Fatalf("ID-less image without bytes = %d fetch calls=%d", response.Code, service.calls)
	}
}

func TestDemoWebViewCacheUsesActiveAccount(t *testing.T) {
	service := &fakeAttachmentService{fakeService: newFakeService(), data: testPNG(t)}
	a := New(service)
	a.schedule = func(fn func()) { fn() }
	a.restoreAttempted = true
	a.notificationsStarted = true
	a.SetMailStore(newFakeMailStore())
	a.Start()
	message := mail.Message{ID: "m1", ThreadID: "t1", HTML: `<img src="cid:logo">`, Attachments: []mail.Attachment{{ID: "img", ContentID: "logo"}}}
	tokens := ownerResourceTokens(t, prepareEmailHTML(a.emailResources, message))
	if len(tokens) != 1 {
		t.Fatalf("resource tokens = %v", tokens)
	}
	if response := serveOwnerResource(a.emailResources, tokens[0]); response.Code != http.StatusOK {
		t.Fatalf("initial Demo image = %d", response.Code)
	}
	service.err = errOffline
	if response := serveOwnerResource(a.emailResources, tokens[0]); response.Code != http.StatusOK || service.calls != 1 {
		t.Fatalf("offline Demo image = %d fetch calls=%d", response.Code, service.calls)
	}
}

func TestForwardBeforeProfileDoesNotUseStore(t *testing.T) {
	service := &fakeAttachmentService{fakeService: newFakeService(), data: []byte("data")}
	a := New(service)
	a.schedule = func(fn func()) { fn() }
	store := newFakeMailStore()
	a.SetMailStore(store)
	a.openForwardMessage(mail.Message{ID: "m1", ThreadID: "t1", Attachments: []mail.Attachment{{ID: "a1", Name: "file.txt"}}})
	if len(store.attachments) != 0 {
		t.Fatalf("forward used the cache before the account was known: %v", store.attachments)
	}
}

type blockingFetchAttachmentService struct {
	*fakeAttachmentService
	started chan struct{}
	release chan struct{}
}

func (s *blockingFetchAttachmentService) Attachment(ctx context.Context, messageID, attachmentID string) ([]byte, error) {
	select {
	case s.started <- struct{}{}:
	default:
	}
	select {
	case <-s.release:
		return s.fakeAttachmentService.Attachment(ctx, messageID, attachmentID)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestAttachmentCacheDrainIncludesFetch(t *testing.T) {
	webViewStart := func(t *testing.T, a *App, message mail.Message, finished chan int) {
		tokens := ownerResourceTokens(t, prepareEmailHTML(a.emailResources, message))
		go func() { finished <- serveOwnerResource(a.emailResources, tokens[0]).Code }()
	}
	for _, tc := range []struct {
		name    string
		web     bool
		startup bool
		start   func(*testing.T, *App, mail.Message, chan int)
	}{
		{name: "download", start: func(_ *testing.T, a *App, message mail.Message, _ chan int) {
			a.downloadAttachment(message, message.Attachments[0], false, 0)
		}},
		{name: "forward", start: func(_ *testing.T, a *App, message mail.Message, _ chan int) {
			a.openForwardMessage(message)
		}},
		{name: "WebView", web: true, start: webViewStart},
		{name: "startup WebView", web: true, startup: true, start: webViewStart},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &blockingFetchAttachmentService{
				fakeAttachmentService: &fakeAttachmentService{fakeService: newFakeService(), data: testPNG(t)},
				started:               make(chan struct{}, 1), release: make(chan struct{}),
			}
			var a *App
			if tc.startup {
				a = New(service)
				a.schedule = func(fn func()) { fn() }
				a.restoreAttempted = true
				a.notificationsStarted = true
			} else {
				a = newTestApp(service)
			}
			store := newFakeMailStore()
			a.SetMailStore(store)
			if tc.startup {
				a.Start()
			}
			a.schedule = func(fn func()) { go fn() }
			a.openPath = func(path string) error { return os.Remove(path) }
			defer func() {
				select {
				case <-service.release:
				default:
					close(service.release)
				}
			}()
			finished := make(chan int, 1)
			message := mail.Message{
				AccountEmail: "demo@example.com", ID: "m1", ThreadID: "t1", HTML: `<img src="cid:logo">`,
				Attachments: []mail.Attachment{{ID: "a1", ContentID: "logo", Name: "image.png"}},
			}
			tc.start(t, a, message, finished)
			select {
			case <-service.started:
			case <-time.After(time.Second):
				t.Fatal("attachment fetch did not start")
			}
			drained := make(chan struct{})
			go func() {
				a.WaitForCacheWrites()
				close(drained)
			}()
			select {
			case <-drained:
				t.Fatal("cache drain returned during the attachment fetch")
			case <-time.After(20 * time.Millisecond):
			}
			close(service.release)
			select {
			case <-drained:
			case <-time.After(time.Second):
				t.Fatal("cache drain did not finish after the attachment fetch")
			}
			if len(store.attachments) != 1 {
				t.Fatalf("cached attachments = %v, want one", store.attachments)
			}
			if tc.web {
				select {
				case code := <-finished:
					if code != http.StatusOK {
						t.Fatalf("WebView status = %d", code)
					}
				case <-time.After(time.Second):
					t.Fatal("WebView request did not finish")
				}
			}
		})
	}
}

func TestAttachmentCacheRejectsWritesAfterQuit(t *testing.T) {
	service := &fakeAttachmentService{fakeService: newFakeService(), data: []byte("data")}
	a := newTestApp(service)
	store := newFakeMailStore()
	a.SetMailStore(store)
	a.openPath = func(path string) error { return os.Remove(path) }
	a.WaitForCacheWrites()
	a.downloadAttachment(mail.Message{AccountEmail: "demo@example.com", ID: "m1", ThreadID: "t1"}, mail.Attachment{ID: "a1"}, false, 0)
	if len(store.attachments) != 0 {
		t.Fatalf("attachment was written after the cache drain: %v", store.attachments)
	}
}
