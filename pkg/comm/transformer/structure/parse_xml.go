package structure

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// maxXMLDepth bounds element nesting. The decoder keeps a stack per open
// element, and the text is upstream-controlled.
const maxXMLDepth = 64

// xmlNode is an element being read.
type xmlNode struct {
	name     string
	attrs    map[string]any
	children map[string]any
	text     strings.Builder
}

// value is what the element becomes: its text when it has nothing else, an
// object otherwise.
func (n *xmlNode) value() any {
	text := strings.TrimSpace(n.text.String())
	if len(n.attrs) == 0 && len(n.children) == 0 {
		return text
	}
	out := make(map[string]any, len(n.attrs)+len(n.children)+1)
	for k, v := range n.attrs {
		out[k] = v
	}
	for k, v := range n.children {
		out[k] = v
	}
	if text != "" {
		out["#text"] = text
	}
	return out
}

func (n *xmlNode) add(name string, v any) {
	if n.children == nil {
		n.children = map[string]any{}
	}
	switch prev := n.children[name].(type) {
	case nil:
		n.children[name] = v
	case []any:
		n.children[name] = append(prev, v)
	default:
		n.children[name] = []any{prev, v}
	}
}

// parseXML decodes a document into {"root": value}.
//
// encoding/xml expands only the five predefined entities and fails on any
// other, and a document type declaration -- where entities are defined -- is
// refused outright, so neither internal nor external entity expansion is
// reachable.
func parseXML(text []byte) (any, error) {
	dec := xml.NewDecoder(bytes.NewReader(text))
	var p xmlParser
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			if p.root == nil {
				return nil, errors.New("invalid xml: no root element")
			}
			return p.root, nil
		}
		if err != nil {
			return nil, fmt.Errorf("invalid xml: %w", err)
		}
		if err := p.token(tok); err != nil {
			return nil, fmt.Errorf("invalid xml: %w", err)
		}
	}
}

// xmlParser holds the open elements and, once the document element has
// closed, the result.
type xmlParser struct {
	stack []*xmlNode
	root  map[string]any
}

func (p *xmlParser) token(tok xml.Token) error {
	switch t := tok.(type) {
	case xml.Directive:
		return errors.New("DOCTYPE and other declarations are refused")
	case xml.StartElement:
		if p.root != nil {
			return errors.New("more than one root element")
		}
		if len(p.stack) >= maxXMLDepth {
			return fmt.Errorf("elements nested deeper than %d", maxXMLDepth)
		}
		p.stack = append(p.stack, startNode(t))
	case xml.CharData:
		if len(p.stack) > 0 {
			p.stack[len(p.stack)-1].text.Write(t)
		}
	case xml.EndElement:
		// The decoder has matched it to a start element, so the stack is not
		// empty.
		n := p.stack[len(p.stack)-1]
		p.stack = p.stack[:len(p.stack)-1]
		if len(p.stack) == 0 {
			p.root = map[string]any{n.name: n.value()}
		} else {
			p.stack[len(p.stack)-1].add(n.name, n.value())
		}
	}
	return nil
}

func startNode(t xml.StartElement) *xmlNode {
	n := &xmlNode{name: t.Name.Local}
	if len(t.Attr) > 0 {
		n.attrs = make(map[string]any, len(t.Attr))
		for _, a := range t.Attr {
			n.attrs["@"+a.Name.Local] = a.Value
		}
	}
	return n
}
