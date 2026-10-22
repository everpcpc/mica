package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"io"
	"mica/internal/mail"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

type imageAttachmentService struct {
	*fakeService
	data         []byte
	messageID    string
	attachmentID string
}

func (s *imageAttachmentService) Attachment(_ context.Context, messageID, attachmentID string) ([]byte, error) {
	s.messageID, s.attachmentID = messageID, attachmentID
	return s.data, nil
}

type imageTransport func(*http.Request) (*http.Response, error)

func (fn imageTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestMessageImagesLoadSources(t *testing.T) {
	data := testPNG(t)
	service := &imageAttachmentService{fakeService: newFakeService(), data: data}
	var remoteRequest string
	client := &http.Client{Transport: imageTransport(func(request *http.Request) (*http.Response, error) {
		remoteRequest = request.URL.String()
		if request.Header.Get("Authorization") != "" {
			t.Error("remote image request contains authorization")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(data)),
		}, nil
	})}
	loader := &messageImages{service: service, client: client}
	attachmentURL := messageImageURL(mail.Message{
		ID:          "message-1",
		Attachments: []mail.Attachment{{ID: "attachment-1", ContentID: "logo"}},
	}, "cid:logo")
	for _, tc := range []struct {
		name   string
		source string
	}{
		{name: "remote", source: "https://images.example/pixel.png"},
		{name: "data", source: "mica-data:image/png;base64," + base64.StdEncoding.EncodeToString(data)},
		{name: "attachment", source: attachmentURL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, err := loader.load(tc.source)
			if err != nil {
				t.Fatal(err)
			}
			bitmap, ok := source.(interface{ Size() (int, int) })
			if !ok {
				t.Fatalf("loaded source has type %T, want a bitmap", source)
			}
			if width, height := bitmap.Size(); width != 2 || height != 3 {
				t.Fatalf("bitmap size = %dx%d, want 2x3", width, height)
			}
		})
	}
	if remoteRequest != "https://images.example/pixel.png" {
		t.Errorf("remote request = %q", remoteRequest)
	}
	if service.messageID != "message-1" || service.attachmentID != "attachment-1" {
		t.Errorf("attachment request = %q/%q", service.messageID, service.attachmentID)
	}
}

func TestPublicIP(t *testing.T) {
	for _, tc := range []struct {
		address string
		want    bool
	}{
		{address: "8.8.8.8", want: true},
		{address: "2606:4700:4700::1111", want: true},
		{address: "127.0.0.1"},
		{address: "10.0.0.1"},
		{address: "172.16.0.1"},
		{address: "192.168.1.1"},
		{address: "100.64.0.1"},
		{address: "169.254.0.1"},
		{address: "::1"},
		{address: "fd00::1"},
		{address: "fe80::1"},
		{address: "::ffff:127.0.0.1"},
	} {
		t.Run(tc.address, func(t *testing.T) {
			if got := publicIP(netip.MustParseAddr(tc.address)); got != tc.want {
				t.Fatalf("publicIP(%s) = %v, want %v", tc.address, got, tc.want)
			}
		})
	}
}

func TestRemoteImageRejectsLoopback(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()
	loader := &messageImages{client: &http.Client{
		Timeout:   time.Second,
		Transport: &http.Transport{DialContext: dialPublic},
	}}
	if _, err := loader.load(server.URL); !errors.Is(err, errImageUnavailable) {
		t.Fatalf("loopback image error = %v, want errImageUnavailable", err)
	}
	if requests != 0 {
		t.Fatalf("loopback server received %d requests", requests)
	}
}

func TestDecodeImageRejectsOversizeData(t *testing.T) {
	if _, err := decodeImage(make([]byte, maxImageBytes+1)); !errors.Is(err, errImageUnavailable) {
		t.Fatalf("oversize image error = %v, want errImageUnavailable", err)
	}
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 2, 3))
	var data bytes.Buffer
	if err := png.Encode(&data, picture); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
