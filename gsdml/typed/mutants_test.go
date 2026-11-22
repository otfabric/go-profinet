// SPDX-License-Identifier: MIT

package typed_test

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	profinet "github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/gsdml/typed"
)

const modularFixture = "../testdata/fixtures/GSDML-V2.43-OTFabric-ModularFixture-20261027.xml"

// mutant is a one-rule change of the modular fixture (V2.43) and the
// issue the validator reports, or "" for a document that stays valid.
// The differential test (xsd_differential_test.go) checks every verdict
// against xmllint.
type mutant struct {
	name, old, new string
	n              int // the occurrence to replace (1-based), 0 = all
	issue          string
}

var mutants = []mutant{
	{"valid", "", "", 0, ""},
	{"unknown attribute", `<VendorName Value="OT Fabric"/>`, `<VendorName Value="OT Fabric" Colour="red"/>`, 1, "attribute Colour is not allowed"},
	{"unknown namespaced attribute", `<VendorName Value="OT Fabric"/>`, `<VendorName xmlns:v="urn:v" v:x="1" Value="OT Fabric"/>`, 1, "attribute {urn:v}x is not allowed"},
	{"missing required attribute", `<VendorName Value="OT Fabric"/>`, `<VendorName/>`, 1, "missing required attribute Value"},
	{"enumeration", `ConformanceClass="A"`, `ConformanceClass="Z"`, 1, `"Z" does not match the pattern`},
	{"pattern", `DeviceID="0x00A1"`, `DeviceID="161"`, 1, "does not match the pattern"},
	{"boolean", `MultipleWriteSupported="true"`, `MultipleWriteSupported="yes"`, 1, `"yes" is not a boolean`},
	{"boolean digits", `MultipleWriteSupported="true"`, `MultipleWriteSupported=" 1 "`, 1, ""},
	{"integer range", `Index="100"`, `Index="70000"`, 1, "is not valid for any member type"},
	{"integer", `Length="2"`, `Length="two"`, 1, "is not an integer"},
	{"list item", `Writeable_IM_Records="1 2 3"`, `Writeable_IM_Records="1 x 3"`, 1, "does not match the pattern of ValueListT"},
	{"unexpected first child", `<ModuleInfo>
                <Name TextId="T_DAP1"/>`, `<ModuleInfo>`, 1, "InfoText is not expected here (expected: Name)"},
	{"unexpected child", `<VendorName Value="OT Fabric"/>
    </DeviceIdentity>`, `<VendorName Value="OT Fabric"/>
      <VendorName Value="Twice"/>
    </DeviceIdentity>`, 1, "element {http://www.profibus.com/GSDML/2003/11/DeviceProfile}VendorName is not expected here (no more elements allowed)"},
	{"wrong order", `<InfoText TextId="T_Info"/>
      <VendorName Value="OT Fabric"/>`, `<VendorName Value="OT Fabric"/>
      <InfoText TextId="T_Info"/>`, 1, "VendorName is not expected here (expected: InfoText)"},
	{"foreign element", `<Family MainFamily="I/O" ProductFamily="OTF Modular Fixture"/>`, `<Family MainFamily="I/O" ProductFamily="OTF Modular Fixture"/><v:X xmlns:v="urn:v"/>`, 1, "{urn:v}X is not expected here"},
	{"character content in element content", `<DeviceFunction>`, `<DeviceFunction>text`, 1, "character content is not allowed in element DeviceFunction"},
	{"character content in empty content", `UsedInSlots="1"/>`, `UsedInSlots="1"> </ModuleItemRef>`, 1, "character content is not allowed: element ModuleItemRef has empty content"},
	{"element in empty content", `UsedInSlots="1"/>`, `UsedInSlots="1"><X/></ModuleItemRef>`, 1, "element ModuleItemRef has empty content, not element X"},
	{"element in optional content", `<IOData/>`, `<IOData><X/></IOData>`, 1, "X is not expected here (expected: Input, Output)"},
	{"missing child", `<ModuleInfo CategoryRef="CAT_HEAD">
            <Name TextId="T_DAP1"/>
            <InfoText TextId="T_DAP1_Info"/>
            <VendorName Value="OT Fabric"/>
            <OrderNumber Value="OTF-MOD-1P"/>
            <HardwareRelease Value="1"/>
            <SoftwareRelease Value="V1.0.0"/>
          </ModuleInfo>`, `<ModuleInfo CategoryRef="CAT_HEAD"/>`, 1, "missing child element (expected: Name)"},
	{"element in simple content", `<ProfileRevision>1.00</ProfileRevision>`, `<ProfileRevision><X/></ProfileRevision>`, 1, "element ProfileRevision has a simple type and no element content, not X"},
	{"attribute on a simple-typed element", `<ProfileRevision>1.00</ProfileRevision>`, `<ProfileRevision a="1">1.00</ProfileRevision>`, 1, "attribute a is not allowed: element ProfileRevision has a simple type"},
	{"simple element value", `<ISO15745Part>4</ISO15745Part>`, `<ISO15745Part>four</ISO15745Part>`, 1, `"four"`},
	{"xsi:type", `<VendorName Value="OT Fabric"/>`, `<VendorName xsi:type="X" Value="OT Fabric"/>`, 1, "xsi:type is not supported"},
	{"duplicate key", `<ValueItem ID="V_SUBST">`, `<ValueItem ID="V_ONOFF">`, 1, "duplicate"},
	{"dangling keyref", `ValueItemTarget="V_RANGE"`, `ValueItemTarget="V_NONE"`, 1, "keyref"},
	{"dangling text reference", `<Name TextId="T_DiagSensorLow"/>`, `<Name TextId="T_Nowhere"/>`, 1, "keyref"},
	{"duplicate text", `<Text TextId="T_DAP1" Value="Head, 1 port"/>`, `<Text TextId="T_DAP1" Value="Head, 1 port"/><Text TextId="T_DAP1" Value="Again"/>`, 1, "duplicate"},
	{"wrong root", `<ISO15745Profile xmlns="http://www.profibus.com/GSDML/2003/11/DeviceProfile"`, `<ISO15745Profile xmlns="urn:other" xmlns:d="http://www.profibus.com/GSDML/2003/11/DeviceProfile"`, 1, "no declaration for the root element"},
}

