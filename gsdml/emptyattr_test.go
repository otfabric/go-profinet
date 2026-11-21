// SPDX-License-Identifier: MIT

package gsdml

import (
	"bytes"
	"encoding/xml"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet"
)

// TestEmptyAttrs: only unqualified attributes among the names, and only
// when their value is "".
func TestEmptyAttrs(t *testing.T) {
	attrs := []xml.Attr{
		{Name: xml.Name{Local: "BitLength"}, Value: ""},
		{Name: xml.Name{Local: "BitOffset"}, Value: "3"},
		{Name: xml.Name{Space: "urn:x", Local: "DataType"}, Value: ""},
		{Name: xml.Name{Local: "Other"}, Value: ""},
	}
	if got := emptyAttrs(attrs, "BitLength", "BitOffset", "DataType"); !slices.Equal(got, []string{"BitLength"}) {
		t.Fatalf("%q", got)
	}
	if got := emptyAttrs(attrs, "Visible"); got != nil {
		t.Fatalf("%q", got)
	}
}

// fixtureVariant returns the modular fixture with old replaced by new,
// failing when old is not in it.
func fixtureVariant(t *testing.T, old, new string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/fixtures/GSDML-V2.43-OTFabric-ModularFixture-20261027.xml")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(old)) {
		t.Fatalf("the fixture no longer holds %s", old)
	}
	return bytes.Replace(b, []byte(old), []byte(new), 1)
}

// TestEmptyAttributeIsNotAbsent: a typed optional attribute a document
// gives as "" is neither absent nor valid: Validate reports it, the Ref
// BitLength and BitOffset take no default for it, and the Boolean
// capabilities ToModel reads are refused. Before v0.12.0 the generated
// types could not tell "" from absent, and both took the default.
func TestEmptyAttributeIsNotAbsent(t *testing.T) {
	const ref = `<Ref ByteOffset="0" BitOffset="0" BitLength="2" DataType="BitArea" DefaultValue="0" TextId="T_Substitute" ValueItemTarget="V_SUBST"/>`
	for _, tc := range []struct {
		name, old, new, dapID, attr string
	}{
		{"BitLength", ref, `<Ref ByteOffset="0" BitOffset="0" BitLength="" DataType="BitArea" DefaultValue="0" TextId="T_Substitute" ValueItemTarget="V_SUBST"/>`, "DAP_2P", "BitLength"},
		{"BitOffset", ref, `<Ref ByteOffset="0" BitOffset="" BitLength="2" DataType="BitArea" DefaultValue="0" TextId="T_Substitute" ValueItemTarget="V_SUBST"/>`, "DAP_2P", "BitOffset"},
		{"LLDP_NoD_Supported", `LLDP_NoD_Supported="true" ResetToFactoryModes="2">`, `LLDP_NoD_Supported="" ResetToFactoryModes="2">`, "DAP_1P", "LLDP_NoD_Supported"},
		{"DCP_HelloSupported", `<InterfaceSubmoduleItem ID="IF_1P"`, `<InterfaceSubmoduleItem ID="IF_1P" DCP_HelloSupported=""`, "DAP_1P", "DCP_HelloSupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := ParseBytes(fixtureVariant(t, tc.old, tc.new))
			if err != nil {
				t.Fatal(err)
			}
			verr := Validate(doc)
			if verr == nil || !strings.Contains(verr.Error(), "attribute "+tc.attr+": empty") {
				t.Errorf("Validate: %v", verr)
			}
			if _, err := ToModel(doc, tc.dapID); !errors.Is(err, profinet.ErrMalformed) {
				t.Errorf("ToModel: %v", err)
			}
		})
	}
}

// TestEmptyAttributeAllowed: an empty attribute whose type admits "" (a
// string, a TokenList, or one always written, such as ApplicationClass) is
// not reported, and the fixture as published validates.
func TestEmptyAttributeAllowed(t *testing.T) {
	src := fixtureVariant(t,
		`<Ref ByteOffset="0" BitOffset="0" BitLength="2" DataType="BitArea" DefaultValue="0" TextId="T_Substitute" ValueItemTarget="V_SUBST"/>`,
		`<Ref ByteOffset="0" BitOffset="0" BitLength="2" DataType="BitArea" DefaultValue="0" AllowedValues="" TextId="T_Substitute" ValueItemTarget="V_SUBST"/>`)
	if !bytes.Contains(src, []byte(`ApplicationClass=""`)) {
		t.Fatal("the fixture no longer has an empty ApplicationClass")
	}
	doc, err := ParseBytes(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(doc); err != nil {
		t.Fatal(err)
	}
	if _, err := ToModel(doc, "DAP_2P"); err != nil {
		t.Fatal(err)
	}
}

// TestEmptyAttributeBuilt: a document built in Go cannot say "present but
// empty", so "" there is absent, as it always was: the defaults apply.
func TestEmptyAttributeBuilt(t *testing.T) {
	r := &Ref{BitOffset: "", BitLength: ""}
	if r.bitOffsetOrDefault() != "0" || r.bitLengthOrDefault() != "1" {
		t.Fatalf("%q %q", r.bitOffsetOrDefault(), r.bitLengthOrDefault())
	}
	var parsed Ref
	if err := xml.Unmarshal([]byte(`<Ref BitLength="" BitOffset="4"/>`), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.bitLengthOrDefault() != "" || parsed.bitOffsetOrDefault() != "4" || !parsed.emptyAttr("BitLength") || parsed.emptyAttr("BitOffset") {
		t.Fatalf("%q %q %q", parsed.bitLengthOrDefault(), parsed.bitOffsetOrDefault(), parsed.empty)
	}
	var none *Ref
	if none.emptyAttr("BitLength") {
		t.Fatal("nil Ref")
	}
}
