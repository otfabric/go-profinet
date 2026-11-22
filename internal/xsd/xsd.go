// SPDX-License-Identifier: MIT

// Package xsd reads the element and attribute declarations of a set of
// XML Schema files: which attributes each element name has (and whether
// they are required) and which child elements it may contain; the named
// simple types and their enumerations; and, for every element occurrence,
// the parent it occurs in and the types of its attributes. It resolves
// named complex types, extension and restriction bases, attribute groups
// and model groups. It is not a validating schema processor; it serves
// the GSDML generator's comparison of its catalog with a schema and its
// audit of the DataType enumerations (docs/CODEGEN.md).
package xsd

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Attr describes an attribute declaration.
type Attr struct {
	Required bool
	Type     string
	// Default is the value of the declaration's default attribute, ""
	// for none.
	Default string
}

// Element merges the declarations of every element with one name.
type Element struct {
	Name       string
	Attributes map[string]Attr
	Children   map[string]bool
}

// Schema is the merged view of the files.
type Schema struct {
	Elements map[string]*Element
	// SimpleTypes are the named simple types.
	SimpleTypes map[string]*SimpleType
	// Uses are the attributes of every element occurrence with its parent,
	// sorted and without duplicates.
	Uses []Use
	// Files are the documents read, in the order read (LoadRoot) or of
	// their names (LoadDir); Missing the schema locations an include or
	// import names that are not there.
	Files   []File
	Missing []string
}

// SimpleType is a named simple type: a restriction of Base with its
// enumeration values, or a union of member types.
type SimpleType struct {
	Name  string
	Base  string
	Enum  []string
	Union []string
	// Notes are the comments that follow an enumeration value in the same
	// restriction, by value (the GSDML schemas mark obsolete values so).
	Notes map[string]string
}

// Use is one attribute of one element occurrence: Element occurring in
// Parent has Attribute of Type (a local name; "" for an inline type).
type Use struct {
	Parent, Element, Attribute, Type string
}

// File is a schema document read.
type File struct {
	Name            string `json:"name"`
	SHA256          string `json:"sha256"`
	Version         string `json:"version,omitempty"`
	TargetNamespace string `json:"targetNamespace,omitempty"`
}

// maxFiles bounds the documents LoadRoot follows.
const maxFiles = 64

// maxDepth bounds the resolution of restriction bases and unions.
const maxDepth = 32

// Enumeration returns the enumeration values of a named simple type,
// following restriction bases without values of their own and the member
// types of unions (their values in member order, without duplicates).
// ok is false when the type is unknown or has no enumeration.
func (s *Schema) Enumeration(name string) (values []string, ok bool) {
	seen := map[string]bool{}
	walked := map[string]bool{}
	var walk func(string, int)
	walk = func(n string, depth int) {
		t := s.SimpleTypes[local(n)]
		// Each type is walked once: a cycle of unions and restrictions
		// ends, and a diamond is not walked twice.
		if t == nil || depth > maxDepth || walked[t.Name] {
			return
		}
		walked[t.Name] = true
		switch {
		case len(t.Enum) > 0:
			for _, v := range t.Enum {
				if !seen[v] {
					seen[v] = true
					values = append(values, v)
				}
			}
		case len(t.Union) > 0:
			for _, m := range t.Union {
				walk(m, depth+1)
			}
		case t.Base != "":
			walk(t.Base, depth+1)
		}
	}
	walk(name, 0)
	return values, len(values) > 0
}

type node struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Children []node     `xml:",any"`
}

func (n *node) attr(name string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == name && a.Name.Space == "" {
			return a.Value
		}
	}
	return ""
}

const nsXSD = "http://www.w3.org/2001/XMLSchema"

func local(qname string) string {
	if i := strings.LastIndex(qname, ":"); i >= 0 {
		return qname[i+1:]
	}
	return qname
}

type loader struct {
	simple     map[string]*node
	notes      map[string]map[string]string
	types      map[string]*node
	attrGroups map[string]*node
	groups     map[string]*node
	globals    map[string]*node
	out        *Schema
	visiting   map[*node]bool
}

// LoadDir reads every .xsd file of dir.
func LoadDir(dir string) (*Schema, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	paths, err := filepath.Glob(filepath.Join(dir, "*.xsd"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("xsd: no .xsd files in %s", dir)
	}
	sort.Strings(paths)
	var docs []*doc
	for _, p := range paths {
		d, err := readDoc(root, filepath.Base(p))
		if err != nil {
			return nil, fmt.Errorf("xsd: %s: %w", p, err)
		}
		docs = append(docs, d)
	}
	return load(docs, nil)
}

// LoadRoot reads the schema document name in dir and every document its
// include, import and redefine elements name, transitively, by their
// schemaLocation in dir (at most 64; a location outside dir is refused).
// A location that is not there is listed in Missing, not an error.
func LoadRoot(dir, name string) (*Schema, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	var docs []*doc
	var missing []string
	seen := map[string]bool{name: true}
	queue := []string{name}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		d, err := readDoc(root, n)
		if errors.Is(err, fs.ErrNotExist) && len(docs) > 0 {
			missing = append(missing, n)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("xsd: %s: %w", n, err)
		}
		docs = append(docs, d)
		for i := range d.root.Children {
			c := &d.root.Children[i]
			if c.XMLName.Space != nsXSD || (c.XMLName.Local != "include" && c.XMLName.Local != "import" && c.XMLName.Local != "redefine") {
				continue
			}
			loc := c.attr("schemaLocation")
			if loc == "" || seen[loc] {
				continue
			}
			if len(seen) >= maxFiles {
				return nil, fmt.Errorf("xsd: more than %d schema documents from %s", maxFiles, name)
			}
			seen[loc] = true
			queue = append(queue, loc)
		}
	}
	return load(docs, missing)
}

