// SPDX-License-Identifier: MIT

package typed

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"math"
	"math/big"
	"strconv"

	profinet "github.com/otfabric/go-profinet"
)

// Integer is an xsd:integer value whose type does not bound it within
// int64 (positiveInteger without maxInclusive, for instance), kept in its
// lexical form so that no valid value is lost.
type Integer string

// Int64 returns the value as an int64, or an error when it does not fit.
func (i Integer) Int64() (int64, error) {
	return strconv.ParseInt(trimPlus(collapse(string(i))), 10, 64)
}

// Big returns the value as a big.Int.
func (i Integer) Big() (*big.Int, bool) {
	return new(big.Int).SetString(trimPlus(collapse(string(i))), 10)
}

// Decimal is an xsd:decimal value in its lexical form.
type Decimal string

// Rat returns the exact value.
func (d Decimal) Rat() (*big.Rat, bool) {
	return new(big.Rat).SetString(trimPlus(collapse(string(d))))
}

// Float64 returns the nearest float64.
func (d Decimal) Float64() (float64, error) {
	return strconv.ParseFloat(trimPlus(collapse(string(d))), 64)
}

// pos records the document order of an element: the field (its index in
// the struct's content fields) and the index within it.
type pos struct {
	f, i uint16
}

// validOrder reports whether an order record still describes the fields:
// every element once, in increasing index per field.
func validOrder(order []pos, lens []int) bool {
	next := make([]int, len(lens))
	for _, p := range order {
		if int(p.f) >= len(lens) || int(p.i) != next[p.f] {
			return false
		}
		next[p.f]++
	}
	for f, n := range lens {
		if next[f] != n {
			return false
		}
	}
	return true
}

// buildError is an error building a typed document from a node tree.
type buildError struct {
	line int
	path string
	msg  string
}

func (e *buildError) Error() string {
	if e.line > 0 {
		return fmt.Sprintf("typed: line %d: %s: %s", e.line, e.path, e.msg)
	}
	return "typed: " + e.path + ": " + e.msg
}

func (e *buildError) Is(target error) bool { return target == profinet.ErrMalformed }

func attrError(n *Node, a xml.Attr, err error) error {
	return &buildError{line: n.Line, path: n.Name.Local + "@" + a.Name.Local, msg: err.Error()}
}

func unknownAttr(n *Node, a xml.Attr) error {
	return &buildError{line: n.Line, path: n.Name.Local, msg: "attribute " + qualified(a.Name.Space, a.Name.Local) + " is not in the schema"}
}

func unknownElement(n, k *Node) error {
	return &buildError{line: k.Line, path: n.Name.Local, msg: "element " + qualified(k.Name.Space, k.Name.Local) + " is not in the schema"}
}

func valueError(n *Node, err error) error {
	return &buildError{line: n.Line, path: n.Name.Local, msg: err.Error()}
}

func parseBoolValue(v string) (bool, error) {
	switch collapse(v) {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	}
	return false, fmt.Errorf("%q is not a boolean", v)
}

func parseUintValue(v string, bits int) (uint64, error) {
	s := trimPlus(collapse(v))
	u, err := strconv.ParseUint(s, 10, bits)
	if err != nil {
		return 0, fmt.Errorf("%q is not an unsigned %d-bit integer", v, bits)
	}
	return u, nil
}

func parseIntValue(v string, bits int) (int64, error) {
	s := trimPlus(collapse(v))
	i, err := strconv.ParseInt(s, 10, bits)
	if err != nil {
		return 0, fmt.Errorf("%q is not a %d-bit integer", v, bits)
	}
	return i, nil
}

func parseFloatValue(v string) (float64, error) { return parseXSDFloat(collapse(v)) }

func formatBool(b bool) string { return strconv.FormatBool(b) }

func formatUint(u uint64) string { return strconv.FormatUint(u, 10) }

func formatInt(i int64) string { return strconv.FormatInt(i, 10) }

func formatFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "INF"
	case math.IsInf(f, -1):
		return "-INF"
	case math.IsNaN(f):
		return "NaN"
	}
	return strconv.FormatFloat(f, 'G', -1, 64)
}

// encoder writes XML: UTF-8, two-space indentation of element content,
// a default namespace declaration where an element's namespace changes,
// and prefixes, declared where first needed, for qualified attributes.
type encoder struct {
	b        bytes.Buffer
	defaults []string // the default namespace per open element
	scopes   []map[string]string
	depth    int
	open     bool // inside a start tag
	inline   []bool
	nextNS   int
	// rootAttrs are written on the root's start tag.
	rootAttrs []rootAttr
}

func newEncoder() *encoder {
	e := &encoder{}
	e.b.WriteString(xml.Header)
	return e
}

