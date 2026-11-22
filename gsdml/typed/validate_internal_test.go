// SPDX-License-Identifier: MIT

package typed

import (
	"bytes"
	"encoding/xml"
	"errors"
	"math"
	"strings"
	"testing"

	profinet "github.com/otfabric/go-profinet"
)

// The GSDML schemas use only part of what the validator evaluates: no
// abstract declarations, no attribute wildcards with declarations, no
// simple content with fixed attributes, no lax element wildcards. These
// tests run the validator on synthetic tables that have all of it.

const nsT = "urn:t"

func xChildOf(local string) xpath {
	return xpath{steps: []xstep{{kind: xChild, ns: nsT, local: local}}}
}

func xAttrOf(local string) xpath { return xpath{steps: []xstep{{kind: xAttr, local: local}}} }

func validatorTables() *schemaTables {
	sc := &schemaTables{}
	sc.simples = []simpleSpec{
		/*0*/ {name: "Str", variety: vAtomic, builtin: bString, ws: wsPreserve, base: -1, item: -1},
		/*1*/ {name: "Int", variety: vAtomic, builtin: bInt, ws: wsCollapse, base: -1, item: -1},
		/*2*/ {name: "Bool", variety: vAtomic, builtin: bBoolean, ws: wsCollapse, base: -1, item: -1},
		/*3*/ {name: "Dbl", variety: vAtomic, builtin: bDouble, ws: wsCollapse, base: -1, item: -1},
		/*4*/ {name: "IntList", variety: vList, base: -1, item: 1},
		/*5*/ {name: "Dec", variety: vAtomic, builtin: bDecimal, ws: wsCollapse, base: -1, item: -1},
	}
	sc.wildcards = []wildcardSpec{
		/*0*/ {mode: wOther, target: nsT, process: pStrict},
		/*1*/ {mode: wList, allowed: []string{"urn:lax"}, process: pLax},
		/*2*/ {mode: wAny, process: pSkip},
		/*3*/ {mode: wList, allowed: []string{"urn:skip", ""}, process: pSkip},
		/*4*/ {mode: wOther, target: nsT, process: pStrict},
	}
	sc.elements = []elemSpec{
		/*0*/ {ns: nsT, local: "Root", complex: 0, simple: -1, idcs: []int32{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}},
		/*1*/ {ns: nsT, local: "Item", complex: 1, simple: -1},
		/*2*/ {ns: nsT, local: "Fixed", complex: -1, simple: 1, flags: fFixed, fixed: "7"},
		/*3*/ {ns: nsT, local: "Def", complex: -1, simple: 1, flags: fDefault, def: "5"},
		/*4*/ {ns: nsT, local: "Abs", complex: 2, simple: -1, flags: fAbstract, substitutes: []int32{5}},
		/*5*/ {ns: nsT, local: "Sub", complex: 2, simple: -1},
		/*6*/ {ns: nsT, local: "Simple", complex: 3, simple: -1},
		/*7*/ {ns: "urn:g", local: "G", complex: -1, simple: 1},
		/*8*/ {ns: nsT, local: "AbsType", complex: 4, simple: -1},
		/*9*/ {ns: nsT, local: "Empty", complex: 5, simple: -1},
		/*10*/ {ns: nsT, local: "Mixed", complex: 6, simple: -1},
		/*11*/ {ns: nsT, local: "FixedS", complex: -1, simple: 4, flags: fFixed, fixed: "1 2"},
		/*12*/ {ns: nsT, local: "V", complex: -1, simple: 1},
		/*13*/ {ns: nsT, local: "Ref", complex: 7, simple: -1},
		/*14*/ {ns: "urn:lax", local: "L", complex: -1, simple: 1},
		/*15*/ {ns: nsT, local: "Need", complex: 8, simple: -1},
		/*16*/ {ns: nsT, local: "Plain", complex: -1, simple: 1},
	}
	var rootTrans []dfaTrans
	for _, e := range []int32{1, 2, 3, 4, 6, 8, 9, 10, 11, 13, 15, 16} {
		rootTrans = append(rootTrans, dfaTrans{elem: e, wild: -1, next: 0})
	}
	for _, w := range []int32{1, 3, 4} {
		rootTrans = append(rootTrans, dfaTrans{elem: -1, wild: w, next: 0})
	}
	sc.complexes = []complexSpec{
		/*0*/ {name: "RootT", simple: -1, attrs: []attrSpec{{local: "ra", simple: 1}}, anyAttr: 0, dfa: []dfaState{{final: true, trans: rootTrans}}},
		/*1*/ {name: "ItemT", simple: -1, anyAttr: 2, attrs: []attrSpec{
			{local: "id", simple: 1, flags: fRequired},
			{local: "kind", simple: 0, flags: fFixed, fixed: "k"},
			{local: "flag", simple: 2, flags: fDefault, def: "true"},
			{local: "f", simple: 3},
			{local: "d", simple: 5},
		}, dfa: []dfaState{{final: true, trans: []dfaTrans{{elem: 12, wild: -1, next: 0}}}}},
		/*2*/ {name: "S", simple: -1, anyAttr: -1},
		/*3*/ {name: "SimpleC", flags: cSimpleContent, simple: 1, anyAttr: -1, attrs: []attrSpec{{local: "u", simple: 0, flags: fFixed, fixed: "x"}}},
		/*4*/ {name: "AbsCT", flags: cAbstract, simple: -1, anyAttr: -1},
		/*5*/ {name: "EmptyT", simple: -1, anyAttr: -1},
		/*6*/ {name: "MixedT", flags: cMixed, simple: -1, anyAttr: -1},
		/*7*/ {name: "RefT", simple: -1, anyAttr: -1, attrs: []attrSpec{{local: "to", simple: 1}}},
		/*8*/ {name: "NeedT", simple: -1, anyAttr: -1, dfa: []dfaState{{trans: []dfaTrans{{elem: 12, wild: -1, next: 1}}}, {final: true}}},
	}
	item := []xpath{xChildOf("Item")}
	sc.idcs = []idcSpec{
		/*0*/ {kind: idcKey, name: "K", selector: item, fields: [][]xpath{{xAttrOf("id")}}},
		/*1*/ {kind: idcUnique, name: "U", selector: item, fields: [][]xpath{{xAttrOf("flag")}, {xAttrOf("kind")}}},
		/*2*/ {kind: idcKeyref, name: "R", refer: 0, selector: []xpath{{descendant: true, steps: []xstep{{kind: xChild, ns: nsT, local: "Ref"}}}}, fields: [][]xpath{{xAttrOf("to")}}},
		/*3*/ {kind: idcUnique, name: "UV", selector: item, fields: [][]xpath{{xChildOf("V")}, {xAttrOf("f")}, {xAttrOf("d")}}},
		/*4*/ {kind: idcUnique, name: "US", selector: []xpath{xChildOf("Simple")}, fields: [][]xpath{{{steps: []xstep{{kind: xSelf}}}}}},
		/*5*/ {kind: idcUnique, name: "UA", selector: []xpath{{steps: []xstep{{kind: xAnyChild}}}}, fields: [][]xpath{{xAttrOf("id")}}},
		/*6*/ {kind: idcUnique, name: "UN", selector: []xpath{{steps: []xstep{{kind: xAnyChildNS, ns: nsT}}}}, fields: [][]xpath{{xAttrOf("zz")}}},
		/*7*/ {kind: idcUnique, name: "UX", selector: item, fields: [][]xpath{{xAttrOf("extra")}}},
		/*8*/ {kind: idcKey, name: "KV", selector: item, fields: [][]xpath{{xChildOf("V")}}},
		/*9*/ {kind: idcUnique, name: "UL", selector: []xpath{xChildOf("FixedS")}, fields: [][]xpath{{{steps: []xstep{{kind: xSelf}}}}}},
	}
	sc.versions = []versionSpec{{version: "t", globals: []int32{0, 7, 14}, attrs: []attrSpec{{ns: "urn:g", local: "ga", simple: 1}}}}
	return sc
}

