// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// schemaDoc is a synthetic schema document that includes or imports locs
// and carries versiondate date when it is not empty.
func schemaDoc(version, date string, locs ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:gsdml" version=%q>`, version)
	if date != "" {
		fmt.Fprintf(&b, `<xs:annotation><xs:appinfo><schemainfo versiondate=%q/></xs:appinfo></xs:annotation>`, date)
	}
	for _, l := range locs {
		fmt.Fprintf(&b, `<xs:include schemaLocation=%q/>`, l)
	}
	b.WriteString(`</xs:schema>`)
	return b.String()
}

// writeSchemas writes files into a new directory, with a SHA256SUMS of
// them when sums is true.
func writeSchemas(t *testing.T, files map[string]string, sums bool) string {
	t.Helper()
	dir := t.TempDir()
	var names []string
	for n, s := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if sums {
		sort.Strings(names)
		var b strings.Builder
		for _, n := range names {
			sum := sha256.Sum256([]byte(files[n]))
			fmt.Fprintf(&b, "%s  %s\r\n", hex.EncodeToString(sum[:]), n)
		}
		if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func registryFiles() map[string]string {
	return map[string]string{
		"GSDML-DeviceProfile-v2.4.xsd":  schemaDoc("2.4", "20190805", "GSDML-Primitives-v2.4.xsd", "xml.xsd"),
		"GSDML-Primitives-v2.4.xsd":     schemaDoc("2.4", "20190701", "xml.xsd"),
		"GSDML-DeviceProfile-V2.35.xsd": schemaDoc("2.35", "20180423", "GSDML-Primitives-v2.35.xsd", "gone.xsd"),
		"GSDML-Primitives-v2.35.xsd":    schemaDoc("2.35", "20180423"),
		"GSDML-DeviceProfile-v1.0.xsd":  schemaDoc("1.0", "", "GSDML-Primitives-v1.0.xsd"),
		"GSDML-Primitives-v1.0.xsd":     schemaDoc("1.0", ""),
		"xml.xsd":                       schemaDoc("", ""),
		"Lone-v1.0.xsd":                 schemaDoc("1.00", "20031130"),
		"notes.txt":                     "not a schema",
	}
}

// TestRegistry orders the versions by GSDML number (2.35 before 2.4),
// records each file once with its users, keeps the files no DeviceProfile
// reads, and gives the same bytes twice.
func TestRegistry(t *testing.T) {
	dir := writeSchemas(t, registryFiles(), true)
	if err := os.Mkdir(filepath.Join(dir, "sub.xsd"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := &catalog{Versions: []string{"2.35", "2.4"}, DataTypeLegality: &legality{Versions: map[string]map[string][]string{"1.0": {}}}}
	var b, again bytes.Buffer
	if err := writeRegistry(c, dir, &b); err != nil {
		t.Fatal(err)
	}
	if err := writeRegistry(c, dir, &again); err != nil || !bytes.Equal(b.Bytes(), again.Bytes()) {
		t.Fatal("not reproducible")
	}
	var r registry
	if err := json.Unmarshal(b.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, v := range r.Versions {
		order = append(order, v.Version)
	}
	if r.Schema != registrySchema || !reflect.DeepEqual(order, []string{"1.0", "2.35", "2.4"}) {
		t.Fatalf("registry %s %v", r.Schema, order)
	}
	v10, v235, v24 := r.Versions[0], r.Versions[1], r.Versions[2]
	if v10.Catalog || !v10.DataTypeLegality || v10.VersionDate != "" || v10.Primitives != "GSDML-Primitives-v1.0.xsd" {
		t.Fatalf("1.0 %+v", v10)
	}
	if !v235.Catalog || v235.DataTypeLegality || v235.DeviceProfile != "GSDML-DeviceProfile-V2.35.xsd" || !reflect.DeepEqual(v235.Missing, []string{"gone.xsd"}) || v235.PrimitivesVersionDate != "" {
		t.Fatalf("2.35 %+v", v235)
	}
	if v24.VersionDate != "20190805" || v24.PrimitivesVersionDate != "20190701" || v24.SchemaVersionAttribute != "2.4" || v24.TargetNamespace != "urn:gsdml" ||
		!reflect.DeepEqual(v24.Files, []string{"GSDML-DeviceProfile-v2.4.xsd", "GSDML-Primitives-v2.4.xsd", "xml.xsd"}) {
		t.Fatalf("2.4 %+v", v24)
	}
	users := map[string][]string{}
	for _, f := range r.Files {
		users[f.Name] = f.UsedBy
		if len(f.SHA256) != 64 || f.Size == 0 {
			t.Errorf("%s: %+v", f.Name, f.File)
		}
	}
	if len(r.Files) != 8 || !reflect.DeepEqual(users["xml.xsd"], []string{"2.4"}) || !reflect.DeepEqual(users["Lone-v1.0.xsd"], []string{}) || r.Files[0].Name != "GSDML-DeviceProfile-V2.35.xsd" {
		t.Fatalf("files %v", users)
	}
	// Without SHA256SUMS nothing is compared, and without a legality
	// section no version has DataType sets.
	dir = writeSchemas(t, registryFiles(), false)
	b.Reset()
	if err := writeRegistry(&catalog{}, dir, &b); err != nil || strings.Contains(b.String(), `"dataTypeLegality": true`) {
		t.Fatalf("%v %s", err, b.String())
	}
}

func TestRegistryErrors(t *testing.T) {
	c := &catalog{}
	for name, tc := range map[string]struct {
		edit func(map[string]string)
		sums string
		want string
	}{
		"no device profile": {edit: func(f map[string]string) {
			for n := range f {
				if strings.HasPrefix(n, "GSDML-") {
					delete(f, n)
				}
			}
		}, want: "no GSDML-DeviceProfile"},
		"no primitives": {edit: func(f map[string]string) { f["GSDML-DeviceProfile-v2.4.xsd"] = schemaDoc("2.4", "", "xml.xsd") }, want: "no Primitives"},
		"two primitives": {edit: func(f map[string]string) {
			f["GSDML-Primitives-v2.4.xsd"] = schemaDoc("2.4", "", "GSDML-Primitives-v2.35.xsd")
		}, want: "two Primitives"},
		"same version": {edit: func(f map[string]string) {
			f["GSDML-DeviceProfile-v2.40.xsd"] = schemaDoc("2.4", "", "GSDML-Primitives-v2.4.xsd")
		}, want: "two DeviceProfile schemas for version 2.4"},
		"bad device profile": {edit: func(f map[string]string) { f["GSDML-DeviceProfile-v2.4.xsd"] = "<xs:schema" }, want: "GSDML-DeviceProfile-v2.4.xsd"},
		"bad lone file":      {edit: func(f map[string]string) { f["Lone-v1.0.xsd"] = "<xs:schema" }, want: "Lone-v1.0.xsd"},
		"checksum":           {sums: "changed", want: "SHA256SUMS"},
		"malformed sums":     {sums: "malformed", want: "malformed line"},
		"sums is a dir":      {sums: "dir", want: "SHA256SUMS"},
	} {
		t.Run(name, func(t *testing.T) {
			files := registryFiles()
			if tc.edit != nil {
				tc.edit(files)
			}
			dir := writeSchemas(t, files, true)
			sums := filepath.Join(dir, "SHA256SUMS")
			switch tc.sums {
			case "changed":
				if err := os.WriteFile(filepath.Join(dir, "xml.xsd"), []byte(schemaDoc("1", "")), 0o600); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := os.WriteFile(sums, []byte("abc xml.xsd\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "dir":
				if err := os.Remove(sums); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(sums, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			var b bytes.Buffer
			err := writeRegistry(c, dir, &b)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
	if err := writeRegistry(c, filepath.Join(t.TempDir(), "none"), &bytes.Buffer{}); err == nil {
		t.Fatal("a missing directory gave no error")
	}
}

func TestSchemaVersionNumber(t *testing.T) {
	for v, want := range map[string]int{"1.0": 100, "2.2": 220, "2.25": 225, "2.3": 230, "2.35": 235, "2.4": 240, "2.41": 241, "2.46": 246, "x.1": -1, "2": -1} {
		if got := schemaVersionNumber(v); got != want {
			t.Errorf("%s: %d, want %d", v, got, want)
		}
	}
}
