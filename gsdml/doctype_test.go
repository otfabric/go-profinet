// SPDX-License-Identifier: MIT

package gsdml

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet"
)

// TestDoctype: a document type declaration with an internal subset is
// refused with ErrUnsupported, because XML 1.0 section 5.1 has even a
// non-validating processor apply its attribute defaults and entities,
// which encoding/xml does not; one without (a name, an external
// identifier, whose DTD is not read) is ignored, and the document parses
// as without it. Before v0.12.0 every declaration was ignored, so an
// ATTLIST default such as BitLength "3" was silently dropped.
func TestDoctype(t *testing.T) {
	b, err := os.ReadFile("testdata/fixtures/GSDML-V2.43-OTFabric-ModularFixture-20261027.xml")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i := strings.Index(s, "<ISO15745Profile")
	if i < 0 {
		t.Fatal("no root element in the fixture")
	}
	with := func(decl string) []byte { return []byte(s[:i] + decl + "\n" + s[i:]) }
	for _, decl := range []string{
		`<!DOCTYPE ISO15745Profile [<!ATTLIST Ref BitLength CDATA "3">]>`,
		`<!DOCTYPE ISO15745Profile [<!ENTITY e "X">]>`,
		`<!DOCTYPE ISO15745Profile SYSTEM "gsdml.dtd" [ ]>`,
		`<!DOCTYPE ISO15745Profile PUBLIC "-//x//y" 'a.dtd'[]>`,
	} {
		if _, err := ParseBytes(with(decl)); !errors.Is(err, profinet.ErrUnsupported) || !strings.Contains(err.Error(), "internal subset") {
			t.Errorf("%s: %v", decl, err)
		}
	}
	want, err := ParseBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range []string{
		`<!DOCTYPE ISO15745Profile>`,
		`<!DOCTYPE ISO15745Profile SYSTEM "file:///etc/passwd">`,
		`<!DOCTYPE ISO15745Profile SYSTEM "odd[name].dtd">`,
		`<!DOCTYPE ISO15745Profile PUBLIC "-//x//[y]" 'b[1].dtd'>`,
		`<!-- a comment --><?pi data?>`,
	} {
		doc, err := ParseBytes(with(decl))
		if err != nil {
			t.Errorf("%s: %v", decl, err)
			continue
		}
		got, err1 := Marshal(doc)
		exp, err2 := Marshal(want)
		if err1 != nil || err2 != nil || string(got) != string(exp) {
			t.Errorf("%s: the document differs from the one without it (%v %v)", decl, err1, err2)
		}
	}
	// An entity reference stays refused: encoding/xml knows only the
	// predefined entities.
	ref := strings.Replace(s, `DNS_CompatibleName="otf-modular"`, `DNS_CompatibleName="&e;"`, 1)
	if _, err := ParseBytes([]byte(ref)); !errors.Is(err, profinet.ErrMalformed) {
		t.Errorf("entity reference: %v", err)
	}
}

// TestPrologNoRoot: a document with no root element is malformed.
func TestPrologNoRoot(t *testing.T) {
	for _, src := range []string{"", "<?xml version=\"1.0\"?>", "<!-- only a comment -->", "<!DOCTYPE x>", "<?xml version=\"1.0\"?><!DOCTYPE x [", "<"} {
		if _, err := ParseBytes([]byte(src)); !errors.Is(err, profinet.ErrMalformed) {
			t.Errorf("%q: %v", src, err)
		}
	}
}

// TestInternalSubset: a '[' counts only outside the quoted literals.
func TestInternalSubset(t *testing.T) {
	for dir, want := range map[string]bool{
		`DOCTYPE x [<!ENTITY e "y">]`: true,
		`DOCTYPE x SYSTEM "a" [`:      true,
		`DOCTYPE x`:                   false,
		`DOCTYPE x SYSTEM "a[b"`:      false,
		`DOCTYPE x PUBLIC 'p[' "s["`:  false,
		`DOCTYPE x SYSTEM "a"b[`:      true,
		`DOCTYPE x SYSTEM "unclosed[`: false,
		`ENTITY e "[x]"`:              false,
		`ELEMENT x [`:                 false,
	} {
		if got := internalSubset([]byte(dir)); got != want {
			t.Errorf("internalSubset(%q) = %v, want %v", dir, got, want)
		}
	}
}

// TestDeepUnknownContent: unknown content is kept as its inner XML, so no
// part of the package recurses over its nesting; 200 000 nested unknown
// elements (within the 1 048 576 elements ParseBytes reads) parse,
// validate and encode, and more elements than that are refused before
// decoding.
func TestDeepUnknownContent(t *testing.T) {
	b, err := os.ReadFile("testdata/fixtures/GSDML-V2.43-OTFabric-ModularFixture-20261027.xml")
	if err != nil {
		t.Fatal(err)
	}
	deep := func(n int) []byte {
		return []byte(strings.Replace(string(b), "</ProfileBody>", strings.Repeat("<x>", n)+strings.Repeat("</x>", n)+"</ProfileBody>", 1))
	}
	doc, err := ParseBytes(deep(200000))
	if err != nil {
		t.Fatal(err)
	}
	r := Check(doc)
	if r.Err() != nil {
		t.Fatalf("Validate: %v", r.Err())
	}
	if _, err := Marshal(doc); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseBytes(deep(maxElements)); !errors.Is(err, profinet.ErrResourceExhausted) {
		t.Fatalf("%d nested elements: %v", maxElements, err)
	}
}
