// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/otfabric/go-profinet/internal/xsdc"
)

// tables builds the validation tables: every entry is interned, so that a
// component that is the same in several versions is one entry.
type tables struct {
	g *gen

	simples   []string
	simpleIx  map[string]int // key -> index
	patterns  []string
	patternIx map[string]int
	complexes []string
	complexIx map[string]int
	elements  []string
	elementIx map[string]int
	wilds     []string
	wildIx    map[string]int
	idcs      []string
	idcIx     map[string]int
	idcByPtr  map[*xsdc.IDC]int
}

func intern(list *[]string, ix map[string]int, lit string) int {
	if i, ok := ix[lit]; ok {
		return i
	}
	*list = append(*list, lit)
	ix[lit] = len(*list) - 1
	return len(*list) - 1
}

func q(s string) string { return strconv.Quote(s) }

var builtinConst = map[string]string{
	"anySimpleType": "bAnySimple", "string": "bString", "normalizedString": "bNormalizedString", "token": "bToken",
	"language": "bLanguage", "Name": "bName", "NCName": "bNCName", "ID": "bID", "IDREF": "bIDREF", "NMTOKEN": "bNMTOKEN",
	"anyURI": "bAnyURI", "QName": "bQName", "boolean": "bBoolean", "float": "bFloat", "double": "bDouble",
	"decimal": "bDecimal", "integer": "bInteger", "nonNegativeInteger": "bNonNegativeInteger",
	"positiveInteger": "bPositiveInteger", "nonPositiveInteger": "bNonPositiveInteger",
	"negativeInteger": "bNegativeInteger", "long": "bLong", "int": "bInt", "short": "bShort", "byte": "bByte",
	"unsignedLong": "bUnsignedLong", "unsignedInt": "bUnsignedInt", "unsignedShort": "bUnsignedShort",
	"unsignedByte": "bUnsignedByte", "date": "bDate", "dateTime": "bDateTime", "time": "bTime",
	"hexBinary": "bHexBinary", "base64Binary": "bBase64Binary",
}

var wsConst = map[string]string{"preserve": "wsPreserve", "replace": "wsReplace", "collapse": "wsCollapse", "": "wsPreserve"}

// simple returns the index of a simple type of version vi.
func (t *tables) simple(vi int, s *xsdc.SimpleType) (int, error) {
	var key string
	if s.Builtin != "" {
		key = "builtin:" + s.Builtin
	} else {
		comp := xsdc.ComponentOfSimple(s)
		key = "class:" + t.g.full.Classes[comp][vi]
		if t.g.full.Classes[comp][vi] == "" {
			return 0, fmt.Errorf("simple type %s has no class in V%s", comp, t.g.c.Versions[vi])
		}
	}
	if i, ok := t.simpleIx[key]; ok {
		return i, nil
	}
	// reserve, then fill (simple types are acyclic)
	t.simples = append(t.simples, "")
	i := len(t.simples) - 1
	t.simpleIx[key] = i
	lit, err := t.simpleLiteral(vi, s)
	if err != nil {
		return 0, err
	}
	t.simples[i] = lit
	return i, nil
}

