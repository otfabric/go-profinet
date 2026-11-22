// SPDX-License-Identifier: MIT

package xsdc

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Unbounded is Particle.Max for maxOccurs="unbounded".
const Unbounded = -1

// Set is a resolved schema set: the root document and what it imports.
type Set struct {
	Root      string
	Documents []string

	Elements     map[QName]*Element
	Attributes   map[QName]*AttrDecl
	ComplexTypes map[QName]*ComplexType
	SimpleTypes  map[QName]*SimpleType

	// Anonymous lists the anonymous complex types in the order they were
	// resolved; each has a unique Path.
	Anonymous []*ComplexType
	// AnonymousSimple likewise for anonymous simple types.
	AnonymousSimple []*SimpleType

	raw      map[string]map[QName]*node // kind -> name -> declaration
	resolved bool
	building map[*node]bool
	// Version is the root document's version attribute.
	Version string
}

func newSet() *Set {
	s := &Set{
		Elements:     map[QName]*Element{},
		Attributes:   map[QName]*AttrDecl{},
		ComplexTypes: map[QName]*ComplexType{},
		SimpleTypes:  map[QName]*SimpleType{},
		raw:          map[string]map[QName]*node{},
		building:     map[*node]bool{},
	}
	for _, k := range []string{"element", "attribute", "complexType", "simpleType", "group", "attributeGroup"} {
		s.raw[k] = map[QName]*node{}
	}
	return s
}

func (s *Set) addDocument(d *document) error {
	for _, c := range d.root.kids() {
		k := c.name.Local
		switch k {
		case "import", "include", "notation":
			continue
		case "redefine":
			return fmt.Errorf("%s: xsd:redefine is not implemented", c.where())
		}
		m, ok := s.raw[k]
		if !ok {
			return fmt.Errorf("%s: top-level xsd:%s is not implemented", c.where(), k)
		}
		name, ok := c.attr("name")
		if !ok {
			return fmt.Errorf("%s: top-level xsd:%s without a name", c.where(), k)
		}
		q := QName{d.target, name}
		if prev, dup := m[q]; dup {
			return fmt.Errorf("%s: %s %s already declared at %s", c.where(), k, q, prev.where())
		}
		m[q] = c
	}
	return nil
}

// Element is an element declaration.
type Element struct {
	Name QName
	// Exactly one of Complex and Simple is set.
	Complex *ComplexType
	Simple  *SimpleType
	Default *string
	Fixed   *string
	IDCs    []*IDC
	Global  bool
	// Abstract elements cannot appear; only their substitutes can.
	Abstract bool
	// SubstitutionHead is the substitutionGroup (nillable, unused by
	// GSDML, is rejected by Load).
	SubstitutionHead *Element
	// Substitutes are the global elements whose substitutionGroup is this
	// element, directly or transitively, in name order.
	Substitutes []*Element
	where       string
}

// AttrDecl is a global attribute declaration.
type AttrDecl struct {
	Name    QName
	Type    *SimpleType
	Default *string
	Fixed   *string
}

// AttrUse is an attribute of a complex type.
type AttrUse struct {
	Name     QName
	Type     *SimpleType
	Required bool
	Default  *string
	Fixed    *string
}

// Wildcard is xsd:any or xsd:anyAttribute.
type Wildcard struct {
	// Any is namespace="##any".
	Any bool
	// Other is "##other": any namespace except Target and no namespace.
	Other bool
	// Target is the target namespace of the declaring document.
	Target string
	// Allowed is an explicit list ("" is ##local).
	Allowed []string
	// Process is strict, lax or skip.
	Process string
}

// Allows reports whether the wildcard admits an item in namespace ns.
func (w *Wildcard) Allows(ns string) bool {
	switch {
	case w.Any:
		return true
	case w.Other:
		return ns != w.Target && ns != ""
	}
	for _, a := range w.Allowed {
		if a == ns {
			return true
		}
	}
	return false
}

// ParticleKind is the kind of a particle.
type ParticleKind int

// Particle kinds.
const (
	Sequence ParticleKind = iota
	Choice
	All
	ElementParticle
	AnyParticle
)

func (k ParticleKind) String() string {
	return [...]string{"sequence", "choice", "all", "element", "any"}[k]
}

// Particle is a term of a content model with its occurrence bounds.
type Particle struct {
	Kind     ParticleKind
	Min, Max int
	Children []*Particle
	Element  *Element
	Wildcard *Wildcard
}

