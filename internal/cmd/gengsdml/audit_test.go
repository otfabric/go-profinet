// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const auditRoot = "../../xsd/testdata/root/root.xsd"

func auditCatalog() *catalog {
	return &catalog{Versions: []string{"9.0", "9.1"}, SimpleTypes: map[string]simpleType{"DataType": {Enum: []string{"Unsigned8", "Unsigned16", "Float32"}}}}
}

// TestAudit reports each DataType context with its enumeration, compares
// it with the catalog and the previous set, and gives the same bytes twice.
func TestAudit(t *testing.T) {
	var b bytes.Buffer
	if err := audit(auditCatalog(), []string{"9.0=" + auditRoot, auditRoot}, &b); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err := audit(auditCatalog(), []string{"9.0=" + auditRoot, auditRoot}, &again); err != nil || !bytes.Equal(b.Bytes(), again.Bytes()) {
		t.Fatal("not reproducible")
	}
	var r auditReport
	if err := json.Unmarshal(b.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Schema != auditSchema || len(r.Versions) != 2 || r.UnverifiedCatalogVersions == nil || len(r.UnverifiedCatalogVersions) != 0 || !reflect.DeepEqual(r.CatalogValues, []string{"Float32", "Unsigned16", "Unsigned8"}) {
		t.Fatalf("report %+v", r)
	}
	v0, v1 := r.Versions[0], r.Versions[1]
	if v0.Version != "9.0" || v0.SchemaVersionAttribute != "9.1" || v1.Version != "9.1" || v0.Root != "root.xsd" || !reflect.DeepEqual(v0.Missing, []string{"gone.xsd"}) {
		t.Fatalf("versions %+v %+v", v0, v1)
	}
	var keys []string
	for _, c := range v0.Contexts {
		keys = append(keys, c.Parent+"/"+c.Element+":"+c.Type)
	}
	if !reflect.DeepEqual(keys, []string{"Input/DataItem:ItemTypeT", "Input/Diag:RefTypeT", "Record/Ref:RefTypeT"}) {
		t.Fatalf("contexts %v", keys)
	}
	item := v0.Contexts[0]
	if !reflect.DeepEqual(item.NotInCatalog, []string{"Bit", "Old16", "Padded "}) || !reflect.DeepEqual(item.CatalogNotInSchema, []string{"Float32"}) || item.New {
		t.Fatalf("item %+v", item)
	}
	ref := v0.Types["RefTypeT"]
	if ref.Base != "string" || !reflect.DeepEqual(ref.Whitespace, []string{"Padded "}) || ref.Notes["Old16"] == "" {
		t.Fatalf("RefTypeT %+v", ref)
	}
	for _, c := range v1.Contexts {
		if c.New || len(c.Added) != 0 || len(c.Removed) != 0 {
			t.Fatalf("second set %+v", c)
		}
	}
	// Only one set: the other catalog version is unverified.
	b.Reset()
	if err := audit(auditCatalog(), []string{auditRoot}, &b); err != nil || !strings.Contains(b.String(), `"unverifiedCatalogVersions": [
    "9.0"
  ]`) {
		t.Fatalf("%v %s", err, b.String())
	}
}

// TestAuditDir audits every DeviceProfile of a directory, oldest first.
func TestAuditDir(t *testing.T) {
	files := registryFiles()
	files["GSDML-Primitives-v2.4.xsd"] = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:simpleType name="T"><xs:restriction base="xs:string"><xs:enumeration value="Unsigned8"/></xs:restriction></xs:simpleType><xs:element name="DataItem"><xs:complexType><xs:attribute name="DataType" type="T"/></xs:complexType></xs:element><xs:element name="Input"><xs:complexType><xs:sequence><xs:element ref="DataItem"/></xs:sequence></xs:complexType></xs:element></xs:schema>`
	for _, n := range []string{"GSDML-DeviceProfile-v1.0.xsd", "GSDML-Primitives-v1.0.xsd", "GSDML-DeviceProfile-V2.35.xsd", "GSDML-Primitives-v2.35.xsd"} {
		delete(files, n)
	}
	dir := writeSchemas(t, files, false)
	var b bytes.Buffer
	if err := audit(auditCatalog(), []string{dir}, &b); err != nil {
		t.Fatal(err)
	}
	var r auditReport
	if err := json.Unmarshal(b.Bytes(), &r); err != nil || len(r.Versions) != 1 || r.Versions[0].Version != "2.4" || r.Versions[0].Root != "GSDML-DeviceProfile-v2.4.xsd" {
		t.Fatalf("%v %s", err, b.String())
	}
	if err := audit(auditCatalog(), []string{t.TempDir()}, &b); err == nil || !strings.Contains(err.Error(), "no GSDML-DeviceProfile") {
		t.Fatalf("empty directory: %v", err)
	}
}

// TestAuditChanges: a value and a context added and removed between sets.
func TestAuditChanges(t *testing.T) {
	dir := t.TempDir()
	const head = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" version="%s">`
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	enum := func(vals ...string) string {
		s := `<xs:simpleType name="T"><xs:restriction base="xs:string">`
		for _, v := range vals {
			s += `<xs:enumeration value="` + v + `"/>`
		}
		return s + `</xs:restriction></xs:simpleType>`
	}
	el := func(parent, child string) string {
		return `<xs:element name="` + parent + `"><xs:complexType><xs:sequence><xs:element name="` + child + `"><xs:complexType><xs:attribute name="DataType" type="T"/></xs:complexType></xs:element></xs:sequence></xs:complexType></xs:element>`
	}
	write("a.xsd", strings.Replace(head, "%s", "1", 1)+enum("A", "B")+el("P", "X")+`</xs:schema>`)
	write("b.xsd", strings.Replace(head, "%s", "2", 1)+enum("B", "C")+el("P", "X")+el("Q", "Y")+`</xs:schema>`)
	write("c.xsd", strings.Replace(head, "%s", "3", 1)+enum("C")+el("Q", "Y")+`</xs:schema>`)
	var b bytes.Buffer
	if err := audit(auditCatalog(), []string{filepath.Join(dir, "a.xsd"), filepath.Join(dir, "b.xsd"), filepath.Join(dir, "c.xsd")}, &b); err != nil {
		t.Fatal(err)
	}
	var r auditReport
	if err := json.Unmarshal(b.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	p, q := r.Versions[1].Contexts[0], r.Versions[1].Contexts[1]
	if !reflect.DeepEqual(p.Added, []string{"C"}) || !reflect.DeepEqual(p.Removed, []string{"A"}) || p.New || !q.New {
		t.Fatalf("2: %+v %+v", p, q)
	}
	if v := r.Versions[2]; !reflect.DeepEqual(v.RemovedContexts, []string{"P/X"}) || !reflect.DeepEqual(v.Contexts[0].Removed, []string{"B"}) {
		t.Fatalf("3: %+v", v)
	}
	if !reflect.DeepEqual(r.UnverifiedCatalogVersions, []string{"9.0", "9.1"}) {
		t.Fatalf("unverified %v", r.UnverifiedCatalogVersions)
	}
}

func TestAuditErrors(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	const head = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`
	free := write("free.xsd", head+`<xs:simpleType name="T"><xs:restriction base="xs:string"/></xs:simpleType><xs:element name="P"><xs:complexType><xs:sequence><xs:element name="X"><xs:complexType><xs:attribute name="DataType" type="T"/></xs:complexType></xs:element></xs:sequence></xs:complexType></xs:element></xs:schema>`)
	none := write("none.xsd", head+`<xs:element name="P"/></xs:schema>`)
	for _, tc := range []struct {
		roots []string
		want  string
	}{
		{[]string{filepath.Join(dir, "missing.xsd")}, "missing.xsd"},
		{[]string{free}, "no version given"},
		{[]string{"1=" + free}, "has no enumeration"},
		{[]string{"1=" + none}, "no element has a DataType"},
		{[]string{auditRoot, auditRoot}, "in the report twice"},
	} {
		var b bytes.Buffer
		if err := audit(auditCatalog(), tc.roots, &b); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: %v", tc.roots, err)
		}
	}
}

// TestLegalityMatchesAudit: the catalog's legality sets are the committed
// audit report's, context by context, so neither can change alone.
func TestLegalityMatchesAudit(t *testing.T) {
	c, _, err := load("../../../gsdml/schema/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	l := c.DataTypeLegality
	raw, err := os.ReadFile(filepath.Join("../../..", l.Evidence))
	if err != nil {
		t.Fatal(err)
	}
	var r auditReport
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Versions) != len(l.Versions) {
		t.Fatalf("report versions %d, catalog %d", len(r.Versions), len(l.Versions))
	}
	for _, v := range r.Versions {
		sets := l.Versions[v.Version]
		if sets == nil {
			t.Fatalf("catalog has no set for %s", v.Version)
		}
		covered := 0
		for ctx, uses := range l.Contexts {
			for _, use := range uses {
				for _, rc := range v.Contexts {
					if rc.Parent+"/"+rc.Element != use {
						continue
					}
					covered++
					var want, ws []string
					for _, x := range v.Types[rc.Type].Values {
						if strings.TrimSpace(x) == x {
							want = append(want, x)
						} else {
							ws = append(ws, strings.TrimSpace(x))
						}
					}
					if !reflect.DeepEqual(sets[ctx], want) {
						t.Errorf("%s %s (%s): catalog %v, report %v", v.Version, ctx, use, sets[ctx], want)
					}
					if !reflect.DeepEqual(l.Unverified[v.Version][ctx], ws) {
						t.Errorf("%s %s: unverified %v, report %v", v.Version, ctx, l.Unverified[v.Version][ctx], ws)
					}
				}
			}
		}
		if covered != len(v.Contexts) {
			t.Errorf("%s: %d of %d report contexts mapped to a catalog context", v.Version, covered, len(v.Contexts))
		}
	}
}

func TestLegalityChecks(t *testing.T) {
	const head = `{"root": "A", "simpleTypes": {"DataType": {"enum": ["X", "Y"]}}, "elements": [{"name": "A"}], "dataTypeLegality": `
	bad := []string{
		`{"contexts": {"Bad": ["P/E"]}}`,
		`{"contexts": {"io": []}}`,
		`{"contexts": {"io": ["P/E"]}, "versions": {"v2": {"io": ["X"]}}}`,
		`{"contexts": {"io": ["P/E"]}, "versions": {"2.0": {"other": ["X"]}}}`,
		`{"contexts": {"io": ["P/E"]}, "versions": {"2.0": {"io": ["Z"]}}}`,
		`{"contexts": {"io": ["P/E"]}, "unverified": {"2.0": {"io": ["Z"]}}}`,
		`{"contexts": {"io": ["P/E"]}, "versions": {"2.0": {"io": ["X"]}}, "unverified": {"2.0": {"io": ["X"]}}}`,
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	for i, b := range bad {
		_ = os.WriteFile(p, []byte(head+b+"}"), 0o600)
		if _, _, err := load(p); err == nil {
			t.Errorf("%d: accepted %s", i, b)
		}
	}
	_ = os.WriteFile(p, []byte(`{"root": "A", "elements": [{"name": "A"}], "dataTypeLegality": {"contexts": {"io": ["P/E"]}}}`), 0o600)
	if _, _, err := load(p); err == nil {
		t.Error("legality without a DataType type")
	}
	_ = os.WriteFile(p, []byte(head+`{"evidence": "e", "contexts": {"io": ["P/E"]}, "versions": {"2.0": {"io": ["X"]}}, "unverified": {"2.0": {"io": ["Y"]}}}}`), 0o600)
	c, raw, err := load(p)
	if err != nil {
		t.Fatal(err)
	}
	src, err := generateGo(c, raw)
	if err != nil || !bytes.Contains(src, []byte(`"2.0": {`)) || !bytes.Contains(src, []byte(`"io": {"Y": true}`)) {
		t.Fatalf("%v\n%s", err, src)
	}
}
