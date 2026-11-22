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

// The records parameterFixture adds to the submodule SM_IO (module 0x50,
// submodule 0x51) of the compact fixture, after its record 1. Record 2
// has a field of every DataType with an encoding, record 3 one without.
const fixtureRecords = `
                <ParameterRecordDataItem Index="2" Length="24" TransferSequence="2">
                  <Name TextId="T_Rich"/>
                  <Const ByteOffset="23" Data="0x5A"/>
                  <Ref ByteOffset="0" DataType="Unsigned8" DefaultValue="7" AllowedValues="0..9 20" TextId="T_U8"/>
                  <Ref ByteOffset="1" DataType="Integer16" DefaultValue="-2" TextId="T_I16"/>
                  <Ref ByteOffset="3" DataType="Unsigned32" DefaultValue="305419896" TextId="T_U32"/>
                  <Ref ByteOffset="7" DataType="Float32" DefaultValue="1.5" AllowedValues="-2..2.5 1e2" TextId="T_F32"/>
                  <Ref ByteOffset="11" DataType="Boolean" DefaultValue="true" TextId="T_Bool"/>
                  <Ref ByteOffset="12" BitOffset="2" DataType="Bit" DefaultValue="1" TextId="T_Bit" ValueItemTarget="V_ONOFF"/>
                  <Ref ByteOffset="12" BitOffset="4" BitLength="3" DataType="BitArea" DefaultValue="5" TextId="T_Area"/>
                  <Ref ByteOffset="13" BitOffset="1" DataType="BitArea" DefaultValue="1" TextId="T_Area1"/>
                  <Ref ByteOffset="14" DataType="VisibleString" Length="4" DefaultValue="ab" TextId="T_Str"/>
                  <Ref ByteOffset="18" DataType="OctetString" Length="2" DefaultValue="0x01,0x02" TextId="T_Oct"/>
                  <Ref ByteOffset="20" DataType="Unsigned16" DefaultValue="513" Changeable="false" Visible="false" ID="R_FIXED" TextId="T_Fixed"/>
                </ParameterRecordDataItem>
                <ParameterRecordDataItem Index="3" Length="3">
                  <Name TextId="T_Status"/>
                  <Ref ByteOffset="0" DataType="Unsigned16_S" DefaultValue="0" TextId="T_U16S"/>
                  <Ref ByteOffset="2" DataType="Unsigned8" DefaultValue="3" TextId="T_U8"/>
                </ParameterRecordDataItem>`

const fixtureTexts = `
          <Text TextId="T_Rich" Value="Every encodable type"/>
          <Text TextId="T_U8" Value="Unsigned8"/>
          <Text TextId="T_I16" Value="Integer16"/>
          <Text TextId="T_U32" Value="Unsigned32"/>
          <Text TextId="T_F32" Value="Float32"/>
          <Text TextId="T_Bool" Value="Boolean"/>
          <Text TextId="T_Bit" Value="Bit"/>
          <Text TextId="T_Area" Value="BitArea of 3"/>
          <Text TextId="T_Area1" Value="BitArea without BitLength"/>
          <Text TextId="T_Str" Value="VisibleString"/>
          <Text TextId="T_Oct" Value="OctetString"/>
          <Text TextId="T_Fixed" Value="Not changeable"/>
          <Text TextId="T_Status" Value="With status"/>
          <Text TextId="T_U16S" Value="Unsigned16 with status"/>`

// parameterFixture is the compact fixture with fixtureRecords. It is valid
// against the schema of V2.35, which the test checks.
func parameterFixture(t testing.TB) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/fixtures/GSDML-V2.35-OTFabric-CompactFixture-20261027.xml")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, r := range [][2]string{
		{"</ParameterRecordDataItem>\n              </RecordDataList>", "</ParameterRecordDataItem>" + fixtureRecords + "\n              </RecordDataList>"},
		{"        </PrimaryLanguage>", fixtureTexts[1:] + "\n        </PrimaryLanguage>"},
	} {
		if strings.Count(s, r[0]) != 1 {
			t.Fatalf("%q is not once in the compact fixture", r[0])
		}
		s = strings.Replace(s, r[0], r[1], 1)
	}
	return []byte(s)
}

func fixtureParameters(t testing.TB) []ParameterRecord {
	t.Helper()
	doc, err := ParseBytes(parameterFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	recs, err := SubmoduleParameters(doc, "", 0x50, 0x51)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("%d records", len(recs))
	}
	return recs
}