// ComplexType is a complex type definition with its effective content and
// attributes (extension and restriction applied).
type ComplexType struct {
	Name QName
	// Path names an anonymous type: the element names from the nearest
	// named component, such as "ProfileBody" or "ModuleItemT/ModuleInfo".
	// Name.NS is then the namespace of the declaring schema and
	// Name.Local is empty.
	Path     string
	Mixed    bool
	Abstract bool
	// Simple is the value type of simple content; nil for element content.
	Simple *SimpleType
	// Content is the effective content model; nil is empty content.
	Content *Particle
	Attrs   []*AttrUse
	AnyAttr *Wildcard
	Base    *ComplexType
	// Derivation is "extension", "restriction" or "" (from anyType).
	Derivation string
	AnyType    bool
	where      string
	building   bool
}

// Anonymous reports whether the type has no name.
func (t *ComplexType) Anonymous() bool { return t.Name.Local == "" }

// Variety of a simple type.
const (
	Atomic = "atomic"
	List   = "list"
	Union  = "union"
)

// SimpleType is a simple type definition.
type SimpleType struct {
	Name QName
	Path string
	// Builtin is the XML Schema built-in this type is, or "".
	Builtin string
	Variety string
	Base    *SimpleType
	Item    *SimpleType
	Members []*SimpleType
	// The facets of this derivation step only; a value must satisfy every
	// step up to the built-in (Base).
	Enums      []string
	HasEnums   bool
	Patterns   []string // ORed within this step
	MinIncl    *string
	MaxIncl    *string
	MinExcl    *string
	MaxExcl    *string
	Length     *int
	MinLength  *int
	MaxLength  *int
	WhiteSpace string // explicit facet of this step, or ""
	where      string
}

// Anonymous reports whether the type has no name.
func (t *SimpleType) Anonymous() bool { return t.Name.Local == "" }

// BuiltinBase returns the nearest built-in type at or above t (for a list
// or union, t itself has none and "" is returned).
func (t *SimpleType) BuiltinBase() string {
	for x := t; x != nil; x = x.Base {
		if x.Builtin != "" {
			return x.Builtin
		}
		if x.Variety != Atomic {
			return ""
		}
	}
	return ""
}

// ItemType is the item type of a list type, inherited by a restriction of
// a list; nil for another variety.
func (t *SimpleType) ItemType() *SimpleType {
	for x := t; x != nil && x.Variety == List; x = x.Base {
		if x.Item != nil {
			return x.Item
		}
	}
	return nil
}

// EffectiveWhiteSpace is the whiteSpace a value of t is normalised with.
func (t *SimpleType) EffectiveWhiteSpace() string {
	for x := t; x != nil; x = x.Base {
		if x.WhiteSpace != "" {
			return x.WhiteSpace
		}
		if x.Variety == List {
			return "collapse"
		}
		if x.Variety == Union {
			return "" // members normalise themselves
		}
		if x.Builtin != "" {
			return builtinWhiteSpace(x.Builtin)
		}
	}
	return "collapse"
}

func builtinWhiteSpace(b string) string {
	switch b {
	case "string", "anySimpleType":
		return "preserve"
	case "normalizedString":
		return "replace"
	}
	return "collapse"
}

// builtins is the XML Schema built-in derivation tree, for the types the
// GSDML schemas use and their ancestors. Each maps to its base.
var builtins = map[string]string{
	"anySimpleType":      "",
	"string":             "anySimpleType",
	"normalizedString":   "string",
	"token":              "normalizedString",
	"language":           "token",
	"Name":               "token",
	"NCName":             "Name",
	"ID":                 "NCName",
	"IDREF":              "NCName",
	"NMTOKEN":            "token",
	"anyURI":             "anySimpleType",
	"QName":              "anySimpleType",
	"boolean":            "anySimpleType",
	"float":              "anySimpleType",
	"double":             "anySimpleType",
	"decimal":            "anySimpleType",
	"integer":            "decimal",
	"nonNegativeInteger": "integer",
	"positiveInteger":    "nonNegativeInteger",
	"nonPositiveInteger": "integer",
	"negativeInteger":    "nonPositiveInteger",
	"long":               "integer",
	"int":                "long",
	"short":              "int",
	"byte":               "short",
	"unsignedLong":       "nonNegativeInteger",
	"unsignedInt":        "unsignedLong",
	"unsignedShort":      "unsignedInt",
	"unsignedByte":       "unsignedShort",
	"date":               "anySimpleType",
	"dateTime":           "anySimpleType",
	"time":               "anySimpleType",
	"hexBinary":          "anySimpleType",
	"base64Binary":       "anySimpleType",
}

// Path is one alternative of an identity-constraint XPath.
type Path struct {
	// Descendant is a leading ".//".
	Descendant bool
	Steps      []Step
}

// Step is a location step: child elements by name (Any for "*", AnyNS
// for "*" alone), "." (Self), or, last in a field, an attribute.
type Step struct {
	Self  bool
	Attr  bool
	Any   bool // "*" or "p:*"
	AnyNS bool // "*": any namespace
	Name  QName
}

