// SPDX-License-Identifier: MIT

package gsdml_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	profinet "github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/gsdml"
)

const compactFixture = "testdata/fixtures/GSDML-V2.35-OTFabric-CompactFixture-20261027.xml"

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCheckBytesValid(t *testing.T) {
	for _, f := range []string{compactFixture, "testdata/fixtures/GSDML-V2.43-OTFabric-ModularFixture-20261027.xml", "testdata/pnio-rs_sample-16real-32bool.xml"} {
		b := readFixture(t, f)
		rep, err := gsdml.CheckBytes(b)
		if err != nil {
			t.Fatal(err)
		}
		if rep.SchemaVersion == "" {
			t.Errorf("%s: no schema version", f)
		}
		for _, i := range rep.Issues {
			if i.Rule == gsdml.RuleSchema || i.Rule == gsdml.RuleStructure || i.Rule == gsdml.RuleUnknown || i.Rule == gsdml.RuleReference {
				t.Errorf("%s: %s %s", f, i.Rule, i)
			}
		}
		if err := gsdml.ValidateBytes(b); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}

func TestCheckBytesSchemaErrors(t *testing.T) {
	b := readFixture(t, compactFixture)
	// a vendor ID that is no hexadecimal number, and an attribute the
	// schema does not have
	bad := bytes.Replace(b, []byte(`VendorID="`), []byte(`Bogus="1" VendorID="0xZZ`), 1)
	if bytes.Equal(bad, b) {
		t.Fatal("fixture has no VendorID")
	}
	rep, err := gsdml.CheckBytes(bad)
	if err != nil {
		t.Fatal(err)
	}
	var schema []gsdml.Issue
	for _, i := range rep.Issues {
		if i.Rule == gsdml.RuleSchema {
			schema = append(schema, i)
		}
	}
	if len(schema) < 2 {
		t.Fatalf("schema issues %v", schema)
	}
	for _, i := range schema {
		if i.Severity != gsdml.SeverityError || i.Line == 0 || !strings.HasPrefix(i.Path, "ISO15745Profile/") {
			t.Errorf("issue %+v", i)
		}
	}
	err = gsdml.ValidateBytes(bad)
	var ve *gsdml.ValidationError
	if !errors.As(err, &ve) || !errors.Is(err, profinet.ErrMalformed) {
		t.Fatalf("ValidateBytes: %v", err)
	}
}

// A duplicate ID in two scopes the schema keeps apart is valid; the
// catalog's global table reports it (finding 2); CheckBytes follows the
// schema.
func TestCheckBytesScopedReferences(t *testing.T) {
	b := readFixture(t, compactFixture)
	rep := gsdml.Check(mustParse(t, b))
	brep, err := gsdml.CheckBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Err() != nil || brep.Err() != nil {
		t.Fatalf("Check %v, CheckBytes %v", rep.Err(), brep.Err())
	}
	// a dangling text reference: a keyref of the schema
	dangling := bytes.Replace(b, []byte(`TextId="`), []byte(`TextId="NoSuchText`), 1)
	if err := gsdml.ValidateBytes(dangling); err == nil || !strings.Contains(err.Error(), "keyref") {
		t.Errorf("dangling text reference: %v", err)
	}
}

func mustParse(t *testing.T, b []byte) *gsdml.Document {
	t.Helper()
	d, err := gsdml.ParseBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCheckBytesWithoutSchema(t *testing.T) {
	b := readFixture(t, compactFixture)
	for name, doc := range map[string][]byte{
		"no version": bytes.Replace(b, []byte("GSDML-DeviceProfile-V2.35.xsd"), []byte("other.xsd"), 1),
		"newer":      bytes.Replace(b, []byte("GSDML-DeviceProfile-V2.35.xsd"), []byte("GSDML-DeviceProfile-V9.99.xsd"), 1),
	} {
		rep, err := gsdml.CheckBytes(doc)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, i := range rep.Issues {
			if i.Rule == gsdml.RuleSchema {
				found = i.Severity == gsdml.SeverityUnvalidated && strings.Contains(i.Message, "catalog's rules only")
			}
		}
		if !found {
			t.Errorf("%s: no unvalidated schema issue: %v", name, rep.Issues)
		}
	}
	if _, err := gsdml.CheckBytes([]byte("<ISO15745Profile")); err == nil {
		t.Error("malformed document checked")
	}
	if err := gsdml.ValidateBytes([]byte("<x")); err == nil {
		t.Error("malformed document validated")
	}
}

// More schema issues than the schema validation lists end in one "limit"
// issue.
func TestCheckBytesTruncated(t *testing.T) {
	b := readFixture(t, compactFixture)
	var attrs strings.Builder
	for k := range 1100 {
		fmt.Fprintf(&attrs, ` U%d="1"`, k)
	}
	doc := bytes.Replace(b, []byte("<ProfileBody"), []byte("<ProfileBody"+attrs.String()), 1)
	rep, err := gsdml.CheckBytes(doc)
	if err != nil {
		t.Fatal(err)
	}
	last := rep.Issues[len(rep.Issues)-1]
	if last.Rule != "limit" || last.Severity != gsdml.SeverityError {
		t.Fatalf("last issue %+v (%d issues)", last, len(rep.Issues))
	}
}
