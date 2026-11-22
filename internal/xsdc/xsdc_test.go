// SPDX-License-Identifier: MIT

package xsdc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	nsDev  = "urn:test:dev"
	nsPrim = "urn:test:prim"
)

// testdata/corpus is a synthetic two-version schema set in the layout of
// the GSDML reference schemas (a DeviceProfile importing a Primitives
// schema importing xml.xsd), with every construct xsdc implements. It is
// valid for xmllint: `xmllint --noout --schema GSDML-DeviceProfile-v1.0.xsd`.
func corpus(t *testing.T) *Corpus {
	t.Helper()
	c, err := LoadCorpus("testdata/corpus")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLoadCorpus(t *testing.T) {
	c := corpus(t)
	if got := strings.Join(c.Versions, ","); got != "1.0,2.0" {
		t.Fatalf("versions %s", got)
	}
	s := c.Sets[0]
	if s.Version != "1.0" || s.Root != "GSDML-DeviceProfile-v1.0.xsd" || len(s.Documents) != 3 {
		t.Fatalf("set %q %q %v", s.Version, s.Root, s.Documents)
	}
	root := s.Elements[QName{nsDev, "Root"}]
	if root == nil || root.Complex == nil || !root.Complex.Anonymous() || root.Complex.Path != "Root" {
		t.Fatalf("Root: %+v", root)
	}
	if len(root.IDCs) != 3 || root.IDCs[1].Refer != root.IDCs[0] {
		t.Fatalf("identity constraints: %+v", root.IDCs)
	}
	sel := root.IDCs[1].Selector
	if len(sel) != 2 || !sel[1].Steps[1].Any || !sel[1].Steps[1].AnyNS {
		t.Fatalf("keyref selector %+v", sel)
	}
	if u := root.IDCs[2]; !u.Selector[0].Steps[1].Any || u.Selector[0].Steps[1].AnyNS || u.Selector[0].Steps[1].Name.NS != nsPrim || !u.Fields[0][0].Steps[0].Self {
		t.Fatalf("unique %+v", u)
	}
	if k := root.IDCs[0]; !k.Selector[0].Descendant || !k.Fields[0][0].Steps[0].Attr {
		t.Fatalf("key %+v", k)
	}
	head := s.Elements[QName{nsDev, "Head"}]
	if !head.Abstract || len(head.Substitutes) != 2 || head.Substitutes[0].Name.Local != "SubA" || head.Substitutes[1].Name.Local != "SubB" {
		t.Fatalf("substitution group: %+v", head)
	}
	if sb := s.Elements[QName{nsDev, "SubB"}]; sb.Complex != head.Complex {
		t.Fatalf("SubB takes the type of its head")
	}
	item := s.ComplexTypes[QName{nsDev, "ItemT"}]
	if item.Derivation != "extension" || item.Base.Name.Local != "NamedT" || len(item.Attrs) != 4 {
		t.Fatalf("ItemT %+v", item)
	}
	if item.Attrs[1].Name != (QName{XMLNS, "lang"}) || !item.Attrs[0].Required {
		t.Fatalf("ItemT attributes %+v %+v", item.Attrs[0], item.Attrs[1])
	}
	rv := s.ComplexTypes[QName{nsDev, "RestrictedValueT"}]
	if rv.Derivation != "restriction" || rv.Simple == nil || !rv.Attrs[0].Required {
		t.Fatalf("RestrictedValueT %+v", rv)
	}
	if m := s.ComplexTypes[QName{nsDev, "MixedT"}]; !m.Mixed || m.Content.Children[0].Wildcard.Process != "skip" {
		t.Fatalf("MixedT %+v", m)
	}
	u := s.SimpleTypes[QName{nsPrim, "UnionT"}]
	if u.Variety != Union || len(u.Members) != 3 || u.EffectiveWhiteSpace() != "" || u.BuiltinBase() != "" {
		t.Fatalf("UnionT %+v", u)
	}
	l := s.SimpleTypes[QName{nsPrim, "ListT"}]
	if l.Variety != List || l.Item.Name.Local != "ByteT" || l.EffectiveWhiteSpace() != "collapse" {
		t.Fatalf("ListT %+v", l)
	}
	id := s.SimpleTypes[QName{nsPrim, "IdT"}]
	if id.BuiltinBase() != "token" || id.EffectiveWhiteSpace() != "collapse" || *id.MaxLength != 32 || len(id.Patterns) != 1 {
		t.Fatalf("IdT %+v", id)
	}
	if c.Sets[1].SimpleTypes[QName{nsPrim, "IdT"}].MaxLength == nil || *c.Sets[1].SimpleTypes[QName{nsPrim, "IdT"}].MaxLength != 64 {
		t.Fatal("v2.0 IdT")
	}
	if s.SimpleTypes[QName{nsPrim, "ColorT"}].EffectiveWhiteSpace() != "preserve" {
		t.Fatal("ColorT whitespace")
	}
	if (QName{nsDev, "Root"}).String() != "{urn:test:dev}Root" || (QName{"", "x"}).String() != "x" {
		t.Fatal("QName.String")
	}
}

func TestVersionNumber(t *testing.T) {
	for v, want := range map[string]int{"1.0": 100, "2.35": 235, "2.4": 240, "2.41": 241, "2.3": 230} {
		if got := VersionNumber(v); got != want {
			t.Errorf("VersionNumber(%s) = %d, want %d", v, got, want)
		}
	}
}

func TestCompileAndShapes(t *testing.T) {
	c := corpus(t)
	for _, s := range c.Sets {
		ix, err := IndexOf(s)
		if err != nil {
			t.Fatal(err)
		}
		if len(ix.Complex) == 0 || len(ix.Simple) == 0 || len(ix.Element) == 0 {
			t.Fatal("empty index")
		}
		for comp, ct := range ix.Complex {
			if comp.String() == "" {
				t.Fatal("component name")
			}
			if ct.Content != nil {
				d, err := Compile(ct.Content)
				if err != nil {
					t.Fatalf("%s: %v", comp, err)
				}
				_ = d.Order()
				_ = Uses(ct.Content)
			}
			sh, err := ShapeOf(ct)
			if err != nil {
				t.Fatalf("%s: %v", comp, err)
			}
			for _, a := range sh.Attrs {
				if GoKind(a.Type) == "" {
					t.Errorf("%s @%s: no Go kind", comp, a.Name)
				}
			}
		}
		for comp, st := range ix.Simple {
			if GoKind(st) == "" {
				t.Errorf("%s: no Go kind", comp)
			}
			_ = EnumValues(st)
		}
	}
	s := c.Sets[0]
	items := s.ComplexTypes[QName{nsDev, "ItemsT"}]
	d, err := Compile(items.Content)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Order() {
		t.Error("ItemsT: a repeated choice of Item and Ref interleaves")
	}
	if !d.Final[0] == false || len(d.Next[0]) != 2 {
		t.Errorf("ItemsT DFA %+v", d)
	}
	item := s.ComplexTypes[QName{nsDev, "ItemT"}]
	d, err = Compile(item.Content)
	if err != nil {
		t.Fatal(err)
	}
	// Value{2,3} expands to three positions, then Note.
	if len(d.Positions) != 4 || d.Final[0] || d.Final[1] || !d.Final[2] || !d.Final[3] || d.Order() {
		t.Errorf("ItemT DFA positions %d final %v", len(d.Positions), d.Final)
	}
	sh, _ := ShapeOf(item)
	if len(sh.Elems) != 2 || !sh.Elems[0].Many || sh.Elems[1].Many || sh.Ordered {
		t.Errorf("ItemT shape %+v", sh)
	}
	rootT := s.Elements[QName{nsDev, "Root"}].Complex
	sh, _ = ShapeOf(rootT)
	if !sh.Any || !sh.AnyAttr || len(sh.Attrs) != 1 || sh.Attrs[0].Default == nil {
		t.Errorf("Root shape %+v", sh)
	}
	sh, _ = ShapeOf(s.ComplexTypes[QName{nsDev, "ValueT"}])
	if sh.Value == nil || len(sh.Attrs) != 1 {
		t.Errorf("ValueT shape %+v", sh)
	}
	sh, _ = ShapeOf(s.ComplexTypes[QName{nsDev, "MixedT"}])
	if !sh.Raw && !sh.Mixed {
		t.Errorf("MixedT shape %+v", sh)
	}
	kinds := map[string]string{
		"ByteT": "uint32", "ColorT": "enum", "ListT": "[]uint32", "IdT": "string",
	}
	for n, want := range kinds {
		if got := GoKind(s.SimpleTypes[QName{nsPrim, n}]); got != want {
			t.Errorf("GoKind(%s) = %s, want %s", n, got, want)
		}
	}
	head := s.ComplexTypes[QName{nsDev, "HeadT"}]
	want := map[string]string{"Num": "Integer", "Dec": "Decimal", "Flt": "float64", "Big": "uint64", "Neg": "int16", "Ok": "bool"}
	for _, u := range Uses(head.Content) {
		if w, ok := want[u.Element.Name.Local]; ok {
			if got := GoKind(u.Element.Simple); got != w {
				t.Errorf("GoKind(%s) = %s, want %s", u.Element.Name.Local, got, w)
			}
		}
	}
	if !HasEnums(s.SimpleTypes[QName{nsPrim, "ColorT"}]) || HasEnums(s.SimpleTypes[QName{nsPrim, "IdT"}]) {
		t.Error("HasEnums")
	}
	if got := EnumValues(s.SimpleTypes[QName{nsPrim, "ColorT"}]); strings.Join(got, ",") != "Red,Green" {
		t.Errorf("EnumValues %v", got)
	}
}

// TestAnalyze checks the deduplication: ItemT changes in 2.0 (a new
// element), IdT in its maxLength; ItemsT refers to ItemT, so it differs at
// the codec and full levels only by reference.
func TestAnalyze(t *testing.T) {
	c := corpus(t)
	counts := map[Level]map[string]int{}
	for _, lv := range []Level{Representation, Codec, Full} {
		v, err := Analyze(c, lv)
		if err != nil {
			t.Fatal(err)
		}
		if v.Level != lv || lv.String() == "" {
			t.Fatal("level")
		}
		counts[lv] = map[string]int{}
		for comp := range v.Classes {
			counts[lv][comp.Kind+":"+comp.Name.Local+comp.Path] = v.Count(comp)
		}
	}
	for _, lv := range []Level{Representation, Codec, Full} {
		if counts[lv]["complex:ItemT"] != 2 {
			t.Errorf("%s: ItemT has %d variants, want 2", lv, counts[lv]["complex:ItemT"])
		}
		if counts[lv]["complex:HeadT"] != 1 {
			t.Errorf("%s: HeadT has %d variants, want 1", lv, counts[lv]["complex:HeadT"])
		}
	}
	if counts[Representation]["simple:IdT"] != 1 {
		t.Errorf("representation: IdT has %d variants, want 1 (both are strings)", counts[Representation]["simple:IdT"])
	}
	if counts[Full]["simple:IdT"] != 2 {
		t.Errorf("full: IdT has %d variants, want 2 (maxLength differs)", counts[Full]["simple:IdT"])
	}
	if counts[Full]["complex:NamedT"] != 2 {
		t.Errorf("full: NamedT has %d variants, want 2 (it uses IdT)", counts[Full]["complex:NamedT"])
	}
}

func TestWildcardAllows(t *testing.T) {
	cases := []struct {
		w    Wildcard
		ns   string
		want bool
	}{
		{Wildcard{Any: true}, "", true},
		{Wildcard{Other: true, Target: "a"}, "a", false},
		{Wildcard{Other: true, Target: "a"}, "", false},
		{Wildcard{Other: true, Target: "a"}, "b", true},
		{Wildcard{Allowed: []string{"", "b"}}, "", true},
		{Wildcard{Allowed: []string{"b"}}, "c", false},
	}
	for i, c := range cases {
		if got := c.w.Allows(c.ns); got != c.want {
			t.Errorf("%d: Allows(%q) = %v", i, c.ns, got)
		}
	}
	for _, k := range []ParticleKind{Sequence, Choice, All, ElementParticle, AnyParticle} {
		if k.String() == "" {
			t.Error("ParticleKind.String")
		}
	}
}

// load writes a one-document schema and loads it.
func load(t *testing.T, body string) (*Set, error) {
	t.Helper()
	dir := t.TempDir()
	doc := `<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:t="urn:t" targetNamespace="urn:t" elementFormDefault="qualified" version="9.9">` + body + `</xsd:schema>`
	if err := os.WriteFile(filepath.Join(dir, "s.xsd"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(dir, "s.xsd")
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]string{
		"include":            `<xsd:include schemaLocation="o.xsd"/>`,
		"import no location": `<xsd:import namespace="urn:o"/>`,
		"import URL":         `<xsd:import namespace="urn:o" schemaLocation="http://example.com/o.xsd"/>`,
		"import parent":      `<xsd:import namespace="urn:o" schemaLocation="../o.xsd"/>`,
		"import missing":     `<xsd:import namespace="urn:o" schemaLocation="o.xsd"/>`,
		"redefine":           `<xsd:redefine schemaLocation="o.xsd"/>`,
		"top unnamed":        `<xsd:element type="xsd:string"/>`,
		"top unknown":        `<xsd:foo name="x"/>`,
		"duplicate":          `<xsd:element name="a" type="xsd:string"/><xsd:element name="a" type="xsd:string"/>`,
		"undeclared type":    `<xsd:element name="a" type="t:Nope"/>`,
		"undeclared prefix":  `<xsd:element name="a" type="q:Nope"/>`,
		"builtin":            `<xsd:element name="a" type="xsd:duration"/>`,
		"nillable":           `<xsd:element name="a" type="xsd:string" nillable="true"/>`,
		"simple no deriv":    `<xsd:simpleType name="s"/>`,
		"simple bad":         `<xsd:simpleType name="s"><xsd:sequence/></xsd:simpleType>`,
		"restriction 2 base": `<xsd:simpleType name="s"><xsd:restriction base="xsd:string"><xsd:simpleType><xsd:restriction base="xsd:string"/></xsd:simpleType></xsd:restriction></xsd:simpleType>`,
		"restriction none":   `<xsd:simpleType name="s"><xsd:restriction/></xsd:simpleType>`,
		"facet":              `<xsd:simpleType name="s"><xsd:restriction base="xsd:decimal"><xsd:totalDigits value="3"/></xsd:restriction></xsd:simpleType>`,
		"length":             `<xsd:simpleType name="s"><xsd:restriction base="xsd:string"><xsd:length value="x"/></xsd:restriction></xsd:simpleType>`,
		"list none":          `<xsd:simpleType name="s"><xsd:list/></xsd:simpleType>`,
		"union none":         `<xsd:simpleType name="s"><xsd:union/></xsd:simpleType>`,
		"union bad":          `<xsd:simpleType name="s"><xsd:union memberTypes="xsd:string"><xsd:sequence/></xsd:union></xsd:simpleType>`,
		"two groups":         `<xsd:complexType name="c"><xsd:sequence/><xsd:choice/></xsd:complexType>`,
		"attributeGroup":     `<xsd:complexType name="c"><xsd:attributeGroup ref="t:g"/></xsd:complexType>`,
		"complex unknown":    `<xsd:complexType name="c"><xsd:foo/></xsd:complexType>`,
		"cc empty":           `<xsd:complexType name="c"><xsd:complexContent/></xsd:complexType>`,
		"cc bad":             `<xsd:complexType name="c"><xsd:complexContent><xsd:list/></xsd:complexContent></xsd:complexType>`,
		"self derived":       `<xsd:complexType name="c"><xsd:complexContent><xsd:extension base="t:c"/></xsd:complexContent></xsd:complexType>`,
		"sc from complex":    `<xsd:complexType name="e"/><xsd:complexType name="c"><xsd:simpleContent><xsd:extension base="t:e"/></xsd:simpleContent></xsd:complexType>`,
		"cc from simple":     `<xsd:complexType name="c"><xsd:complexContent><xsd:extension base="xsd:string"/></xsd:complexContent></xsd:complexType>`,
		"sc facet":           `<xsd:complexType name="v"><xsd:simpleContent><xsd:extension base="xsd:string"/></xsd:simpleContent></xsd:complexType><xsd:complexType name="c"><xsd:simpleContent><xsd:restriction base="t:v"><xsd:maxLength value="1"/></xsd:restriction></xsd:simpleContent></xsd:complexType>`,
		"redeclared attr":    `<xsd:complexType name="b"><xsd:attribute name="a" type="xsd:string"/></xsd:complexType><xsd:complexType name="c"><xsd:complexContent><xsd:extension base="t:b"><xsd:attribute name="a" type="xsd:string"/></xsd:extension></xsd:complexContent></xsd:complexType>`,
		"attr undeclared":    `<xsd:complexType name="c"><xsd:attribute ref="t:nope"/></xsd:complexType>`,
		"xml attr":           `<xsd:complexType name="c" xmlns:xml="http://www.w3.org/XML/1998/namespace"><xsd:attribute ref="xml:lang"/></xsd:complexType>`,
		"minOccurs":          `<xsd:complexType name="c"><xsd:sequence><xsd:element name="a" minOccurs="x"/></xsd:sequence></xsd:complexType>`,
		"maxOccurs":          `<xsd:complexType name="c"><xsd:sequence><xsd:element name="a" maxOccurs="x"/></xsd:sequence></xsd:complexType>`,
		"max below min":      `<xsd:complexType name="c"><xsd:sequence><xsd:element name="a" minOccurs="2" maxOccurs="1"/></xsd:sequence></xsd:complexType>`,
		"group no ref":       `<xsd:complexType name="c"><xsd:sequence><xsd:group/></xsd:sequence></xsd:complexType>`,
		"group undeclared":   `<xsd:complexType name="c"><xsd:group ref="t:g"/></xsd:complexType>`,
		"group self":         `<xsd:group name="g"><xsd:sequence><xsd:group ref="t:g"/></xsd:sequence></xsd:group><xsd:complexType name="c"><xsd:group ref="t:g"/></xsd:complexType>`,
		"group two":          `<xsd:group name="g"><xsd:sequence/><xsd:choice/></xsd:group><xsd:complexType name="c"><xsd:group ref="t:g"/></xsd:complexType>`,
		"particle":           `<xsd:complexType name="c"><xsd:sequence><xsd:attribute name="a"/></xsd:sequence></xsd:complexType>`,
		"process":            `<xsd:complexType name="c"><xsd:sequence><xsd:any processContents="x"/></xsd:sequence></xsd:complexType>`,
		"element ref":        `<xsd:complexType name="c"><xsd:sequence><xsd:element ref="t:nope"/></xsd:sequence></xsd:complexType>`,
		"element child":      `<xsd:element name="a"><xsd:foo/></xsd:element>`,
		"keyref refer":       `<xsd:element name="a"><xsd:keyref name="r" refer="t:nope"><xsd:selector xpath="."/><xsd:field xpath="."/></xsd:keyref></xsd:element>`,
		"keyref to keyref":   `<xsd:element name="a"><xsd:keyref name="r" refer="t:r2"><xsd:selector xpath="."/><xsd:field xpath="."/></xsd:keyref><xsd:keyref name="r2" refer="t:r"><xsd:selector xpath="."/><xsd:field xpath="."/></xsd:keyref></xsd:element>`,
		"keyref fields":      `<xsd:element name="a"><xsd:key name="k"><xsd:selector xpath="."/><xsd:field xpath="@a"/><xsd:field xpath="@b"/></xsd:key><xsd:keyref name="r" refer="t:k"><xsd:selector xpath="."/><xsd:field xpath="@a"/></xsd:keyref></xsd:element>`,
		"idc empty":          `<xsd:element name="a"><xsd:key name="k"/></xsd:element>`,
		"xpath empty":        `<xsd:element name="a"><xsd:key name="k"><xsd:selector xpath=""/><xsd:field xpath="."/></xsd:key></xsd:element>`,
		"xpath attr":         `<xsd:element name="a"><xsd:key name="k"><xsd:selector xpath="."/><xsd:field xpath="@a/b"/></xsd:key></xsd:element>`,
		"xpath @*":           `<xsd:element name="a"><xsd:key name="k"><xsd:selector xpath="."/><xsd:field xpath="@*"/></xsd:key></xsd:element>`,
		"xpath subset":       `<xsd:element name="a"><xsd:key name="k"><xsd:selector xpath="a[1]"/><xsd:field xpath="."/></xsd:key></xsd:element>`,
		"xpath prefix":       `<xsd:element name="a"><xsd:key name="k"><xsd:selector xpath="q:a"/><xsd:field xpath="."/></xsd:key></xsd:element>`,
		"all":                `<xsd:complexType name="c"><xsd:all><xsd:element name="a"/></xsd:all></xsd:complexType>`,
		"not deterministic":  `<xsd:complexType name="c"><xsd:choice><xsd:element name="a"/><xsd:element name="a"/></xsd:choice></xsd:complexType>`,
		"not xsd":            `</xsd:schema><x/><xsd:schema>`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			s, err := load(t, body)
			if err == nil {
				// Some constructs are refused only when compiled.
				err = compileAll(s)
			}
			if err == nil {
				t.Fatal("loaded")
			}
		})
	}
}