// IDC is an identity constraint (xsd:key, xsd:keyref, xsd:unique).
type IDC struct {
	Kind     string
	Name     QName
	Refer    *IDC
	Selector []Path
	// Fields: each field is a union of paths.
	Fields [][]Path
	where  string
}

// resolve builds every global component, then the substitution groups.
func (s *Set) resolve() error {
	for _, k := range []string{"simpleType", "complexType", "attribute", "element"} {
		names := make([]QName, 0, len(s.raw[k]))
		for q := range s.raw[k] {
			names = append(names, q)
		}
		sortQNames(names)
		for _, q := range names {
			var err error
			switch k {
			case "simpleType":
				_, err = s.simpleType(q)
			case "complexType":
				_, err = s.complexType(q)
			case "attribute":
				_, err = s.globalAttr(q)
			case "element":
				_, err = s.globalElement(q)
			}
			if err != nil {
				return err
			}
		}
	}
	// Keyref targets: by name across the set.
	idcs := map[QName]*IDC{}
	var all []*IDC
	var visit func(e *Element)
	seen := map[*Element]bool{}
	var visitP func(p *Particle)
	seenT := map[*ComplexType]bool{}
	visitT := func(t *ComplexType) {
		if t == nil || seenT[t] {
			return
		}
		seenT[t] = true
		visitP(t.Content)
	}
	visit = func(e *Element) {
		if seen[e] {
			return
		}
		seen[e] = true
		for _, c := range e.IDCs {
			idcs[c.Name] = c
			all = append(all, c)
		}
		visitT(e.Complex)
	}
	visitP = func(p *Particle) {
		if p == nil {
			return
		}
		if p.Element != nil {
			visit(p.Element)
		}
		for _, c := range p.Children {
			visitP(c)
		}
	}
	for _, q := range sortedKeys(s.Elements) {
		visit(s.Elements[q])
	}
	for _, q := range sortedKeys(s.ComplexTypes) {
		visitT(s.ComplexTypes[q])
	}
	for _, c := range all {
		if c.Kind != "keyref" {
			continue
		}
		ref := c.Refer
		target, ok := idcs[ref.Name]
		if !ok || target.Kind == "keyref" {
			return fmt.Errorf("%s: keyref %s refers to %s, which is no key or unique", c.where, c.Name, ref.Name)
		}
		if len(target.Fields) != len(c.Fields) {
			return fmt.Errorf("%s: keyref %s has %d fields, %s has %d", c.where, c.Name, len(c.Fields), target.Name, len(target.Fields))
		}
		c.Refer = target
	}
	// Substitution groups.
	for _, q := range sortedKeys(s.Elements) {
		e := s.Elements[q]
		for h := e.SubstitutionHead; h != nil; h = h.SubstitutionHead {
			h.Substitutes = append(h.Substitutes, e)
		}
	}
	s.resolved = true
	return nil
}

func sortQNames(q []QName) {
	sort.Slice(q, func(i, j int) bool {
		if q[i].NS != q[j].NS {
			return q[i].NS < q[j].NS
		}
		return q[i].Local < q[j].Local
	})
}

