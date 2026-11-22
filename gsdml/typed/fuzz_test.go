// SPDX-License-Identifier: MIT

package typed_test

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet/gsdml/typed"
)

var update = flag.Bool("update", false, "rewrite testdata/golden")

// fixtures are go-profinet's own GSDML documents (gsdml/testdata), one per
// layout: compact and modular, ISO-8859-1, and pnio-rs's sample.
func fixtures(t testing.TB) map[string][]byte {
	t.Helper()
	names, _ := filepath.Glob("../testdata/*.xml")
	more, _ := filepath.Glob("../testdata/fixtures/*.xml")
	out := map[string][]byte{}
	for _, n := range append(names, more...) {
		b, err := os.ReadFile(n)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(n)] = b
	}
	if len(out) < 4 {
		t.Fatalf("%d fixtures", len(out))
	}
	return out
}

// TestGolden: each fixture but the ISO-8859-1 one parses (it is valid against its schema
// version), and its encoding equals the committed golden vector, which is
// itself valid and encodes to itself. go test -run TestGolden -update
// rewrites the vectors.
func TestGolden(t *testing.T) {
	for name, data := range fixtures(t) {
		d, err := typed.Parse(data)
		if name == "otf-fixture-latin1.xml" {
			// vendor extension content on purpose: xmllint rejects it too
			if err == nil {
				t.Errorf("%s: valid", name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var out bytes.Buffer
		if err := d.Encode(&out); err != nil {
			t.Fatal(err)
		}
		golden := filepath.Join("testdata", "golden", strings.TrimSuffix(name, ".xml")+".typed.xml")
		if *update {
			if err := os.WriteFile(golden, out.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out.Bytes(), want) {
			t.Errorf("%s: the encoding differs from %s", name, golden)
		}
		d2, err := typed.ParseVersion(want, d.SchemaVersion())
		if err != nil {
			t.Errorf("%s: the golden vector is not valid: %v", golden, err)
			continue
		}
		var again bytes.Buffer
		_ = d2.Encode(&again)
		if !bytes.Equal(again.Bytes(), want) {
			t.Errorf("%s: the golden vector does not encode to itself", golden)
		}
	}
}

// TestGoldenIssues: the issues of a document that breaks one rule of each
// kind, against the committed list.
func TestGoldenIssues(t *testing.T) {
	data := fixtures(t)["GSDML-V2.43-OTFabric-ModularFixture-20261027.xml"]
	for _, m := range []struct{ old, new string }{
		{`VendorID="`, `Unknown="1" VendorID="`},     // an attribute not in the schema
		{`<DeviceFunction>`, `<DeviceFunction><X/>`}, // an element not expected
		{`ID="`, `ID="1`},                            // the first is VendorID: outside its pattern
	} {
		data = bytes.Replace(data, []byte(m.old), []byte(m.new), 1)
	}
	err := typed.Validate(data)
	var se *typed.SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("not a SchemaError: %v", err)
	}
	var b strings.Builder
	for _, i := range se.Issues {
		b.WriteString(i.String() + "\n")
	}
	golden := filepath.Join("testdata", "golden", "issues.txt")
	if *update {
		if err := os.WriteFile(golden, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if b.String() != string(want) {
		t.Errorf("issues:\n%s\nwant:\n%s", b.String(), want)
	}
}

func addSeeds(f *testing.F) {
	for _, d := range fixtures(f) {
		f.Add(d)
	}
	f.Add([]byte(`<ISO15745Profile xmlns="http://www.profibus.com/GSDML/2003/11/DeviceProfile"/>`))
	f.Add([]byte(`<?xml version="1.0"?><!DOCTYPE x [<!ENTITY a "b">]><x/>`))
	f.Add([]byte(`<a xmlns:p="urn:p"><p:b p:c="1">t<![CDATA[x]]></p:b></a>`))
}

// FuzzParseNode: ParseNode never panics, and a tree it reads encodes to a
// document it reads again to the same encoding.
func FuzzParseNode(f *testing.F) {
	addSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		n, err := typed.ParseNode(data)
		if err != nil {
			return
		}
		out := n.Marshal()
		n2, err := typed.ParseNode(out)
		if err != nil {
			t.Fatalf("the encoding does not parse: %v\n%s", err, out)
		}
		if again := n2.Marshal(); !bytes.Equal(again, out) {
			t.Fatalf("the encoding is not stable:\n%s\n%s", out, again)
		}
	})
}

// FuzzValidate: Validate never panics, and a document it accepts decodes,
// and encodes to a document that is valid and encodes to itself.
func FuzzValidate(f *testing.F) {
	addSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if typed.Validate(data) != nil {
			return
		}
		d, err := typed.Parse(data)
		if err != nil {
			t.Fatalf("valid, and Parse fails: %v", err)
		}
		var out bytes.Buffer
		_ = d.Encode(&out)
		d2, err := typed.ParseVersion(out.Bytes(), d.SchemaVersion())
		if err != nil {
			t.Fatalf("the encoding of a valid document is not valid: %v\n%s", err, out.Bytes())
		}
		var again bytes.Buffer
		_ = d2.Encode(&again)
		if !bytes.Equal(again.Bytes(), out.Bytes()) {
			t.Fatal("the encoding is not stable")
		}
	})
}

// FuzzDecode: Decode never panics, and a document it decodes encodes to
// one it decodes to the same encoding.
func FuzzDecode(f *testing.F) {
	addSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := typed.Decode(data)
		if err != nil {
			return
		}
		var out bytes.Buffer
		_ = d.Encode(&out)
		d2, err := typed.DecodeVersion(out.Bytes(), d.SchemaVersion())
		if err != nil {
			t.Fatalf("the encoding does not decode: %v\n%s", err, out.Bytes())
		}
		var again bytes.Buffer
		_ = d2.Encode(&again)
		if !bytes.Equal(again.Bytes(), out.Bytes()) {
			t.Fatalf("the encoding is not stable:\n%s\n%s", out.Bytes(), again.Bytes())
		}
	})
}

func BenchmarkParseNode(b *testing.B) {
	benchFixtures(b, func(d []byte) error { _, err := typed.ParseNode(d); return err })
}
func BenchmarkValidate(b *testing.B) { benchFixtures(b, typed.Validate) }
func BenchmarkParse(b *testing.B) {
	benchFixtures(b, func(d []byte) error { _, err := typed.Parse(d); return err })
}
func BenchmarkDecode(b *testing.B) {
	benchFixtures(b, func(d []byte) error { _, err := typed.Decode(d); return err })
}
func BenchmarkEncode(b *testing.B) {
	data := fixtures(b)["GSDML-V2.43-OTFabric-ModularFixture-20261027.xml"]
	d, err := typed.Decode(data)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var out bytes.Buffer
		_ = d.Encode(&out)
	}
}

func benchFixtures(b *testing.B, fn func([]byte) error) {
	data := fixtures(b)["GSDML-V2.43-OTFabric-ModularFixture-20261027.xml"]
	if big := os.Getenv("GSDML_BENCH_FILE"); big != "" {
		var err error
		if data, err = os.ReadFile(big); err != nil {
			b.Fatal(err)
		}
	}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if err := fn(data); err != nil {
			b.Fatal(err)
		}
	}
}
