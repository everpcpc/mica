package app

import (
	"strings"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui/richtext"
	xhtml "golang.org/x/net/html"
)

type emailDocument struct {
	preview    string
	hasContent bool
}

func (a *App) emailDocument(message mail.Message) *emailDocument {
	if document, ok := a.documents[message.ID]; ok {
		return document
	}
	source := prepareMessageHTML(message)
	root, _ := xhtml.Parse(strings.NewReader(source))
	preview := emailMarkdown(emailOuterHTML(root))
	document := &emailDocument{preview: preview, hasContent: strings.TrimSpace(preview) != ""}
	if a.documents == nil {
		a.documents = make(map[string]*emailDocument)
	}
	a.documents[message.ID] = document
	return document
}

func emailMarkdown(source string) string {
	return richtext.ParseHTML(source).Markdown()
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