const validatorRoot = `<Root xmlns="urn:t" xmlns:g="urn:g" xmlns:l="urn:lax" xmlns:s="urn:skip" xmlns:o="urn:o" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"`

func validateSynthetic(t *testing.T, doc string) error {
	t.Helper()
	n, err := ParseNode([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return validateWith(validatorTables(), n, "t")
}

func TestValidatorSynthetic(t *testing.T) {
	valid := validatorRoot + ` ra="1" g:ga="3">
  <Item id="1" f="1.5" d="2.50" extra="e"><V>1</V></Item>
  <Item id="2" flag="false" kind="k" s:any="z"><V>2</V></Item>
  <Fixed/><Fixed>7</Fixed><Fixed> 7 </Fixed>
  <Def/>
  <Sub/>
  <Simple u="x">5</Simple>
  <Empty/>
  <Mixed>text</Mixed>
  <FixedS>1  2</FixedS>
  <Ref to="1"/>
  <Need><V>3</V></Need>
  <Plain>4</Plain>
  <l:L>6</l:L><l:Unknown/><s:Z><x/></s:Z><Local xmlns=""/><g:G>8</g:G>
</Root>`
	if err := validateSynthetic(t, valid); err != nil {
		t.Fatalf("valid: %v", err)
	}

	invalid := validatorRoot + ` ra="x" g:ga="y" o:oa="1" bogus="1">stray
  <Item kind="z"><V>1</V></Item>
  <Item id="5"><V>1</V><V>2</V></Item>
  <Item id="5"/>
  <Fixed>8</Fixed>
  <FixedS>1 3</FixedS>
  <Abs/>
  <AbsType/>
  <Simple u="y">x<V>1</V></Simple>
  <Simple>z</Simple>
  <Empty> </Empty>
  <Empty><V>1</V></Empty>
  <Mixed>t<V>1</V></Mixed>
  <Need/>
  <Plain a="1"><V>1</V></Plain>
  <Plain xsi:type="x">1</Plain>
  <Ref to="99"/>
  <l:L>no</l:L>
  <o:X/>
  <Bogus/>
</Root>`
	err := validateSynthetic(t, invalid)
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("invalid: %v", err)
	}
	var got []string
	for _, i := range se.Issues {
		got = append(got, i.String())
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		`attribute ra: `,
		`attribute ga: `,
		`attribute oa: no declaration for the wildcard (strict)`,
		`attribute bogus is not allowed`,
		`character content is not allowed in element Root`,
		`missing required attribute id`,
		`attribute kind: the value "z" is not the fixed value "k"`,
		`key K: a field selects no value`,
		`key KV: a field selects more than one value`,
		`key K: duplicate value "5"`,
		`/Root/Fixed: the value "8" is not the fixed value "7"`,
		`/Root/FixedS: the value "1 3" is not the fixed value "1 2"`,
		`element Abs is abstract`,
		`complex type AbsCT is abstract`,
		`attribute u: the value "y" is not the fixed value "x"`,
		`element Simple has simple content, not element V`,
		`/Root/Simple: "z"`,
		`character content is not allowed: element Empty has empty content`,
		`element Empty has empty content, not element V`,
		`element Mixed has empty content, not element V`,
		`missing child element (expected: V)`,
		`attribute a is not allowed: element Plain has a simple type`,
		`element Plain has a simple type and no element content, not V`,
		`xsi:type is not supported`,
		`keyref R: no K with value "99"`,
		`/Root/L: "no"`,
		`no declaration for element {urn:o}X (strict wildcard)`,
		`element {urn:t}Bogus is not expected here (expected: Item, Fixed, Def, Abs, Simple, AbsType, Empty, Mixed, FixedS, Ref, Need, Plain, (any element), (any element), (any element))`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no issue %q in:\n%s", want, all)
		}
	}

	if err := validateSynthetic(t, `<Other xmlns="urn:t"/>`); err == nil || !strings.Contains(err.Error(), "no declaration for the root element {urn:t}Other") {
		t.Fatalf("root: %v", err)
	}
	n, _ := ParseNode([]byte(`<Root xmlns="urn:t"/>`))
	if err := validateWith(validatorTables(), n, "nope"); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("version: %v", err)
	}
}

