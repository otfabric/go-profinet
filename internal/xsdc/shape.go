// SPDX-License-Identifier: MIT

package xsdc

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Shape is the Go representation of a complex type: what the generated
// struct holds. Two versions of a type share a struct exactly when their
// shapes are equal and the types their fields refer to share structs
// (Analyze at Representation).
type Shape struct {
	Mixed bool
	// Value is the simple content's type; nil for element content.
	Value *SimpleType
	Attrs []AttrField
	Elems []ElemField
	// Any: the content has a wildcard; its elements are kept as nodes.
	Any bool
	// AnyAttr: an attribute wildcard; its attributes are kept.
	AnyAttr bool
	// Ordered: elements can occur in more than one relative order, so
	// the struct records the document order for encoding.
	Ordered bool
	// Raw: the whole content is kept as nodes (mixed content, anyType).
	Raw bool
}

// AttrField is an attribute of a shape.
type AttrField struct {
	Name QName
	Type *SimpleType
	// Value: the field is a value, not a pointer (required, no default).
	Value   bool
	Default *string
}

// ElemField is an element of a shape.
type ElemField struct {
	Name    QName
	Element *Element
	Many    bool
	// Raw: kept as a node (anyType, or a substitution group head).
	Raw bool
}

// ShapeOf computes the shape of a complex type.
func ShapeOf(t *ComplexType) (*Shape, error) {
	s := &Shape{Mixed: t.Mixed, AnyAttr: t.AnyAttr != nil}
	if t.Simple != nil {
		s.Value = t.Simple
	}
	if t.AnyType || (t.Mixed && t.Content != nil) {
		s.Raw = true
	}
	for _, a := range t.Attrs {
		s.Attrs = append(s.Attrs, AttrField{Name: a.Name, Type: a.Type, Value: a.Required && a.Default == nil && a.Fixed == nil, Default: firstOf(a.Default, a.Fixed)})
	}
	if s.Raw || t.Content == nil {
		return s, nil
	}
	var walk func(p *Particle)
	walk = func(p *Particle) {
		if p.Kind == AnyParticle {
			s.Any = true
		}
		for _, c := range p.Children {
			walk(c)
		}
	}
	walk(t.Content)
	for _, u := range Uses(t.Content) {
		raw := len(u.Element.Substitutes) > 0 || (u.Element.Complex != nil && u.Element.Complex.AnyType)
		s.Elems = append(s.Elems, ElemField{Name: u.Element.Name, Element: u.Element, Many: u.Many, Raw: raw})
	}
	d, err := Compile(t.Content)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", t.where, err)
	}
	s.Ordered = d.Order()
	return s, nil
}

func firstOf(a, b *string) *string {
	if a != nil {
		return a
	}
	return b
}

// GoKind is the Go representation of a simple type: "string", "bool",
// "uint8" to "uint64", "int8" to "int64", "float64", "Integer" (an
// integer type whose facets do not bound it within int64), "Decimal",
// "enum" for a named type with enumerations, or "[]" and the item's kind.
func GoKind(t *SimpleType) string {
	switch t.Variety {
	case List:
		return "[]" + GoKind(t.ItemType())
	case Union:
		return "string"
	}
	if t.Builtin == "" && !t.Anonymous() && hasEnums(t) {
		return "enum"
	}
	switch t.BuiltinBase() {
	case "boolean":
		return "bool"
	case "unsignedByte":
		return "uint8"
	case "unsignedShort":
		return "uint16"
	case "unsignedInt":
		return "uint32"
	case "unsignedLong":
		return "uint64"
	case "byte":
		return "int8"
	case "short":
		return "int16"
	case "int":
		return "int32"
	case "long":
		return "int64"
	case "integer", "nonNegativeInteger", "positiveInteger", "nonPositiveInteger", "negativeInteger":
		lo, hi := integerBounds(t)
		if lo != nil && hi != nil && lo.IsInt64() && hi.IsInt64() {
			return "int64"
		}
		return "Integer"
	case "float", "double":
		return "float64"
	case "decimal":
		return "Decimal"
	}
	return "string"
}

var (
	zero = big.NewInt(0)
	one  = big.NewInt(1)
)

// integerBounds returns the tightest inclusive bounds the facets of an
// integer type give (nil: unbounded).
func integerBounds(t *SimpleType) (lo, hi *big.Int) {
	tighter := func(cur *big.Int, v *big.Int, low bool) *big.Int {
		if cur == nil || (low && v.Cmp(cur) > 0) || (!low && v.Cmp(cur) < 0) {
			return v
		}
		return cur
	}
	parse := func(s string) *big.Int {
		r, ok := new(big.Rat).SetString(strings.TrimPrefix(strings.TrimSpace(s), "+"))
		if !ok {
			return nil
		}
		return new(big.Int).Quo(r.Num(), r.Denom())
	}
	for x := t; x != nil; x = x.Base {
		switch x.Builtin {
		case "nonNegativeInteger":
			lo = tighter(lo, zero, true)
		case "positiveInteger":
			lo = tighter(lo, one, true)
		case "nonPositiveInteger":
			hi = tighter(hi, zero, false)
		case "negativeInteger":
			hi = tighter(hi, big.NewInt(-1), false)
		}
		if x.MinIncl != nil {
			if v := parse(*x.MinIncl); v != nil {
				lo = tighter(lo, v, true)
			}
		}
		if x.MaxIncl != nil {
			if v := parse(*x.MaxIncl); v != nil {
				hi = tighter(hi, v, false)
			}
		}
		if x.MinExcl != nil {
			if v := parse(*x.MinExcl); v != nil {
				lo = tighter(lo, new(big.Int).Add(v, one), true)
			}
		}
		if x.MaxExcl != nil {
			if v := parse(*x.MaxExcl); v != nil {
				hi = tighter(hi, new(big.Int).Sub(v, one), false)
			}
		}
	}
	return lo, hi
}

func hasEnums(t *SimpleType) bool {
	for x := t; x != nil && x.Builtin == ""; x = x.Base {
		if x.HasEnums {
			return true
		}
		if x.Variety != Atomic {
			return false
		}
	}
	return false
}

// HasEnums reports whether a simple type or a user-defined ancestor has an
// enumeration facet.
func HasEnums(t *SimpleType) bool { return hasEnums(t) }

// EnumValues returns the enumeration of the nearest step that has one.
func EnumValues(t *SimpleType) []string {
	for x := t; x != nil && x.Builtin == ""; x = x.Base {
		if x.HasEnums {
			return x.Enums
		}
	}
	return nil
}

// signature renders a shape for the representation fingerprint; refs are
// the components it refers to.
func (s *Shape) signature(g *signer) string {
	var b strings.Builder
	fmt.Fprintf(&b, "mixed=%v raw=%v any=%v anyattr=%v ordered=%v", s.Mixed, s.Raw, s.Any, s.AnyAttr, s.Ordered)
	if s.Value != nil {
		b.WriteString(" value=" + g.ref(s.Value))
	}
	attrs := append([]AttrField(nil), s.Attrs...)
	sort.Slice(attrs, func(i, j int) bool { return attrs[i].Name.String() < attrs[j].Name.String() })
	for _, a := range attrs {
		fmt.Fprintf(&b, " @%s=%s value=%v default=%s", a.Name, g.ref(a.Type), a.Value, strp(a.Default))
	}
	for _, e := range s.Elems {
		fmt.Fprintf(&b, " <%s many=%v raw=%v", e.Name, e.Many, e.Raw)
		if !e.Raw {
			b.WriteString(" " + g.elementType(e.Element))
		}
		b.WriteString(">")
	}
	return b.String()
}