// The golden octets are derived by hand from the encodings, not from
// this package: integers are big endian, two's complement when signed
// (IEC 61158-6-10), Float32 is IEEE 754 binary32, a Boolean one octet.
var fixtureDefault = []byte{
	0x07,       // 0 Unsigned8 7
	0xFF, 0xFE, // 1 Integer16 -2: 0x10000 - 2
	0x12, 0x34, 0x56, 0x78, // 3 Unsigned32 305419896 = 0x12345678
	0x3F, 0xC0, 0x00, 0x00, // 7 Float32 1.5: sign 0, exponent 127 = 0x7F, fraction .1 → 0 01111111 100… = 0x3FC00000
	0x01,           // 11 Boolean true
	0x54,           // 12 bit 2 = 1 (0x04), bits 4..6 = 5 (0b101 << 4 = 0x50)
	0x02,           // 13 bit 1 = 1, BitLength absent: 1 bit
	'a', 'b', 0, 0, // 14 VisibleString "ab" in 4 octets, zero padded
	0x01, 0x02, // 18 OctetString
	0x02, 0x01, // 20 Unsigned16 513 = 0x0201
	0x00, // 22 no field
	0x5A, // 23 Const
}

func TestSubmoduleParametersFixture(t *testing.T) {
	t.Parallel()
	rep, err := CheckBytes(parameterFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	// Its one error: record 2's TransferSequence 2, the only sequenced
	// record of SM_IO (Table D.28), which the plan tests rely on.
	if n := rep.Count(SeverityError); n != 1 || len(issuesOf(rep, RuleTransferSequence, SeverityError)) != 1 {
		t.Fatalf("the fixture has %d errors: %v", n, rep.Issues)
	}
	recs := fixtureParameters(t)
	// Record 1 of the compact fixture: Unsigned16 100 = 0x0064, bit 0 of
	// octet 2 = 0, Unsigned8 1.
	r1 := recs[0]
	if r1.Index != 1 || r1.Name != "Counter parameters" || r1.Length != 4 || r1.TransferSequence != 0 ||
		!bytes.Equal(r1.Default, []byte{0x00, 0x64, 0x00, 0x01}) || len(r1.Fields) != 3 {
		t.Fatalf("record 1 %+v", r1)
	}
	if f := r1.Fields[0]; f.Name != "Debounce (us)" || f.DataType != DataTypeUnsigned16 || f.Length != 2 || f.AllowedValues != "1..10000" || f.Default != "100" || !f.Encodable {
		t.Fatalf("debounce %+v", f)
	}
	if f := r1.Fields[2]; len(f.Values) != 2 || f.Values[1] != (ParameterValue{Content: "2", Text: "Frequency"}) {
		t.Fatalf("mode values %+v", f.Values)
	}
	r2 := recs[1]
	if r2.Index != 2 || r2.TransferSequence != 2 || r2.Name != "Every encodable type" || !bytes.Equal(r2.Default, fixtureDefault) {
		t.Fatalf("record 2 %+v\n% X\n% X", r2, r2.Default, fixtureDefault)
	}
	if len(r2.Consts) != 1 || r2.Consts[0].ByteOffset != 23 || !bytes.Equal(r2.Consts[0].Data, []byte{0x5A}) {
		t.Fatalf("consts %+v", r2.Consts)
	}
	for i, want := range []struct {
		off       uint32
		bit, bits uint8
		length    int
	}{{0, 0, 0, 1}, {1, 0, 0, 2}, {3, 0, 0, 4}, {7, 0, 0, 4}, {11, 0, 0, 1}, {12, 2, 1, 0}, {12, 4, 3, 0}, {13, 1, 1, 0}, {14, 0, 0, 4}, {18, 0, 0, 2}, {20, 0, 0, 2}} {
		f := r2.Fields[i]
		if f.ByteOffset != want.off || f.BitOffset != want.bit || f.BitLength != want.bits || f.Length != want.length || !f.Encodable {
			t.Errorf("field %d %+v, want %+v", i, f, want)
		}
		// Changeable and Visible are true where the file does not say.
		if fixed := i == 10; f.Changeable == fixed || f.Visible == fixed {
			t.Errorf("field %d changeable %v visible %v", i, f.Changeable, f.Visible)
		}
	}
	if f := r2.Fields[5]; len(f.Values) != 2 || f.Values[0].Text != "Off" {
		t.Fatalf("bit values %+v", f.Values)
	}
	if r2.Fields[10].ID != "R_FIXED" {
		t.Fatalf("ID %q", r2.Fields[10].ID)
	}
	// Record 3: a field without a verified encoding. It is described, the
	// record has no default, the other field can be set.
	r3 := recs[2]
	if r3.Default != nil || r3.Fields[0].Encodable || r3.Fields[0].Length != 2 || !r3.Fields[1].Encodable {
		t.Fatalf("record 3 %+v", r3)
	}
	if _, err := r3.Encode(nil); !errors.Is(err, profinet.ErrUnsupported) {
		t.Fatalf("encode record 3: %v", err)
	}
	rec := []byte{0, 0, 0}
	if err := r3.Set(rec, 0, "1"); !errors.Is(err, profinet.ErrUnsupported) {
		t.Fatalf("set Unsigned16_S: %v", err)
	}
	if err := r3.Set(rec, 1, "200"); err != nil || !bytes.Equal(rec, []byte{0, 0, 200}) {
		t.Fatalf("set Unsigned8: %v % X", err, rec)
	}
}

// Set writes one field and nothing else; a value it refuses leaves the
// record as it was.
func TestParameterSet(t *testing.T) {
	t.Parallel()
	r := fixtureParameters(t)[1]
	for _, c := range []struct {
		field int
		value string
		// at and want: the octets at at the value gives, the others
		// unchanged; want nil: refused with err.
		at   int
		want []byte
		err  error
	}{
		{0, "9", 0, []byte{0x09}, nil},
		{0, "20", 0, []byte{0x14}, nil},
		{0, "10", 0, nil, profinet.ErrInvalidConfig}, // not in 0..9 20
		{0, "x", 0, nil, profinet.ErrInvalidConfig},
		{1, "-32768", 1, []byte{0x80, 0x00}, nil}, // 0x10000 - 0x8000
		{1, "32767", 1, []byte{0x7F, 0xFF}, nil},
		{1, "32768", 1, nil, profinet.ErrInvalidConfig},
		{2, "4294967295", 3, []byte{0xFF, 0xFF, 0xFF, 0xFF}, nil},
		{2, "4294967296", 3, nil, profinet.ErrInvalidConfig},
		{2, "-1", 3, nil, profinet.ErrInvalidConfig},
		{3, "-2", 7, []byte{0xC0, 0x00, 0x00, 0x00}, nil},  // sign 1, exponent 128, fraction 0
		{3, "2.5", 7, []byte{0x40, 0x20, 0x00, 0x00}, nil}, // 1.25 * 2^1: 0 10000000 01…
		{3, "100", 7, []byte{0x42, 0xC8, 0x00, 0x00}, nil}, // 1.5625 * 2^6: 0 10000101 1001…
		{3, "2.6", 7, nil, profinet.ErrInvalidConfig},
		{3, "NaN", 7, nil, profinet.ErrInvalidConfig},
		{4, "false", 11, []byte{0x00}, nil},
		{4, "1", 11, []byte{0x01}, nil},
		{4, "yes", 11, nil, profinet.ErrInvalidConfig},
		{5, "0", 12, []byte{0x50}, nil}, // bit 2 cleared, bits 4..6 kept
		{5, "2", 12, nil, profinet.ErrInvalidConfig},
		{6, "7", 12, []byte{0x74}, nil}, // bits 4..6 = 0b111, bit 2 kept
		{6, "0", 12, []byte{0x04}, nil},
		{6, "8", 12, nil, profinet.ErrInvalidConfig},
		{7, "0", 13, []byte{0x00}, nil},
		{7, "2", 13, nil, profinet.ErrInvalidConfig}, // one bit
		{8, "abcd", 14, []byte("abcd"), nil},
		{8, "z", 14, []byte{'z', 0, 0, 0}, nil},
		{8, "abcde", 14, nil, profinet.ErrInvalidConfig},
		{9, "0xFF", 18, []byte{0xFF, 0x00}, nil},
		{9, "0x01,0x02,0x03", 18, nil, profinet.ErrInvalidConfig},
		{10, "65535", 20, []byte{0xFF, 0xFF}, nil}, // not Changeable: reported, not enforced
	} {
		rec := append([]byte(nil), r.Default...)
		err := r.Set(rec, c.field, c.value)
		want := append([]byte(nil), r.Default...)
		if c.want != nil {
			copy(want[c.at:], c.want)
		}
		if (c.err == nil) != (err == nil) || (c.err != nil && !errors.Is(err, c.err)) || !bytes.Equal(rec, want) {
			t.Errorf("field %d = %q: %v\n got % X\nwant % X", c.field, c.value, err, rec, want)
		}
	}
	rec := append([]byte(nil), r.Default...)
	if err := r.Set(rec[:3], 0, "1"); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("short record: %v", err)
	}
	for _, i := range []int{-1, len(r.Fields)} {
		if err := r.Set(rec, i, "1"); !errors.Is(err, profinet.ErrInvalidConfig) {
			t.Fatalf("field %d: %v", i, err)
		}
	}
}

