package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mica/internal/mail"
)

func TestEmailDocumentPreservesLayoutWithoutScripts(t *testing.T) {
	resources := newEmailResourceStore(newFakeService())
	body := prepareEmailHTML(resources, mail.Message{HTML: `<style>.card{margin:0 auto}</style><table class="card"><tr><td style="padding:12px">Hello</td></tr></table><script>alert(1)</script><span style="display:none">hidden-metadata</span>`})
	for _, want := range []string{"<table", "padding: 12px", "#godwit-body .card"} {
		if !strings.Contains(body, want) {
			t.Fatalf("sanitized body is missing %q: %q", want, body)
		}
	}
	for _, unwanted := range []string{"<script", "hidden-metadata"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("sanitized body contains %q: %q", unwanted, body)
		}
	}
	document := emailDocumentHTML(body, false)
	if !strings.Contains(document, "default-src 'none'") || !strings.Contains(document, `id="godwit-body"`) {
		t.Fatalf("document has no isolated body or CSP: %q", document)
	}
}

func TestEmailHTMLProxiesImagesAndRestoresSafeLinks(t *testing.T) {
	resources := newEmailResourceStore(newFakeService())
	body := prepareEmailHTML(resources, mail.Message{HTML: `<a href="https://example.com">Example</a><a href="javascript:alert(1)">bad</a><img src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="><div style="background:url('https://images.example/bg.png')">Background</div>`})
	if !strings.Contains(body, "mica-img://") {
		t.Fatalf("images were not proxied: %q", body)
	}
	if !strings.Contains(body, `href="https://example.com"`) || strings.Contains(body, "javascript:") {
		t.Fatalf("link contract is wrong: %q", body)
	}
	if !strings.Contains(body, "background-image") {
		t.Fatalf("background image was lost: %q", body)
	}
}

func TestEmailResourceHandlerServesDataImage(t *testing.T) {
	resources := newEmailResourceStore(newFakeService())
	token := resources.register(mail.Message{}, "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")
	request := httptest.NewRequest(http.MethodGet, "mica-img://"+token, nil)
	response := httptest.NewRecorder()
	resources.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "image/png") {
		t.Fatalf("image response = %d %q", response.Code, response.Header().Get("Content-Type"))
	}
	resources.revoke(token)
	response = httptest.NewRecorder()
	resources.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("revoked resource status = %d, want 404", response.Code)
	}
}

func TestEmailDocumentUsesDarkModeWithoutInvertingAwareMail(t *testing.T) {
	if document := emailDocumentHTML(`<style data-dark="">#godwit-body{color:white}</style><p>Dark</p>`, true); !strings.Contains(document, `class="godwit-dark"`) {
		t.Fatalf("dark-aware mail was inverted: %q", document)
	}
	if document := emailDocumentHTML(`<p>Light</p>`, true); !strings.Contains(document, `class="godwit-dark godwit-invert"`) {
		t.Fatalf("light-only mail was not inverted: %q", document)
	}
}

func TestEmailHTMLKeepsLayoutCSS(t *testing.T) {
	resources := newEmailResourceStore(newFakeService())
	body := prepareEmailHTML(resources, mail.Message{HTML: `<div style="margin:0 auto;max-width:200px"><p style="font-size:22px;font-weight:bold;text-align:center">Heading</p></div><td><div class="mj-column-per-40" style="display:inline-block">Column</div></td>`})
	for _, want := range []string{"max-width: 200px", "font-size: 22px", "text-align: center", "display: inline-block", "mj-column-per-40"} {
		if !strings.Contains(body, want) {
			t.Fatalf("layout CSS %q was lost: %q", want, body)
		}
	}
}

func TestEmailHTMLKeepsInlineImageWithText(t *testing.T) {
	resources := newEmailResourceStore(newFakeService())
	body := prepareEmailHTML(resources, mail.Message{HTML: `<div style="line-height:20px"><img alt="favicon" width="20" height="20" src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="><span>Site</span></div>`})
	if !strings.Contains(body, "mica-img://") || !strings.Contains(body, ">Site</span>") {
		t.Fatalf("inline image or text was lost: %q", body)
	}
}

func TestEmailResourceStoreIsNotReplacedOnAccountSwitch(t *testing.T) {
	a := New(newFakeService())
	a.schedule = func(fn func()) { fn() }
	a.restoreAttempted = true
	resources := a.emailResources
	a.useService(newFakeService())
	if a.emailResources != resources {
		t.Fatal("account switch replaced the protocol's resource store")
	}
}

func TestEmailDocumentUsesSystemFont(t *testing.T) {
	for _, want := range []string{"-apple-system", "14px/normal"} {
		if !strings.Contains(emailFrameCSS, want) {
			t.Fatalf("system font is missing %q", want)
		}
	}
}

func TestEmailWheelScriptForwardsVerticalWheel(t *testing.T) {
	for _, want := range []string{"MicaMail.Wheel", "preventDefault", "deltaY"} {
		if !strings.Contains(emailHeightScript, want) {
			t.Fatalf("wheel forwarding is missing %q", want)
		}
	}
}