func (t *tables) simpleLiteral(vi int, s *xsdc.SimpleType) (string, error) {
	var b strings.Builder
	name := s.Name.Local
	if s.Builtin != "" {
		fmt.Fprintf(&b, "{name: %s, variety: vAtomic, builtin: %s, ws: %s, base: -1, item: -1}", q(name), builtinConst[s.Builtin], wsConst[s.EffectiveWhiteSpace()])
		return b.String(), nil
	}
	variety := map[string]string{xsdc.Atomic: "vAtomic", xsdc.List: "vList", xsdc.Union: "vUnion"}[s.Variety]
	bt := "bAnySimple"
	if s.Variety == xsdc.Atomic {
		bb := s.BuiltinBase()
		c, ok := builtinConst[bb]
		if !ok {
			return "", fmt.Errorf("simple type %s: built-in %q", xsdc.ComponentOfSimple(s), bb)
		}
		bt = c
	}
	base := -1
	if s.Base != nil && s.Base.Builtin == "" {
		i, err := t.simple(vi, s.Base)
		if err != nil {
			return "", err
		}
		base = i
	}
	item := -1
	if s.Item != nil {
		i, err := t.simple(vi, s.Item)
		if err != nil {
			return "", err
		}
		item = i
	}
	var members []string
	for _, m := range s.Members {
		i, err := t.simple(vi, m)
		if err != nil {
			return "", err
		}
		members = append(members, strconv.Itoa(i))
	}
	fmt.Fprintf(&b, "{name: %s, variety: %s, builtin: %s, ws: %s, base: %d, item: %d", q(name), variety, bt, wsConst[s.EffectiveWhiteSpace()], base, item)
	if len(members) > 0 {
		fmt.Fprintf(&b, ", members: []int32{%s}", strings.Join(members, ", "))
	}
	var has []string
	ws := s.EffectiveWhiteSpace()
	if s.HasEnums {
		has = append(has, "hasEnums")
		var vals []string
		for _, e := range s.Enums {
			vals = append(vals, q(normalize(e, ws)))
		}
		fmt.Fprintf(&b, ", enums: []string{%s}", strings.Join(vals, ", "))
	}
	if len(s.Patterns) > 0 {
		var ps []string
		for _, p := range s.Patterns {
			re, err := translatePattern(p)
			if err != nil {
				return "", fmt.Errorf("%s: %w", xsdc.ComponentOfSimple(s), err)
			}
			ps = append(ps, strconv.Itoa(intern(&t.patterns, t.patternIx, fmt.Sprintf("{xsd: %s, re2: %s}", q(p), q(re)))))
		}
		fmt.Fprintf(&b, ", patterns: []int32{%s}", strings.Join(ps, ", "))
	}
	bound := func(p *string, field, bit string) {
		if p != nil {
			has = append(has, bit)
			fmt.Fprintf(&b, ", %s: %s", field, q(strings.TrimSpace(*p)))
		}
	}
	bound(s.MinIncl, "minIncl", "hasMinIncl")
	bound(s.MaxIncl, "maxIncl", "hasMaxIncl")
	bound(s.MinExcl, "minExcl", "hasMinExcl")
	bound(s.MaxExcl, "maxExcl", "hasMaxExcl")
	length := func(p *int, field, bit string) {
		if p != nil {
			has = append(has, bit)
			fmt.Fprintf(&b, ", %s: %d", field, *p)
		}
	}
	length(s.Length, "length", "hasLength")
	length(s.MinLength, "minLength", "hasMinLength")
	length(s.MaxLength, "maxLength", "hasMaxLength")
	if s.MinIncl != nil || s.MaxIncl != nil || s.MinExcl != nil || s.MaxExcl != nil {
		bb := s.BuiltinBase()
		numeric := bb == "float" || bb == "double" || bb == "decimal" || strings.Contains(bb, "nteger") || strings.Contains(bb, "nsigned") ||
			bb == "long" || bb == "int" || bb == "short" || bb == "byte"
		if !numeric {
			return "", fmt.Errorf("%s: bounds on %s are not implemented", xsdc.ComponentOfSimple(s), bb)
		}
	}
	if len(has) > 0 {
		fmt.Fprintf(&b, ", has: %s", strings.Join(has, " | "))
	}
	b.WriteString("}")
	return b.String(), nil
}

func (t *tables) wildcard(w *xsdc.Wildcard) int {
	mode := "wList"
	switch {
	case w.Any:
		mode = "wAny"
	case w.Other:
		mode = "wOther"
	}
	proc := map[string]string{"strict": "pStrict", "lax": "pLax", "skip": "pSkip"}[w.Process]
	var allowed []string
	for _, a := range w.Allowed {
		allowed = append(allowed, q(a))
	}
	lit := fmt.Sprintf("{mode: %s, target: %s, process: %s", mode, q(w.Target), proc)
	if len(allowed) > 0 {
		lit += ", allowed: []string{" + strings.Join(allowed, ", ") + "}"
	}
	return intern(&t.wilds, t.wildIx, lit+"}")
}

