package app

import (
	"strconv"
	"strings"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/richtext"
	xhtml "golang.org/x/net/html"
)

type emailDocument struct {
	root       *xhtml.Node
	hasContent bool
}

type emailOptionsMap map[*xhtml.Node]*markdown.Options

func (a *App) emailDocument(message mail.Message) *emailDocument {
	if document, ok := a.documents[message.ID]; ok {
		return document
	}
	source := prepareMessageHTML(message)
	root, _ := xhtml.Parse(strings.NewReader(source))
	document := &emailDocument{root: root, hasContent: strings.TrimSpace(emailMarkdown(source)) != ""}
	if a.documents == nil {
		a.documents = make(map[string]*emailDocument)
	}
	a.documents[message.ID] = document
	return document
}

func (a *App) emailView(c *ui.Context, message mail.Message) ui.Element {
	return a.renderEmailNode(c, a.emailDocument(message).root)
}

func (a *App) renderEmailNode(c *ui.Context, node *xhtml.Node) ui.Element {
	if node == nil || emailHidden(node) {
		return ui.Element{}
	}
	if node.Type == xhtml.TextNode {
		return a.emailMarkdownView(c, node.Parent, node.Data)
	}
	if node.Type != xhtml.ElementNode && node.Type != xhtml.DocumentNode {
		return ui.Element{}
	}
	if !hasEmailLayoutChildren(node) {
		return a.emailStyled(c, a.emailMarkdownView(c, node, emailMarkdown(emailOuterHTML(node))), node)
	}
	var element ui.Element
	if emailRow(node) {
		element = ui.Row(c).Gap(16).AlignItems(ui.Start)
	} else {
		element = ui.Column(c).Gap(8)
	}
	element = a.emailStyled(c, element, node)
	element.Children(func() { a.renderEmailChildren(c, node) })
	return element
}

func (a *App) renderEmailChildren(c *ui.Context, node *xhtml.Node) {
	var inline strings.Builder
	flush := func() {
		source := emailMarkdown(inline.String())
		inline.Reset()
		if strings.TrimSpace(source) == "" {
			return
		}
		a.emailMarkdownView(c, node, source)
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if emailHidden(child) {
			continue
		}
		if emailLayoutElement(child) {
			flush()
			a.renderEmailNode(c, child)
		} else {
			inline.WriteString(emailOuterHTML(child))
		}
	}
	flush()
}

func (a *App) emailMarkdownView(c *ui.Context, node *xhtml.Node, source string) ui.Element {
	options := &a.markdown
	if theme := emailMarkdownTheme(node); theme != nil {
		if cached, ok := a.emailOptions[node]; ok {
			options = cached
		} else {
			options = &markdown.Options{LoadImage: a.markdown.LoadImage, Theme: theme}
			if a.emailOptions == nil {
				a.emailOptions = make(emailOptionsMap)
			}
			a.emailOptions[node] = options
		}
	}
	element := markdown.View(c, source, options)
	style := emailTextStyle(node)
	if emailBold(style) {
		element = element.Bold()
	}
	switch style["text-align"] {
	case "center":
		element = element.TextAlign(ui.Center)
	case "right", "end":
		element = element.TextAlign(ui.End)
	case "left", "start":
		element = element.TextAlign(ui.Start)
	}
	return element
}

func emailMarkdownTheme(node *xhtml.Node) *markdown.Theme {
	style := emailTextStyle(node)
	theme := &markdown.Theme{}
	custom := false
	if size := emailPixels(style["font-size"]); size > 0 {
		theme.FontSize = size
		custom = true
	}
	if lineHeight := emailNumber(style["line-height"]); lineHeight > 0 {
		theme.LineHeight = float32(lineHeight)
		custom = true
	}
	if color := emailColor(style["color"]); color.A > 0 {
		theme.Text = color
		theme.Strong = color
		custom = true
	}
	if color := emailLinkColor(node); color.A > 0 {
		theme.Link = color
		custom = true
	}
	if !custom {
		return nil
	}
	return theme
}

func emailTextStyle(node *xhtml.Node) map[string]string {
	style := emailStyle(node)
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		parentStyle := emailStyle(parent)
		for _, key := range []string{"color", "font-size", "font-weight", "text-align", "line-height"} {
			if style[key] == "" {
				style[key] = parentStyle[key]
			}
		}
	}
	return style
}