func TestWildcardAllowsModes(t *testing.T) {
	cases := []struct {
		w    wildcardSpec
		ns   string
		want bool
	}{
		{wildcardSpec{mode: wAny}, "urn:x", true},
		{wildcardSpec{mode: wAny}, "", true},
		{wildcardSpec{mode: wOther, target: "urn:t"}, "urn:x", true},
		{wildcardSpec{mode: wOther, target: "urn:t"}, "urn:t", false},
		{wildcardSpec{mode: wOther, target: "urn:t"}, "", false},
		{wildcardSpec{mode: wList, allowed: []string{"a", ""}}, "", true},
		{wildcardSpec{mode: wList, allowed: []string{"a"}}, "b", false},
	}
	for _, c := range cases {
		if got := c.w.allows(c.ns); got != c.want {
			t.Errorf("%+v allows(%q) = %v", c.w, c.ns, got)
		}
	}
}

func TestSelectNodesAttrStep(t *testing.T) {
	n, err := ParseNode([]byte(`<a x="1"><b/></a>`))
	if err != nil {
		t.Fatal(err)
	}
	got := selectNodes(n, []xpath{{steps: []xstep{{kind: xAttr, local: "x"}}}})
	if len(got) != 1 || got[0] != n {
		t.Fatalf("%v", got)
	}
}