func sortedKeys[V any](m map[QName]V) []QName {
	out := make([]QName, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortQNames(out)
	return out
}

func (s *Set) builtin(name string) *SimpleType {
	q := QName{XS, name}
	if t, ok := s.SimpleTypes[q]; ok {
		return t
	}
	t := &SimpleType{Name: q, Builtin: name, Variety: Atomic}
	if b := builtins[name]; b != "" {
		t.Base = s.builtin(b)
	}
	s.SimpleTypes[q] = t
	return t
}

func (s *Set) simpleType(q QName) (*SimpleType, error) {
	if q.NS == XS {
		if _, ok := builtins[q.Local]; !ok {
			return nil, fmt.Errorf("built-in simple type xsd:%s is not implemented", q.Local)
		}
		return s.builtin(q.Local), nil
	}
	if t, ok := s.SimpleTypes[q]; ok {
		return t, nil
	}
	n, ok := s.raw["simpleType"][q]
	if !ok {
		return nil, fmt.Errorf("simple type %s is not declared", q)
	}
	t := &SimpleType{Name: q}
	s.SimpleTypes[q] = t
	if err := s.fillSimple(t, n, q.Local); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Set) anonSimple(n *node, path string) (*SimpleType, error) {
	t := &SimpleType{Name: QName{NS: n.doc.target}, Path: path}
	s.AnonymousSimple = append(s.AnonymousSimple, t)
	return t, s.fillSimple(t, n, path)
}

func (s *Set) typeRef(n *node, attr string) (*SimpleType, error) {
	v, _ := n.attr(attr)
	q, err := n.qname(v)
	if err != nil {
		return nil, err
	}
	return s.simpleType(q)
}

func (s *Set) fillSimple(t *SimpleType, n *node, path string) error {
	t.where = n.where()
	kids := n.kids()
	if len(kids) != 1 {
		return fmt.Errorf("%s: simple type %s: expected one of restriction, list, union", n.where(), path)
	}
	c := kids[0]
	switch c.name.Local {
	case "restriction":
		t.Variety = Atomic
		if _, ok := c.attr("base"); ok {
			b, err := s.typeRef(c, "base")
			if err != nil {
				return fmt.Errorf("%s: %w", c.where(), err)
			}
			t.Base = b
		}
		for _, f := range c.kids() {
			v, _ := f.attr("value")
			switch f.name.Local {
			case "simpleType":
				if t.Base != nil {
					return fmt.Errorf("%s: restriction with both base and a simple type", f.where())
				}
				b, err := s.anonSimple(f, path+"/base")
				if err != nil {
					return err
				}
				t.Base = b
			case "enumeration":
				t.Enums = append(t.Enums, v)
				t.HasEnums = true
			case "pattern":
				t.Patterns = append(t.Patterns, v)
			case "minInclusive":
				t.MinIncl = &v
			case "maxInclusive":
				t.MaxIncl = &v
			case "minExclusive":
				t.MinExcl = &v
			case "maxExclusive":
				t.MaxExcl = &v
			case "length", "minLength", "maxLength":
				i, err := strconv.Atoi(strings.TrimSpace(v))
				if err != nil || i < 0 {
					return fmt.Errorf("%s: %s %q", f.where(), f.name.Local, v)
				}
				switch f.name.Local {
				case "length":
					t.Length = &i
				case "minLength":
					t.MinLength = &i
				default:
					t.MaxLength = &i
				}
			case "whiteSpace":
				t.WhiteSpace = strings.TrimSpace(v)
			default:
				return fmt.Errorf("%s: facet xsd:%s is not implemented", f.where(), f.name.Local)
			}
		}
		if t.Base == nil {
			return fmt.Errorf("%s: restriction without a base", c.where())
		}
		if t.Base.Variety == Union || t.Base.Variety == List {
			// a restriction of a union or list keeps its variety
			t.Variety = t.Base.Variety
		}
	case "list":
		t.Variety = List
		if _, ok := c.attr("itemType"); ok {
			it, err := s.typeRef(c, "itemType")
			if err != nil {
				return err
			}
			t.Item = it
		} else {
			k := c.kids()
			if len(k) != 1 {
				return fmt.Errorf("%s: list without an item type", c.where())
			}
			it, err := s.anonSimple(k[0], path+"/item")
			if err != nil {
				return err
			}
			t.Item = it
		}
	case "union":
		t.Variety = Union
		if mt, ok := c.attr("memberTypes"); ok {
			for _, m := range strings.Fields(mt) {
				q, err := c.qname(m)
				if err != nil {
					return err
				}
				mtt, err := s.simpleType(q)
				if err != nil {
					return err
				}
				t.Members = append(t.Members, mtt)
			}
		}
		for i, k := range c.kids() {
			if k.name.Local != "simpleType" {
				return fmt.Errorf("%s: unexpected xsd:%s in a union", k.where(), k.name.Local)
			}
			m, err := s.anonSimple(k, fmt.Sprintf("%s/member%d", path, i+1))
			if err != nil {
				return err
			}
			t.Members = append(t.Members, m)
		}
		if len(t.Members) == 0 {
			return fmt.Errorf("%s: union without members", c.where())
		}
	default:
		return fmt.Errorf("%s: xsd:%s in a simple type", c.where(), c.name.Local)
	}
	return nil
}

func (s *Set) anyType() *ComplexType {
	q := QName{XS, "anyType"}
	if t, ok := s.ComplexTypes[q]; ok {
		return t
	}
	w := &Wildcard{Any: true, Process: "lax"}
	t := &ComplexType{
		Name:    q,
		Mixed:   true,
		AnyType: true,
		Content: &Particle{Kind: Sequence, Min: 1, Max: 1, Children: []*Particle{{Kind: AnyParticle, Min: 0, Max: Unbounded, Wildcard: w}}},
		AnyAttr: w,
	}
	s.ComplexTypes[q] = t
	return t
}

func (s *Set) complexType(q QName) (*ComplexType, error) {
	if q == (QName{XS, "anyType"}) {
		return s.anyType(), nil
	}
	if t, ok := s.ComplexTypes[q]; ok {
		return t, nil
	}
	n, ok := s.raw["complexType"][q]
	if !ok {
		return nil, fmt.Errorf("complex type %s is not declared", q)
	}
	t := &ComplexType{Name: q, building: true}
	s.ComplexTypes[q] = t
	defer func() { t.building = false }()
	if err := s.fillComplex(t, n, q.Local); err != nil {
		return nil, err
	}
	return t, nil
}

// typeOf resolves a type attribute that may name a simple or complex type.
func (s *Set) typeOf(n *node, v string) (*ComplexType, *SimpleType, error) {
	q, err := n.qname(v)
	if err != nil {
		return nil, nil, err
	}
	if q.NS == XS {
		if q.Local == "anyType" {
			return s.anyType(), nil, nil
		}
		st, err := s.simpleType(q)
		return nil, st, err
	}
	if _, ok := s.raw["complexType"][q]; ok {
		ct, err := s.complexType(q)
		return ct, nil, err
	}
	st, err := s.simpleType(q)
	return nil, st, err
}

func boolAttr(n *node, k string) bool {
	v, _ := n.attr(k)
	v = strings.TrimSpace(v)
	return v == "true" || v == "1"
}

func (s *Set) fillComplex(t *ComplexType, n *node, path string) error {
	t.Mixed = boolAttr(n, "mixed")
	t.Abstract = boolAttr(n, "abstract")
	var content *Particle
	var own []*node // attribute nodes
	var anyAttr *node
	derivation := ""
	var base *ComplexType
	var simpleBase *SimpleType
	collect := func(parent *node) error {
		for _, k := range parent.kids() {
			switch k.name.Local {
			case "sequence", "choice", "all", "group":
				if content != nil {
					return fmt.Errorf("%s: two model groups", k.where())
				}
				p, err := s.particle(k, path)
				if err != nil {
					return err
				}
				content = p
			case "attribute":
				own = append(own, k)
			case "attributeGroup":
				return fmt.Errorf("%s: xsd:attributeGroup is not used by GSDML and not implemented", k.where())
			case "anyAttribute":
				anyAttr = k
			case "simpleType":
				// the simple type of a simpleContent restriction
				st, err := s.anonSimple(k, path+"/simple")
				if err != nil {
					return err
				}
				simpleBase = st
			case "complexContent", "simpleContent":
				// handled by the caller
			default:
				return fmt.Errorf("%s: xsd:%s in a complex type is not implemented", k.where(), k.name.Local)
			}
		}
		return nil
	}
	kids := n.kids()
	if len(kids) == 1 && (kids[0].name.Local == "complexContent" || kids[0].name.Local == "simpleContent") {
		cc := kids[0]
		if cc.name.Local == "complexContent" && boolAttr(cc, "mixed") {
			t.Mixed = true
		}
		dk := cc.kids()
		if len(dk) != 1 {
			return fmt.Errorf("%s: expected one extension or restriction", cc.where())
		}
		d := dk[0]
		derivation = d.name.Local
		if derivation != "extension" && derivation != "restriction" {
			return fmt.Errorf("%s: xsd:%s in %s", d.where(), derivation, cc.name.Local)
		}
		bv, _ := d.attr("base")
		bc, bs, err := s.typeOf(d, bv)
		if err != nil {
			return fmt.Errorf("%s: %w", d.where(), err)
		}
		if bc != nil && bc.building {
			return fmt.Errorf("%s: complex type %s is derived from itself", d.where(), bv)
		}
		if cc.name.Local == "simpleContent" {
			switch {
			case bs != nil:
				simpleBase = bs
			case bc != nil && bc.Simple != nil:
				base = bc
				simpleBase = bc.Simple
			default:
				return fmt.Errorf("%s: simple content from %s, which has no simple content", d.where(), bv)
			}
		} else {
			if bc == nil {
				return fmt.Errorf("%s: complex content from simple type %s", d.where(), bv)
			}
			base = bc
		}
		if err := collect(d); err != nil {
			return err
		}
		if cc.name.Local == "simpleContent" {
			t.Simple = simpleBase
			if derivation == "restriction" {
				// facets directly in the restriction are not used by GSDML
				for _, f := range d.kids() {
					switch f.name.Local {
					case "attribute", "anyAttribute", "simpleType":
					default:
						return fmt.Errorf("%s: facet in a simpleContent restriction is not implemented", f.where())
					}
				}
			}
		}
	} else {
		if err := collect(n); err != nil {
			return err
		}
	}
	t.Base, t.Derivation = base, derivation
	t.where = n.where()
	// Effective content.
	switch {
	case derivation == "extension" && base != nil && t.Simple == nil:
		switch {
		case base.Content == nil:
			t.Content = content
		case content == nil:
			t.Content = base.Content
		default:
			t.Content = &Particle{Kind: Sequence, Min: 1, Max: 1, Children: []*Particle{base.Content, content}}
		}
		if base.Mixed {
			t.Mixed = true
		}
	default:
		t.Content = content
	}
	// Effective attributes.
	var attrs []*AttrUse
	if base != nil {
		attrs = append(attrs, base.Attrs...)
		t.AnyAttr = base.AnyAttr
	}
	for _, a := range own {
		use, prohibited, err := s.attrUse(a, path)
		if err != nil {
			return err
		}
		replaced := false
		for i, b := range attrs {
			if b.Name == use.Name {
				if derivation == "extension" {
					return fmt.Errorf("%s: extension redeclares attribute %s", a.where(), use.Name)
				}
				if prohibited {
					attrs = append(attrs[:i:i], attrs[i+1:]...)
				} else {
					attrs[i] = use
				}
				replaced = true
				break
			}
		}
		if !replaced && !prohibited {
			attrs = append(attrs, use)
		}
	}
	if anyAttr != nil {
		w, err := wildcard(anyAttr)
		if err != nil {
			return err
		}
		t.AnyAttr = w
	}
	t.Attrs = attrs
	return nil
}

func (s *Set) attrUse(a *node, path string) (*AttrUse, bool, error) {
	use := strings.TrimSpace(a.attrs["use"])
	u := &AttrUse{Required: use == "required"}
	prohibited := use == "prohibited"
	if ref, ok := a.attr("ref"); ok {
		q, err := a.qname(ref)
		if err != nil {
			return nil, false, err
		}
		g, err := s.globalAttr(q)
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", a.where(), err)
		}
		u.Name, u.Type, u.Default, u.Fixed = g.Name, g.Type, g.Default, g.Fixed
	} else {
		name := a.attrs["name"]
		ns := ""
		form := a.attrs["form"]
		if form == "qualified" || (form == "" && a.doc.attributeQualified) {
			ns = a.doc.target
		}
		u.Name = QName{ns, name}
		st, err := s.attrType(a, path+"@"+name)
		if err != nil {
			return nil, false, err
		}
		u.Type = st
	}
	if v, ok := a.attr("default"); ok {
		u.Default = &v
	}
	if v, ok := a.attr("fixed"); ok {
		u.Fixed = &v
	}
	return u, prohibited, nil
}

