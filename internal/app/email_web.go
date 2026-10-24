package app

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"

	"mica/internal/mail"

	godwitmail "github.com/godwit-email/godwit/core/mail"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const emailResourcePlaceholder = "https://resource.mica.invalid/"

type emailResourceBinding struct {
	owner      string
	service    mail.Service
	generation uint64
	loader     *messageImages
	client     *accountClient
}

type emailResource struct {
	message    mail.Message
	messageKey string
	source     string
	cacheOwner string
	binding    emailResourceBinding
}

type emailResourceStore struct {
	loader           *messageImages
	store            attachmentStore
	attachmentWriter func(string, uint64) attachmentCacheWriter
	attachmentBegin  func(string, uint64) attachmentCacheBegin
	defaultOwner     func() string
	ownerService     func(string) (mail.Service, uint64)
	ownerClient      func(string) *accountClient
	activeVersion    func() int
	automaticRemote  bool
	allowedRemote    map[string]bool
	mu               sync.RWMutex
	resources        map[string]emailResource
}

func newEmailResourceStore(service mail.Service, stores ...attachmentStore) *emailResourceStore {
	var store attachmentStore
	if len(stores) > 0 {
		store = stores[0]
	}
	return &emailResourceStore{
		loader: newMessageImageLoader(service, store),
		store:  store, resources: make(map[string]emailResource), allowedRemote: make(map[string]bool),
	}
}

func (s *emailResourceStore) bindingFor(message mail.Message) emailResourceBinding {
	owner := normalizedEmail(message.AccountEmail)
	s.mu.RLock()
	fallback, resolve, ownerClient := s.loader, s.ownerService, s.ownerClient
	store, attachmentWriter, attachmentBegin := s.store, s.attachmentWriter, s.attachmentBegin
	s.mu.RUnlock()
	if owner == "" || resolve == nil {
		return emailResourceBinding{}
	}
	service, generation := resolve(owner)
	loader := newMessageImageLoader(service, store)
	loader.owner = owner
	if attachmentWriter != nil {
		loader.save = attachmentWriter(owner, generation)
	}
	if attachmentBegin != nil {
		loader.begin = attachmentBegin(owner, generation)
	}
	if fallback != nil {
		loader.client = fallback.client
	}
	binding := emailResourceBinding{owner: owner, service: service, generation: generation, loader: loader}
	if ownerClient != nil {
		binding.client = ownerClient(owner)
	}
	return binding
}

func (s *emailResourceStore) register(message mail.Message, source string) string {
	return s.registerWith(s.bindingFor(message), message, source)
}

func (s *emailResourceStore) registerWith(binding emailResourceBinding, message mail.Message, source string) string {
	var random [16]byte
	_, _ = rand.Read(random[:])
	token := hex.EncodeToString(random[:])
	s.mu.RLock()
	defaultOwner := s.defaultOwner
	s.mu.RUnlock()
	cacheOwner := ""
	if normalizedEmail(message.AccountEmail) == "" && defaultOwner != nil {
		cacheOwner = defaultOwner()
	}
	s.mu.Lock()
	s.resources[token] = emailResource{
		message: message, messageKey: readerMessageKey(message), source: source, cacheOwner: cacheOwner, binding: binding,
	}
	s.mu.Unlock()
	return token
}

func (s *emailResourceStore) revoke(tokens ...string) {
	s.mu.Lock()
	for _, token := range tokens {
		delete(s.resources, token)
	}
	s.mu.Unlock()
}

func (s *emailResourceStore) revokeMessage(key string) {
	s.mu.Lock()
	for token, resource := range s.resources {
		if resource.messageKey == key {
			delete(s.resources, token)
		}
	}
	s.mu.Unlock()
}

func (s *emailResourceStore) valid(resource emailResource) bool {
	if resource.binding.owner == "" {
		return true
	}
	if resource.binding.service == nil {
		return false
	}
	if client := resource.binding.client; client != nil {
		return client.generation.Load() == resource.binding.generation
	}
	s.mu.RLock()
	activeVersion := s.activeVersion
	s.mu.RUnlock()
	return activeVersion != nil && uint64(activeVersion()) == resource.binding.generation
}

