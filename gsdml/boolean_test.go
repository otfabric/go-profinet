// SPDX-License-Identifier: MIT

package gsdml

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet"
)

// TestParseBoolAttr: the lexical space of xsd:boolean (true, false, 1, 0,
// with white space around it); an empty value is the default; anything
// else, including other capitalisations, is an error.
func TestParseBoolAttr(t *testing.T) {
	for _, tc := range []struct {
		s        string
		def      bool
		want, ok bool
	}{
		{"true", false, true, true},
		{"1", false, true, true},
		{"false", true, false, true},
		{"0", true, false, true},
		{" true ", false, true, true},
		{"\tfalse\n", true, false, true},
		{"", true, true, true},
		{"", false, false, true},
		{" ", false, false, false},
		{"TRUE", false, false, false},
		{"True", false, false, false},
		{"yes", false, false, false},
		{"2", false, false, false},
		{"01", false, false, false},
		{"t", false, false, false},
	} {
		got, err := parseBool(tc.s, false, tc.def)
		if tc.ok != (err == nil) || got != tc.want {
			t.Errorf("parseBool(%q, %v) = %v, %v; want %v ok %v", tc.s, tc.def, got, err, tc.want, tc.ok)
		}
	}
	// Given as "" (present but empty) is no Boolean either.
	if _, err := parseBool("", true, true); err == nil {
		t.Fatal("an empty attribute was taken for an absent one")
	}
	if _, err := parseBool("yes", false, false); err == nil || !strings.Contains(err.Error(), `"yes"`) {
		t.Fatalf("err %v", err)
	}
}

// TestBooleanAttributesDocument: ToModel reads LLDP_NoD_Supported and
// DCP_HelloSupported as written, takes an absent one as false, and refuses
// one that is no Boolean, which Validate reports as well; before v0.12.0
// such a value silently became false. The variants are edits of the
// fixture's text.
func TestBooleanAttributesDocument(t *testing.T) {
	b, err := os.ReadFile("testdata/fixtures/GSDML-V2.43-OTFabric-ModularFixture-20261027.xml")
	if err != nil {
		t.Fatal(err)
	}
	// DAP_1P is conformance class A, so its LLDP capability is the
	// attribute's.
	const lldp = `ID="DAP_1P" PNIO_Version="V2.43" PhysicalSlots="0..8" ModuleIdentNumber="0x00000101" MinDeviceInterval="32" DNS_CompatibleName="otf-modular" FixedInSlots="0" ObjectUUID_LocalIndex="1" MultipleWriteSupported="true" DeviceAccessSupported="false" CheckDeviceID_Allowed="true" NameOfStationNotTransferable="false" LLDP_NoD_Supported="true"`
	const ifc = `<InterfaceSubmoduleItem ID="IF_1P"`
	if !bytes.Contains(b, []byte(lldp)) || !bytes.Contains(b, []byte(ifc)) {
		t.Fatal("the fixture's DAP_1P changed")
	}
	variant := func(lldpAttr, helloAttr string) []byte {
		src := bytes.Replace(b, []byte(lldp), []byte(strings.TrimSuffix(lldp, ` LLDP_NoD_Supported="true"`)+lldpAttr), 1)
		return bytes.Replace(src, []byte(ifc), []byte(ifc+helloAttr), 1)
	}
	type caps struct{ lldp, hello bool }
	load := func(src []byte) (caps, error, error) {
		doc, err := ParseBytes(src)
		if err != nil {
			t.Fatal(err)
		}
		verrs := Validate(doc)
		m, err := ToModel(doc, "DAP_1P")
		if err != nil {
			return caps{}, verrs, err
		}
		return caps{m.Capabilities.LLDP, m.Capabilities.DCPHello}, verrs, nil
	}
	for _, tc := range []struct {
		name, lldp, hello string
		want              caps
	}{
		{"true", ` LLDP_NoD_Supported="true"`, ` DCP_HelloSupported="true"`, caps{true, true}},
		{"1", ` LLDP_NoD_Supported="1"`, ` DCP_HelloSupported="1"`, caps{true, true}},
		{"false", ` LLDP_NoD_Supported="false"`, ` DCP_HelloSupported="0"`, caps{false, false}},
		{"absent", ``, ``, caps{false, false}},
	} {
		got, verrs, err := load(variant(tc.lldp, tc.hello))
		if err != nil || verrs != nil || got != tc.want {
			t.Errorf("%s: %+v, want %+v; %v %v", tc.name, got, tc.want, err, verrs)
		}
	}
	for _, tc := range []struct{ name, lldp, hello, attr string }{
		{"LLDP yes", ` LLDP_NoD_Supported="yes"`, ``, "LLDP_NoD_Supported"},
		{"LLDP TRUE", ` LLDP_NoD_Supported="TRUE"`, ``, "LLDP_NoD_Supported"},
		{"Hello 2", ``, ` DCP_HelloSupported="2"`, "DCP_HelloSupported"},
	} {
		_, verrs, err := load(variant(tc.lldp, tc.hello))
		if !errors.Is(err, profinet.ErrMalformed) || !strings.Contains(err.Error(), tc.attr) {
			t.Errorf("%s: ToModel %v", tc.name, err)
		}
		if verrs == nil {
			t.Errorf("%s: Validate reported nothing", tc.name)
		}
	}
}