func (s *Set) attrType(a *node, path string) (*SimpleType, error) {
	if _, ok := a.attr("type"); ok {
		t, err := s.typeRef(a, "type")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", a.where(), err)
		}
		return t, nil
	}
	for _, k := range a.kids() {
		if k.name.Local == "simpleType" {
			return s.anonSimple(k, path)
		}
	}
	return s.builtin("anySimpleType"), nil
}

func (s *Set) globalAttr(q QName) (*AttrDecl, error) {
	if g, ok := s.Attributes[q]; ok {
		return g, nil
	}
	if q.NS == XMLNS {
		if _, ok := s.raw["attribute"][q]; !ok {
			return nil, fmt.Errorf("attribute %s is not declared (xml.xsd not imported)", q)
		}
	}
	n, ok := s.raw["attribute"][q]
	if !ok {
		return nil, fmt.Errorf("attribute %s is not declared", q)
	}
	st, err := s.attrType(n, "@"+q.Local)
	if err != nil {
		return nil, err
	}
	g := &AttrDecl{Name: q, Type: st}
	if v, ok := n.attr("default"); ok {
		g.Default = &v
	}
	if v, ok := n.attr("fixed"); ok {
		g.Fixed = &v
	}
	s.Attributes[q] = g
	return g, nil
}