func (s *emailResourceStore) remoteAllowed(messageKey, source string) bool {
	if !remoteImageSource(source) {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.automaticRemote || s.allowedRemote[messageKey]
}

func (s *emailResourceStore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	resource, ok := s.resources[r.URL.Host]
	loader := resource.binding.loader
	if resource.binding.owner == "" {
		loader = s.loader
	}
	s.mu.RUnlock()
	if !ok || loader == nil || !s.valid(resource) {
		http.NotFound(w, r)
		return
	}
	if !s.remoteAllowed(resource.messageKey, resource.source) {
		http.NotFound(w, r)
		return
	}
	source := messageImageOwner(messageImageURL(resource.message, resource.source), resource.cacheOwner)
	inline := inlineAttachmentData(resource.message, resource.source)
	var data []byte
	var err error
	if inline != nil {
		data, err = loader.loadData(source, inline)
	} else {
		data, err = loader.loadData(source)
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := decodeImage(data); err != nil {
		http.NotFound(w, r)
		return
	}
	switch http.DetectContentType(data) {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp":
	default:
		http.NotFound(w, r)
		return
	}
	if !s.valid(resource) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(data))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

func prepareEmailHTML(resources *emailResourceStore, message mail.Message) string {
	root, err := xhtml.Parse(strings.NewReader(message.HTML))
	if err != nil {
		return ""
	}
	binding := resources.bindingFor(message)
	var hidden []*xhtml.Node
	walkEmailNodes(root, func(node *xhtml.Node) {
		if emailHidden(node) {
			hidden = append(hidden, node)
		}
	})
	for _, node := range hidden {
		node.Parent.RemoveChild(node)
	}
	walkEmailNodes(root, func(node *xhtml.Node) {
		if node.Type != xhtml.ElementNode || node.DataAtom != atom.Img {
			return
		}
		source := imageAttribute(node, "src")
		if source == "" {
			return
		}
		setImageAttribute(node, "src", emailResourcePlaceholder+resources.registerWith(binding, message, source))
	})
	var input strings.Builder
	if err := xhtml.Render(&input, root); err != nil {
		return ""
	}
	safe, _ := godwitmail.Sanitize(input.String(), nil, true)
	safe = strings.ReplaceAll(safe, emailResourcePlaceholder, "mica-img://")
	fragment := parseEmailFragment(safe)
	walkEmailNodes(fragment, func(node *xhtml.Node) {
		if node.Type != xhtml.ElementNode {
			return
		}
		if background := imageAttribute(node, "data-bg"); background != "" {
			removeImageAttribute(node, "data-bg")
			token := resources.registerWith(binding, message, background)
			declaration := `background-image:url("mica-img://` + token + `");--mica-bg:url("mica-img://` + token + `")`
			if style := imageAttribute(node, "style"); style != "" {
				declaration = strings.TrimRight(strings.TrimSpace(style), ";") + ";" + declaration
			}
			setImageAttribute(node, "style", declaration)
		}
		if node.DataAtom == atom.A || node.DataAtom == atom.Area {
			if href := imageAttribute(node, "data-href"); href != "" {
				setImageAttribute(node, "href", href)
				removeImageAttribute(node, "data-href")
			}
		}
	})
	return renderEmailFragment(fragment)
}

func parseEmailFragment(source string) *xhtml.Node {
	context := &xhtml.Node{Type: xhtml.ElementNode, Data: "body", DataAtom: atom.Body}
	root := &xhtml.Node{Type: xhtml.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, _ := xhtml.ParseFragment(strings.NewReader(source), context)
	for _, node := range nodes {
		root.AppendChild(node)
	}
	return root
}

func renderEmailFragment(root *xhtml.Node) string {
	var output strings.Builder
	for node := root.FirstChild; node != nil; node = node.NextSibling {
		_ = xhtml.Render(&output, node)
	}
	return output.String()
}

func walkEmailNodes(node *xhtml.Node, fn func(*xhtml.Node)) {
	fn(node)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walkEmailNodes(child, fn)
	}
}

func emailDocumentHTML(body string) string {
	class := ""
	if !strings.HasPrefix(body, `<style data-dark="">`) {
		class = "godwit-invert"
	}
	return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data: mica-img:"><style>` + emailFrameCSS + `</style></head><body><div id="godwit-body" class="` + class + `">` + body + `</div></body></html>`
}

const emailFrameCSS = `
html,body{margin:0;padding:0;background:transparent}
html{-webkit-text-size-adjust:100%;font:14px/1.5 -apple-system,BlinkMacSystemFont,"Helvetica Neue",sans-serif}
#godwit-body{overflow-x:auto;overflow-y:hidden;overflow-wrap:break-word;word-break:break-word}
:where(#godwit-body){box-sizing:border-box;padding:0 24px}
#godwit-body:has(>[style*="padding"]){padding:0}
#godwit-body.godwit-plain{white-space:pre-wrap}
#godwit-body{color:#0a0a0a}
#godwit-body a[href],#godwit-body area[href]{cursor:pointer;text-decoration:underline;color:#0b57d0}
#godwit-body img{max-width:100%!important}
#godwit-body table{max-width:100%}
#godwit-body td,#godwit-body th{word-break:break-word}
#godwit-body img:not([height]):not([width]){height:auto}
@media (prefers-color-scheme: dark){
#godwit-body{color:#fafafa}
#godwit-body.godwit-invert{color:#0a0a0a}
#godwit-body:not(.godwit-invert) a[href]{color:#8ab4f8}
#godwit-body.godwit-invert,#godwit-body.godwit-invert img{filter:invert(1) hue-rotate(180deg)}
#godwit-body.godwit-invert [style*="--mica-bg"]{position:relative;isolation:isolate;background-image:none!important}
#godwit-body.godwit-invert [style*="--mica-bg"]::before{content:"";position:absolute;inset:0;z-index:-1;pointer-events:none;background-image:var(--mica-bg);background-size:inherit;background-position:inherit;background-repeat:inherit;border-radius:inherit;filter:invert(1) hue-rotate(180deg)}
}
`
