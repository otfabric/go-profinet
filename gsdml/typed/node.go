// SPDX-License-Identifier: MIT

package typed

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"

	profinet "github.com/otfabric/go-profinet"
)

// Limits of Parse and Validate, the same as gsdml's.
const (
	// MaxDocumentSize is the largest document read, in octets.
	MaxDocumentSize = 64 << 20
	// MaxElements is the largest number of elements in a document.
	MaxElements = 1 << 20
	// MaxDepth is the deepest element nesting.
	MaxDepth = 256
)

// Node is an XML element as the schema sees it: its name, attributes
// (without namespace declarations), and content. It holds content that the
// schemas leave open (xsd:any, mixed content, xsd:anyType) verbatim.
type Node struct {
	Name  xml.Name
	Attrs []xml.Attr
	// Children is the content in document order: elements, and text
	// nodes (Name.Local empty) with their character data in Text.
	Children []*Node
	Text     string
	// Line is the line of the start tag, 0 for a node built in memory.
	Line int
	// ns is the in-scope namespace declarations, for QName values.
	ns map[string]string
}

// IsText reports whether the node is character data.
func (n *Node) IsText() bool { return n.Name.Local == "" }

// Elements returns the element children.
func (n *Node) Elements() []*Node {
	out := make([]*Node, 0, len(n.Children))
	for _, c := range n.Children {
		if !c.IsText() {
			out = append(out, c)
		}
	}
	return out
}

