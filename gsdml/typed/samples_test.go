// SPDX-License-Identifier: MIT

package typed

import (
	"bytes"
	"encoding/xml"
	"testing"
)

// sample is a generated test case (zz_samples_test.go): a node with every
// attribute and element of a struct.
type sample struct {
	name    string
	build   func(n *Node, b *builder) root
	anyAttr bool
	anyElem bool
	text    bool
	check   func(v any)
	node    *Node
}

func encodeRoot(r root) []byte {
	e := newEncoder()
	r.encodeAs(e, "urn:sample", "root")
	return e.bytes()
}

// TestSamples builds every struct from its sample node, encodes it,
// parses and builds the encoding again, and requires the second encoding
// to equal the first: every field is decoded and encoded, losslessly.
func TestSamples(t *testing.T) {
	if len(samples) == 0 {
		t.Fatal("no samples")
	}
	for _, s := range samples {
		b := &builder{}
		r := s.build(s.node, b)
		if b.err != nil {
			t.Errorf("%s: build: %v", s.name, b.err)
			continue
		}
		if s.check != nil {
			s.check(r)
		}
		first := encodeRoot(r)
		n, err := ParseNode(first)
		if err != nil {
			t.Errorf("%s: the encoding does not parse: %v\n%s", s.name, err, first)
			continue
		}
		b2 := &builder{}
		second := encodeRoot(s.build(n, b2))
		if b2.err != nil {
			t.Errorf("%s: rebuild: %v", s.name, b2.err)
			continue
		}
		if !bytes.Equal(first, second) {
			t.Errorf("%s: the encoding is not stable:\n%s\n%s", s.name, first, second)
		}
		// an attribute and an element that are not in the type
		bad := &Node{Name: s.node.Name, Attrs: append(append([]xml.Attr(nil), s.node.Attrs...), xml.Attr{Name: xml.Name{Space: "urn:unknown", Local: "u"}, Value: "1"})}
		b3 := &builder{}
		s.build(bad, b3)
		if (b3.err == nil) != s.anyAttr {
			t.Errorf("%s: unknown attribute: err %v, attribute wildcard %v", s.name, b3.err, s.anyAttr)
		}
		if !s.text {
			bad = &Node{Name: s.node.Name, Children: []*Node{{Name: xml.Name{Space: "urn:unknown", Local: "u"}}}}
			b4 := &builder{}
			s.build(bad, b4)
			if (b4.err == nil) != s.anyElem {
				t.Errorf("%s: unknown element: err %v, element wildcard %v", s.name, b4.err, s.anyElem)
			}
		}
	}
}
