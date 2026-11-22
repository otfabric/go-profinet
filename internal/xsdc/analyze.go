// SPDX-License-Identifier: MIT

package xsdc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Level is how much of a component a fingerprint covers.
type Level int

// Fingerprint levels, each including the one before.
const (
	// Representation: the Go shape (fields, their Go kinds and whether
	// they repeat), which decides the generated structs.
	Representation Level = iota
	// Codec adds element order, model groups and occurrence bounds,
	// which decide the generated encoder and content-model matcher.
	Codec
	// Full adds every facet, enumeration, use, default, fixed value,
	// wildcard and identity constraint: the generated validator.
	Full
)

func (l Level) String() string { return [...]string{"representation", "codec", "full"}[l] }

// Component is a version-independent identity: a named type or global
// element, or an anonymous type by its path.
type Component struct {
	Kind string // "complex", "simple" or "element"
	Name QName
	Path string
}

func (c Component) String() string {
	if c.Path != "" {
		return c.Kind + ":{" + c.Name.NS + "}" + c.Path
	}
	return c.Kind + ":" + c.Name.String()
}

// Corpus is the schema sets of every version, in version order.
type Corpus struct {
	Versions []string
	Sets     []*Set
}

// Index lists the components of a set by identity.
type Index struct {
	Complex map[Component]*ComplexType
	Simple  map[Component]*SimpleType
	Element map[Component]*Element
}

// ComponentOf returns the identity of a complex type.
func ComponentOfComplex(t *ComplexType) Component {
	if t.Anonymous() {
		return Component{Kind: "complex", Name: QName{NS: t.Name.NS}, Path: t.Path}
	}
	return Component{Kind: "complex", Name: t.Name}
}

// ComponentOfSimple returns the identity of a simple type.
func ComponentOfSimple(t *SimpleType) Component {
	if t.Anonymous() {
		return Component{Kind: "simple", Name: QName{NS: t.Name.NS}, Path: t.Path}
	}
	return Component{Kind: "simple", Name: t.Name}
}

// IndexOf builds the identity index of a set. Anonymous paths that repeat
// within one set get a "#n" suffix in resolution order.
func IndexOf(s *Set) (*Index, error) {
	ix := &Index{Complex: map[Component]*ComplexType{}, Simple: map[Component]*SimpleType{}, Element: map[Component]*Element{}}
	for q, t := range s.ComplexTypes {
		ix.Complex[Component{Kind: "complex", Name: q}] = t
	}
	seen := map[string]int{}
	for _, t := range s.Anonymous {
		if strings.Contains(t.Path, "#") {
			ix.Complex[ComponentOfComplex(t)] = t
			continue
		}
		seen[t.Name.NS+t.Path]++
		if n := seen[t.Name.NS+t.Path]; n > 1 {
			t.Path += "#" + strconv.Itoa(n)
		}
		ix.Complex[ComponentOfComplex(t)] = t
	}
	for q, t := range s.SimpleTypes {
		ix.Simple[Component{Kind: "simple", Name: q}] = t
	}
	seen = map[string]int{}
	for _, t := range s.AnonymousSimple {
		if strings.Contains(t.Path, "#") {
			ix.Simple[ComponentOfSimple(t)] = t
			continue
		}
		seen[t.Name.NS+t.Path]++
		if n := seen[t.Name.NS+t.Path]; n > 1 {
			t.Path += "#" + strconv.Itoa(n)
		}
		ix.Simple[ComponentOfSimple(t)] = t
	}
	for q, e := range s.Elements {
		ix.Element[Component{Kind: "element", Name: q}] = e
	}
	return ix, nil
}

// signer renders the local signature of a component at a level; the
// references it makes are returned in order, as components of the same
// set, and the signature refers to them by position.
type signer struct {
	level Level
	refs  []any
	err   error
}

func (g *signer) ref(x any) string {
	g.refs = append(g.refs, x)
	return "#" + strconv.Itoa(len(g.refs)-1)
}

func strp(p *string) string {
	if p == nil {
		return "-"
	}
	return strconv.Quote(*p)
}

func intp(p *int) string {
	if p == nil {
		return "-"
	}
	return strconv.Itoa(*p)
}

func (g *signer) simple(t *SimpleType) string {
	var b strings.Builder
	if g.level == Representation {
		// Named types with enumerations become named Go types; the rest
		// are their Go kind.
		if k := GoKind(t); k != "enum" {
			return k
		}
		return "enum " + t.Name.String()
	}
	fmt.Fprintf(&b, "%s builtin=%s ws=%s", t.Variety, t.Builtin, t.WhiteSpace)
	if t.Base != nil {
		b.WriteString(" base=" + g.ref(t.Base))
	}
	if t.Item != nil {
		b.WriteString(" item=" + g.ref(t.Item))
	}
	for _, m := range t.Members {
		b.WriteString(" member=" + g.ref(m))
	}
	if g.level == Codec {
		return b.String()
	}
	if t.HasEnums {
		e := append([]string(nil), t.Enums...)
		sort.Strings(e)
		fmt.Fprintf(&b, " enums=%q", e)
	}
	fmt.Fprintf(&b, " patterns=%q", t.Patterns)
	fmt.Fprintf(&b, " bounds=%s,%s,%s,%s len=%s,%s,%s", strp(t.MinIncl), strp(t.MaxIncl), strp(t.MinExcl), strp(t.MaxExcl), intp(t.Length), intp(t.MinLength), intp(t.MaxLength))
	return b.String()
}