func (e *encoder) currentDefault() string {
	if len(e.defaults) == 0 {
		return ""
	}
	return e.defaults[len(e.defaults)-1]
}

func (e *encoder) indent() {
	if len(e.inline) > 0 && e.inline[len(e.inline)-1] {
		return
	}
	if e.b.Len() > len(xml.Header) {
		e.b.WriteByte('\n')
	}
	for i := 0; i < e.depth; i++ {
		e.b.WriteString("  ")
	}
}

// start opens a start tag; inline content (text) is not indented.
func (e *encoder) start(ns, local string, inline bool) {
	e.closeStart()
	e.indent()
	e.b.WriteByte('<')
	e.b.WriteString(local)
	if ns != e.currentDefault() {
		e.b.WriteString(` xmlns="`)
		escapeAttr(&e.b, ns)
		e.b.WriteByte('"')
	}
	e.defaults = append(e.defaults, ns)
	e.scopes = append(e.scopes, nil)
	e.inline = append(e.inline, inline || (len(e.inline) > 0 && e.inline[len(e.inline)-1]))
	e.open = true
	e.depth++
	if e.depth == 1 {
		for _, a := range e.rootAttrs {
			e.attr(a.ns, a.local, a.value)
		}
	}
}

func (e *encoder) prefix(ns string) string {
	if ns == nsXML {
		return "xml"
	}
	for i := len(e.scopes) - 1; i >= 0; i-- {
		if p, ok := e.scopes[i][ns]; ok {
			return p
		}
	}
	p := "xsi"
	if ns != nsXSI {
		e.nextNS++
		p = "ns" + strconv.Itoa(e.nextNS)
	}
	top := len(e.scopes) - 1
	if e.scopes[top] == nil {
		e.scopes[top] = map[string]string{}
	}
	e.scopes[top][ns] = p
	e.b.WriteString(" xmlns:" + p + `="`)
	escapeAttr(&e.b, ns)
	e.b.WriteByte('"')
	return p
}

func (e *encoder) attr(ns, local, value string) {
	if ns != "" {
		p := e.prefix(ns)
		e.b.WriteString(" " + p + ":")
	} else {
		e.b.WriteByte(' ')
	}
	e.b.WriteString(local)
	e.b.WriteString(`="`)
	escapeAttr(&e.b, value)
	e.b.WriteByte('"')
}

func (e *encoder) closeStart() {
	if e.open {
		e.b.WriteByte('>')
		e.open = false
	}
}

func (e *encoder) text(s string) {
	e.closeStart()
	escapeText(&e.b, s)
}

// end closes the element, as an empty-element tag when it has no content.
func (e *encoder) end(local string) {
	e.depth--
	inline := e.inline[len(e.inline)-1]
	e.inline = e.inline[:len(e.inline)-1]
	e.defaults = e.defaults[:len(e.defaults)-1]
	e.scopes = e.scopes[:len(e.scopes)-1]
	if e.open {
		e.b.WriteString("/>")
		e.open = false
		return
	}
	if !inline {
		e.b.WriteByte('\n')
		for i := 0; i < e.depth; i++ {
			e.b.WriteString("  ")
		}
	}
	e.b.WriteString("</" + local + ">")
}

// node writes a node tree verbatim (wildcard, mixed and anyType content).
func (e *encoder) node(n *Node) {
	if n.IsText() {
		e.text(n.Text)
		return
	}
	hasText := false
	for _, c := range n.Children {
		if c.IsText() {
			hasText = true
		}
	}
	e.start(n.Name.Space, n.Name.Local, hasText)
	for _, a := range n.Attrs {
		e.attr(a.Name.Space, a.Name.Local, a.Value)
	}
	e.content(n.Children)
	e.end(n.Name.Local)
}

// content writes raw content nodes into the current element.
func (e *encoder) content(nodes []*Node) {
	for _, c := range nodes {
		if c.IsText() {
			e.text(c.Text)
			continue
		}
		e.node(c)
	}
}

func (e *encoder) bytes() []byte {
	e.closeStart()
	e.b.WriteByte('\n')
	return e.b.Bytes()
}

func escapeText(b *bytes.Buffer, s string) {
	for _, r := range s {
		switch r {
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '&':
			b.WriteString("&amp;")
		case '\r':
			b.WriteString("&#xD;")
		default:
			b.WriteRune(r)
		}
	}
}

func escapeAttr(b *bytes.Buffer, s string) {
	for _, r := range s {
		switch r {
		case '<':
			b.WriteString("&lt;")
		case '&':
			b.WriteString("&amp;")
		case '"':
			b.WriteString("&quot;")
		case '\t':
			b.WriteString("&#x9;")
		case '\n':
			b.WriteString("&#xA;")
		case '\r':
			b.WriteString("&#xD;")
		default:
			b.WriteRune(r)
		}
	}
}
