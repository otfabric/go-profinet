// SPDX-License-Identifier: MIT

package gsdml

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet"
)

// TestApplyRefBoolean: a Boolean Ref default is one octet, 0x00 or 0x01,
// from the xsd:boolean forms; any other value is refused with
// ErrMalformed. Before v0.12.0 it took the Unsigned8 path, so "true" was
// refused and "2" wrote 0x02.
func TestApplyRefBoolean(t *testing.T) {
	for _, tc := range []struct {
		def  string
		want byte
		ok   bool
	}{
		{"true", 1, true}, {"false", 0, true}, {"1", 1, true}, {"0", 0, true}, {" true ", 1, true},
		{"2", 0, false}, {"255", 0, false}, {"TRUE", 0, false}, {"yes", 0, false}, {"", 0, false},
	} {
		rec := []byte{0xEE, 0xEE, 0xEE}
		err := applyRef(rec, &Ref{ByteOffset: "1", DataType: "Boolean", DefaultValue: tc.def})
		if !tc.ok {
			if !errors.Is(err, profinet.ErrMalformed) || !bytes.Equal(rec, []byte{0xEE, 0xEE, 0xEE}) {
				t.Errorf("%q: %v % x", tc.def, err, rec)
			}
			continue
		}
		if err != nil || !bytes.Equal(rec, []byte{0xEE, tc.want, 0xEE}) {
			t.Errorf("%q: %v % x", tc.def, err, rec)
		}
	}
}

// TestApplyRefUnverifiedEncoding: a Ref whose DataType has no verified
// DefaultValue encoding is refused with ErrUnsupported and leaves the
// record as it was. Before v0.12.0 these took the unsigned path: an
// Integer16_S could not be negative and its status bits were ignored, and
// a TimeStamp was zero-filled above its low 8 octets.
func TestApplyRefUnverifiedEncoding(t *testing.T) {
	for _, dt := range []string{
		"Unsigned8_S", "Unsigned16_S", "Integer16_S", "Integer32_S", "Unsigned32_S", "OctetString_S",
		"Unsigned8+Unsigned8", "Float32+Unsigned8", "Float32+Status8", "Float64+Status8", "OctetString2+Unsigned8",
		"Date", "TimeOfDay with date indication", "TimeOfDay without date indication",
		"TimeDifference with date indication", "TimeDifference without date indication",
		"NetworkTime", "NetworkTimeDifference", "TimeStamp", "TimeStampDifference", "TimeStampDifferenceShort",
		"N2", "N4", "V2", "L2", "R2", "T2", "T4", "D2", "E2", "C4", "X2", "X4", "Unipolar2.16",
		"F_MessageTrailer4Byte", "F_MessageTrailer5Byte",
	} {
		if _, err := DataItemSize(dt, "2"); err != nil {
			t.Errorf("%s is not a sized DataType: %v", dt, err)
		}
		rec := bytes.Repeat([]byte{0xEE}, 16)
		err := applyRef(rec, &Ref{ByteOffset: "0", DataType: dt, Length: "2", DefaultValue: "1"})
		if !errors.Is(err, profinet.ErrUnsupported) || !strings.Contains(err.Error(), "no verified DefaultValue encoding") || !strings.Contains(err.Error(), dt) {
			t.Errorf("%s: %v", dt, err)
		}
		if !bytes.Equal(rec, bytes.Repeat([]byte{0xEE}, 16)) {
			t.Errorf("%s wrote % x", dt, rec)
		}
	}
	// An unknown DataType stays unsupported too.
	if err := applyRef(make([]byte, 4), &Ref{ByteOffset: "0", DataType: "Nibble", DefaultValue: "1"}); !errors.Is(err, profinet.ErrUnsupported) {
		t.Errorf("unknown DataType: %v", err)
	}
}

// TestRefDefaultEncodedCoversSizes: every DataType applyRef writes has a
// size, and every sized DataType is either written or refused, so a
// DataType added to the catalog is not silently written as unsigned.
func TestRefDefaultEncodedCoversSizes(t *testing.T) {
	for dt := range refDefaultEncoded {
		if _, err := DataItemSize(string(dt), "1"); err != nil {
			t.Errorf("%s: %v", dt, err)
		}
	}
	for dt := range dataTypeSizes {
		err := applyRef(make([]byte, 16), &Ref{ByteOffset: "0", DataType: string(dt), DefaultValue: "1"})
		if refDefaultEncoded[dt] != (err == nil) {
			t.Errorf("%s: encoded %v, err %v", dt, refDefaultEncoded[dt], err)
		}
	}
}

// TestToModelUnverifiedEncoding: ToModel refuses a record with such a Ref
// with ErrUnsupported, naming the record; a Boolean Ref converts.
func TestToModelUnverifiedEncoding(t *testing.T) {
	const old = `<Ref ByteOffset="2" DataType="Unsigned16" DefaultValue="50" AllowedValues="10..1000" TextId="T_Filter"/>`
	for _, tc := range []struct {
		ref  string
		want error
	}{
		{`<Ref ByteOffset="2" DataType="Integer16_S" DefaultValue="-1" TextId="T_Filter"/>`, profinet.ErrUnsupported},
		{`<Ref ByteOffset="2" DataType="Boolean" DefaultValue="true" TextId="T_Filter"/>`, nil},
		{`<Ref ByteOffset="2" DataType="Boolean" DefaultValue="2" TextId="T_Filter"/>`, profinet.ErrMalformed},
	} {
		doc, err := ParseBytes(fixtureVariant(t, old, tc.ref))
		if err != nil {
			t.Fatal(err)
		}
		_, err = ToModel(doc, "DAP_2P")
		if tc.want == nil && err != nil || tc.want != nil && (!errors.Is(err, tc.want) || !strings.Contains(err.Error(), "record 102")) {
			t.Errorf("%s: %v", tc.ref, err)
		}
	}
}
