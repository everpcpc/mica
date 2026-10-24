package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mica/internal/mail"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	maxImageBytes  = 20 << 20
	maxImagePixels = 1 << 25
	maxScaledSide  = 2048
)

var (
	errImageUnavailable = errors.New("mail: image unavailable")
	carrierGradeNAT     = netip.MustParsePrefix("100.64.0.0/10")
)

type messageImages struct {
	service mail.Service
	client  *http.Client
}

func newMessageImageLoader(service mail.Service) *messageImages {
	return &messageImages{
		service: service,
		client: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				DialContext: dialPublic,
			},
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errImageUnavailable
				}
				return nil
			},
		},
	}
}

func newMessageImages(service mail.Service) func(string) (ui.ImageSource, error) {
	return newMessageImageLoader(service).load
}

func (a *App) resetMessageViews() {
	a.closeEmailViews()
	a.emailHeights = nil
	a.attachmentDownloads = nil
	a.documents = nil
	a.expandedMessages = nil
	if a.emailResources == nil {
		a.emailResources = newEmailResourceStore(a.service)
	}
	a.emailResources.mu.Lock()
	a.emailResources.loader = newMessageImageLoader(a.service)
	a.emailResources.ownerService = a.ownerService
	a.emailResources.ownerClient = func(email string) *accountClient {
		return a.accountClients[normalizedEmail(email)]
	}
	a.emailResources.activeVersion = a.currentCacheVersion
	a.emailResources.mu.Unlock()
	a.syncRemoteImagePolicy()
}

