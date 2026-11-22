// SPDX-License-Identifier: MIT

//go:build gsdmlxsd

package xsdc

import (
	"os"
	"testing"
)

// TestReferenceSchemas loads the 18 GSDML reference schema versions
// (scripts/gsdml-schemas.sh fetch) and checks the measured deduplication
// that docs/research/GSDML-CODEGEN.md reports, so that a schema or
// compiler change that moves it is noticed.
func TestReferenceSchemas(t *testing.T) {
	dir := os.Getenv("GSDML_SCHEMAS_XSD")
	if dir == "" {
		t.Fatal("GSDML_SCHEMAS_XSD is not set: the test needs the reference schemas (make gsdml-schemas)")
	}
	c, err := LoadCorpus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Versions) != 18 {
		t.Fatalf("%d versions: %v", len(c.Versions), c.Versions)
	}
	type row struct{ ids, naive, dedup int }
	want := map[Level]map[string]row{
		Representation: {"complex": {354, 2921, 828}, "simple": {199, 1915, 204}, "element": {93, 556, 110}},
		Codec:          {"complex": {354, 2921, 836}, "simple": {199, 1915, 221}},
		Full:           {"complex": {354, 2921, 917}, "simple": {199, 1915, 253}},
	}
	for _, lv := range []Level{Representation, Codec, Full} {
		v, err := Analyze(c, lv)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]row{}
		for comp, cl := range v.Classes {
			if comp.Name.NS == XS {
				continue
			}
			r := got[comp.Kind]
			r.ids++
			for _, k := range cl {
				if k != "" {
					r.naive++
				}
			}
			r.dedup += v.Count(comp)
			got[comp.Kind] = r
		}
		for kind, w := range want[lv] {
			if got[kind] != w {
				t.Errorf("%s %s: identities, naive, deduplicated = %v, want %v", lv, kind, got[kind], w)
			}
		}
		t.Logf("%s: %+v", lv, got)
	}
	for i, s := range c.Sets {
		if err := compileAll(s); err != nil {
			t.Errorf("%s: %v", c.Versions[i], err)
		}
	}
}