// CharData returns the concatenated character data of the node's direct
// text children.
func (n *Node) CharData() string {
	var b strings.Builder
	for _, c := range n.Children {
		if c.IsText() {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// Attr returns the value of an attribute.
func (n *Node) Attr(space, local string) (string, bool) {
	for _, a := range n.Attrs {
		if a.Name.Space == space && a.Name.Local == local {
			return a.Value, true
		}
	}
	return "", false
}

// lookupPrefix resolves a prefix in the node's scope; "" without a
// default namespace is no namespace.
func (n *Node) lookupPrefix(prefix string) (string, bool) {
	if prefix == "xml" {
		return nsXML, true
	}
	ns, ok := n.ns[prefix]
	if !ok && prefix == "" {
		return "", true
	}
	return ns, ok
}

const (
	nsXML   = "http://www.w3.org/XML/1998/namespace"
	nsXSI   = "http://www.w3.org/2001/XMLSchema-instance"
	nsXMLNS = "http://www.w3.org/2000/xmlns/"
)

// ParseNode reads an XML document into a Node tree, with the limits of
// this package. UTF-8 (with or without a byte order mark), ISO-8859-1 and
// windows-1252 are accepted, as gsdml accepts them. A document type
// declaration with an internal subset is refused (its declarations would
// apply, and encoding/xml does not apply them).
func ParseNode(data []byte) (*Node, error) {
	if len(data) > MaxDocumentSize {
		return nil, fmt.Errorf("typed: %w: document larger than %d octets", profinet.ErrResourceExhausted, MaxDocumentSize)
	}
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	d := xml.NewDecoder(bytes.NewReader(data))
	d.CharsetReader = charsetReader
	d.Strict = true
	var stack []*Node
	var raw []xml.Name // the start tags as written, to match end tags
	var root *Node
	elements := 0
	for {
		tok, err := d.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("typed: %w: %v", profinet.ErrMalformed, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			elements++
			if elements > MaxElements {
				return nil, fmt.Errorf("typed: %w: document with more than %d elements", profinet.ErrResourceExhausted, MaxElements)
			}
			if len(stack) >= MaxDepth {
				return nil, fmt.Errorf("typed: %w: elements nested deeper than %d", profinet.ErrResourceExhausted, MaxDepth)
			}
			if len(stack) == 0 && root != nil {
				return nil, fmt.Errorf("typed: %w: more than one root element", profinet.ErrMalformed)
			}
			line, _ := d.InputPos()
			var parentNS map[string]string
			if len(stack) > 0 {
				parentNS = stack[len(stack)-1].ns
			}
			n, err := startNode(t, parentNS, line)
			if err != nil {
				return nil, err
			}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.Children = append(p.Children, n)
			} else {
				root = n
			}
			stack = append(stack, n)
			raw = append(raw, t.Name)
		case xml.EndElement:
			if len(raw) == 0 || raw[len(raw)-1] != t.Name {
				return nil, fmt.Errorf("typed: %w: line %d: unexpected end tag </%s>", profinet.ErrMalformed, lineOf(d), rawName(t.Name))
			}
			stack, raw = stack[:len(stack)-1], raw[:len(raw)-1]
		case xml.CharData:
			if len(stack) == 0 || len(t) == 0 {
				continue
			}
			p := stack[len(stack)-1]
			if k := len(p.Children); k > 0 && p.Children[k-1].IsText() {
				p.Children[k-1].Text += string(t)
			} else {
				p.Children = append(p.Children, &Node{Text: string(t)})
			}
		case xml.Directive:
			if internalSubset(t) {
				return nil, fmt.Errorf("typed: %w: a document type declaration with an internal subset (XML 1.0 section 5.1: its declarations would apply)", profinet.ErrUnsupported)
			}
		}
	}
	if len(stack) > 0 {
		return nil, fmt.Errorf("typed: %w: unexpected end of document in <%s>", profinet.ErrMalformed, rawName(raw[len(raw)-1]))
	}
	if root == nil {
		return nil, fmt.Errorf("typed: %w: no root element", profinet.ErrMalformed)
	}
	return root, nil
}

func lineOf(d *xml.Decoder) int {
	line, _ := d.InputPos()
	return line
}

func rawName(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}
	return n.Space + ":" + n.Local
}

// startNode makes the node of a start tag read without namespace
// translation: it applies the tag's namespace declarations (Namespaces in
// XML 1.0), resolves the element's and the attributes' prefixes, and
// refuses an undeclared prefix, an undeclared default (xmlns:p=""), a
// reserved prefix bound elsewhere, and two attributes with one expanded
// name.
func startNode(t xml.StartElement, parentNS map[string]string, line int) (*Node, error) {
	n := &Node{Line: line, ns: parentNS}
	copied := false
	for _, a := range t.Attr {
		var prefix string
		switch {
		case a.Name.Space == "xmlns":
			prefix = a.Name.Local
			if a.Value == "" {
				return nil, fmt.Errorf("typed: %w: line %d: xmlns:%s=\"\" (a prefix cannot be undeclared in XML 1.0)", profinet.ErrMalformed, line, prefix)
			}
			if prefix == "xmlns" || (prefix == "xml") != (a.Value == nsXML) || a.Value == nsXMLNS {
				return nil, fmt.Errorf("typed: %w: line %d: xmlns:%s=%q binds a reserved prefix or namespace", profinet.ErrMalformed, line, prefix, a.Value)
			}
		case a.Name.Space == "" && a.Name.Local == "xmlns":
			if a.Value == nsXML || a.Value == nsXMLNS {
				return nil, fmt.Errorf("typed: %w: line %d: xmlns=%q binds a reserved namespace", profinet.ErrMalformed, line, a.Value)
			}
		default:
			continue
		}
		if !copied {
			n.ns = make(map[string]string, len(parentNS)+1)
			for k, v := range parentNS {
				n.ns[k] = v
			}
			copied = true
		}
		n.ns[prefix] = a.Value
	}
	if t.Name.Space == "xmlns" {
		return nil, fmt.Errorf("typed: %w: line %d: element <%s> has the reserved prefix xmlns", profinet.ErrMalformed, line, rawName(t.Name))
	}
	space, ok := n.lookupPrefix(t.Name.Space)
	if !ok {
		return nil, fmt.Errorf("typed: %w: line %d: element <%s>: prefix %q is not declared", profinet.ErrMalformed, line, rawName(t.Name), t.Name.Space)
	}
	n.Name = xml.Name{Space: space, Local: t.Name.Local}
	for _, a := range t.Attr {
		if a.Name.Space == "xmlns" || (a.Name.Space == "" && a.Name.Local == "xmlns") {
			continue
		}
		name := xml.Name{Local: a.Name.Local}
		if a.Name.Space != "" {
			ns, ok := n.lookupPrefix(a.Name.Space)
			if !ok || a.Name.Space == "" {
				return nil, fmt.Errorf("typed: %w: line %d: attribute %s: prefix %q is not declared", profinet.ErrMalformed, line, rawName(a.Name), a.Name.Space)
			}
			name.Space = ns
		}
		for _, b := range n.Attrs {
			if b.Name == name {
				return nil, fmt.Errorf("typed: %w: line %d: attribute {%s}%s given twice", profinet.ErrMalformed, line, name.Space, name.Local)
			}
		}
		n.Attrs = append(n.Attrs, xml.Attr{Name: name, Value: a.Value})
	}
	return n, nil
}

func internalSubset(dir xml.Directive) bool {
	rest, ok := bytes.CutPrefix(dir, []byte("DOCTYPE"))
	if !ok {
		return false
	}
	var quote byte
	for _, c := range rest {
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '[':
			return true
		}
	}
	return false
}

func charsetReader(label string, in io.Reader) (io.Reader, error) {
	switch strings.ToLower(label) {
	case "utf-8", "utf8", "us-ascii", "ascii":
		return in, nil
	case "iso-8859-1", "latin1", "iso8859-1", "iso_8859-1", "windows-1252", "cp1252":
		b, err := io.ReadAll(in)
		if err != nil {
			return nil, err
		}
		var out bytes.Buffer
		out.Grow(len(b))
		for _, c := range b {
			out.WriteRune(rune(c))
		}
		return &out, nil
	}
	return nil, fmt.Errorf("typed: %w: encoding %q", profinet.ErrUnsupported, label)
}

// Marshal writes the node tree as an XML document (UTF-8, indented
// element content).
func (n *Node) Marshal() []byte {
	e := newEncoder()
	e.node(n)
	return e.bytes()
}