func TestParameterEncode(t *testing.T) {
	t.Parallel()
	r := fixtureParameters(t)[1]
	got, err := r.Encode(map[int]string{0: "1", 6: "0", 8: "OK"})
	want := append([]byte(nil), fixtureDefault...)
	want[0], want[12], want[14], want[15] = 0x01, 0x04, 'O', 'K'
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("%v\n got % X\nwant % X", err, got, want)
	}
	if got, err := r.Encode(nil); err != nil || !bytes.Equal(got, fixtureDefault) {
		t.Fatalf("defaults: %v % X", err, got)
	}
	// Encode does not change the record's Default.
	if !bytes.Equal(r.Default, fixtureDefault) {
		t.Fatalf("default changed: % X", r.Default)
	}
	for _, values := range []map[int]string{{0: "10"}, {99: "1"}, {-1: "1"}} {
		if _, err := r.Encode(values); !errors.Is(err, profinet.ErrInvalidConfig) {
			t.Fatalf("%v: %v", values, err)
		}
	}
}

// AllowedValues is compared exactly, for every numeric type and Boolean;
// a string type with AllowedValues is not compared.
func TestParameterAllowedValues(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		t       DataType
		allowed string
		value   string
		err     error
	}{
		{DataTypeUnsigned64, "0..18446744073709551614", "18446744073709551614", nil},
		{DataTypeUnsigned64, "0..18446744073709551614", "18446744073709551615", profinet.ErrInvalidConfig},
		{DataTypeInteger64, "-9223372036854775808..-1", "-9223372036854775808", nil},
		{DataTypeInteger64, "-9223372036854775808..-1", "0", profinet.ErrInvalidConfig},
		{DataTypeFloat64, "-1.5..2.5e1", "25", nil},
		{DataTypeFloat64, "-1.5..2.5e1", "-1.50001", profinet.ErrInvalidConfig},
		{DataTypeInteger8, "-5 5", "5", nil},
		{DataTypeInteger8, "-5 5", "0", profinet.ErrInvalidConfig},
		{DataTypeBoolean, "1", "true", nil},
		{DataTypeBoolean, "1", "false", profinet.ErrInvalidConfig},
		{DataTypeBoolean, "1", "yes", profinet.ErrInvalidConfig},
		{DataTypeUnsigned8, "1..x", "1", profinet.ErrMalformed},
		{DataTypeUnsigned8, strings.Repeat("1 ", maxAllowedValues+1), "1", profinet.ErrUnsupported},
		{DataTypeVisibleString, "1", "a", profinet.ErrUnsupported},
		{DataTypeOctetString, "1", "0x01", profinet.ErrUnsupported},
	} {
		f := ParameterField{DataType: c.t, AllowedValues: c.allowed}
		if err := f.allowed(c.value); (c.err == nil) != (err == nil) || (c.err != nil && !errors.Is(err, c.err)) {
			t.Errorf("%s %q in %q: %v, want %v", c.t, c.value, clip(c.allowed), err, c.err)
		}
	}
}

