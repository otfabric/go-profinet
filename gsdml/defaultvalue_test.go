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