func prepareMessageHTML(message mail.Message) string {
	root, err := xhtml.Parse(strings.NewReader(message.HTML))
	if err != nil {
		return message.HTML
	}
	var visit func(*xhtml.Node)
	visit = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && node.Data == "img" {
			normalizeImageNode(node)
			if node.Data == "img" {
				for i := range node.Attr {
					if node.Attr[i].Key == "src" {
						node.Attr[i].Val = messageImageNodeURL(message, node)
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	var output strings.Builder
	if err := xhtml.Render(&output, root); err != nil {
		return message.HTML
	}
	return output.String()
}

func normalizeImageNode(node *xhtml.Node) {
	if trackingImageNode(node) {
		node.Data, node.DataAtom, node.Attr = "span", atom.Span, nil
		return
	}
	style := imageStyle(node)
	if style["display"] == "none" {
		node.Data, node.DataAtom, node.Attr = "span", atom.Span, nil
		return
	}
	for _, dimension := range []string{"width", "height"} {
		value, ok := style[dimension]
		if !ok {
			continue
		}
		if size, ok := cssPixels(value); ok {
			setImageAttribute(node, dimension, size)
		} else if dimension != "width" || !strings.HasSuffix(strings.TrimSpace(value), "%") {
			removeImageAttribute(node, dimension)
		}
	}
}

func trackingImageNode(node *xhtml.Node) bool {
	style := imageStyle(node)
	width, hasWidth := imageNodeDimension(node, "width", style)
	height, hasHeight := imageNodeDimension(node, "height", style)
	return hasWidth && hasHeight && width <= 1 && height <= 1
}

func imageNodeDimension(node *xhtml.Node, key string, style map[string]string) (float64, bool) {
	value, ok := style[key]
	if !ok {
		value = imageAttribute(node, key)
	}
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSpace(strings.TrimSuffix(value, "!important"))
	value = strings.TrimSpace(strings.TrimSuffix(value, "px"))
	pixels, err := strconv.ParseFloat(value, 64)
	return pixels, err == nil && pixels >= 0
}

func imageStyle(node *xhtml.Node) map[string]string {
	style := map[string]string{}
	for _, declaration := range strings.Split(imageAttribute(node, "style"), ";") {
		name, value, ok := strings.Cut(declaration, ":")
		if ok {
			style[strings.ToLower(strings.TrimSpace(name))] = strings.ToLower(strings.TrimSpace(value))
		}
	}
	return style
}

func messageImageNodeURL(message mail.Message, node *xhtml.Node) string {
	source := messageImageURL(message, imageAttribute(node, "src"))
	width, height := imageAttribute(node, "width"), imageAttribute(node, "height")
	if width == "" && height == "" {
		return source
	}
	query := url.Values{"source": {source}}
	if width != "" {
		query.Set("width", width)
	}
	if height != "" {
		query.Set("height", height)
	}
	return (&url.URL{Scheme: "mica-image", RawQuery: query.Encode()}).String()
}

func messageImageURL(message mail.Message, source string) string {
	source = strings.TrimSpace(source)
	if len(source) >= len("data:") && strings.EqualFold(source[:len("data:")], "data:") {
		return "mica-data:" + source[len("data:"):]
	}
	if len(source) < len("cid:") || !strings.EqualFold(source[:len("cid:")], "cid:") {
		return source
	}
	contentID := strings.Trim(strings.TrimSpace(source[len("cid:"):]), "<>")
	if contentID == "" {
		return source
	}
	for _, attachment := range message.Attachments {
		if !strings.EqualFold(attachment.ContentID, contentID) && attachment.ID != contentID {
			continue
		}
		query := url.Values{"message": {message.ID}, "attachment": {attachment.ID}}
		return (&url.URL{Scheme: "mica-attachment", RawQuery: query.Encode()}).String()
	}
	return source
}

func cssPixels(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSpace(strings.TrimSuffix(value, "!important"))
	if !strings.HasSuffix(value, "px") {
		return "", false
	}
	pixels, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(value, "px")), 32)
	if err != nil || pixels <= 0 || pixels > 1e5 {
		return "", false
	}
	return strconv.FormatFloat(pixels, 'f', -1, 32), true
}

func imageAttribute(node *xhtml.Node, key string) string {
	for _, attribute := range node.Attr {
		if attribute.Key == key {
			return attribute.Val
		}
	}
	return ""
}

func setImageAttribute(node *xhtml.Node, key, value string) {
	for i := range node.Attr {
		if node.Attr[i].Key == key {
			node.Attr[i].Val = value
			return
		}
	}
	node.Attr = append(node.Attr, xhtml.Attribute{Key: key, Val: value})
}

func removeImageAttribute(node *xhtml.Node, key string) {
	for i := range node.Attr {
		if node.Attr[i].Key == key {
			node.Attr = append(node.Attr[:i], node.Attr[i+1:]...)
			return
		}
	}
}

func (l *messageImages) load(source string) (ui.ImageSource, error) {
	parsed, err := url.Parse(strings.TrimSpace(source))
	if err != nil {
		return nil, errImageUnavailable
	}
	if strings.EqualFold(parsed.Scheme, "mica-image") {
		data, err := l.loadData(parsed.Query().Get("source"))
		if err != nil {
			return nil, err
		}
		return decodeSizedImage(data, parsed.Query().Get("width"), parsed.Query().Get("height"))
	}
	data, err := l.loadData(source)
	if err != nil {
		return nil, err
	}
	return decodeImage(data)
}

func (l *messageImages) loadData(source string) ([]byte, error) {
	parsed, err := url.Parse(strings.TrimSpace(source))
	if err != nil {
		return nil, errImageUnavailable
	}
	switch strings.ToLower(parsed.Scheme) {
	case "data", "mica-data":
		_, payload, ok := strings.Cut(source, ":")
		if !ok {
			return nil, errImageUnavailable
		}
		return decodeDataImage(payload)
	case "mica-attachment":
		fetcher, ok := l.service.(mail.AttachmentFetcher)
		if !ok {
			return nil, errImageUnavailable
		}
		data, err := fetcher.Attachment(context.Background(), parsed.Query().Get("message"), parsed.Query().Get("attachment"))
		if err != nil {
			return nil, errImageUnavailable
		}
		return data, nil
	case "http", "https":
		return l.loadRemote(source)
	default:
		return nil, errImageUnavailable
	}
}

func decodeDataImage(payload string) ([]byte, error) {
	metadata, encoded, ok := strings.Cut(payload, ",")
	if !ok || len(encoded) > maxImageBytes*2 {
		return nil, errImageUnavailable
	}
	encoded, err := url.PathUnescape(encoded)
	if err != nil {
		return nil, errImageUnavailable
	}
	if !strings.HasSuffix(strings.ToLower(metadata), ";base64") {
		return []byte(encoded), nil
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil {
		return nil, errImageUnavailable
	}
	return data, nil
}

func (l *messageImages) loadRemote(source string) ([]byte, error) {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, source, nil)
	if err != nil {
		return nil, errImageUnavailable
	}
	response, err := l.client.Do(request)
	if err != nil {
		return nil, errImageUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, errImageUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxImageBytes+1))
	if err != nil {
		return nil, errImageUnavailable
	}
	return data, nil
}

func decodeImage(data []byte) (ui.ImageSource, error) {
	return decodeSizedImage(data, "", "")
}

func decodeSizedImage(data []byte, width, height string) (ui.ImageSource, error) {
	if len(data) == 0 || len(data) > maxImageBytes {
		return nil, errImageUnavailable
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxImagePixels {
		return nil, errImageUnavailable
	}
	if width == "" && height == "" {
		bitmap, err := ui.DecodeBitmap(data)
		if err != nil {
			return nil, errImageUnavailable
		}
		return bitmap, nil
	}
	picture, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errImageUnavailable
	}
	if format == "jpeg" {
		if orientation := exifOrientation(data); orientation > 1 {
			picture = orientImage(picture, orientation)
		}
	}
	w, h, err := emailImageSize(picture, width, height)
	if err != nil {
		return nil, err
	}
	if w > maxScaledSide || h > maxScaledSide {
		scale := min(float64(maxScaledSide)/w, float64(maxScaledSide)/h)
		w, h = w*scale, h*scale
	}
	targetW, targetH := max(1, int(w+0.5)), max(1, int(h+0.5))
	bounds := picture.Bounds()
	if targetW == bounds.Dx() && targetH == bounds.Dy() {
		return ui.NewBitmap(picture), nil
	}
	scaled := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), picture, bounds, xdraw.Over, nil)
	return ui.NewBitmap(scaled), nil
}