// complex returns the index of a complex type of version vi; the literal
// is filled in later (complex types can be recursive).
func (t *tables) complex(vi int, c *xsdc.ComplexType) (int, bool) {
	comp := xsdc.ComponentOfComplex(c)
	key := "class:" + t.g.full.Classes[comp][vi]
	if i, ok := t.complexIx[key]; ok {
		return i, false
	}
	t.complexes = append(t.complexes, "")
	i := len(t.complexes) - 1
	t.complexIx[key] = i
	return i, true
}

type pendingComplex struct {
	i  int
	vi int
	c  *xsdc.ComplexType
}

func (t *tables) complexLiteral(vi int, c *xsdc.ComplexType, queue *[]pendingComplex) (string, error) {
	var b strings.Builder
	var flags []string
	if c.Mixed {
		flags = append(flags, "cMixed")
	}
	if c.Abstract {
		flags = append(flags, "cAbstract")
	}
	if c.AnyType {
		flags = append(flags, "cAnyType")
	}
	simple := -1
	if c.Simple != nil {
		flags = append(flags, "cSimpleContent")
		i, err := t.simple(vi, c.Simple)
		if err != nil {
			return "", err
		}
		simple = i
	}
	name := c.Name.Local
	if c.Anonymous() {
		name = c.Path
	}
	fmt.Fprintf(&b, "{name: %s, simple: %d, anyAttr: ", q(name), simple)
	if c.AnyAttr != nil {
		fmt.Fprintf(&b, "%d", t.wildcard(c.AnyAttr))
	} else {
		b.WriteString("-1")
	}
	if len(flags) > 0 {
		fmt.Fprintf(&b, ", flags: %s", strings.Join(flags, " | "))
	}
	if len(c.Attrs) > 0 {
		var attrs []string
		for _, a := range c.Attrs {
			lit, err := t.attrLiteral(vi, a.Name, a.Type, a.Required, a.Default, a.Fixed)
			if err != nil {
				return "", err
			}
			attrs = append(attrs, lit)
		}
		fmt.Fprintf(&b, ", attrs: []attrSpec{%s}", strings.Join(attrs, ", "))
	}
	if c.Content != nil {
		d, err := xsdc.Compile(c.Content)
		if err != nil {
			return "", fmt.Errorf("%s: %w", xsdc.ComponentOfComplex(c), err)
		}
		var states []string
		for s := range d.Next {
			var trans []string
			for _, p := range d.Next[s] {
				pos := d.Positions[p-1]
				if pos.Wildcard != nil {
					trans = append(trans, fmt.Sprintf("{elem: -1, wild: %d, next: %d}", t.wildcard(pos.Wildcard), p))
					continue
				}
				ei, err := t.element(vi, pos.Element, queue)
				if err != nil {
					return "", err
				}
				trans = append(trans, fmt.Sprintf("{elem: %d, wild: -1, next: %d}", ei, p))
			}
			st := "{"
			if d.Final[s] {
				st += "final: true"
			}
			if len(trans) > 0 {
				if d.Final[s] {
					st += ", "
				}
				st += "trans: []dfaTrans{" + strings.Join(trans, ", ") + "}"
			}
			states = append(states, st+"}")
		}
		fmt.Fprintf(&b, ", dfa: []dfaState{%s}", strings.Join(states, ", "))
	}
	b.WriteString("}")
	return b.String(), nil
}

func (t *tables) attrLiteral(vi int, name xsdc.QName, typ *xsdc.SimpleType, required bool, def, fixed *string) (string, error) {
	si, err := t.simple(vi, typ)
	if err != nil {
		return "", err
	}
	var flags []string
	if required {
		flags = append(flags, "fRequired")
	}
	lit := fmt.Sprintf("{ns: %s, local: %s, simple: %d", q(name.NS), q(name.Local), si)
	if def != nil {
		flags = append(flags, "fDefault")
		lit += ", def: " + q(*def)
	}
	if fixed != nil {
		flags = append(flags, "fFixed")
		lit += ", fixed: " + q(*fixed)
	}
	if len(flags) > 0 {
		lit += ", flags: " + strings.Join(flags, " | ")
	}
	return lit + "}", nil
}