func compileAll(s *Set) error {
	ix, err := IndexOf(s)
	if err != nil {
		return err
	}
	for _, ct := range ix.Complex {
		if ct.Content != nil {
			if _, err := Compile(ct.Content); err != nil {
				return err
			}
		}
	}
	return nil
}

func TestLoadCorpusErrors(t *testing.T) {
	if _, err := LoadCorpus(t.TempDir()); err == nil {
		t.Error("an empty directory loaded")
	}
	if _, err := LoadCorpus(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("a missing directory loaded")
	}
	dir := t.TempDir()
	for _, n := range []string{"GSDML-DeviceProfile-v2.4.xsd", "GSDML-DeviceProfile-V2.40.xsd"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(`<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema"/>`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := LoadCorpus(dir); err == nil {
		t.Error("two schemas for one version loaded")
	}
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "GSDML-DeviceProfile-v1.0.xsd"), []byte(`<x`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCorpus(dir); err == nil {
		t.Error("a malformed schema loaded")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "nope"), "s.xsd"); err == nil {
		t.Error("Load from a missing directory")
	}
}

// TestLoadValid covers constructs the corpus does not: named groups of
// choices, ##targetNamespace and ##local wildcards, a simple type with
// every facet, global attributes.
func TestLoadValid(t *testing.T) {
	s, err := load(t, `
<xsd:attribute name="g" type="xsd:int" default="1"/>
<xsd:simpleType name="s"><xsd:restriction base="xsd:decimal">
	<xsd:minExclusive value="0"/><xsd:maxExclusive value="10"/><xsd:whiteSpace value="collapse"/>
</xsd:restriction></xsd:simpleType>
<xsd:simpleType name="n"><xsd:restriction base="xsd:normalizedString"><xsd:length value="3"/></xsd:restriction></xsd:simpleType>
<xsd:simpleType name="i"><xsd:restriction base="xsd:integer"><xsd:minInclusive value="-5"/><xsd:maxInclusive value="5"/></xsd:restriction></xsd:simpleType>
<xsd:simpleType name="l"><xsd:list><xsd:simpleType><xsd:restriction base="xsd:int"/></xsd:simpleType></xsd:list></xsd:simpleType>
<xsd:simpleType name="r"><xsd:restriction base="t:l"><xsd:maxLength value="4"/></xsd:restriction></xsd:simpleType>
<xsd:group name="G"><xsd:choice><xsd:element name="a" type="xsd:string"/><xsd:element name="b" type="xsd:string"/></xsd:choice></xsd:group>
<xsd:complexType name="c">
	<xsd:sequence>
		<xsd:group ref="t:G" maxOccurs="unbounded"/>
		<xsd:any namespace="##local urn:x" processContents="strict" minOccurs="0"/>
	</xsd:sequence>
	<xsd:attribute ref="t:g"/>
	<xsd:attribute name="loc"><xsd:simpleType><xsd:restriction base="xsd:string"/></xsd:simpleType></xsd:attribute>
	<xsd:attribute name="x" type="t:s" use="prohibited"/>
	<xsd:anyAttribute/>
</xsd:complexType>
<xsd:complexType name="tns"><xsd:sequence><xsd:any namespace="##targetNamespace"/></xsd:sequence></xsd:complexType>
<xsd:complexType name="d"><xsd:complexContent><xsd:restriction base="t:c">
	<xsd:sequence><xsd:group ref="t:G" maxOccurs="unbounded"/></xsd:sequence>
</xsd:restriction></xsd:complexContent></xsd:complexType>
<xsd:element name="e" type="t:d" fixed=""/>
<xsd:element name="f" type="t:i"/>`)
	if err != nil {
		t.Fatal(err)
	}
	if err := compileAll(s); err != nil {
		t.Fatal(err)
	}
	c := s.ComplexTypes[QName{"urn:t", "c"}]
	w := c.Content.Children[1].Wildcard
	if tw := s.ComplexTypes[QName{"urn:t", "tns"}].Content.Children[0].Wildcard; !tw.Allows("urn:t") || tw.Allows("") {
		t.Errorf("##targetNamespace %+v", tw)
	}
	if w == nil || w.Allows("urn:t") || !w.Allows("") || !w.Allows("urn:x") || w.Allows("urn:y") || c.AnyAttr == nil || !c.AnyAttr.Any {
		t.Fatalf("wildcards %+v %+v", w, c.AnyAttr)
	}
	d, err := Compile(c.Content)
	if err != nil || !d.Order() {
		t.Fatalf("c: a repeated choice interleaves: %v", err)
	}
	if got := GoKind(s.SimpleTypes[QName{"urn:t", "i"}]); got != "int64" {
		t.Errorf("GoKind(i) = %s, want int64 (bounded integer)", got)
	}
	if got := GoKind(s.SimpleTypes[QName{"urn:t", "s"}]); got != "Decimal" {
		t.Errorf("GoKind(s) = %s", got)
	}
	if got := GoKind(s.SimpleTypes[QName{"urn:t", "r"}]); got != "[]int32" {
		t.Errorf("GoKind(r) = %s", got)
	}
	if s.SimpleTypes[QName{"urn:t", "n"}].EffectiveWhiteSpace() != "replace" {
		t.Error("normalizedString whitespace")
	}
	sh, err := ShapeOf(s.ComplexTypes[QName{"urn:t", "d"}])
	if err != nil || len(sh.Elems) != 2 || !sh.Ordered {
		t.Fatalf("d shape %+v %v", sh, err)
	}
	if e := s.Elements[QName{"urn:t", "e"}]; e.Fixed == nil {
		t.Error("fixed")
	}
}
