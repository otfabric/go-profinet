// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestGeneratedUpToDate is make generate-check as a test: the committed
// files are what the catalog generates.
func TestGeneratedUpToDate(t *testing.T) {
	c, raw, err := load("../../../gsdml/schema/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	src, err := generateGo(c, raw)
	if err != nil {
		t.Fatal(err)
	}
	have, err := os.ReadFile("../../../gsdml/zz_generated.go")
	if err != nil || !bytes.Equal(have, src) {
		t.Fatal("gsdml/zz_generated.go is out of date: run go generate ./gsdml")
	}
	doc, err := os.ReadFile("../../../gsdml/SCHEMA.md")
	if err != nil || !bytes.Equal(doc, generateDoc(c)) {
		t.Fatal("gsdml/SCHEMA.md is out of date: run go generate ./gsdml")
	}
}

func TestCatalogChecks(t *testing.T) {
	bad := []string{
		`{"root": "A", "elements": [{"name": "A"}, {"name": "A"}]}`,
		`{"root": "B", "elements": [{"name": "A"}]}`,
		`{"root": "A", "simpleTypes": {"x": {"pattern": "("}}, "elements": [{"name": "A"}]}`,
		`{"root": "A", "elements": [{"name": "A", "attrs": ["noType"]}]}`,
		`{"root": "A", "elements": [{"name": "A", "attrs": ["a:missing"]}]}`,
		`{"root": "A", "simpleTypes": {"s": {}}, "elements": [{"name": "A", "attrs": ["a:s", "a:s"]}]}`,
		`{"root": "A", "elements": [{"name": "A", "children": ["B"]}]}`,
		`{"root": "A", "elements": [{"name": "A", "text": "string", "children": ["A"]}]}`,
		`{"root": "A", "unknownField": 1, "elements": [{"name": "A"}]}`,
		// Defaults: only for an optional attribute, and a value of its type.
		`{"root": "A", "simpleTypes": {"u": {"uint": 8}}, "elements": [{"name": "A", "attrs": ["a:u!=1"]}]}`,
		`{"root": "A", "simpleTypes": {"u": {"uint": 8}}, "elements": [{"name": "A", "attrs": ["a:u~=1"]}]}`,
		`{"root": "A", "simpleTypes": {"u": {"uint": 8}}, "elements": [{"name": "A", "attrs": ["a:u=256"]}]}`,
		`{"root": "A", "simpleTypes": {"u": {"uint": 8}}, "elements": [{"name": "A", "attrs": ["a:u="]}]}`,
		`{"root": "A", "simpleTypes": {"e": {"enum": ["x"]}}, "elements": [{"name": "A", "attrs": ["a:e=y"]}]}`,
		`{"root": "A", "simpleTypes": {"p": {"pattern": "^a$"}}, "elements": [{"name": "A", "attrs": ["a:p=b"]}]}`,
		`{"root": "A", "elements": [{"name": "A", "attrs": ["a:missing=1"]}]}`,
		// whiteSpace: only "collapse"; a default is collapsed, then matched.
		`{"root": "A", "simpleTypes": {"t": {"whiteSpace": "preserve"}}, "elements": [{"name": "A"}]}`,
		`{"root": "A", "simpleTypes": {"t": {"whiteSpace": "collapse", "pattern": "^a$"}}, "elements": [{"name": "A", "attrs": ["a:t=a b"]}]}`,
	}
	dir := t.TempDir()
	for i, b := range bad {
		p := filepath.Join(dir, "c.json")
		_ = os.WriteFile(p, []byte(b), 0o644)
		if _, _, err := load(p); err == nil {
			t.Errorf("%d: accepted %s", i, b)
		}
	}
	if _, _, err := load(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("missing file")
	}
	// A valid default.
	p := filepath.Join(dir, "ok.json")
	_ = os.WriteFile(p, []byte(`{"root": "A", "simpleTypes": {"u": {"uint": 8}, "e": {"enum": ["x"]}, "t": {"whiteSpace": "collapse", "pattern": "^a b$"}}, "elements": [{"name": "A", "attrs": ["a:u=1", "b:e=x", "c:t=a  b"]}]}`), 0o644)
	c, _, err := load(p)
	if err != nil {
		t.Fatal(err)
	}
	src, err := generateGo(c, []byte("{}"))
	if err != nil || !bytes.Contains(src, []byte("func (x *A) aOrDefault() string {")) || !bytes.Contains(src, []byte(`return "1"`)) || !bytes.Contains(src, []byte(`"t": {doc: "", pattern: regexp.MustCompile("^a b$"), collapse: true}`)) {
		t.Fatalf("accessor missing: %v\n%s", err, src)
	}
	if doc := generateDoc(c); !bytes.Contains(doc, []byte("| `a` | `u` | default `1` |")) {
		t.Fatalf("doc\n%s", doc)
	}
}

func TestNames(t *testing.T) {
	for in, want := range map[string]string{
		"TextId": "TextID", "DeviceIdentity": "DeviceIdentity", "DNS_CompatibleName": "DNSCompatibleName",
		"xml:lang": "Lang", "RT_Class3Properties": "RTClass3Properties", "ModuleIdentNumber": "ModuleIdentNumber",
	} {
		if got := goName(in); got != want {
			t.Errorf("goName(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"Entry": "Entries", "Day": "Days", "Class": "Classes", "Item": "Items"} {
		if got := plural(in); got != want {
			t.Errorf("plural(%q) = %q", in, got)
		}
	}
	if lowerFirst("") != "" || lowerFirst("ID list") != "ID list" || lowerFirst("The x") != "the x" {
		t.Fatal("lowerFirst")
	}
	if c := parseChild("A+"); c.min != 1 || c.max != -1 {
		t.Fatal(c)
	}
}

func TestCompareXSD(t *testing.T) {
	c := &catalog{Elements: []element{
		// Kind required, Missing undeclared, Width's default 2 (the schema's
		// is 1), Note a default the schema lacks.
		{Name: "Item", Attrs: []string{"ID:x!", "Kind:x!", "Missing:x", "Width:x=2", "Note:x=n"}, Children: []string{"Name"}},
		{Name: "Absent"},
	}}
	err := compareXSD(c, "../../xsd/testdata")
	if err == nil || !strings.Contains(err.Error(), "5 places") {
		t.Fatalf("err %v", err)
	}
	if err := compareXSD(c, t.TempDir()); err == nil {
		t.Fatal("no schema")
	}
}

// TestTrackedAttrs: the attributes whose emptiness is recorded are the
// optional, typed, unqualified ones; required, always-written (~), string
// and xml: attributes are not.
func TestTrackedAttrs(t *testing.T) {
	e := element{Name: "E", Attrs: []string{
		"Req:Unsigned8!", "Always:token~", "Text:string", "xml:lang:Language",
		"Width:Unsigned8=1", "Flag:Boolean", "List:TokenList",
	}}
	var got []string
	for _, a := range trackedAttrs(e) {
		got = append(got, a.name)
	}
	if want := []string{"Width", "Flag", "List"}; !slices.Equal(got, want) {
		t.Fatalf("%q, want %q", got, want)
	}
	if trackedAttrs(element{Name: "F", Attrs: []string{"Text:string"}}) != nil {
		t.Fatal("an element with no tracked attribute")
	}
}
