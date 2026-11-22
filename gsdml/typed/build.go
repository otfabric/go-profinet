// SPDX-License-Identifier: MIT

package typed

import (
	"encoding/xml"
	"fmt"
)

// The generated build and encode functions are straight-line code over
// these helpers: a builder keeps the first error, and the generic helpers
// parse, format and write one field each.

type builder struct {
	err error
}

func (b *builder) fail(err error) {
	if b.err == nil {
		b.err = err
	}
}

// unknownAttr records an attribute that is not in the type; xsi
// attributes are ignored, and kept is the attribute wildcard's list (nil
// when the type has none).
func (b *builder) unknownAttr(n *Node, a xml.Attr, kept *[]xml.Attr) {
	switch {
	case a.Name.Space == nsXSI:
	case kept != nil:
		*kept = append(*kept, a)
	default:
		b.fail(unknownAttr(n, a))
	}
}

// unknownElement records an element that is not in the type; kept is the
// element wildcard's list (nil when the type has none).
func (b *builder) unknownElement(n, k *Node, kept *[]*Node) bool {
	if kept != nil {
		*kept = append(*kept, k)
		return true
	}
	b.fail(unknownElement(n, k))
	return false
}

func attrValue[T any](b *builder, n *Node, a xml.Attr, parse func(string) (T, error)) T {
	v, err := parse(a.Value)
	if err != nil {
		b.fail(attrError(n, a, err))
	}
	return v
}

func attrPtr[T any](b *builder, n *Node, a xml.Attr, parse func(string) (T, error)) *T {
	v := attrValue(b, n, a, parse)
	return &v
}

func textValue[T any](b *builder, n *Node, parse func(string) (T, error)) T {
	v, err := parse(n.CharData())
	if err != nil {
		b.fail(valueError(n, err))
	}
	return v
}

func textPtr[T any](b *builder, n *Node, parse func(string) (T, error)) *T {
	v := textValue(b, n, parse)
	return &v
}

// Parsers by Go kind.

func parsePreserve(v string) (string, error) { return v, nil }
func parseReplace(v string) (string, error)  { return replaceWS(v), nil }
func parseCollapse(v string) (string, error) { return collapse(v), nil }

func parseEnum[T ~string](v string) (T, error) { return T(collapse(v)), nil }

func parseBool(v string) (bool, error) { return parseBoolValue(v) }

// parseUintN parses an unsigned integer of T's width.
func parseUintN[T ~uint8 | ~uint16 | ~uint32 | ~uint64](v string) (T, error) {
	u, err := parseUintValue(v, 64)
	if err == nil && uint64(T(u)) != u {
		err = fmt.Errorf("%q is out of range", v)
	}
	return T(u), err
}

// parseIntN parses a signed integer of T's width.
func parseIntN[T ~int8 | ~int16 | ~int32 | ~int64](v string) (T, error) {
	i, err := parseIntValue(v, 64)
	if err == nil && int64(T(i)) != i {
		err = fmt.Errorf("%q is out of range", v)
	}
	return T(i), err
}

// parseLexical keeps an Integer or Decimal in its lexical form after
// checking it.
func parseLexical[T ~string](v string) (T, error) {
	s := collapse(v)
	var zero T
	re := reInteger
	if _, ok := any(zero).(Decimal); ok {
		re = reDecimal
	}
	if !re.MatchString(s) {
		return zero, fmt.Errorf("%q is not a number of the type", v)
	}
	return T(s), nil
}

func parseFloat(v string) (float64, error) { return parseFloatValue(v) }

// Formatters by Go kind.

func fmtString[T ~string](v T) string { return string(v) }

func fmtBool(v bool) string { return formatBool(v) }

func fmtUint[T ~uint8 | ~uint16 | ~uint32 | ~uint64](v T) string { return formatUint(uint64(v)) }

func fmtInt[T ~int8 | ~int16 | ~int32 | ~int64](v T) string { return formatInt(int64(v)) }

func fmtFloat(v float64) string { return formatFloat(v) }

// orDefault returns *p, or d when p is nil.
func orDefault[T any](p *T, d T) T {
	if p != nil {
		return *p
	}
	return d
}

// Writers.

func writeAttr[T any](e *encoder, ns, local string, p *T, f func(T) string) {
	if p != nil {
		e.attr(ns, local, f(*p))
	}
}

type encodable[T any] interface {
	*T
	encodeAs(e *encoder, ns, local string)
}

func encodeOne[T any, P encodable[T]](e *encoder, ns, local string, p P) {
	if p != nil {
		p.encodeAs(e, ns, local)
	}
}

func encodeAll[T any, P encodable[T]](e *encoder, ns, local string, ps []P) {
	for _, p := range ps {
		encodeOne(e, ns, local, p)
	}
}

func textOne[T any](e *encoder, ns, local string, p *T, f func(T) string) {
	if p != nil {
		e.start(ns, local, true)
		e.text(f(*p))
		e.end(local)
	}
}

func textAll[T any](e *encoder, ns, local string, vs []T, f func(T) string) {
	for i := range vs {
		textOne(e, ns, local, &vs[i], f)
	}
}

func nodeOne(e *encoder, n *Node) {
	if n != nil {
		e.node(n)
	}
}

func nodeAll(e *encoder, ns []*Node) {
	for _, n := range ns {
		nodeOne(e, n)
	}
}

// one is the number of elements a single (pointer) field holds.
func one(present bool) int {
	if present {
		return 1
	}
	return 0
}

// effectiveOrder is the order record when it still describes the fields,
// and the schema order (field by field) otherwise.
func effectiveOrder(order []pos, lens []int) []pos {
	if order != nil && validOrder(order, lens) {
		return order
	}
	var out []pos
	for f, n := range lens {
		for i := 0; i < n; i++ {
			out = append(out, pos{f: uint16(f), i: uint16(i)})
		}
	}
	return out
}

// position records an element's place for an order record.
func position(f, n int) pos {
	if f > 0xFFFF || n > 0xFFFF {
		panic(fmt.Sprintf("typed: order position %d/%d out of range", f, n))
	}
	return pos{f: uint16(f), i: uint16(n - 1)}
}