// doc is one schema document: its tree, its file record and the comments
// after its enumeration values.
type doc struct {
	root  *node
	file  File
	notes map[string]map[string]string
}

func readDoc(root *os.Root, name string) (*doc, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, 64<<20))
	if err != nil {
		return nil, err
	}
	n, err := parse(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	notes, err := enumNotes(b)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	return &doc{root: n, notes: notes, file: File{Name: name, SHA256: hex.EncodeToString(sum[:]), Version: n.attr("version"), TargetNamespace: n.attr("targetNamespace")}}, nil
}

// enumNotes returns, per named simple type, the comment that follows an
// enumeration value before the next element of the restriction.
func enumNotes(b []byte) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	d := xml.NewDecoder(bytes.NewReader(b))
	d.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	var typ, last string
	depth, typDepth := 0, 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			last = ""
			if t.Name.Space == nsXSD && t.Name.Local == "simpleType" && depth == 2 {
				typ, typDepth = "", depth
				for _, a := range t.Attr {
					if a.Name.Local == "name" && a.Name.Space == "" {
						typ = a.Value
					}
				}
			}
			if typ != "" && t.Name.Space == nsXSD && t.Name.Local == "enumeration" {
				for _, a := range t.Attr {
					if a.Name.Local == "value" && a.Name.Space == "" {
						last = a.Value
					}
				}
			}
		case xml.EndElement:
			if depth == typDepth {
				typ, typDepth = "", 0
			}
			depth--
		case xml.Comment:
			if typ != "" && last != "" {
				if out[typ] == nil {
					out[typ] = map[string]string{}
				}
				out[typ][last] = strings.TrimSpace(string(t))
				last = ""
			}
		default:
		}
	}
}