func occurs(n *node) (int, int, error) {
	lo, hi := 1, 1
	if v, ok := n.attr("minOccurs"); ok {
		i, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || i < 0 {
			return 0, 0, fmt.Errorf("%s: minOccurs %q", n.where(), v)
		}
		lo = i
	}
	if v, ok := n.attr("maxOccurs"); ok {
		v = strings.TrimSpace(v)
		if v == "unbounded" {
			hi = Unbounded
		} else {
			i, err := strconv.Atoi(v)
			if err != nil || i < 0 {
				return 0, 0, fmt.Errorf("%s: maxOccurs %q", n.where(), v)
			}
			hi = i
		}
	}
	if hi != Unbounded && hi < lo {
		return 0, 0, fmt.Errorf("%s: maxOccurs below minOccurs", n.where())
	}
	return lo, hi, nil
}

func (s *Set) particle(n *node, path string) (*Particle, error) {
	lo, hi, err := occurs(n)
	if err != nil {
		return nil, err
	}
	switch n.name.Local {
	case "sequence", "choice", "all":
		kind := map[string]ParticleKind{"sequence": Sequence, "choice": Choice, "all": All}[n.name.Local]
		p := &Particle{Kind: kind, Min: lo, Max: hi}
		for _, k := range n.kids() {
			c, err := s.particle(k, path)
			if err != nil {
				return nil, err
			}
			p.Children = append(p.Children, c)
		}
		return p, nil
	case "group":
		ref, ok := n.attr("ref")
		if !ok {
			return nil, fmt.Errorf("%s: local xsd:group without ref", n.where())
		}
		q, err := n.qname(ref)
		if err != nil {
			return nil, err
		}
		g, ok := s.raw["group"][q]
		if !ok {
			return nil, fmt.Errorf("%s: group %s is not declared", n.where(), q)
		}
		if s.building[g] {
			return nil, fmt.Errorf("%s: group %s refers to itself", n.where(), q)
		}
		s.building[g] = true
		defer delete(s.building, g)
		gk := g.kids()
		if len(gk) != 1 {
			return nil, fmt.Errorf("%s: group %s: expected one model group", g.where(), q)
		}
		inner, err := s.particle(gk[0], path)
		if err != nil {
			return nil, err
		}
		if lo == 1 && hi == 1 {
			return inner, nil
		}
		return &Particle{Kind: Sequence, Min: lo, Max: hi, Children: []*Particle{inner}}, nil
	case "element":
		e, err := s.localElement(n, path)
		if err != nil {
			return nil, err
		}
		return &Particle{Kind: ElementParticle, Min: lo, Max: hi, Element: e}, nil
	case "any":
		w, err := wildcard(n)
		if err != nil {
			return nil, err
		}
		return &Particle{Kind: AnyParticle, Min: lo, Max: hi, Wildcard: w}, nil
	}
	return nil, fmt.Errorf("%s: xsd:%s in a model group is not implemented", n.where(), n.name.Local)
}