// ElementUse is how a complex type's content uses an element name.
type ElementUse struct {
	Element *Element
	// Many: it can occur more than once; Required: it always occurs.
	Many, Required bool
}

// Uses returns the elements a content model can contain, in first
// occurrence order, with their multiplicity. Elements of a substitution
// group are listed with their head.
func Uses(p *Particle) []ElementUse {
	type acc struct {
		e *Element
	}
	var order []QName
	m := map[QName]*acc{}
	var walk func(p *Particle) map[QName][2]int
	add := func(dst, src map[QName][2]int) {
		for k, v := range src {
			o := dst[k]
			dst[k] = [2]int{o[0] + v[0], addMax(o[1], v[1])}
		}
	}
	walk = func(p *Particle) map[QName][2]int {
		out := map[QName][2]int{}
		switch p.Kind {
		case ElementParticle:
			q := p.Element.Name
			if _, ok := m[q]; !ok {
				m[q] = &acc{e: p.Element}
				order = append(order, q)
			}
			out[q] = [2]int{1, 1}
		case AnyParticle:
			// a wildcard is no element use
		case Sequence, All:
			for _, c := range p.Children {
				add(out, walk(c))
			}
		case Choice:
			first := true
			for _, c := range p.Children {
				w := walk(c)
				if first {
					out = w
					first = false
					continue
				}
				for k, v := range out {
					wv := w[k]
					out[k] = [2]int{min(v[0], wv[0]), max2(v[1], wv[1])}
				}
				for k, v := range w {
					if _, ok := out[k]; !ok {
						out[k] = [2]int{0, v[1]}
					}
				}
			}
		}
		for k, v := range out {
			out[k] = [2]int{v[0] * p.Min, mulMax(v[1], p.Max)}
		}
		return out
	}
	if p == nil {
		return nil
	}
	r := walk(p)
	var uses []ElementUse
	for _, q := range order {
		v := r[q]
		uses = append(uses, ElementUse{Element: m[q].e, Many: v[1] == Unbounded || v[1] > 1, Required: v[0] > 0})
	}
	return uses
}

func addMax(a, b int) int {
	if a == Unbounded || b == Unbounded {
		return Unbounded
	}
	return a + b
}

func mulMax(a, b int) int {
	if a == 0 || b == 0 {
		return 0
	}
	if a == Unbounded || b == Unbounded {
		return Unbounded
	}
	return a * b
}

func max2(a, b int) int {
	if a == Unbounded || b == Unbounded {
		return Unbounded
	}
	return max(a, b)
}

func (g *signer) elementType(e *Element) string {
	if e.Complex != nil {
		return "ct=" + g.ref(e.Complex)
	}
	return "st=" + g.ref(e.Simple)
}

func (g *signer) particle(p *Particle, b *strings.Builder) {
	fmt.Fprintf(b, "(%s %d %d", p.Kind, p.Min, p.Max)
	switch p.Kind {
	case ElementParticle:
		b.WriteString(" " + p.Element.Name.String())
		if p.Element.Global {
			b.WriteString(" ref=" + g.ref(p.Element))
		} else {
			b.WriteString(" " + g.element(p.Element))
		}
	case AnyParticle:
		w := p.Wildcard
		fmt.Fprintf(b, " any=%v other=%v target=%s allowed=%q process=%s", w.Any, w.Other, w.Target, w.Allowed, w.Process)
	default:
	}
	for _, c := range p.Children {
		b.WriteString(" ")
		g.particle(c, b)
	}
	b.WriteString(")")
}

// element signs a local element declaration inline (its identity is its
// parent's).
func (g *signer) element(e *Element) string {
	s := g.elementType(e)
	if g.level == Full {
		s += " default=" + strp(e.Default) + " fixed=" + strp(e.Fixed)
		for _, c := range e.IDCs {
			s += " " + g.idc(c)
		}
	}
	return s
}

func (g *signer) idc(c *IDC) string {
	r := ""
	if c.Refer != nil {
		r = c.Refer.Name.String()
	}
	return fmt.Sprintf("[%s %s refer=%s sel=%v fields=%v]", c.Kind, c.Name, r, c.Selector, c.Fields)
}