func emailLinkColor(node *xhtml.Node) ui.Color {
	var find func(*xhtml.Node) ui.Color
	find = func(current *xhtml.Node) ui.Color {
		if current.Data == "a" {
			if color := emailColor(emailStyle(current)["color"]); color.A > 0 {
				return color
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			if color := find(child); color.A > 0 {
				return color
			}
		}
		return ui.Color{}
	}
	if color := find(node); color.A > 0 {
		return color
	}
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if parent.Data == "a" {
			if color := emailColor(emailStyle(parent)["color"]); color.A > 0 {
				return color
			}
		}
	}
	return ui.Color{}
}

func emailBold(style map[string]string) bool {
	weight := style["font-weight"]
	return weight == "bold" || weight == "bolder" || emailNumber(weight) >= 600
}

func emailMarkdown(source string) string {
	return richtext.ParseHTML(source).Markdown()
}

func emailLayoutElement(node *xhtml.Node) bool {
	if node.Type != xhtml.ElementNode {
		return false
	}
	switch node.Data {
	case "html", "body", "table", "tbody", "thead", "tfoot", "tr", "td", "th", "div", "section", "article", "main", "header", "footer", "figure", "figcaption", "p", "blockquote", "pre", "h1", "h2", "h3", "h4", "h5", "h6", "img":
		return true
	}
	return false
}

func hasEmailLayoutChildren(node *xhtml.Node) bool {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if emailLayoutElement(child) {
			return true
		}
	}
	return false
}

func emailRow(node *xhtml.Node) bool {
	if node.Data == "tr" {
		return true
	}
	style := emailStyle(node)
	if style["display"] == "flex" && style["flex-direction"] != "column" {
		return true
	}
	columns := 0
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Data == "img" && emailStyle(child)["display"] != "block" {
			switch node.Data {
			case "div", "p", "span", "td", "th", "a":
				return true
			}
		}
		if child.Type == xhtml.ElementNode && emailStyle(child)["display"] == "inline-block" && strings.Contains(emailStyle(child)["class"], "mj-column-per-") {
			columns++
		}
	}
	return columns > 1
}

func emailOuterHTML(node *xhtml.Node) string {
	if emailHidden(node) {
		return ""
	}
	clone := &xhtml.Node{Type: node.Type, DataAtom: node.DataAtom, Data: node.Data, Namespace: node.Namespace, Attr: append([]xhtml.Attribute(nil), node.Attr...)}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if emailHidden(child) {
			continue
		}
		clone.AppendChild(cloneEmailNode(child))
	}
	var output strings.Builder
	if err := xhtml.Render(&output, clone); err != nil {
		return ""
	}
	return output.String()
}

func cloneEmailNode(node *xhtml.Node) *xhtml.Node {
	clone := &xhtml.Node{Type: node.Type, DataAtom: node.DataAtom, Data: node.Data, Namespace: node.Namespace, Attr: append([]xhtml.Attribute(nil), node.Attr...)}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if emailHidden(child) {
			continue
		}
		clone.AppendChild(cloneEmailNode(child))
	}
	return clone
}

func emailHidden(node *xhtml.Node) bool {
	if node.Type != xhtml.ElementNode {
		return false
	}
	style := emailStyle(node)
	return style["display"] == "none" || style["visibility"] == "hidden"
}

func (a *App) emailStyled(c *ui.Context, element ui.Element, node *xhtml.Node) ui.Element {
	style := emailStyle(node)
	element = element.MaxWidthPercent(100)
	if emailCentered(node, style) {
		element = element.AlignSelf(ui.Center)
	}
	if color := emailColor(firstNonempty(style["background-color"], style["background"], style["bgcolor"])); color.A > 0 {
		element = element.Background(color)
	}
	if color := emailColor(style["color"]); color.A > 0 {
		element = element.TextColor(color)
	}
	switch firstNonempty(style["text-align"], style["align"]) {
	case "center":
		element = element.TextAlign(ui.Center)
	case "right", "end":
		element = element.TextAlign(ui.End)
	case "left", "start":
		element = element.TextAlign(ui.Start)
	}
	if size := emailPixels(style["font-size"]); size > 0 {
		element = element.FontSize(size)
	}
	if weight := style["font-weight"]; weight == "bold" || weight == "bolder" || emailNumber(weight) >= 600 {
		element = element.Bold()
	}
	if box, ok := emailBox(style["padding"]); ok {
		element = element.Padding(box[0], box[1], box[2], box[3])
	}
	if box, ok := emailBox(style["margin"]); ok {
		element = element.Margin(box[0], box[1], box[2], box[3])
	}
	if radius := emailPixels(style["border-radius"]); radius > 0 {
		element = element.Radius(radius)
	}
	if border := emailColor(style["border"]); border.A > 0 {
		element = element.Border(1, border)
	}
	switch {
	case strings.Contains(style["class"], "mj-column-per-40"):
		element = element.Grow(40).MinWidth(0)
	case strings.Contains(style["class"], "mj-column-per-60"):
		element = element.Grow(60).MinWidth(0)
	}
	if width := emailPixels(style["width"]); width > 0 {
		if node.Data == "td" || node.Data == "th" {
			element = element.Width(width)
		} else {
			element = element.MaxWidth(width)
		}
	} else if node.Data == "td" || node.Data == "th" {
		element = element.Grow(1).MinWidth(0)
	}
	if width := emailPixels(style["max-width"]); width > 0 {
		element = element.MaxWidth(width)
	}
	switch style["justify-content"] {
	case "center":
		element = element.Justify(ui.Center)
	case "flex-end", "end", "right":
		element = element.Justify(ui.End)
	}
	switch firstNonempty(style["align-items"], style["valign"]) {
	case "center", "middle":
		element = element.AlignItems(ui.Center)
	case "flex-end", "end", "bottom":
		element = element.AlignItems(ui.End)
	case "flex-start", "start", "top":
		element = element.AlignItems(ui.Start)
	}
	return element
}