func wildcard(n *node) (*Wildcard, error) {
	w := &Wildcard{Target: n.doc.target, Process: "strict"}
	if v, ok := n.attr("processContents"); ok {
		w.Process = strings.TrimSpace(v)
		if w.Process != "strict" && w.Process != "lax" && w.Process != "skip" {
			return nil, fmt.Errorf("%s: processContents %q", n.where(), v)
		}
	}
	ns, ok := n.attr("namespace")
	if !ok {
		ns = "##any"
	}
	switch strings.TrimSpace(ns) {
	case "##any":
		w.Any = true
	case "##other":
		w.Other = true
	default:
		for _, f := range strings.Fields(ns) {
			switch f {
			case "##targetNamespace":
				w.Allowed = append(w.Allowed, n.doc.target)
			case "##local":
				w.Allowed = append(w.Allowed, "")
			default:
				w.Allowed = append(w.Allowed, f)
			}
		}
	}
	return w, nil
}

func (s *Set) globalElement(q QName) (*Element, error) {
	if e, ok := s.Elements[q]; ok {
		return e, nil
	}
	n, ok := s.raw["element"][q]
	if !ok {
		return nil, fmt.Errorf("element %s is not declared", q)
	}
	e := &Element{Name: q, Global: true, Abstract: boolAttr(n, "abstract")}
	s.Elements[q] = e
	if sg, ok := n.attr("substitutionGroup"); ok {
		hq, err := n.qname(sg)
		if err != nil {
			return nil, err
		}
		h, err := s.globalElement(hq)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n.where(), err)
		}
		e.SubstitutionHead = h
	}
	if err := s.fillElement(e, n, q.Local); err != nil {
		return nil, err
	}
	if e.Complex == nil && e.Simple == nil && e.SubstitutionHead != nil {
		// no type: the head's type
		e.Complex, e.Simple = e.SubstitutionHead.Complex, e.SubstitutionHead.Simple
	}
	return e, nil
}

func (s *Set) localElement(n *node, path string) (*Element, error) {
	if ref, ok := n.attr("ref"); ok {
		q, err := n.qname(ref)
		if err != nil {
			return nil, err
		}
		e, err := s.globalElement(q)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n.where(), err)
		}
		return e, nil
	}
	name := n.attrs["name"]
	ns := ""
	form := n.attrs["form"]
	if form == "qualified" || (form == "" && n.doc.elementQualified) {
		ns = n.doc.target
	}
	e := &Element{Name: QName{ns, name}}
	if err := s.fillElement(e, n, path+"/"+name); err != nil {
		return nil, err
	}
	return e, nil
}

