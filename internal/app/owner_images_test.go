package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mica/internal/mail"
)

type ownerCallbackAttachmentService struct {
	*imageAttachmentService
	afterFetch func()
}

func (s *ownerCallbackAttachmentService) Attachment(ctx context.Context, messageID, attachmentID string) ([]byte, error) {
	data, err := s.imageAttachmentService.Attachment(ctx, messageID, attachmentID)
	if s.afterFetch != nil {
		s.afterFetch()
	}
	return data, err
}

func newOwnerResourceApp(active mail.Service) *App {
	a := New(active)
	a.storeAccount = "a@example.com"
	a.account = mail.Account{Email: "a@example.com"}
	return a
}

func ownerTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func ownerResourceTokens(t *testing.T, body string) []string {
	t.Helper()
	const prefix = "mica-img://"
	var tokens []string
	for remaining := body; ; {
		start := strings.Index(remaining, prefix)
		if start < 0 {
			return tokens
		}
		remaining = remaining[start+len(prefix):]
		if len(remaining) < 32 {
			t.Fatalf("truncated resource token in %q", body)
		}
		token := remaining[:32]
		if _, err := hex.DecodeString(token); err != nil {
			t.Fatalf("invalid resource token %q: %v", token, err)
		}
		tokens = append(tokens, token)
		remaining = remaining[32:]
	}
}

func serveOwnerResource(resources *emailResourceStore, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "mica-img://"+token, nil)
	response := httptest.NewRecorder()
	resources.ServeHTTP(response, request)
	return response
}

func TestEmailResourcesUseMessageOwner(t *testing.T) {
	dataA, dataB := ownerTestPNG(t, 1, 1), ownerTestPNG(t, 2, 3)
	remoteData := ownerTestPNG(t, 4, 1)
	serviceA := &imageAttachmentService{fakeService: newFakeService(), data: dataA}
	serviceB := &imageAttachmentService{fakeService: newFakeService(), data: dataB}
	a := newOwnerResourceApp(serviceA)
	a.registerAccountClient(serviceB, mail.Account{Email: "b@example.com"}, nil, true)
	a.setRemoteImagesAutomatic(true)
	var remoteRequests []string
	a.emailResources.loader.client = &http.Client{Transport: imageTransport(func(request *http.Request) (*http.Response, error) {
		remoteRequests = append(remoteRequests, request.URL.String())
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(remoteData)),
		}, nil
	})}
	message := func(owner string) mail.Message {
		return mail.Message{
			AccountEmail: owner,
			ID:           "shared-message",
			ThreadID:     "shared-thread",
			HTML:         `<img src="cid:logo"><img src="https://images.example/shared.png">`,
			Attachments:  []mail.Attachment{{ID: "shared-attachment", ContentID: "logo"}},
		}
	}
	bodyA := prepareEmailHTML(a.emailResources, message("a@example.com"))
	bodyB := prepareEmailHTML(a.emailResources, message("b@example.com"))
	tokensA, tokensB := ownerResourceTokens(t, bodyA), ownerResourceTokens(t, bodyB)
	if len(tokensA) != 2 || len(tokensB) != 2 {
		t.Fatalf("resource tokens: A=%v B=%v", tokensA, tokensB)
	}
	unique := make(map[string]bool)
	for _, token := range append(tokensA, tokensB...) {
		unique[token] = true
	}
	if len(unique) != 4 {
		t.Fatalf("resource tokens are not independent: A=%v B=%v", tokensA, tokensB)
	}
	for _, tc := range []struct {
		name  string
		token string
		want  []byte
	}{
		{name: "A CID", token: tokensA[0], want: dataA},
		{name: "B CID", token: tokensB[0], want: dataB},
		{name: "A remote", token: tokensA[1], want: remoteData},
		{name: "B remote", token: tokensB[1], want: remoteData},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := serveOwnerResource(a.emailResources, tc.token)
			if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), tc.want) {
				t.Fatalf("resource response = %d, bytes match=%v", response.Code, bytes.Equal(response.Body.Bytes(), tc.want))
			}
		})
	}
	if serviceA.messageID != "shared-message" || serviceB.messageID != "shared-message" {
		t.Fatalf("CID loads did not reach both owners: A=%q B=%q", serviceA.messageID, serviceB.messageID)
	}
	if len(remoteRequests) != 2 {
		t.Fatalf("remote image requests = %v", remoteRequests)
	}
}

