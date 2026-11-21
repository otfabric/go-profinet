// SPDX-License-Identifier: MIT

package gsdml

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/pnio"
)

// TestApplyRefBitLengthDefault: an absent BitLength of a BitArea Ref is 1
// and an absent BitOffset 0, the defaults of ISO 15745-4:2003/Amd 1:2006,
// Annex D, Table D.31 (go-profinet issue #74). A value that is present is
// used as written, and one that is no Unsigned8, or a BitLength of 0, is
// malformed. The bits of the octet outside the Ref are kept.
func TestApplyRefBitLengthDefault(t *testing.T) {
	for _, tc := range []struct {
		name, bitOffset, bitLength, def string
		want                            byte // from 0xFF
		ok                              bool
	}{
		{name: "absent: one bit", bitOffset: "0", def: "0", want: 0xFE, ok: true},
		{name: "absent, set", bitOffset: "5", def: "1", want: 0xFF, ok: true},
		{name: "absent, cleared at bit 5", bitOffset: "5", def: "0", want: 0xDF, ok: true},
		{name: "absent BitOffset too", def: "0", want: 0xFE, ok: true},
		{name: "absent: a value of two bits does not fit", bitOffset: "0", def: "2"},
		{name: "1", bitOffset: "0", bitLength: "1", def: "0", want: 0xFE, ok: true},
		{name: "2", bitOffset: "3", bitLength: "2", def: "2", want: 0xF7, ok: true},
		{name: "8", bitOffset: "0", bitLength: "8", def: "0x5A", want: 0x5A, ok: true},
		{name: "hexadecimal", bitOffset: "0", bitLength: "0x2", def: "1", want: 0xFD, ok: true},
		{name: "beyond the octet", bitOffset: "7", bitLength: "2", def: "0"},
		{name: "wider than an octet", bitOffset: "0", bitLength: "9", def: "0"},
		{name: "0", bitOffset: "0", bitLength: "0", def: "0"},
		{name: "blank", bitOffset: "0", bitLength: " ", def: "0"},
		{name: "no number", bitOffset: "0", bitLength: "abc", def: "0"},
		{name: "negative", bitOffset: "0", bitLength: "-1", def: "0"},
		{name: "above Unsigned8", bitOffset: "0", bitLength: "257", def: "0"},
		{name: "BitOffset no number", bitOffset: "x", def: "0"},
		{name: "BitOffset above Unsigned8", bitOffset: "256", def: "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := []byte{0xFF, 0xFF}
			err := applyRef(rec, &Ref{ByteOffset: "1", BitOffset: tc.bitOffset, BitLength: tc.bitLength, DataType: "BitArea", DefaultValue: tc.def})
			if !tc.ok {
				if !errors.Is(err, profinet.ErrMalformed) || !bytes.Equal(rec, []byte{0xFF, 0xFF}) {
					t.Fatalf("err %v, record % x", err, rec)
				}
				return
			}
			if err != nil || !bytes.Equal(rec, []byte{0xFF, tc.want}) {
				t.Fatalf("record % x, want ff %02x; %v", rec, tc.want, err)
			}
		})
	}
	// The error names the BitLength that was refused.
	err := applyRef(make([]byte, 6), &Ref{ByteOffset: "5", BitLength: "abc", DataType: "BitArea", DefaultValue: "0"})
	if err == nil || !strings.Contains(err.Error(), `BitLength "abc"`) {
		t.Fatalf("err %v", err)
	}
}

// TestBitLengthDefaultDocument: a published GSDML file that leaves out the
// BitLength of a BitArea Ref loads (issue #74), with the same record as
// BitLength="1"; one with a BitLength that is no number is refused by
// ToModel and reported by Validate. The variants are edits of the fixture's
// text, not documents the package wrote.
func TestBitLengthDefaultDocument(t *testing.T) {
	b, err := os.ReadFile("testdata/fixtures/GSDML-V2.43-OTFabric-ModularFixture-20261027.xml")
	if err != nil {
		t.Fatal(err)
	}
	const ref = `<Ref ByteOffset="0" BitOffset="0" BitLength="2" DataType="BitArea" DefaultValue="0" TextId="T_Substitute" ValueItemTarget="V_SUBST"/>`
	if !bytes.Contains(b, []byte(ref)) {
		t.Fatal("the fixture's Ref changed")
	}
	// A Const sets every bit of the octet; the one-bit Ref at bit 3 clears
	// only its bit.
	variant := func(bitLength string) []byte {
		r := `<Const ByteOffset="0" Data="0xFF"/><Ref ByteOffset="0" BitOffset="3"` + bitLength + ` DataType="BitArea" DefaultValue="0" AllowedValues="0 1" TextId="T_Substitute"/>`
		return bytes.Replace(b, []byte(ref), []byte(r), 1)
	}
	record := func(src []byte) ([]byte, error, error) {
		doc, err := ParseBytes(src)
		if err != nil {
			t.Fatal(err)
		}
		verrs := Validate(doc)
		m, err := ToModel(doc, "DAP_2P")
		if err != nil {
			return nil, verrs, err
		}
		var got []byte
		m.Each(func(p model.PluggedSubmodule) {
			for _, r := range p.Submodule.Records {
				if r.Index == pnio.Index(101) {
					got = r.Default
				}
			}
		})
		return got, verrs, nil
	}
	absent, verrs, err := record(variant(""))
	if err != nil || verrs != nil {
		t.Fatalf("absent BitLength: %v %v", err, verrs)
	}
	one, _, err := record(variant(` BitLength="1"`))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(absent, []byte{0xF7}) || !bytes.Equal(absent, one) {
		t.Fatalf("absent BitLength % x, BitLength=\"1\" % x, want f7", absent, one)
	}
	for _, bad := range []string{` BitLength="0"`, ` BitLength="abc"`, ` BitLength="-1"`} {
		_, verrs, err := record(variant(bad))
		if !errors.Is(err, profinet.ErrMalformed) {
			t.Errorf("%s: ToModel %v", bad, err)
		}
		if bad != ` BitLength="0"` && verrs == nil {
			t.Errorf("%s: Validate reported nothing", bad)
		}
	}
}
