// SPDX-License-Identifier: MIT

package gsdx

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet"
)

// certPackage has signatures signature parts, each with rels certificate
// relationships to one certificate part.
func certPackage(t *testing.T, signatures, rels int, cert []byte) []byte {
	t.Helper()
	const head = `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`
	var or, cr strings.Builder
	or.WriteString(head)
	for i := range signatures {
		fmt.Fprintf(&or, `<Relationship Id="s%d" Type="%s" Target="/sig/s%d.xml"/>`, i, RelTypeSignature, i)
	}
	or.WriteString(`</Relationships>`)
	cr.WriteString(head + `<Relationship Id="other" Type="urn:other" Target="cert.cer"/>`)
	for i := range rels {
		// The same part, written in three ways.
		target := []string{"/sig/cert.cer", "cert.cer", "/SIG/Cert.CER"}[i%3]
		fmt.Fprintf(&cr, `<Relationship Id="c%d" Type="%s" Target="%s"/>`, i, RelTypeCertificate, target)
	}
	cr.WriteString(`</Relationships>`)
	names := []string{"[Content_Types].xml", "_rels/.rels", "sig/origin.sigs", "sig/_rels/origin.sigs.rels", "sig/cert.cer"}
	files := map[string][]byte{
		"[Content_Types].xml":        []byte(testContentTypes),
		"_rels/.rels":                []byte(head + `<Relationship Id="o" Type="` + RelTypeSignatureOrigin + `" Target="/sig/origin.sigs"/></Relationships>`),
		"sig/origin.sigs":            nil,
		"sig/_rels/origin.sigs.rels": []byte(or.String()),
		"sig/cert.cer":               cert,
	}
	for i := range signatures {
		s, r := fmt.Sprintf("sig/s%d.xml", i), fmt.Sprintf("sig/_rels/s%d.xml.rels", i)
		names = append(names, s, r)
		files[s] = []byte(`<Signature xmlns="http://www.w3.org/2000/09/xmldsig#"/>`)
		files[r] = []byte(cr.String())
	}
	return testZip(t, names, files, zip.Deflate)
}

// TestCertificatePartReadOnce: a certificate part is parsed once, however
// many relationships and signatures name it, and a signature lists it
// once.
func TestCertificatePartReadOnce(t *testing.T) {
	cert := selfSigned(t)
	for _, tc := range []struct{ signatures, rels int }{{1, 0}, {1, 1}, {1, 2}, {1, 60}, {3, 1}, {5, 40}} {
		z := certPackage(t, tc.signatures, tc.rels, cert)
		p, err := Open(bytes.NewReader(z), int64(len(z)), Options{})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Signatures) != tc.signatures {
			t.Fatalf("%d signatures, want %d", len(p.Signatures), tc.signatures)
		}
		want := min(tc.rels, 1)
		for _, s := range p.Signatures {
			if len(s.Certificates) != want {
				t.Fatalf("%d signatures with %d certificate relationships each: signature %s lists %d certificates, want %d",
					tc.signatures, tc.rels, s.Part, len(s.Certificates), want)
			}
			if want == 1 && (s.Certificates[0].Source != "/sig/cert.cer" || s.Certificates[0].Error != "" || s.Certificates[0].SHA256 == "") {
				t.Fatalf("certificate %+v", s.Certificates[0])
			}
		}
		if p.certParses != want {
			t.Fatalf("%d signatures with %d certificate relationships each: the certificate part was parsed %d times, want %d",
				tc.signatures, tc.rels, p.certParses, want)
		}
	}
}

// typesWith returns a [Content_Types].xml with n Default elements for the
// extension ext after the usual ones; distinct numbers them.
func typesWith(n int, distinct bool) []byte {
	var b strings.Builder
	b.WriteString(strings.TrimSuffix(testContentTypes, `</Types>`))
	for i := range n {
		ext := "dup"
		if distinct {
			ext = fmt.Sprintf("e%d", i)
		}
		fmt.Fprintf(&b, `<Default Extension="%s" ContentType="application/x"/>`, ext)
	}
	b.WriteString(`</Types>`)
	return []byte(b.String())
}

// TestOpenIssuesBounded: Open lists maxIssues issues and says in one more
// how many it left out.
func TestOpenIssuesBounded(t *testing.T) {
	// n Default elements for one extension: every one after the first is
	// an issue.
	for _, issues := range []int{0, 1, maxIssues - 1, maxIssues, maxIssues + 1, maxIssues + 500} {
		z := testZip(t, []string{"[Content_Types].xml"}, map[string][]byte{"[Content_Types].xml": typesWith(issues+1, false)}, zip.Store)
		p, err := Open(bytes.NewReader(z), int64(len(z)), Options{})
		if err != nil {
			t.Fatal(err)
		}
		want := issues
		if issues > maxIssues {
			want = maxIssues + 1
		}
		if len(p.Issues) != want {
			t.Fatalf("%d issues found: %d listed, want %d", issues, len(p.Issues), want)
		}
		if issues > maxIssues {
			last := fmt.Sprintf("%d more issues are not listed: Open lists the first %d", issues-maxIssues, maxIssues)
			if got := p.Issues[len(p.Issues)-1]; got.Message != last || got.Part != "" {
				t.Fatalf("last issue %+v, want %q", got, last)
			}
		}
	}
}