func TestEmailResourceWithoutOwnerKeepsActiveService(t *testing.T) {
	dataA, dataB := ownerTestPNG(t, 1, 1), ownerTestPNG(t, 2, 3)
	serviceA := &imageAttachmentService{fakeService: newFakeService(), data: dataA}
	a := New(serviceA)
	message := mail.Message{
		ID:          "shared-message",
		ThreadID:    "shared-thread",
		HTML:        `<img src="cid:logo">`,
		Attachments: []mail.Attachment{{ID: "shared-attachment", ContentID: "logo"}},
	}
	tokens := ownerResourceTokens(t, prepareEmailHTML(a.emailResources, message))
	if len(tokens) != 1 {
		t.Fatalf("resource tokens = %v", tokens)
	}
	serviceB := &imageAttachmentService{fakeService: newFakeService(), data: dataB}
	a.service = serviceB
	a.resetMessageViews()
	response := serveOwnerResource(a.emailResources, tokens[0])
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), dataB) {
		t.Fatalf("legacy resource did not follow the active service: %d", response.Code)
	}
	if serviceA.messageID != "" || serviceB.messageID != message.ID {
		t.Fatalf("legacy CID services: A=%q B=%q", serviceA.messageID, serviceB.messageID)
	}
}

func TestEmailResourceRejectsStaleOwner(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove bool
		readd  bool
	}{
		{name: "replacement", readd: true},
		{name: "removal", remove: true},
		{name: "removal and readd", remove: true, readd: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldData, replacementData := ownerTestPNG(t, 1, 1), ownerTestPNG(t, 2, 3)
			active := &imageAttachmentService{fakeService: newFakeService(), data: ownerTestPNG(t, 4, 1)}
			old := &ownerCallbackAttachmentService{imageAttachmentService: &imageAttachmentService{fakeService: newFakeService(), data: oldData}}
			replacement := &imageAttachmentService{fakeService: newFakeService(), data: replacementData}
			a := newOwnerResourceApp(active)
			a.registerAccountClient(old, mail.Account{Email: "b@example.com"}, nil, true)
			old.afterFetch = func() {
				if tc.remove {
					a.removeAccountClient("b@example.com")
				}
				if tc.readd {
					a.registerAccountClient(replacement, mail.Account{Email: "b@example.com"}, nil, true)
				}
			}
			message := mail.Message{
				AccountEmail: "b@example.com",
				ID:           "shared-message",
				ThreadID:     "shared-thread",
				Attachments:  []mail.Attachment{{ID: "shared-attachment", ContentID: "logo"}},
			}
			token := a.emailResources.register(message, "cid:logo")
			response := serveOwnerResource(a.emailResources, token)
			if response.Code != http.StatusNotFound {
				t.Fatalf("stale resource status = %d, want 404", response.Code)
			}
			if old.messageID != message.ID || active.messageID != "" {
				t.Fatalf("stale CID services: old=%q active=%q", old.messageID, active.messageID)
			}
			if tc.readd {
				fresh := serveOwnerResource(a.emailResources, a.emailResources.register(message, "cid:logo"))
				if fresh.Code != http.StatusOK || !bytes.Equal(fresh.Body.Bytes(), replacementData) {
					t.Fatalf("replacement resource status = %d, bytes match=%v", fresh.Code, bytes.Equal(fresh.Body.Bytes(), replacementData))
				}
			}
		})
	}
}
