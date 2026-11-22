// SPDX-License-Identifier: MIT

package typed_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	profinet "github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/gsdml/typed"
	v235 "github.com/otfabric/go-profinet/gsdml/versions/v235"
)

const emptyRoot = `<ISO15745Profile xmlns="http://www.profibus.com/GSDML/2003/11/DeviceProfile"/>`

// Every version decodes through its own case of the generated switch, and
// an empty root is not valid against any of them.
func TestEveryVersionDecodes(t *testing.T) {
	for _, v := range typed.Versions() {
		d, err := typed.DecodeVersion([]byte(emptyRoot), v)
		if err != nil {
			t.Fatalf("%s: %v", v, err)
		}
		if d.SchemaVersion() != v || d.RootElement() == nil {
			t.Fatalf("%s: %s %v", v, d.SchemaVersion(), d.RootElement())
		}
		var se *typed.SchemaError
		if _, err := typed.ParseVersion([]byte(emptyRoot), v); !errors.As(err, &se) {
			t.Fatalf("%s: ParseVersion %v", v, err)
		}
	}
	if _, err := typed.DecodeVersion([]byte(emptyRoot), "9.9"); !errors.Is(err, typed.ErrUnknownVersion) {
		t.Fatalf("unknown version: %v", err)
	}
}

func TestDecodeAsErrors(t *testing.T) {
	if _, err := v235.Decode([]byte("<")); !errors.Is(err, profinet.ErrMalformed) {
		t.Fatalf("malformed: %v", err)
	}
	if _, err := typed.DecodeAs[typed.ISO15745Profile_V235]([]byte(emptyRoot), "9.9"); !errors.Is(err, typed.ErrUnknownVersion) {
		t.Fatalf("unknown version: %v", err)
	}
	if _, err := v235.Decode([]byte(`<Foo/>`)); err == nil || !strings.Contains(err.Error(), "not ISO15745Profile") {
		t.Fatalf("root: %v", err)
	}
	var se *typed.SchemaError
	if _, err := v235.Parse([]byte(emptyRoot)); !errors.As(err, &se) {
		t.Fatalf("invalid: %v", err)
	}
	if _, err := v235.Decode([]byte(`<ISO15745Profile xmlns="http://www.profibus.com/GSDML/2003/11/DeviceProfile" bogus="1"/>`)); !errors.Is(err, profinet.ErrMalformed) {
		t.Fatalf("unknown attribute: %v", err)
	}
}

// A decoded document validates as the document it writes, with or without
// its schema location.
func TestDocumentValidate(t *testing.T) {
	data, err := os.ReadFile("testdata/golden/GSDML-V2.35-OTFabric-CompactFixture-20261027.typed.xml")
	if err != nil {
		t.Fatal(err)
	}
	d, err := v235.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	d.SchemaLocation = ""
	if strings.Contains(string(d.Marshal()), "schemaLocation") {
		t.Fatal("schema location written")
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	d.Root.ProfileBody.DeviceIdentity.VendorID = "1"
	var se *typed.SchemaError
	if err := d.Validate(); !errors.As(err, &se) {
		t.Fatalf("changed: %v", err)
	}
}