func TestCanonical(t *testing.T) {
	v := &validator{sc: validatorTables()}
	for _, c := range []struct {
		si       int32
		raw, out string
	}{
		{-1, "x", "?x"},
		{1, " +05 ", "d5"},
		{1, "x5", "sx5"},
		{3, "1.50", "f1.5"},
		{3, "x", "sx"},
		{2, "1", "btrue"},
		{4, " 1  2 ", "s1 2"},
		{0, " a ", "s a "},
	} {
		if got := v.canonical(c.si, c.raw); got != c.out {
			t.Errorf("canonical(%d, %q) = %q, want %q", c.si, c.raw, got, c.out)
		}
	}
}

func TestLexicalNumbers(t *testing.T) {
	if i, err := Integer(" +42 ").Int64(); err != nil || i != 42 {
		t.Fatal(i, err)
	}
	if _, err := Integer("99999999999999999999").Int64(); err == nil {
		t.Fatal("no range error")
	}
	if b, ok := Integer("99999999999999999999").Big(); !ok || b.String() != "99999999999999999999" {
		t.Fatal(b, ok)
	}
	if r, ok := Decimal("+2.50").Rat(); !ok || r.RatString() != "5/2" {
		t.Fatal(r, ok)
	}
	if f, err := Decimal("2.5").Float64(); err != nil || f != 2.5 {
		t.Fatal(f, err)
	}
	for _, c := range []struct {
		v    string
		good bool
	}{{"12", true}, {"-1", true}, {"1.5", false}} {
		if _, err := parseLexical[Integer](c.v); (err == nil) != c.good {
			t.Errorf("Integer %q: %v", c.v, err)
		}
	}
	for _, c := range []struct {
		v    string
		good bool
	}{{"1.5", true}, {".5", true}, {"1e5", false}} {
		if _, err := parseLexical[Decimal](c.v); (err == nil) != c.good {
			t.Errorf("Decimal %q: %v", c.v, err)
		}
	}
}

func TestValueParsers(t *testing.T) {
	if _, err := parseBoolValue("yes"); err == nil {
		t.Fatal("bool")
	}
	if _, err := parseUintValue("-1", 8); err == nil {
		t.Fatal("uint")
	}
	if _, err := parseIntValue("x", 8); err == nil {
		t.Fatal("int")
	}
	if _, err := parseUintN[uint8]("256"); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatal("uint8", err)
	}
	if _, err := parseIntN[int8]("-129"); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatal("int8", err)
	}
	if _, err := parseXSDFloat("1e"); err == nil {
		t.Fatal("float")
	}
	if f, err := parseXSDFloat("1e999"); err != nil || !math.IsInf(f, 1) {
		t.Fatal("float range", f, err)
	}
	for f, s := range map[float64]string{math.Inf(1): "INF", math.Inf(-1): "-INF", math.NaN(): "NaN", 1.5: "1.5"} {
		if got := formatFloat(f); got != s {
			t.Errorf("formatFloat(%v) = %q", f, got)
		}
	}
}

