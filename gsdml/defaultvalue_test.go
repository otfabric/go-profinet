// SPDX-License-Identifier: MIT

package gsdml

import (
	"bytes"
	"errors"
	"math"
	"os"
	"strconv"
	"testing"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/pnio"
)

// TestParseInt: decimal (leading zeros are decimal, as in xsd:integer) and
// 0x hexadecimal, with an optional sign; Go's octal, binary and digit
// separator forms are refused, and so is a value outside bits bits.
func TestParseInt(t *testing.T) {
	for _, tc := range []struct {
		s    string
		bits int
		want int64
		ok   bool
	}{
		{"0", 8, 0, true},
		{"010", 8, 10, true},
		{"-010", 8, -10, true},
		{"+010", 8, 10, true},
		{"-0", 8, 0, true},
		{" 7 ", 8, 7, true},
		{"0x10", 8, 16, true},
		{"-0x10", 8, -16, true},
		{"127", 8, 127, true},
		{"-128", 8, -128, true},
		{"128", 8, 0, false},
		{"-129", 8, 0, false},
		{"32767", 16, 32767, true},
		{"-32768", 16, -32768, true},
		{"-32769", 16, 0, false},
		{"9223372036854775807", 64, math.MaxInt64, true},
		{"-9223372036854775808", 64, math.MinInt64, true},
		{"9223372036854775808", 64, 0, false},
		{"-9223372036854775809", 64, 0, false},
		{"0b101", 8, 0, false},
		{"0o7", 8, 0, false},
		{"1_0", 8, 0, false},
		{"0x1_0", 8, 0, false},
		{"--1", 8, 0, false},
		{"+-1", 8, 0, false},
		{"- 1", 8, 0, false},
		{"-", 8, 0, false},
		{"", 8, 0, false},
		{"0x", 8, 0, false},
		{"1.0", 8, 0, false},
		{"1e2", 8, 0, false},
	} {
		got, err := parseInt(tc.s, tc.bits)
		if tc.ok != (err == nil) || got != tc.want {
			t.Errorf("parseInt(%q, %d) = %d, %v; want %d ok %v", tc.s, tc.bits, got, err, tc.want, tc.ok)
		}
	}
}

// TestApplyRefIntegerDefault: the DefaultValue of an integer Ref is
// decimal or 0x hexadecimal. "010" is ten, not Go's octal eight, and the
// other Go literal forms are malformed (go-profinet, base-0 parsing before
// v0.12.0).
func TestApplyRefIntegerDefault(t *testing.T) {
	for _, tc := range []struct {
		dataType, def string
		want          []byte // nil: malformed
	}{
		{"Unsigned8", "010", []byte{10}},
		{"Unsigned8", "0x10", []byte{0x10}},
		{"Unsigned8", "255", []byte{0xFF}},
		{"Unsigned8", "256", nil},
		{"Unsigned8", "-1", nil},
		{"Unsigned8", "+1", nil},
		{"Unsigned8", "0b101", nil},
		{"Unsigned8", "0o7", nil},
		{"Unsigned8", "1_0", nil},
		{"Unsigned16", "00100", []byte{0, 100}},
		{"Unsigned16", "0x1234", []byte{0x12, 0x34}},
		{"Unsigned32", "0010", []byte{0, 0, 0, 10}},
		{"Unsigned64", "010", []byte{0, 0, 0, 0, 0, 0, 0, 10}},
		{"Integer8", "-010", []byte{0xF6}},
		{"Integer8", "010", []byte{10}},
		{"Integer8", "-128", []byte{0x80}},
		{"Integer8", "128", nil},
		{"Integer8", "0b1", nil},
		{"Integer16", "-0x10", []byte{0xFF, 0xF0}},
		{"Integer16", "-1_0", nil},
		{"Integer32", "-010", []byte{0xFF, 0xFF, 0xFF, 0xF6}},
		{"Integer64", "010", []byte{0, 0, 0, 0, 0, 0, 0, 10}},
	} {
		t.Run(tc.dataType+"/"+tc.def, func(t *testing.T) {
			size, err := DataItemSize(tc.dataType, "")
			if err != nil {
				t.Fatal(err)
			}
			rec := bytes.Repeat([]byte{0xAA}, size)
			err = applyRef(rec, &Ref{ByteOffset: "0", DataType: tc.dataType, DefaultValue: tc.def})
			if tc.want == nil {
				if !errors.Is(err, profinet.ErrMalformed) || !bytes.Equal(rec, bytes.Repeat([]byte{0xAA}, size)) {
					t.Fatalf("err %v, record % x", err, rec)
				}
				return
			}
			if err != nil || !bytes.Equal(rec, tc.want) {
				t.Fatalf("record % x, want % x; %v", rec, tc.want, err)
			}
		})
	}
	// The bit path parses its DefaultValue the same way.
	for def, want := range map[string]byte{"01": 0x02, "0x1": 0x02, "0b1": 0, "0o1": 0} {
		rec := []byte{0}
		err := applyRef(rec, &Ref{ByteOffset: "0", BitOffset: "1", DataType: "Bit", DefaultValue: def})
		if want == 0 {
			if !errors.Is(err, profinet.ErrMalformed) {
				t.Errorf("Bit %q: err %v", def, err)
			}
			continue
		}
		if err != nil || rec[0] != want {
			t.Errorf("Bit %q: record %02x, %v", def, rec[0], err)
		}
	}
}

