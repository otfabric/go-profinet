// SPDX-License-Identifier: MIT

package xsd

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestLoadDir(t *testing.T) {
	s, err := LoadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	item := s.Elements["Item"]
	if item == nil {
		t.Fatal("no Item")
	}
	if !item.Attributes["ID"].Required || item.Attributes["Note"].Required || item.Attributes["Kind"].Type != "token" {
		t.Fatalf("Item attributes %+v", item.Attributes)
	}
	want := map[string]bool{"Name": true, "Info": true, "Leaf": true, "Nested": true}
	if !reflect.DeepEqual(item.Children, want) {
		t.Fatalf("Item children %v", item.Children)
	}
	if a := item.Attributes["Width"]; a.Required || a.Default != "1" || item.Attributes["Kind"].Default != "" {
		t.Fatalf("Item Width %+v, Kind %+v", a, item.Attributes["Kind"])
	}
	if !s.Elements["Nested"].Attributes["Depth"].Required {
		t.Fatal("Nested Depth")
	}
	if _, ok := s.Elements["Leaf"].Attributes["Lang"]; !ok {
		t.Fatal("Leaf Lang")
	}
	if r := s.Elements["Root"]; !r.Children["Item"] || !r.Children["Self"] {
		t.Fatalf("Root %+v", r)
	}
}

func TestErrors(t *testing.T) {
	if _, err := LoadDir(t.TempDir()); err == nil {
		t.Fatal("empty dir")
	}
	if _, err := Parse(strings.NewReader("<notschema/>")); err == nil {
		t.Fatal("not a schema")
	}
	if _, err := Parse(strings.NewReader("<xs:schema")); err == nil {
		t.Fatal("truncated")
	}
	dir := t.TempDir()
	_ = os.WriteFile(dir+"/bad.xsd", []byte("<x/>"), 0o644)
	if _, err := LoadDir(dir); err == nil {
		t.Fatal("bad file")
	}
	if _, err := LoadDir(dir + "/missing"); err == nil {
		t.Fatal("missing dir")
	}
}

// TestLoadRoot follows the include and imports of a root document, lists
// the one that is not there, records each file once with its digest and
// version, and resolves the simple types and the attribute uses.
func TestLoadRoot(t *testing.T) {
	s, err := LoadRoot("testdata/root", "root.xsd")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Files) != 2 || s.Files[0].Name != "root.xsd" || s.Files[1].Name != "types.xsd" || s.Files[0].Version != "9.1" || s.Files[1].TargetNamespace != "urn:t" || len(s.Files[0].SHA256) != 64 {
		t.Fatalf("files %+v", s.Files)
	}
	if !reflect.DeepEqual(s.Missing, []string{"gone.xsd"}) {
		t.Fatalf("missing %v", s.Missing)
	}
	ref := s.SimpleTypes["RefTypeT"]
	if ref == nil || ref.Base != "string" || !reflect.DeepEqual(ref.Enum, []string{"Unsigned8", "Old16", "Unsigned16", "Padded "}) {
		t.Fatalf("RefTypeT %+v", ref)
	}
	if !reflect.DeepEqual(ref.Notes, map[string]string{"Old16": `obsolete, superseded by "Unsigned16"`}) {
		t.Fatalf("notes %v", ref.Notes)
	}
	for _, tc := range []struct {
		name string
		want []string
	}{
		{"AliasT", []string{"Unsigned8", "Old16", "Unsigned16", "Padded "}},
		{"gsdml:ItemTypeT", []string{"Unsigned8", "Old16", "Unsigned16", "Padded ", "Bit"}},
	} {
		if got, ok := s.Enumeration(tc.name); !ok || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v %v", tc.name, got, ok)
		}
	}
	for _, n := range []string{"FreeT", "NoSuchT"} {
		if got, ok := s.Enumeration(n); ok || got != nil {
			t.Errorf("%s: %v %v", n, got, ok)
		}
	}
	want := []Use{
		{"Input", "DataItem", "DataType", "ItemTypeT"},
		{"Input", "Diag", "ByteOffset", "unsignedInt"},
		{"Input", "Diag", "DataType", "RefTypeT"},
		{"Input", "Diag", "Id", "string"},
		{"Record", "Ref", "ByteOffset", "unsignedInt"},
		{"Record", "Ref", "DataType", "RefTypeT"},
	}
	var got []Use
	for _, u := range s.Uses {
		if u.Parent != "Root" {
			got = append(got, u)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("uses %+v", s.Uses)
	}
}