func applyMutant(t testing.TB, base []byte, m mutant) []byte {
	t.Helper()
	if m.old == "" {
		return base
	}
	idx := 0
	for k := 1; ; k++ {
		j := bytes.Index(base[idx:], []byte(m.old))
		if j < 0 {
			t.Fatalf("%s: occurrence %d of %q not in the fixture", m.name, m.n, m.old)
		}
		if k == m.n {
			out := append([]byte(nil), base[:idx+j]...)
			out = append(out, m.new...)
			return append(out, base[idx+j+len(m.old):]...)
		}
		idx += j + len(m.old)
	}
}

func TestMutants(t *testing.T) {
	base, err := os.ReadFile(modularFixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mutants {
		t.Run(m.name, func(t *testing.T) {
			data := applyMutant(t, base, m)
			err := typed.Validate(data)
			if m.issue == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var se *typed.SchemaError
			if !errors.As(err, &se) || !errors.Is(err, profinet.ErrMalformed) {
				t.Fatalf("not a SchemaError: %v", err)
			}
			if se.Version != "2.43" || se.Truncated {
				t.Errorf("version %s truncated %v", se.Version, se.Truncated)
			}
			for _, i := range se.Issues {
				// an element after content that did not match is not
				// assessed, and no identity constraint reports it
				if strings.Contains(i.Message, "keyref") && strings.Contains(m.issue, "is not expected here") {
					t.Errorf("follow-on issue %s", i)
				}
			}
			for _, i := range se.Issues {
				if strings.Contains(i.Message, m.issue) {
					if i.Line == 0 || !strings.HasPrefix(i.Path, "/ISO15745Profile") {
						t.Errorf("issue without position: %+v", i)
					}
					return
				}
			}
			t.Errorf("no issue %q in %v", m.issue, se)
		})
	}
}

func TestAPI(t *testing.T) {
	data, err := os.ReadFile(modularFixture)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := typed.DetectVersion(data); err != nil || v != "2.43" {
		t.Fatalf("DetectVersion %q %v", v, err)
	}
	vs := typed.Versions()
	if len(vs) != 18 || vs[0] != "1.0" || vs[17] != "2.46" {
		t.Fatalf("Versions %v", vs)
	}
	noVersion := bytes.Replace(data, []byte("GSDML-DeviceProfile-V2.43.xsd"), []byte("other.xsd"), 1)
	for name, err := range map[string]error{
		"DetectVersion":   func() error { _, err := typed.DetectVersion(noVersion); return err }(),
		"Validate":        typed.Validate(noVersion),
		"Parse":           func() error { _, err := typed.Parse(noVersion); return err }(),
		"Decode":          func() error { _, err := typed.Decode(noVersion); return err }(),
		"ParseVersion":    func() error { _, err := typed.ParseVersion(data, "9.9"); return err }(),
		"DecodeVersion":   func() error { _, err := typed.DecodeVersion(data, "9.9"); return err }(),
		"ValidateVersion": typed.ValidateVersion(data, "9.9"),
	} {
		if !errors.Is(err, typed.ErrUnknownVersion) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, f := range []func([]byte) error{
		typed.Validate,
		func(d []byte) error { _, err := typed.DetectVersion(d); return err },
		func(d []byte) error { _, err := typed.Parse(d); return err },
		func(d []byte) error { _, err := typed.Decode(d); return err },
		func(d []byte) error { _, err := typed.ParseVersion(d, "2.43"); return err },
		func(d []byte) error { _, err := typed.DecodeVersion(d, "2.43"); return err },
		func(d []byte) error { return typed.ValidateVersion(d, "2.43") },
	} {
		if err := f([]byte("<x")); !errors.Is(err, profinet.ErrMalformed) {
			t.Errorf("malformed: %v", err)
		}
	}
	// the document validates against another version too (2.43 content
	// is not valid V1.0)
	if err := typed.ValidateVersion(data, "1.0"); err == nil {
		t.Error("V2.43 content valid against V1.0")
	}
	d, err := typed.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if d.SchemaVersion() != "2.43" || d.RootElement() == nil {
		t.Fatal("document")
	}
	// Decode accepts what only validation rejects: a missing required
	// attribute
	bad := bytes.Replace(data, []byte(`<VendorName Value="OT Fabric"/>`), []byte(`<VendorName/>`), 1)
	if _, err := typed.Parse(bad); err == nil {
		t.Error("Parse accepted an invalid document")
	}
	if _, err := typed.Decode(bad); err != nil {
		t.Errorf("Decode: %v", err)
	}
	// but not what the model cannot hold
	unknown := bytes.Replace(data, []byte(`<VendorName Value="OT Fabric"/>`), []byte(`<VendorName Value="OT Fabric" X="1"/>`), 1)
	if _, err := typed.Decode(unknown); !errors.Is(err, profinet.ErrMalformed) {
		t.Errorf("Decode of an unknown attribute: %v", err)
	}
	wrongRoot := bytes.Replace(data, []byte("<ISO15745Profile "), []byte("<Other "), 1)
	wrongRoot = bytes.Replace(wrongRoot, []byte("</ISO15745Profile>"), []byte("</Other>"), 1)
	if _, err := typed.DecodeVersion(wrongRoot, "2.43"); !errors.Is(err, profinet.ErrMalformed) {
		t.Errorf("wrong root: %v", err)
	}
	var se *typed.SchemaError
	err = typed.Validate(bad)
	if !errors.As(err, &se) || !strings.Contains(se.Error(), "missing required attribute Value") {
		t.Fatalf("%v", err)
	}
}

func TestSchemaErrorMessage(t *testing.T) {
	se := &typed.SchemaError{Version: "2.46"}
	for i := range 12 {
		se.Issues = append(se.Issues, typed.Issue{Line: i + 1, Path: "/a", Message: "m"})
	}
	msg := se.Error()
	if !strings.Contains(msg, "V2.46") || !strings.Contains(msg, "(and 2 more)") || !strings.Contains(msg, "line 1: /a: m") {
		t.Errorf("%s", msg)
	}
	if (typed.Issue{Path: "/a", Message: "m"}).String() != "/a: m" {
		t.Error("Issue.String without a line")
	}
}