func emailImageSize(picture image.Image, width, height string) (float64, float64, error) {
	var w, h float64
	var err error
	if width != "" {
		w, err = strconv.ParseFloat(width, 64)
		if err != nil || w <= 0 {
			return 0, 0, errImageUnavailable
		}
	}
	if height != "" {
		h, err = strconv.ParseFloat(height, 64)
		if err != nil || h <= 0 {
			return 0, 0, errImageUnavailable
		}
	}
	bounds := picture.Bounds()
	switch {
	case w == 0 && h == 0:
		return 0, 0, errImageUnavailable
	case w == 0:
		w = h * float64(bounds.Dx()) / float64(bounds.Dy())
	case h == 0:
		h = w * float64(bounds.Dy()) / float64(bounds.Dx())
	}
	return w, h, nil
}

func exifOrientation(jpg []byte) int {
	if len(jpg) < 4 || jpg[0] != 0xff || jpg[1] != 0xd8 {
		return 0
	}
	for i := 2; i+4 <= len(jpg); {
		if jpg[i] != 0xff {
			return 0
		}
		marker, size := jpg[i+1], int(binary.BigEndian.Uint16(jpg[i+2:]))
		if marker == 0xda || size < 2 || i+2+size > len(jpg) {
			return 0
		}
		segment := jpg[i+4 : i+2+size]
		if marker == 0xe1 && len(segment) > 14 && string(segment[:6]) == "Exif\x00\x00" {
			return tiffOrientation(segment[6:])
		}
		i += 2 + size
	}
	return 0
}

func tiffOrientation(data []byte) int {
	var order binary.ByteOrder
	switch string(data[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0
	}
	ifd := int(order.Uint32(data[4:]))
	if ifd < 8 || ifd+2 > len(data) {
		return 0
	}
	count := int(order.Uint16(data[ifd:]))
	for entry := ifd + 2; entry+12 <= len(data) && count > 0; entry, count = entry+12, count-1 {
		if order.Uint16(data[entry:]) == 0x0112 && order.Uint16(data[entry+2:]) == 3 {
			if orientation := int(order.Uint16(data[entry+8:])); orientation >= 1 && orientation <= 8 {
				return orientation
			}
			return 0
		}
	}
	return 0
}

func orientImage(picture image.Image, orientation int) image.Image {
	bounds := picture.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	dw, dh := w, h
	if orientation >= 5 {
		dw, dh = h, w
	}
	out := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sx, sy int
			switch orientation {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8:
				sx, sy = w-1-y, x
			default:
				sx, sy = x, y
			}
			out.Set(x, y, picture.At(bounds.Min.X+sx, bounds.Min.Y+sy))
		}
	}
	return out
}

func dialPublic(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errImageUnavailable
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, errImageUnavailable
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	for _, address := range addresses {
		if !publicIP(address) {
			continue
		}
		connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.String(), port))
		if err == nil {
			return connection, nil
		}
	}
	return nil, errImageUnavailable
}

func publicIP(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	return !carrierGradeNAT.Contains(address)
}