// TestLoadRootErrors: a missing root, a location outside the directory, a
// malformed included document and a cycle bound.
func TestLoadRootErrors(t *testing.T) {
	if _, err := LoadRoot("testdata/root", "none.xsd"); err == nil {
		t.Fatal("missing root")
	}
	if _, err := LoadRoot("testdata/missing", "root.xsd"); err == nil {
		t.Fatal("missing dir")
	}
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(dir+"/"+name, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const head = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`
	write("escape.xsd", head+`<xs:include schemaLocation="../outside.xsd"/></xs:schema>`)
	if _, err := LoadRoot(dir, "escape.xsd"); err == nil {
		t.Fatal("location outside the directory")
	}
	write("bad.xsd", head+`<xs:include schemaLocation="broken.xsd"/></xs:schema>`)
	write("broken.xsd", `<xs:schema`)
	if _, err := LoadRoot(dir, "bad.xsd"); err == nil {
		t.Fatal("malformed include")
	}
	write("badcomment.xsd", head+`<!-- -- --></xs:schema>`)
	if _, err := LoadRoot(dir, "badcomment.xsd"); err == nil {
		t.Fatal("malformed comment")
	}
	// A chain longer than the bound.
	for i := range maxFiles + 1 {
		write(fmt.Sprintf("c%d.xsd", i), head+fmt.Sprintf(`<xs:include schemaLocation="c%d.xsd"/><xs:include schemaLocation="c%d.xsd"/></xs:schema>`, i+1, i))
	}
	if _, err := LoadRoot(dir, "c0.xsd"); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("chain: %v", err)
	}
	// A cycle ends: c-a includes c-b includes c-a.
	write("a.xsd", head+`<xs:include schemaLocation="b.xsd"/></xs:schema>`)
	write("b.xsd", head+`<xs:include schemaLocation="a.xsd"/></xs:schema>`)
	if s, err := LoadRoot(dir, "a.xsd"); err != nil || len(s.Files) != 2 {
		t.Fatalf("cycle: %v", err)
	}
}

// TestParseNotes: Parse reads the comments too, and a comment outside an
// enumeration is not a note.
func TestParseNotes(t *testing.T) {
	s, err := Parse(strings.NewReader(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><!-- top -->
<xs:simpleType name="E"><!-- before --><xs:restriction base="xs:token"><xs:enumeration value="A"/><!-- a --><xs:enumeration value="B"/></xs:restriction></xs:simpleType>
<xs:element name="X"><xs:simpleType><xs:restriction base="xs:string"><xs:enumeration value="Q"/><!-- inline --></xs:restriction></xs:simpleType></xs:element></xs:schema>`))
	if err != nil {
		t.Fatal(err)
	}
	if e := s.SimpleTypes["E"]; e == nil || !reflect.DeepEqual(e.Notes, map[string]string{"A": "a"}) || e.Base != "token" {
		t.Fatalf("E %+v", e)
	}
	if len(s.Files) != 0 {
		t.Fatalf("files %v", s.Files)
	}
}

// FuzzParse: any input either fails or gives a schema whose enumerations
// resolve without looping.
func FuzzParse(f *testing.F) {
	for _, p := range []string{"testdata/sample.xsd", "testdata/root/root.xsd", "testdata/root/types.xsd"} {
		b, err := os.ReadFile(p)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:simpleType name="A"><xs:union memberTypes="A B"/></xs:simpleType><xs:simpleType name="B"><xs:restriction base="A"/></xs:simpleType></xs:schema>`))
	f.Fuzz(func(t *testing.T, b []byte) {
		s, err := Parse(bytes.NewReader(b))
		if err != nil {
			return
		}
		for name := range s.SimpleTypes {
			s.Enumeration(name)
		}
	})
}

// TestEnumerationCycle: a cycle of unions and restrictions ends, and a
// chain deeper than the bound stops.
func TestEnumerationCycle(t *testing.T) {
	s, err := Parse(strings.NewReader(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
<xs:simpleType name="A"><xs:union memberTypes="A B C"/></xs:simpleType>
<xs:simpleType name="B"><xs:restriction base="A"/></xs:simpleType>
<xs:simpleType name="C"><xs:restriction base="xs:string"><xs:enumeration value="x"/></xs:restriction></xs:simpleType></xs:schema>`))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Enumeration("B"); !ok || !reflect.DeepEqual(got, []string{"x"}) {
		t.Fatalf("B %v %v", got, ok)
	}
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`)
	for i := range maxDepth + 2 {
		fmt.Fprintf(&b, `<xs:simpleType name="T%d"><xs:restriction base="T%d"/></xs:simpleType>`, i, i+1)
	}
	fmt.Fprintf(&b, `<xs:simpleType name="T%d"><xs:restriction base="xs:string"><xs:enumeration value="deep"/></xs:restriction></xs:simpleType></xs:schema>`, maxDepth+2)
	if s, err = Parse(strings.NewReader(b.String())); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Enumeration("T0"); ok {
		t.Fatal("chain deeper than the bound resolved")
	}
	if got, ok := s.Enumeration("T2"); !ok || got[0] != "deep" {
		t.Fatalf("T2 %v", got)
	}
}
