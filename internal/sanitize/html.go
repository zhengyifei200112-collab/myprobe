package sanitize

import (
	"bytes"
	"errors"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const MaxHTMLBytes = 16 << 10

var allowedElements = map[string]bool{
	"a": true, "b": true, "br": true, "code": true, "em": true, "i": true,
	"p": true, "small": true, "span": true, "strong": true,
}

func HTML(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > MaxHTMLBytes {
		return "", errors.New("custom HTML is too large")
	}
	context := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(value), context)
	if err != nil {
		return "", errors.New("custom HTML is invalid")
	}
	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	for _, node := range nodes {
		appendClean(root, node)
	}
	var output bytes.Buffer
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if err := html.Render(&output, child); err != nil {
			return "", err
		}
	}
	if output.Len() > MaxHTMLBytes {
		return "", errors.New("sanitized custom HTML is too large")
	}
	return output.String(), nil
}

func appendClean(parent, source *html.Node) {
	switch source.Type {
	case html.TextNode:
		parent.AppendChild(&html.Node{Type: html.TextNode, Data: source.Data})
	case html.ElementNode:
		name := strings.ToLower(source.Data)
		if name == "script" || name == "style" || name == "iframe" || name == "object" || name == "svg" || name == "math" {
			return
		}
		if !allowedElements[name] {
			for child := source.FirstChild; child != nil; child = child.NextSibling {
				appendClean(parent, child)
			}
			return
		}
		target := &html.Node{Type: html.ElementNode, Data: name}
		if name == "a" {
			for _, attribute := range source.Attr {
				switch strings.ToLower(attribute.Key) {
				case "href":
					if safeLink(attribute.Val) {
						target.Attr = append(target.Attr, html.Attribute{Key: "href", Val: strings.TrimSpace(attribute.Val)})
					}
				case "title":
					if len(attribute.Val) <= 200 {
						target.Attr = append(target.Attr, html.Attribute{Key: "title", Val: attribute.Val})
					}
				}
			}
			target.Attr = append(target.Attr, html.Attribute{Key: "rel", Val: "noopener noreferrer"})
		}
		parent.AppendChild(target)
		for child := source.FirstChild; child != nil; child = child.NextSibling {
			appendClean(target, child)
		}
	}
}

func safeLink(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 2048 || strings.HasPrefix(value, "//") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "mailto":
		return true
	case "":
		return strings.HasPrefix(value, "/") || strings.HasPrefix(value, "#")
	default:
		return false
	}
}