// TestIntegerDefaultDocument: a GSDML document whose integer Refs have
// leading-zero defaults gives the decimal values in the record, and one
// with a Go octal or binary literal is refused by ToModel. The variants
// are edits of the fixture's text.
func TestIntegerDefaultDocument(t *testing.T) {
	b, err := os.ReadFile("testdata/fixtures/GSDML-V2.43-OTFabric-ModularFixture-20261027.xml")
	if err != nil {
		t.Fatal(err)
	}
	const u16 = `<Ref ByteOffset="2" DataType="Unsigned16" DefaultValue="50" AllowedValues="10..1000" TextId="T_Filter"/>`
	const i16 = `<Ref ByteOffset="4" DataType="Integer16" DefaultValue="0" AllowedValues="-1000..1000" TextId="T_Offset"/>`
	if !bytes.Contains(b, []byte(u16)) || !bytes.Contains(b, []byte(i16)) {
		t.Fatal("the fixture's Refs changed")
	}
	record := func(u, i string) ([]byte, error) {
		src := bytes.Replace(b, []byte(u16), []byte(`<Ref ByteOffset="2" DataType="Unsigned16" DefaultValue="`+u+`" TextId="T_Filter"/>`), 1)
		src = bytes.Replace(src, []byte(i16), []byte(`<Ref ByteOffset="4" DataType="Integer16" DefaultValue="`+i+`" TextId="T_Offset"/>`), 1)
		doc, err := ParseBytes(src)
		if err != nil {
			t.Fatal(err)
		}
		// Check reports a DefaultValue ToModel refuses, and nothing else.
		onlyRecordErrors(t, doc)
		m, err := ToModel(doc, "DAP_2P")
		if err != nil {
			return nil, err
		}
		var got []byte
		m.Each(func(p model.PluggedSubmodule) {
			for _, r := range p.Submodule.Records {
				if r.Index == pnio.Index(102) {
					got = r.Default
				}
			}
		})
		return got, nil
	}
	decimal, err := record("50", "-10")
	if err != nil {
		t.Fatal(err)
	}
	padded, err := record("0050", "-010")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decimal[2:], []byte{0, 50, 0xFF, 0xF6}) || !bytes.Equal(decimal, padded) {
		t.Fatalf("decimal % x, leading zeros % x", decimal, padded)
	}
	for _, bad := range [][2]string{{"0b110010", "0"}, {"50", "-0o12"}, {"5_0", "0"}} {
		if _, err := record(bad[0], bad[1]); !errors.Is(err, profinet.ErrMalformed) {
			t.Errorf("%q %q: ToModel %v", bad[0], bad[1], err)
		}
	}
}

// FuzzParseInt: parseInt never panics, and a value it accepts in decimal
// form is the one strconv gives for the digits in base 10.
func FuzzParseInt(f *testing.F) {
	for _, s := range []string{"0", "010", "-010", "+7", "0x7F", "-0x80", "-128", "128", "0b1", "1_0", "--1", "- 1", "9223372036854775808"} {
		f.Add(s, uint8(8))
	}
	f.Fuzz(func(t *testing.T, s string, bits uint8) {
		n := int(bits%64) + 1
		v, err := parseInt(s, n)
		if err != nil {
			return
		}
		if n < 64 && (v < -(1<<(n-1)) || v >= 1<<(n-1)) {
			t.Fatalf("parseInt(%q, %d) = %d outside the range", s, n, v)
		}
		if w, err := strconv.ParseInt(trimPlus(s), 10, n); err == nil && w != v {
			t.Fatalf("parseInt(%q, %d) = %d, base 10 gives %d", s, n, v, w)
		}
	})
}

func trimPlus(s string) string {
	s = string(bytes.TrimSpace([]byte(s)))
	if len(s) > 1 && s[0] == '+' && s[1] >= '0' && s[1] <= '9' {
		return s[1:]
	}
	return s
}