func emailCentered(node *xhtml.Node, style map[string]string) bool {
	if node.Data == "table" && style["align"] == "center" {
		return true
	}
	if style["max-width"] == "" && emailPixels(style["width"]) == 0 {
		return false
	}
	margin := strings.Fields(style["margin"])
	return len(margin) > 1 && margin[1] == "auto" || style["margin-left"] == "auto" && style["margin-right"] == "auto"
}

func emailStyle(node *xhtml.Node) map[string]string {
	style := map[string]string{}
	for _, attribute := range node.Attr {
		switch attribute.Key {
		case "align", "valign", "bgcolor", "width", "class":
			style[attribute.Key] = strings.ToLower(strings.TrimSpace(attribute.Val))
		case "style":
			for _, declaration := range strings.Split(attribute.Val, ";") {
				name, value, ok := strings.Cut(declaration, ":")
				if ok {
					style[strings.ToLower(strings.TrimSpace(name))] = strings.ToLower(strings.TrimSpace(value))
				}
			}
		}
	}
	return style
}

func emailBox(value string) ([4]float32, bool) {
	var values [4]float32
	if value == "" {
		return values, false
	}
	parts := strings.Fields(value)
	if len(parts) == 0 || len(parts) > 4 {
		return values, false
	}
	parsed := make([]float32, len(parts))
	for i, part := range parts {
		parsed[i] = emailPixels(part)
	}
	switch len(parsed) {
	case 1:
		values = [4]float32{parsed[0], parsed[0], parsed[0], parsed[0]}
	case 2:
		values = [4]float32{parsed[0], parsed[1], parsed[0], parsed[1]}
	case 3:
		values = [4]float32{parsed[0], parsed[1], parsed[2], parsed[1]}
	case 4:
		values = [4]float32{parsed[0], parsed[1], parsed[2], parsed[3]}
	}
	return values, true
}

func emailPixels(value string) float32 {
	value = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "px"))
	if value == "" {
		return 0
	}
	pixels, err := strconv.ParseFloat(value, 32)
	if err != nil || pixels <= 0 {
		return 0
	}
	return float32(pixels)
}

func emailNumber(value string) float64 {
	value = strings.TrimSpace(value)
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return number
}

func emailColor(value string) ui.Color {
	value = strings.TrimSpace(value)
	if value == "" {
		return ui.Color{}
	}
	if fields := strings.Fields(value); len(fields) > 0 {
		value = fields[0]
	}
	if strings.HasPrefix(value, "#") {
		hex := value[1:]
		if len(hex) == 3 {
			hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
		}
		if len(hex) == 6 {
			return ui.Hex("#" + hex)
		}
		return ui.Color{}
	}
	if strings.HasPrefix(value, "rgb(") || strings.HasPrefix(value, "rgba(") {
		start, end := strings.Index(value, "("), strings.LastIndex(value, ")")
		if start < 0 || end <= start {
			return ui.Color{}
		}
		parts := strings.Split(value[start+1:end], ",")
		if len(parts) < 3 {
			return ui.Color{}
		}
		channels := make([]uint8, 3)
		for i := range channels {
			number, err := strconv.Atoi(strings.TrimSpace(parts[i]))
			if err != nil || number < 0 || number > 255 {
				return ui.Color{}
			}
			channels[i] = uint8(number)
		}
		alpha := 1.0
		if len(parts) > 3 {
			var err error
			alpha, err = strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
			if err != nil {
				return ui.Color{}
			}
		}
		return ui.RGBA(channels[0], channels[1], channels[2], float32(alpha))
	}
	return ui.Color{}
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