func TestBuildErrors(t *testing.T) {
	n := &Node{Name: xml.Name{Local: "E"}, Line: 3}
	a := xml.Attr{Name: xml.Name{Local: "x"}, Value: "v"}
	var b builder
	if got := attrValue(&b, n, a, parseUintN[uint8]); got != 0 || b.err == nil || b.err.Error() != `typed: line 3: E@x: "v" is not an unsigned 64-bit integer` {
		t.Fatalf("attrValue: %v", b.err)
	}
	if !errors.Is(b.err, profinet.ErrMalformed) {
		t.Fatal("not ErrMalformed")
	}
	b = builder{}
	n.Children = []*Node{{Text: "q"}}
	n.Line = 0
	textValue(&b, n, parseBool)
	if b.err == nil || b.err.Error() != `typed: E: "q" is not a boolean` {
		t.Fatalf("textValue: %v", b.err)
	}
	if got := effectiveOrder([]pos{{f: 5}}, []int{1, 2}); len(got) != 3 || got[2] != (pos{f: 1, i: 1}) {
		t.Fatalf("effectiveOrder: %v", got)
	}
	for _, o := range [][]pos{{{f: 0, i: 1}}, {{f: 0}}} {
		if validOrder(o, []int{1, 1}) {
			t.Fatalf("validOrder(%v)", o)
		}
	}
	defer func() {
		if recover() == nil {
			t.Fatal("position: no panic")
		}
	}()
	position(0x10000, 1)
}

func TestEscapes(t *testing.T) {
	var b bytes.Buffer
	escapeText(&b, "a<b>&c\r\"")
	if b.String() != "a&lt;b&gt;&amp;c&#xD;\"" {
		t.Fatalf("text %q", b.String())
	}
	b.Reset()
	escapeAttr(&b, "<&\"\t\n\r>")
	if b.String() != "&lt;&amp;&quot;&#x9;&#xA;&#xD;>" {
		t.Fatalf("attr %q", b.String())
	}
}

func TestEncodeNodeVerbatim(t *testing.T) {
	e := newEncoder()
	e.node(&Node{Text: "a<b"})
	if !strings.Contains(e.b.String(), "a&lt;b") {
		t.Fatalf("%q", e.b.String())
	}
}

func TestCharsetReader(t *testing.T) {
	r, err := charsetReader("UTF-8", strings.NewReader("x"))
	if err != nil || r == nil {
		t.Fatal(err)
	}
	if _, err := charsetReader("latin1", errReader{}); err == nil {
		t.Fatal("read error not returned")
	}
	if _, err := charsetReader("koi8-r", strings.NewReader("x")); err == nil {
		t.Fatal("unknown charset accepted")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("broken") }

func TestNames(t *testing.T) {
	for _, c := range []struct {
		s     string
		colon bool
		want  bool
	}{
		{"a:b", false, false}, {"a:b", true, true}, {"Àx", false, true}, {"1a", false, false}, {"à", false, true}, {"က0", false, true},
	} {
		if got := isName(c.s, c.colon); got != c.want {
			t.Errorf("isName(%q, %v) = %v", c.s, c.colon, got)
		}
	}
	if isNameStart('-') || !isNameStart(0x10000) {
		t.Fatal("isNameStart")
	}
}

func TestInternalSubset(t *testing.T) {
	for d, want := range map[string]bool{"ELEMENT a": false, `DOCTYPE a [<!ENTITY x "y">]`: true, `DOCTYPE a SYSTEM "a[1]"`: false, `DOCTYPE a SYSTEM 'b'`: false} {
		if got := internalSubset(xml.Directive(d)); got != want {
			t.Errorf("internalSubset(%q) = %v", d, got)
		}
	}
}

func TestParseNodeXMLNSElement(t *testing.T) {
	if _, err := ParseNode([]byte(`<xmlns:a xmlns:xmlns="urn:x"/>`)); err == nil {
		t.Fatal("xmlns element prefix accepted")
	}
}