// TestRefDefaultDecimalHexEquivalence: a decimal and a 0x hexadecimal
// integer DefaultValue of the same value write the same record octets for
// every integer DataType, at the bounds of each. Hexadecimal is a
// compatibility tolerance (docs/GSDML.md): ISO 15745-4:2003/Amd 1:2006
// Table D.31 constrains DefaultValue to decimal digits. It is a magnitude
// with an optional sign, not a two's complement pattern: "0xFF" is 255, so
// it does not fit an Integer8.
func TestRefDefaultDecimalHexEquivalence(t *testing.T) {
	for _, tc := range []struct{ dataType, dec, hex string }{
		{"Unsigned8", "0", "0x0"}, {"Unsigned8", "255", "0xFF"}, {"Unsigned8", "171", "0xab"},
		{"Unsigned16", "65535", "0xFFFF"}, {"Unsigned32", "4294967295", "0xFFFFFFFF"},
		{"Unsigned64", "18446744073709551615", "0xFFFFFFFFFFFFFFFF"},
		{"Integer8", "127", "0x7F"}, {"Integer8", "-128", "-0x80"}, {"Integer8", "-1", "-0x1"},
		{"Integer16", "-32768", "-0x8000"}, {"Integer32", "2147483647", "0x7FFFFFFF"},
		{"Integer64", "-9223372036854775808", "-0x8000000000000000"},
		{"Bit", "1", "0x1"},
	} {
		write := func(def string) ([]byte, error) {
			rec := make([]byte, 8)
			return rec, applyRef(rec, &Ref{ByteOffset: "0", DataType: tc.dataType, DefaultValue: def})
		}
		d, err1 := write(tc.dec)
		h, err2 := write(tc.hex)
		if err1 != nil || err2 != nil || !bytes.Equal(d, h) {
			t.Errorf("%s %s / %s: % x %v, % x %v", tc.dataType, tc.dec, tc.hex, d, err1, h, err2)
		}
	}
	// Width and signedness hold for hexadecimal as for decimal.
	for _, tc := range []struct{ dataType, def string }{
		{"Unsigned8", "0x100"}, {"Unsigned8", "256"}, {"Unsigned8", "-0x1"},
		{"Integer8", "0xFF"}, {"Integer8", "0x80"}, {"Integer8", "-0x81"}, {"Integer8", "128"},
		{"Unsigned16", "0x10000"}, {"Integer16", "0x8000"},
		{"Unsigned8", "0x"}, {"Unsigned8", "0x+1"}, {"Unsigned8", "0b1"}, {"Unsigned8", "0o7"}, {"Unsigned8", "1_0"},
		{"Integer16", "0x-1"}, {"Integer16", "0x1_0"},
	} {
		if err := applyRef(make([]byte, 8), &Ref{ByteOffset: "0", DataType: tc.dataType, DefaultValue: tc.def}); !errors.Is(err, profinet.ErrMalformed) {
			t.Errorf("%s %q: %v", tc.dataType, tc.def, err)
		}
	}
}

// TestRefDefaultLeadingZeros: leading zeros are decimal for every integer
// DataType ("010" is ten, "08" and "09" are valid), never octal.
func TestRefDefaultLeadingZeros(t *testing.T) {
	for _, tc := range []struct {
		dataType, def string
		want          []byte
	}{
		{"Unsigned8", "010", []byte{10}}, {"Unsigned8", "08", []byte{8}}, {"Unsigned8", "0009", []byte{9}},
		{"Unsigned16", "00100", []byte{0, 100}}, {"Integer8", "-010", []byte{0xF6}}, {"Integer8", "+010", []byte{10}},
		{"Integer16", "-0008", []byte{0xFF, 0xF8}}, {"Unsigned32", "0255", []byte{0, 0, 0, 255}},
	} {
		rec := make([]byte, len(tc.want))
		if err := applyRef(rec, &Ref{ByteOffset: "0", DataType: tc.dataType, DefaultValue: tc.def}); err != nil || !bytes.Equal(rec, tc.want) {
			t.Errorf("%s %q: % x %v, want % x", tc.dataType, tc.def, rec, err, tc.want)
		}
	}
}

// TestGeneratedDocumentHasNoRefDefault: FromModel describes a record's
// default with Const octets, so it writes no Ref DefaultValue, decimal or
// hexadecimal; should it ever write one, it is to be decimal (the form of
// ISO 15745-4 Table D.31).
func TestGeneratedDocumentHasNoRefDefault(t *testing.T) {
	m := &model.Device{
		Identity: model.Identity{VendorID: 0xF000, DeviceID: 1, VendorName: "OT Fabric", TypeOfStation: "test", OrderID: "T", HardwareRevision: 1},
		DAP:      model.Module{IdentNumber: 1, Submodules: []model.Submodule{{Subslot: 1, IdentNumber: 1}}},
		Slots: []model.Slot{{Number: 1, Module: model.Module{IdentNumber: 2, Submodules: []model.Submodule{{Subslot: 1, IdentNumber: 2, InputLen: 1,
			Records: []model.Record{{Index: 0x10, Length: 2, Default: []byte{0, 1}}}}}}}},
	}
	doc, err := FromModel(m, GenerateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("<Ref ")) || bytes.Contains(b, []byte("DefaultValue=")) || !bytes.Contains(b, []byte(`Data="0x00,0x01"`)) {
		t.Fatalf("generated record:\n%s", b)
	}
}
