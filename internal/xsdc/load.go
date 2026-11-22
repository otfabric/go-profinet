// SPDX-License-Identifier: MIT

// Package xsdc is the front end of the GSDML schema compiler: it loads a
// set of XML Schema 1.0 documents (a GSDML DeviceProfile and what it
// imports), resolves every component, and builds the effective content
// models, attribute uses and identity constraints that the generator
// (internal/cmd/gsdmlc) turns into Go. It implements the constructs the
// GSDML schemas use and fails on any other: it never approximates a
// construct it does not handle.
//
// It runs at generation time only. No library package imports it, and no
// schema is read at run time.
package xsdc

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// XS is the XML Schema namespace.
const XS = "http://www.w3.org/2001/XMLSchema"

// XMLNS is the namespace of the xml: prefix.
const XMLNS = "http://www.w3.org/XML/1998/namespace"

// QName is a namespace-qualified name.
type QName struct {
	NS, Local string
}

func (q QName) String() string {
	if q.NS == "" {
		return q.Local
	}
	return "{" + q.NS + "}" + q.Local
}

// node is a schema document element with its in-scope namespaces.
type node struct {
	name     QName
	attrs    map[string]string // unqualified attributes by local name
	ns       map[string]string // in-scope prefixes ("" is the default namespace)
	children []*node
	doc      *document
	line     int
}

func (n *node) attr(k string) (string, bool) {
	v, ok := n.attrs[k]
	return v, ok
}

func (n *node) where() string {
	return fmt.Sprintf("%s:%d", n.doc.name, n.line)
}

// qname resolves a QName-valued attribute in n's namespace scope.
func (n *node) qname(v string) (QName, error) {
	v = strings.TrimSpace(v)
	prefix, local, ok := strings.Cut(v, ":")
	if !ok {
		local, prefix = prefix, ""
	}
	ns, found := n.ns[prefix]
	if !found {
		if prefix == "xml" {
			return QName{XMLNS, local}, nil
		}
		if prefix != "" {
			return QName{}, fmt.Errorf("%s: undeclared prefix %q in %q", n.where(), prefix, v)
		}
	}
	return QName{ns, local}, nil
}

// kids returns the XS children of n, skipping annotations.
func (n *node) kids() []*node {
	var out []*node
	for _, c := range n.children {
		if c.name.NS == XS && c.name.Local != "annotation" {
			out = append(out, c)
		}
	}
	return out
}

type document struct {
	name               string
	target             string
	elementQualified   bool
	attributeQualified bool
	root               *node
	version            string
}

// parseDocument reads a schema document. It keeps the prefixes, which
// encoding/xml's namespace translation would lose, by reading raw tokens.
func parseDocument(name string, r io.Reader) (*document, error) {
	d := xml.NewDecoder(r)
	d.Strict = true
	doc := &document{name: name}
	var stack []*node
	for {
		tok, err := d.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			line, _ := d.InputPos()
			parentNS := map[string]string{}
			if len(stack) > 0 {
				parentNS = stack[len(stack)-1].ns
			}
			ns := parentNS
			copied := false
			for _, a := range t.Attr {
				if a.Name.Space != "xmlns" && (a.Name.Space != "" || a.Name.Local != "xmlns") {
					continue
				}
				if !copied {
					ns = make(map[string]string, len(parentNS)+2)
					for k, v := range parentNS {
						ns[k] = v
					}
					copied = true
				}
				if a.Name.Space == "xmlns" {
					ns[a.Name.Local] = a.Value
				} else {
					ns[""] = a.Value
				}
			}
			n := &node{attrs: map[string]string{}, ns: ns, doc: doc, line: line}
			for _, a := range t.Attr {
				if a.Name.Space == "" && a.Name.Local != "xmlns" {
					n.attrs[a.Name.Local] = a.Value
				}
			}
			space := ns[t.Name.Space]
			if t.Name.Space == "xml" {
				space = XMLNS
			}
			n.name = QName{space, t.Name.Local}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.children = append(p.children, n)
			} else {
				doc.root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		}
	}
	if doc.root == nil || doc.root.name != (QName{XS, "schema"}) {
		return nil, fmt.Errorf("%s: not an XML Schema document", name)
	}
	r0 := doc.root
	doc.target = r0.attrs["targetNamespace"]
	doc.elementQualified = r0.attrs["elementFormDefault"] == "qualified"
	doc.attributeQualified = r0.attrs["attributeFormDefault"] == "qualified"
	doc.version = r0.attrs["version"]
	return doc, nil
}

// Load reads the schema document name in dir and every document it
// includes or imports, resolved by schemaLocation inside dir only (an
// os.Root: no path leaves the directory, no URL is fetched), and returns
// the resolved schema set.
func Load(dir, name string) (*Set, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	l := &loader{set: newSet(), seen: map[string]bool{}}
	open := func(n string) (*document, error) {
		f, err := root.Open(n)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		return parseDocument(n, f)
	}
	if err := l.walk(open, name); err != nil {
		return nil, err
	}
	l.set.Root = name
	l.set.Version = l.rootVersion
	if err := l.set.resolve(); err != nil {
		return nil, err
	}
	return l.set, nil
}

type loader struct {
	set         *Set
	seen        map[string]bool
	rootVersion string
}

func (l *loader) walk(open func(string) (*document, error), name string) error {
	key := strings.ToLower(path.Clean(name))
	if l.seen[key] {
		return nil
	}
	l.seen[key] = true
	if strings.Contains(name, "://") || path.IsAbs(name) || strings.HasPrefix(path.Clean(name), "..") {
		return fmt.Errorf("schemaLocation %q: only files next to the root schema are read", name)
	}
	doc, err := open(name)
	if err != nil {
		return err
	}
	l.set.Documents = append(l.set.Documents, doc.name)
	if len(l.seen) == 1 {
		l.rootVersion = doc.version
	}
	for _, c := range doc.root.kids() {
		switch c.name.Local {
		case "import", "include":
			loc, ok := c.attr("schemaLocation")
			if !ok {
				return fmt.Errorf("%s: %s without schemaLocation", c.where(), c.name.Local)
			}
			if c.name.Local == "include" {
				return fmt.Errorf("%s: xsd:include is not used by GSDML and not implemented", c.where())
			}
			if err := l.walk(open, path.Join(path.Dir(name), loc)); err != nil {
				return err
			}
		}
	}
	return l.set.addDocument(doc)
}