// Parse reads one schema document.
func Parse(r io.Reader) (*Schema, error) {
	b, err := io.ReadAll(io.LimitReader(r, 64<<20))
	if err != nil {
		return nil, err
	}
	n, err := parse(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	notes, err := enumNotes(b)
	if err != nil {
		return nil, err
	}
	return load([]*doc{{root: n, notes: notes}}, nil)
}

func parse(r io.Reader) (*node, error) {
	var n node
	d := xml.NewDecoder(r)
	d.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	if err := d.Decode(&n); err != nil {
		return nil, err
	}
	if n.XMLName.Local != "schema" || n.XMLName.Space != nsXSD {
		return nil, fmt.Errorf("root element %s is not an XML Schema", n.XMLName.Local)
	}
	return &n, nil
}

// load merges parsed schema documents.
func load(docs []*doc, missing []string) (*Schema, error) {
	l := &loader{
		simple: map[string]*node{}, notes: map[string]map[string]string{},
		types: map[string]*node{}, attrGroups: map[string]*node{}, groups: map[string]*node{}, globals: map[string]*node{},
		out: &Schema{Elements: map[string]*Element{}, SimpleTypes: map[string]*SimpleType{}, Missing: missing}, visiting: map[*node]bool{},
	}
	for _, d := range docs {
		if d.file.Name != "" {
			l.out.Files = append(l.out.Files, d.file)
		}
		for t, m := range d.notes {
			l.notes[t] = m
		}
		r := d.root
		for i := range r.Children {
			c := &r.Children[i]
			if c.XMLName.Space != nsXSD {
				continue
			}
			name := c.attr("name")
			switch c.XMLName.Local {
			case "simpleType":
				l.simple[name] = c
			case "complexType":
				l.types[name] = c
			case "attributeGroup":
				l.attrGroups[name] = c
			case "group":
				l.groups[name] = c
			case "element":
				l.globals[name] = c
			}
		}
	}
	names := make([]string, 0, len(l.globals))
	for n := range l.globals {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		l.element(l.globals[n])
	}
	for name, n := range l.simple {
		l.out.SimpleTypes[name] = simpleType(name, n, l.notes[name])
	}
	slices.SortFunc(l.out.Uses, func(a, b Use) int {
		return cmp.Or(cmp.Compare(a.Parent, b.Parent), cmp.Compare(a.Element, b.Element), cmp.Compare(a.Attribute, b.Attribute), cmp.Compare(a.Type, b.Type))
	})
	l.out.Uses = slices.Compact(l.out.Uses)
	return l.out, nil
}

func simpleType(name string, n *node, notes map[string]string) *SimpleType {
	t := &SimpleType{Name: name, Notes: notes}
	for i := range n.Children {
		c := &n.Children[i]
		if c.XMLName.Space != nsXSD {
			continue
		}
		switch c.XMLName.Local {
		case "restriction":
			t.Base = local(c.attr("base"))
			for j := range c.Children {
				if e := &c.Children[j]; e.XMLName.Space == nsXSD && e.XMLName.Local == "enumeration" {
					t.Enum = append(t.Enum, e.attr("value"))
				}
			}
		case "union":
			for _, m := range strings.Fields(c.attr("memberTypes")) {
				t.Union = append(t.Union, local(m))
			}
		default:
		}
	}
	return t
}

// declAttrs returns the attributes of one element declaration (a
// reference resolved): those of its type or inline complex type with
// their bases and attribute groups, without its children's.
func (l *loader) declAttrs(n *node) map[string]Attr {
	if ref := n.attr("ref"); ref != "" {
		if g := l.globals[local(ref)]; g != nil {
			n = g
		}
	}
	out := map[string]Attr{}
	seen := map[*node]bool{}
	var walk func(*node)
	walk = func(c *node) {
		if c.XMLName.Space != nsXSD || seen[c] {
			return
		}
		seen[c] = true
		switch c.XMLName.Local {
		case "attribute":
			name := c.attr("name")
			if name == "" {
				name = local(c.attr("ref"))
			}
			if name != "" {
				out[name] = Attr{Required: c.attr("use") == "required", Type: local(c.attr("type")), Default: c.attr("default")}
			}
		case "attributeGroup":
			if g := l.attrGroups[local(c.attr("ref"))]; g != nil {
				for i := range g.Children {
					walk(&g.Children[i])
				}
			}
		case "extension", "restriction":
			if base := l.types[local(c.attr("base"))]; base != nil {
				walk(base)
			}
			for i := range c.Children {
				walk(&c.Children[i])
			}
		case "complexType", "complexContent", "simpleContent":
			for i := range c.Children {
				walk(&c.Children[i])
			}
		default:
		}
	}
	if t := n.attr("type"); t != "" {
		if ct := l.types[local(t)]; ct != nil {
			walk(ct)
		}
	}
	for i := range n.Children {
		if c := &n.Children[i]; c.XMLName.Space == nsXSD && c.XMLName.Local == "complexType" {
			walk(c)
		}
	}
	return out
}

func (l *loader) get(name string) *Element {
	e := l.out.Elements[name]
	if e == nil {
		e = &Element{Name: name, Attributes: map[string]Attr{}, Children: map[string]bool{}}
		l.out.Elements[name] = e
	}
	return e
}

// element records an element declaration (or reference).
func (l *loader) element(n *node) string {
	if ref := n.attr("ref"); ref != "" {
		g := l.globals[local(ref)]
		if g == nil {
			return local(ref)
		}
		return l.element(g)
	}
	name := n.attr("name")
	if name == "" {
		return ""
	}
	if l.visiting[n] {
		return name
	}
	l.visiting[n] = true
	e := l.get(name)
	if t := n.attr("type"); t != "" {
		if ct := l.types[local(t)]; ct != nil {
			l.complexType(ct, e)
		}
	}
	for i := range n.Children {
		c := &n.Children[i]
		if c.XMLName.Space == nsXSD && c.XMLName.Local == "complexType" {
			l.complexType(c, e)
		}
	}
	return name
}

// complexType adds the attributes and children of a complex type to e.
func (l *loader) complexType(n *node, e *Element) {
	for i := range n.Children {
		l.content(&n.Children[i], e)
	}
}

func (l *loader) content(c *node, e *Element) {
	if c.XMLName.Space != nsXSD {
		return
	}
	switch c.XMLName.Local {
	case "attribute":
		name := c.attr("name")
		if name == "" {
			name = local(c.attr("ref"))
		}
		if name != "" {
			e.Attributes[name] = Attr{Required: c.attr("use") == "required", Type: local(c.attr("type")), Default: c.attr("default")}
		}
	case "attributeGroup":
		if g := l.attrGroups[local(c.attr("ref"))]; g != nil {
			for i := range g.Children {
				l.content(&g.Children[i], e)
			}
		}
	case "group":
		if g := l.groups[local(c.attr("ref"))]; g != nil {
			for i := range g.Children {
				l.content(&g.Children[i], e)
			}
		}
	case "sequence", "choice", "all", "complexContent", "simpleContent":
		for i := range c.Children {
			l.content(&c.Children[i], e)
		}
	case "extension", "restriction":
		if base := l.types[local(c.attr("base"))]; base != nil && !l.visiting[base] {
			l.visiting[base] = true
			l.complexType(base, e)
			delete(l.visiting, base)
		}
		for i := range c.Children {
			l.content(&c.Children[i], e)
		}
	case "element":
		if name := l.element(c); name != "" {
			e.Children[name] = true
			for a, info := range l.declAttrs(c) {
				l.out.Uses = append(l.out.Uses, Use{Parent: e.Name, Element: name, Attribute: a, Type: info.Type})
			}
		}
	}
}