func (g *signer) complex(t *ComplexType) string {
	if g.level == Representation {
		sh, err := ShapeOf(t)
		if err != nil {
			g.err = err
			return ""
		}
		return sh.signature(g)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "mixed=%v abstract=%v anytype=%v", t.Mixed, t.Abstract, t.AnyType)
	if t.Simple != nil {
		b.WriteString(" simple=" + g.ref(t.Simple))
	}
	attrs := append([]*AttrUse(nil), t.Attrs...)
	sort.Slice(attrs, func(i, j int) bool { return attrs[i].Name.String() < attrs[j].Name.String() })
	for _, a := range attrs {
		fmt.Fprintf(&b, " @%s=%s", a.Name, g.ref(a.Type))
		if g.level == Full {
			fmt.Fprintf(&b, " req=%v", a.Required)
			fmt.Fprintf(&b, " default=%s fixed=%s", strp(a.Default), strp(a.Fixed))
		}
	}
	if t.AnyAttr != nil {
		w := t.AnyAttr
		fmt.Fprintf(&b, " anyattr(any=%v other=%v target=%s allowed=%q process=%s)", w.Any, w.Other, w.Target, w.Allowed, w.Process)
	}
	if t.Content != nil {
		b.WriteString(" content=")
		g.particle(t.Content, &b)
	}
	return b.String()
}

func (g *signer) globalElement(e *Element) string {
	s := g.elementType(e)
	if g.level >= Codec {
		s += fmt.Sprintf(" abstract=%v", e.Abstract)
		for _, x := range e.Substitutes {
			s += " subst=" + g.ref(x)
		}
	}
	if g.level == Full {
		s += " default=" + strp(e.Default) + " fixed=" + strp(e.Fixed)
		for _, c := range e.IDCs {
			s += " " + g.idc(c)
		}
	}
	return s
}

// Variants is the result of Analyze at one level: for every component,
// the class of each version where it exists ("" where it does not).
type Variants struct {
	Level   Level
	Classes map[Component][]string
}

// Count returns, per component, how many distinct variants it has.
func (v *Variants) Count(c Component) int {
	seen := map[string]bool{}
	for _, k := range v.Classes[c] {
		if k != "" {
			seen[k] = true
		}
	}
	return len(seen)
}

// Analyze computes the variants of every component across the corpus at
// a level by partition refinement: two (component, version) pairs are in
// the same class when their local signatures are equal and the
// components they refer to are pairwise in the same class, recursively
// (so the result is the coarsest such partition, cycles included).
func Analyze(c *Corpus, level Level) (*Variants, error) {
	type key struct {
		comp Component
		v    int
	}
	type entry struct {
		sig  string
		refs []key
	}
	nodes := map[key]*entry{}
	var order []key
	for vi, s := range c.Sets {
		ix, err := IndexOf(s)
		if err != nil {
			return nil, err
		}
		compOf := func(x any) Component {
			switch t := x.(type) {
			case *ComplexType:
				return ComponentOfComplex(t)
			case *SimpleType:
				return ComponentOfSimple(t)
			case *Element:
				return Component{Kind: "element", Name: t.Name}
			}
			panic("xsdc: unknown component")
		}
		var signErr error
		add := func(comp Component, sign func(g *signer) string) {
			g := &signer{level: level}
			sig := comp.String() + "|" + sign(g)
			if g.err != nil && signErr == nil {
				signErr = g.err
			}
			e := &entry{sig: sig}
			for _, r := range g.refs {
				e.refs = append(e.refs, key{compOf(r), vi})
			}
			k := key{comp, vi}
			nodes[k] = e
			order = append(order, k)
		}
		for comp, t := range ix.Complex {
			add(comp, func(g *signer) string { return g.complex(t) })
		}
		for comp, t := range ix.Simple {
			add(comp, func(g *signer) string { return g.simple(t) })
		}
		for comp, e := range ix.Element {
			add(comp, func(g *signer) string { return g.globalElement(e) })
		}
		if signErr != nil {
			return nil, signErr
		}
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].v != order[j].v {
			return order[i].v < order[j].v
		}
		return order[i].comp.String() < order[j].comp.String()
	})
	class := map[key]string{}
	for _, k := range order {
		class[k] = hash(nodes[k].sig)
	}
	count := func() int {
		seen := map[string]bool{}
		for _, v := range class {
			seen[v] = true
		}
		return len(seen)
	}
	n := count()
	for {
		next := make(map[key]string, len(class))
		for _, k := range order {
			e := nodes[k]
			var b strings.Builder
			b.WriteString(class[k])
			for _, r := range e.refs {
				rc, ok := class[r]
				if !ok {
					return nil, fmt.Errorf("xsdc: %s refers to %s, which is not indexed", k.comp, r.comp)
				}
				b.WriteString("," + rc)
			}
			next[k] = hash(b.String())
		}
		class = next
		m := count()
		if m == n {
			break
		}
		n = m
	}
	out := &Variants{Level: level, Classes: map[Component][]string{}}
	for _, k := range order {
		cl := out.Classes[k.comp]
		if cl == nil {
			cl = make([]string, len(c.Sets))
			out.Classes[k.comp] = cl
		}
		cl[k.v] = class[k]
	}
	return out, nil
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:12])
}