func TestSubmoduleParametersLookup(t *testing.T) {
	t.Parallel()
	doc, err := ParseBytes(parameterFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	// The DAP's own submodule, which has no records.
	if recs, err := SubmoduleParameters(doc, "DAP_C", 0x201, 0x1); err != nil || recs == nil || len(recs) != 0 {
		t.Fatalf("DAP submodule: %v %v", recs, err)
	}
	for _, c := range []struct {
		dap               string
		module, submodule uint32
	}{{"NoDAP", 0x50, 0x51}, {"", 0x99, 0x51}, {"", 0x50, 0x99}, {"", 0x201, 0x99}} {
		if _, err := SubmoduleParameters(doc, c.dap, c.module, c.submodule); !errors.Is(err, profinet.ErrNotFound) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	// A ValueItemTarget that names no ValueItem has no values.
	ap := doc.ProfileBody.ApplicationProcess
	ap.ValueList.ValueItems = ap.ValueList.ValueItems[:1] // V_ONOFF only
	recs, err := SubmoduleParameters(doc, "", 0x50, 0x51)
	if err != nil || recs[0].Fields[2].Values != nil || len(recs[0].Fields[1].Values) != 2 {
		t.Fatalf("dangling ValueItemTarget: %v %+v", err, recs)
	}
	// A module the DAP cannot use is not found, though the ModuleList has it.
	dap := ap.DeviceAccessPointList.DeviceAccessPointItems[0]
	dap.UseableModules.ModuleItemRefs = nil
	if _, err := SubmoduleParameters(doc, "", 0x50, 0x51); !errors.Is(err, profinet.ErrNotFound) {
		t.Fatalf("not useable: %v", err)
	}
	dap.UseableModules = nil
	if _, err := SubmoduleParameters(doc, "", 0x50, 0x51); !errors.Is(err, profinet.ErrNotFound) {
		t.Fatalf("no UseableModules: %v", err)
	}
	if _, err := SubmoduleParameters(&Document{}, "", 0x50, 0x51); !errors.Is(err, profinet.ErrMalformed) {
		t.Fatalf("empty document: %v", err)
	}
}

// A malformed record is refused, also after a field without a verified
// encoding, which stops ToModel's conversion of the record.
func TestSubmoduleParametersMalformed(t *testing.T) {
	t.Parallel()
	for name, repl := range map[string][2]string{
		"bit offset after unsupported": {`<Ref ByteOffset="2" DataType="Unsigned8" DefaultValue="3" TextId="T_U8"/>`, `<Ref ByteOffset="2" BitOffset="8" DataType="Bit" DefaultValue="1" TextId="T_U8"/>`},
		"default after unsupported":    {`<Ref ByteOffset="2" DataType="Unsigned8" DefaultValue="3" TextId="T_U8"/>`, `<Ref ByteOffset="2" DataType="Unsigned8" DefaultValue="300" TextId="T_U8"/>`},
		"byte offset":                  {`<Ref ByteOffset="0" DataType="Unsigned8" DefaultValue="7"`, `<Ref ByteOffset="x" DataType="Unsigned8" DefaultValue="7"`},
		"changeable":                   {`Changeable="false"`, `Changeable="no"`},
		"visible":                      {`Visible="false"`, `Visible="no"`},
		"transfer sequence":            {`TransferSequence="2"`, `TransferSequence="x"`},
		"const":                        {`<Const ByteOffset="23" Data="0x5A"/>`, `<Const ByteOffset="24" Data="0x5A"/>`},
	} {
		b := parameterFixture(t)
		if !bytes.Contains(b, []byte(repl[0])) {
			t.Fatalf("%s: %q not in the fixture", name, repl[0])
		}
		doc, err := ParseBytes(bytes.Replace(b, []byte(repl[0]), []byte(repl[1]), 1))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := SubmoduleParameters(doc, "", 0x50, 0x51); !errors.Is(err, profinet.ErrMalformed) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// FuzzParameterSet: Set either refuses a value and leaves the record as
// it was, or changes only the octets of the field.
func FuzzParameterSet(f *testing.F) {
	for _, s := range []string{"0", "1", "-1", "7", "1.5", "true", "0x01,0x02", "abcd", "65536", "1e309", ""} {
		for i := range 11 {
			f.Add(i, s)
		}
	}
	recs := fixtureParameters(f)
	f.Fuzz(func(t *testing.T, i int, value string) {
		r := recs[1]
		if i < 0 || i >= len(r.Fields) {
			return
		}
		rec := append([]byte(nil), r.Default...)
		err := r.Set(rec, i, value)
		fl := r.Fields[i]
		lo, hi := int(fl.ByteOffset), int(fl.ByteOffset)+max(fl.Length, 1)
		for k := range rec {
			if (err != nil || k < lo || k >= hi) && rec[k] != r.Default[k] {
				t.Fatalf("field %d = %q (%v) changed octet %d", i, value, err, k)
			}
		}
		if err == nil && fl.BitLength > 0 {
			mask := byte(0xFF) >> (8 - fl.BitLength) << fl.BitOffset
			if (rec[lo]^r.Default[lo])&^mask != 0 {
				t.Fatalf("field %d = %q changed bits outside it", i, value)
			}
		}
	})
}