func (s *Set) fillElement(e *Element, n *node, path string) error {
	e.where = n.where()
	if boolAttr(n, "nillable") {
		return fmt.Errorf("%s: nillable is not used by GSDML and not implemented", n.where())
	}
	if v, ok := n.attr("default"); ok {
		e.Default = &v
	}
	if v, ok := n.attr("fixed"); ok {
		e.Fixed = &v
	}
	if tv, ok := n.attr("type"); ok {
		ct, st, err := s.typeOf(n, tv)
		if err != nil {
			return fmt.Errorf("%s: %w", n.where(), err)
		}
		e.Complex, e.Simple = ct, st
	}
	for _, k := range n.kids() {
		switch k.name.Local {
		case "complexType":
			t := &ComplexType{Name: QName{NS: n.doc.target}, Path: path}
			s.Anonymous = append(s.Anonymous, t)
			if err := s.fillComplex(t, k, path); err != nil {
				return err
			}
			e.Complex = t
		case "simpleType":
			st, err := s.anonSimple(k, path)
			if err != nil {
				return err
			}
			e.Simple = st
		case "key", "keyref", "unique":
			c, err := idc(k)
			if err != nil {
				return err
			}
			e.IDCs = append(e.IDCs, c)
		default:
			return fmt.Errorf("%s: xsd:%s in an element is not implemented", k.where(), k.name.Local)
		}
	}
	if e.Complex == nil && e.Simple == nil && e.SubstitutionHead == nil {
		e.Complex = s.anyType()
	}
	return nil
}

func idc(n *node) (*IDC, error) {
	c := &IDC{Kind: n.name.Local, Name: QName{n.doc.target, n.attrs["name"]}, where: n.where()}
	if c.Kind == "keyref" {
		q, err := n.qname(n.attrs["refer"])
		if err != nil {
			return nil, err
		}
		c.Refer = &IDC{Name: q}
	}
	for _, k := range n.kids() {
		xp := k.attrs["xpath"]
		switch k.name.Local {
		case "selector":
			p, err := parseXPath(k, xp, false)
			if err != nil {
				return nil, err
			}
			c.Selector = p
		case "field":
			p, err := parseXPath(k, xp, true)
			if err != nil {
				return nil, err
			}
			c.Fields = append(c.Fields, p)
		}
	}
	if c.Selector == nil || len(c.Fields) == 0 {
		return nil, fmt.Errorf("%s: %s without selector or field", n.where(), c.Kind)
	}
	return c, nil
}

// parseXPath parses the identity-constraint XPath subset of XML Schema
// 1.0 (3.11.6): unions of paths of child steps, optionally starting with
// ".//", "." and "*" steps, and for fields a final attribute step.
// Unprefixed names are in no namespace, as the specification says.
func parseXPath(n *node, xp string, field bool) ([]Path, error) {
	var out []Path
	for _, alt := range strings.Split(xp, "|") {
		alt = strings.TrimSpace(alt)
		var p Path
		if strings.HasPrefix(alt, ".//") {
			p.Descendant = true
			alt = strings.TrimSpace(alt[3:])
		}
		if alt == "" {
			return nil, fmt.Errorf("%s: XPath %q: empty path", n.where(), xp)
		}
		parts := strings.Split(alt, "/")
		for i, raw := range parts {
			st := strings.TrimSpace(raw)
			last := i == len(parts)-1
			switch {
			case st == ".":
				p.Steps = append(p.Steps, Step{Self: true})
			case st == "*":
				p.Steps = append(p.Steps, Step{Any: true, AnyNS: true})
			case strings.HasPrefix(st, "@"):
				if !field || !last {
					return nil, fmt.Errorf("%s: XPath %q: attribute step not last in a field", n.where(), xp)
				}
				name := strings.TrimSpace(st[1:])
				if name == "*" {
					return nil, fmt.Errorf("%s: XPath %q: @* is not implemented", n.where(), xp)
				}
				q, err := xpathName(n, name)
				if err != nil {
					return nil, err
				}
				p.Steps = append(p.Steps, Step{Attr: true, Name: q})
			case st == "" || strings.ContainsAny(st, "[]()=\"'"):
				return nil, fmt.Errorf("%s: XPath %q: step %q is outside the XML Schema subset", n.where(), xp, st)
			case strings.HasSuffix(st, ":*"):
				q, err := xpathName(n, strings.TrimSuffix(st, "*")+"x")
				if err != nil {
					return nil, err
				}
				p.Steps = append(p.Steps, Step{Any: true, Name: QName{NS: q.NS}})
			default:
				q, err := xpathName(n, st)
				if err != nil {
					return nil, err
				}
				p.Steps = append(p.Steps, Step{Name: q})
			}
		}
		out = append(out, p)
	}
	return out, nil
}

func xpathName(n *node, s string) (QName, error) {
	prefix, local, ok := strings.Cut(s, ":")
	if !ok {
		return QName{"", s}, nil
	}
	ns, found := n.ns[prefix]
	if !found {
		if prefix == "xml" {
			return QName{XMLNS, local}, nil
		}
		return QName{}, fmt.Errorf("%s: XPath prefix %q is not declared", n.where(), prefix)
	}
	return QName{ns, local}, nil
}