// element returns the index of an element declaration of version vi.
func (t *tables) element(vi int, e *xsdc.Element, queue *[]pendingComplex) (int, error) {
	complexIdx, simpleIdx := -1, -1
	if e.Complex != nil {
		i, isNew := t.complex(vi, e.Complex)
		if isNew {
			*queue = append(*queue, pendingComplex{i, vi, e.Complex})
		}
		complexIdx = i
	} else {
		i, err := t.simple(vi, e.Simple)
		if err != nil {
			return 0, err
		}
		simpleIdx = i
	}
	var key string
	if e.Global {
		comp := xsdc.Component{Kind: "element", Name: e.Name}
		key = "global:" + t.g.full.Classes[comp][vi]
		if i, ok := t.elementIx[key]; ok {
			return i, nil
		}
		t.elements = append(t.elements, "")
		i := len(t.elements) - 1
		t.elementIx[key] = i
		lit, err := t.elementLiteral(vi, e, complexIdx, simpleIdx, queue)
		if err != nil {
			return 0, err
		}
		t.elements[i] = lit
		return i, nil
	}
	lit, err := t.elementLiteral(vi, e, complexIdx, simpleIdx, queue)
	if err != nil {
		return 0, err
	}
	return intern(&t.elements, t.elementIx, lit), nil
}

func (t *tables) elementLiteral(vi int, e *xsdc.Element, complexIdx, simpleIdx int, queue *[]pendingComplex) (string, error) {
	var flags []string
	lit := fmt.Sprintf("{ns: %s, local: %s, complex: %d, simple: %d", q(e.Name.NS), q(e.Name.Local), complexIdx, simpleIdx)
	if e.Abstract {
		flags = append(flags, "fAbstract")
	}
	if e.Global {
		flags = append(flags, "fGlobal")
	}
	if e.Default != nil {
		flags = append(flags, "fDefault")
		lit += ", def: " + q(*e.Default)
	}
	if e.Fixed != nil {
		flags = append(flags, "fFixed")
		lit += ", fixed: " + q(*e.Fixed)
	}
	if len(flags) > 0 {
		lit += ", flags: " + strings.Join(flags, " | ")
	}
	if len(e.Substitutes) > 0 {
		var subs []string
		for _, s := range e.Substitutes {
			i, err := t.element(vi, s, queue)
			if err != nil {
				return "", err
			}
			subs = append(subs, strconv.Itoa(i))
		}
		lit += ", substitutes: []int32{" + strings.Join(subs, ", ") + "}"
	}
	if len(e.IDCs) > 0 {
		var ids []string
		for _, c := range e.IDCs {
			i, err := t.idc(c)
			if err != nil {
				return "", err
			}
			ids = append(ids, strconv.Itoa(i))
		}
		lit += ", idcs: []int32{" + strings.Join(ids, ", ") + "}"
	}
	return lit + "}", nil
}

func xpathLiteral(paths []xsdc.Path) string {
	var out []string
	for _, p := range paths {
		var steps []string
		for _, s := range p.Steps {
			switch {
			case s.Self:
				steps = append(steps, "{kind: xSelf}")
			case s.Attr:
				steps = append(steps, fmt.Sprintf("{kind: xAttr, ns: %s, local: %s}", q(s.Name.NS), q(s.Name.Local)))
			case s.Any && s.AnyNS:
				steps = append(steps, "{kind: xAnyChild}")
			case s.Any:
				steps = append(steps, fmt.Sprintf("{kind: xAnyChildNS, ns: %s}", q(s.Name.NS)))
			default:
				steps = append(steps, fmt.Sprintf("{kind: xChild, ns: %s, local: %s}", q(s.Name.NS), q(s.Name.Local)))
			}
		}
		out = append(out, fmt.Sprintf("{descendant: %v, steps: []xstep{%s}}", p.Descendant, strings.Join(steps, ", ")))
	}
	return "[]xpath{" + strings.Join(out, ", ") + "}"
}

