// SPDX-License-Identifier: MIT

package gsdx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func testZip(t *testing.T, names []string, files map[string][]byte, method uint16) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(files[n]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const testContentTypes = `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Default Extension="bin" ContentType="application/octet-stream"/></Types>`

func signedPackage(t *testing.T, n, m, s int) []byte {
	rels := `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="o" Type="` + RelTypeSignatureOrigin + `" Target="/sig/origin.sigs"/></Relationships>`
	var or strings.Builder
	or.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&or, `<Relationship Id="s%d" Type="%s" Target="/sig/s.xml"/>`, i, RelTypeSignature)
	}
	or.WriteString(`</Relationships>`)
	var sg strings.Builder
	sg.WriteString(`<Signature xmlns="http://www.w3.org/2000/09/xmldsig#"><Object><Manifest>`)
	for i := 0; i < m; i++ {
		fmt.Fprintf(&sg, `<Reference URI="/big.bin"><DigestMethod Algorithm="%s"/><DigestValue>AA==</DigestValue></Reference>`, DigestSHA256)
	}
	sg.WriteString(`</Manifest></Object></Signature>`)
	big := make([]byte, s)
	for i := range big {
		big[i] = byte(i*131 + i>>8)
	}
	files := map[string][]byte{"[Content_Types].xml": []byte(testContentTypes), "_rels/.rels": []byte(rels), "sig/origin.sigs": nil,
		"sig/_rels/origin.sigs.rels": []byte(or.String()), "sig/s.xml": []byte(sg.String()), "big.bin": big}
	return testZip(t, []string{"[Content_Types].xml", "_rels/.rels", "sig/origin.sigs", "sig/_rels/origin.sigs.rels", "sig/s.xml", "big.bin"}, files, zip.Deflate)
}

// A signature part named by many relationships is read once, and a part
// referenced many times is hashed once.
func TestSignatureWorkBounded(t *testing.T) {
	z := signedPackage(t, 50, 200, 16)
	p, err := Open(bytes.NewReader(z), int64(len(z)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Signatures) != 1 {
		t.Fatalf("%d signatures, want 1", len(p.Signatures))
	}
	if len(p.digests) != 1 {
		t.Fatalf("%d digests computed, want 1", len(p.digests))
	}
}

// Core property text split into many pieces is built in linear time.
func TestCorePropertiesPieces(t *testing.T) {
	rels := `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="c" Type="` + RelTypeCoreProperties + `" Target="/core.xml"/></Relationships>`
	const k = 40000
	core := `<cp:coreProperties xmlns:cp="x" xmlns:dc="y"><dc:title>` + strings.Repeat("a<!---->", k) + `</dc:title></cp:coreProperties>`
	files := map[string][]byte{"[Content_Types].xml": []byte(testContentTypes), "_rels/.rels": []byte(rels), "core.xml": []byte(core)}
	z := testZip(t, []string{"[Content_Types].xml", "_rels/.rels", "core.xml"}, files, zip.Store)
	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	p, err := Open(bytes.NewReader(z), int64(len(z)), Options{})
	runtime.ReadMemStats(&m1)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(p.CoreProperties.Title); got != k {
		t.Fatalf("title of %d octets", got)
	}
	if a := m1.TotalAlloc - m0.TotalAlloc; a > 64<<20 {
		t.Fatalf("%d octets allocated", a)
	}
}