// TestOpenElementBudget: the XML parts of the package model share a
// number of elements: 65 536, more where MaxParts allows more ZIP entries
// than the default.
func TestOpenElementBudget(t *testing.T) {
	for maxParts, want := range map[int]int{
		0: 65536, 1: 65536, DefaultMaxParts: 65536, DefaultMaxParts + 1: 65552, 2 * DefaultMaxParts: 131072,
		math.MaxInt / elementsPerEntry: math.MaxInt / elementsPerEntry * elementsPerEntry, math.MaxInt/elementsPerEntry + 1: math.MaxInt, math.MaxInt: math.MaxInt,
	} {
		if got := elementBudget(maxParts); got != want {
			t.Errorf("elementBudget(%d) = %d, want %d", maxParts, got, want)
		}
	}
	// [Content_Types].xml alone: its Types element, three Default
	// elements and more.
	types := func(elements int) []byte {
		return testZip(t, []string{"[Content_Types].xml"}, map[string][]byte{"[Content_Types].xml": typesWith(elements-4, true)}, zip.Store)
	}
	for _, tc := range []struct {
		budget, elements int
		ok               bool
	}{
		{0, 4, false}, {3, 4, false}, {4, 4, true}, {5, 4, true},
		{16, 15, true}, {16, 16, true}, {16, 17, false}, {16, 1000, false},
	} {
		z := types(tc.elements)
		p, err := open(bytes.NewReader(z), int64(len(z)), Options{}.withDefaults(), tc.budget)
		switch {
		case tc.ok && (err != nil || len(p.Defaults) != tc.elements-1 || p.elements != tc.budget-tc.elements):
			t.Errorf("%d elements of %d: %v", tc.elements, tc.budget, err)
		case !tc.ok && !errors.Is(err, profinet.ErrResourceExhausted):
			t.Errorf("%d elements of %d: err %v, want ErrResourceExhausted", tc.elements, tc.budget, err)
		}
	}
	// Open: the bound of the default options, and a larger MaxParts.
	const def = DefaultMaxParts * elementsPerEntry
	for _, tc := range []struct {
		maxParts, elements int
		ok                 bool
	}{{0, def, true}, {0, def + 1, false}, {1, def + 1, false}, {DefaultMaxParts + 1, def + 1, true}} {
		z := types(tc.elements)
		p, err := Open(bytes.NewReader(z), int64(len(z)), Options{MaxParts: tc.maxParts})
		switch {
		case tc.ok && (err != nil || len(p.Defaults) != tc.elements-1):
			t.Errorf("MaxParts %d, %d elements: %v", tc.maxParts, tc.elements, err)
		case !tc.ok && !errors.Is(err, profinet.ErrResourceExhausted):
			t.Errorf("MaxParts %d, %d elements: err %v, want ErrResourceExhausted", tc.maxParts, tc.elements, err)
		}
	}

	// A relationships part or a signature part with more elements than
	// are left is not decoded and reported; the parts before it are.
	// Of 96 elements, the content types of signedPackage take 4, its
	// package relationships 2 and its origin relationships 2.
	const budget = 96
	for _, tc := range []struct {
		refs   int // Reference elements of the signature: 3 start tags each, in 3 more
		decode bool
	}{{0, true}, {1, true}, {28, true}, {29, false}, {400, false}} {
		z := signedPackage(t, 1, tc.refs, 16)
		p, err := open(bytes.NewReader(z), int64(len(z)), Options{}.withDefaults(), budget)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Signatures) != 1 || len(p.Relationships) != 1 {
			t.Fatalf("%d signatures, %d package relationships", len(p.Signatures), len(p.Relationships))
		}
		got, reported := len(p.Signatures[0].PartReferences), hasIssue(p, "/sig/s.xml", "signature: not read")
		if tc.decode && (got != tc.refs || reported) || !tc.decode && (got != 0 || !reported) {
			t.Errorf("a signature of %d elements with %d left: %d references decoded, reported %v", 3+3*tc.refs, budget-8, got, reported)
		}
	}
	// The origin's relationships part with more elements than are left:
	// no signature is found, and the part is reported.
	z := signedPackage(t, 200, 1, 16)
	p, err := open(bytes.NewReader(z), int64(len(z)), Options{}.withDefaults(), budget)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Signatures) != 0 || !hasIssue(p, "/sig/_rels/origin.sigs.rels", "not read: more elements than the 90 left") {
		t.Fatalf("%d signatures, issues %v", len(p.Signatures), p.Issues)
	}
}

// TestStartTags: the elements of an XML part are counted as its start
// tags, up to one above the limit.
func TestStartTags(t *testing.T) {
	for in, want := range map[string]int{
		"": 0, "<": 0, "<a": 1, "<a/>": 1, "</a>": 0, "<a><b/></a>": 2, `<?xml version="1.0"?><a/>`: 1, "<!-- <a> --><b/>": 2,
	} {
		if got := startTags([]byte(in), 10); got != want {
			t.Errorf("startTags(%q) = %d, want %d", in, got, want)
		}
	}
	many := bytes.Repeat([]byte("<a/>"), 9)
	for limit, want := range map[int]int{0: 1, 3: 4, 8: 9, 9: 9, 10: 9} {
		if got := startTags(many, limit); got != want {
			t.Errorf("limit %d: %d, want %d", limit, got, want)
		}
	}
}
