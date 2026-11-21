// SPDX-License-Identifier: MIT

package gsdml

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/pnio"
)

// TestApplyRefStringDefault: a VisibleString, UTF8String or OctetString
// default longer than its Length is refused with ErrMalformed (before
// v0.12.0 it was cut short without an error); one that fits is written and
// the rest of the field is zeroed, also over octets a Const wrote; a
// UTF-8 value counts its encoded octets; white space is kept.
func TestApplyRefStringDefault(t *testing.T) {
	for _, tc := range []struct {
		name, dataType, length, def string
		want                        []byte // nil: refused
	}{
		{"visible fits", "VisibleString", "4", "abcd", []byte("abcd")},
		{"visible short", "VisibleString", "4", "ab", []byte{'a', 'b', 0, 0}},
		{"visible empty", "VisibleString", "2", "", []byte{0, 0}},
		{"visible long", "VisibleString", "3", "abcd", nil},
		{"visible spaces kept", "VisibleString", "4", " a ", []byte{' ', 'a', ' ', 0}},
		{"visible spaces count", "VisibleString", "2", " a ", nil},
		{"utf8 octets fit", "UTF8String", "4", "äö", []byte("äö")},
		{"utf8 octets exceed", "UTF8String", "3", "äö", nil},
		{"utf8 short", "UTF8String", "3", "é", []byte{0xC3, 0xA9, 0}},
		{"octets fit", "OctetString", "2", "0x01,0x02", []byte{1, 2}},
		{"octets short", "OctetString", "3", "0x01", []byte{1, 0, 0}},
		{"octets long", "OctetString", "2", "0x01,0x02,0x03", nil},
		{"octets empty", "OctetString", "2", "", []byte{0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A Const wrote 0xEE over the whole record first.
			rec := bytes.Repeat([]byte{0xEE}, 6)
			err := applyRef(rec, &Ref{ByteOffset: "1", DataType: tc.dataType, Length: tc.length, DefaultValue: tc.def})
			if tc.want == nil {
				if !errors.Is(err, profinet.ErrMalformed) || !strings.Contains(err.Error(), "exceeds its Length") || !strings.Contains(err.Error(), tc.dataType) {
					t.Fatalf("err = %v", err)
				}
				if !bytes.Equal(rec, bytes.Repeat([]byte{0xEE}, 6)) {
					t.Fatalf("refused default wrote % x", rec)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := append(append([]byte{0xEE}, tc.want...), bytes.Repeat([]byte{0xEE}, 5-len(tc.want))...)
			if !bytes.Equal(rec, want) {
				t.Fatalf("% x, want % x", rec, want)
			}
		})
	}
}

// TestToModelStringDefault: ToModel writes a string Ref default that fits
// into the record, zero-padded, and refuses one that exceeds its Length,
// naming the record.
func TestToModelStringDefault(t *testing.T) {
	const old = `<Ref ByteOffset="2" DataType="Unsigned16" DefaultValue="50" AllowedValues="10..1000" TextId="T_Filter"/>`
	record := func(ref string) ([]byte, error) {
		doc, err := ParseBytes(fixtureVariant(t, old, ref))
		if err != nil {
			t.Fatal(err)
		}
		// Validate does not check DefaultValue (an xsd:string).
		if err := Validate(doc); err != nil {
			t.Fatalf("Validate: %v", err)
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
	got, err := record(`<Ref ByteOffset="2" DataType="VisibleString" Length="2" DefaultValue="a" TextId="T_Filter"/>`)
	if err != nil || len(got) < 4 || got[2] != 'a' || got[3] != 0 {
		t.Fatalf("% x %v", got, err)
	}
	for _, ref := range []string{
		`<Ref ByteOffset="2" DataType="VisibleString" Length="2" DefaultValue="abc" TextId="T_Filter"/>`,
		`<Ref ByteOffset="2" DataType="OctetString" Length="2" DefaultValue="0x1,0x2,0x3" TextId="T_Filter"/>`,
	} {
		if _, err := record(ref); !errors.Is(err, profinet.ErrMalformed) || !strings.Contains(err.Error(), "record 102") {
			t.Errorf("%s: %v", ref, err)
		}
	}
}