func (t *tables) idc(c *xsdc.IDC) (int, error) {
	if i, ok := t.idcByPtr[c]; ok {
		return i, nil
	}
	kind := map[string]string{"key": "idcKey", "unique": "idcUnique", "keyref": "idcKeyref"}[c.Kind]
	refer := -1
	if c.Kind == "keyref" {
		i, err := t.idc(c.Refer)
		if err != nil {
			return 0, err
		}
		refer = i
	}
	var fields []string
	for _, f := range c.Fields {
		for _, p := range f {
			if len(p.Steps) == 0 {
				return 0, fmt.Errorf("identity constraint %s: empty field", c.Name)
			}
		}
		fields = append(fields, xpathLiteral(f))
	}
	lit := fmt.Sprintf("{kind: %s, name: %s, refer: %d, selector: %s, fields: [][]xpath{%s}}", kind, q(c.Name.Local), refer, xpathLiteral(c.Selector), strings.Join(fields, ", "))
	i := intern(&t.idcs, t.idcIx, lit)
	t.idcByPtr[c] = i
	return i, nil
}

func (g *gen) genTables() ([]byte, error) {
	t := &tables{g: g, simpleIx: map[string]int{}, patternIx: map[string]int{}, complexIx: map[string]int{}, elementIx: map[string]int{}, wildIx: map[string]int{}, idcIx: map[string]int{}, idcByPtr: map[*xsdc.IDC]int{}}
	var versions []string
	var queue []pendingComplex
	for vi, s := range g.c.Sets {
		// keyref targets are per set
		t.idcByPtr = map[*xsdc.IDC]int{}
		var globals []int
		for _, q := range sortedQNames(s.Elements) {
			i, err := t.element(vi, s.Elements[q], &queue)
			if err != nil {
				return nil, err
			}
			globals = append(globals, i)
		}
		for len(queue) > 0 {
			p := queue[0]
			queue = queue[1:]
			lit, err := t.complexLiteral(p.vi, p.c, &queue)
			if err != nil {
				return nil, err
			}
			t.complexes[p.i] = lit
		}
		var attrs []string
		for _, q := range sortedQNames(s.Attributes) {
			a := s.Attributes[q]
			lit, err := t.attrLiteral(vi, a.Name, a.Type, false, a.Default, a.Fixed)
			if err != nil {
				return nil, err
			}
			attrs = append(attrs, lit)
		}
		var gs []string
		for _, i := range globals {
			gs = append(gs, strconv.Itoa(i))
		}
		versions = append(versions, fmt.Sprintf("{version: %s, globals: []int32{%s}, attrs: []attrSpec{%s}}", q(g.c.Versions[vi]), strings.Join(gs, ", "), strings.Join(attrs, ", ")))
	}
	var b bytes.Buffer
	b.WriteString(header)
	b.WriteString("package typed\n\n")
	fmt.Fprintf(&b, "// tables: %d simple types, %d patterns, %d complex types, %d element declarations,\n// %d wildcards and %d identity constraints for %d schema versions.\n",
		len(t.simples), len(t.patterns), len(t.complexes), len(t.elements), len(t.wilds), len(t.idcs), len(g.c.Versions))
	b.WriteString("var tables = &schemaTables{\n")
	list := func(field, typ string, items []string) {
		fmt.Fprintf(&b, "\t%s: []%s{\n", field, typ)
		for i, it := range items {
			fmt.Fprintf(&b, "\t\t/* %d */ %s,\n", i, it)
		}
		b.WriteString("\t},\n")
	}
	list("simples", "simpleSpec", t.simples)
	list("patterns", "pattern", t.patterns)
	list("complexes", "complexSpec", t.complexes)
	list("elements", "elemSpec", t.elements)
	list("wildcards", "wildcardSpec", t.wilds)
	list("idcs", "idcSpec", t.idcs)
	list("versions", "versionSpec", versions)
	b.WriteString("}\n")
	return b.Bytes(), nil
}

func sortedQNames[V any](m map[xsdc.QName]V) []xsdc.QName {
	out := make([]xsdc.QName, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NS != out[j].NS {
			return out[i].NS < out[j].NS
		}
		return out[i].Local < out[j].Local
	})
	return out
}
