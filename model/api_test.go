// SPDX-License-Identifier: MIT

package model

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet/pnio"
)

// apiDevice has a module in slot 1 whose submodules belong to two APIs,
// listed out of subslot order, and one in slot 2 of API 0x3A00 only.
func apiDevice() *Device {
	d := validDevice()
	d.Slots = []Slot{
		{Number: 2, Module: Module{Name: "P", IdentNumber: 0x50, Submodules: []Submodule{{Name: "p", IdentNumber: 0x51, API: 0x3A00, Subslot: 1, OutputLen: 2}}}},
		{Number: 1, Module: Module{Name: "IOL", IdentNumber: 0x40, Submodules: []Submodule{
			{Name: "data", IdentNumber: 0x42, API: 0x4E01, Subslot: 2, InputLen: 4},
			{Name: "port", IdentNumber: 0x41, Subslot: 1},
		}}},
	}
	return d
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.Join(strings.Fields(s), ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The ExpectedSubmoduleBlockReq of a slot with submodules of two APIs, as
// IEC 61158-6-10 lays it out: NumberOfAPIs, then per API the API, the slot,
// the module ident and properties and that API's submodules, each with its
// data descriptions.
func TestExpectedAPIWire(t *testing.T) {
	d := apiDevice()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	exp := d.Expected()
	if len(exp) != 3 {
		t.Fatalf("%d blocks", len(exp))
	}
	got := exp[1].Append(nil)
	want := unhex(t, `
		0104 003c 0100
		0002
		00000000 0001 00000040 0000 0001
		  0001 00000041 0000  0001 0000 01 01
		00004e01 0001 00000040 0000 0001
		  0002 00000042 0001  0001 0004 01 01`)
	if !bytes.Equal(got, want) {
		t.Fatalf("slot 1\n got %x\nwant %x", got, want)
	}
	if m := exp[2].Modules; len(m) != 1 || m[0].API != 0x3A00 || m[0].Slot != 2 {
		t.Fatalf("slot 2 %+v", m)
	}
	if m := exp[0].Modules; len(m) != 1 || m[0].API != API {
		t.Fatalf("DAP %+v", m)
	}
}

func TestRealIdentificationAPIs(t *testing.T) {
	id := apiDevice().RealIdentification()
	if len(id.APIs) != 3 || id.APIs[0].API != 0 || id.APIs[1].API != 0x3A00 || id.APIs[2].API != 0x4E01 {
		t.Fatalf("APIs %+v", id.APIs)
	}
	// API 0: the DAP and slot 1's subslot 1.
	if s := id.APIs[0].Slots; len(s) != 2 || s[0].Slot != 0 || s[1].Slot != 1 || len(s[1].Submodules) != 1 || s[1].Submodules[0].Subslot != 1 {
		t.Fatalf("API 0 %+v", s)
	}
	if s := id.APIs[1].Slots; len(s) != 1 || s[0].Slot != 2 || s[0].IdentNumber != 0x50 {
		t.Fatalf("API 0x3A00 %+v", s)
	}
	if s := id.APIs[2].Slots; len(s) != 1 || s[0].Slot != 1 || s[0].IdentNumber != 0x40 || len(s[0].Submodules) != 1 || s[0].Submodules[0].IdentNumber != 0x42 {
		t.Fatalf("API 0x4E01 %+v", s)
	}
	// It encodes and decodes with the same grouping.
	blk, _, err := pnio.ReadBlock(id.Append(nil))
	if err != nil {
		t.Fatal(err)
	}
	dec, err := pnio.DecodeIdentificationData(blk)
	if err != nil || len(dec.APIs) != 3 || dec.APIs[2].API != 0x4E01 {
		t.Fatalf("%+v %v", dec, err)
	}
}

func TestAPIs(t *testing.T) {
	if a := validDevice().APIs(); len(a) != 1 || a[0] != 0 {
		t.Fatalf("API 0 model %v", a)
	}
	if a := apiDevice().APIs(); len(a) != 3 || a[0] != 0 || a[1] != 0x3A00 || a[2] != 0x4E01 {
		t.Fatalf("%v", a)
	}
}

// API 0 is left out of the JSON; another API is kept.
func TestSubmoduleAPIJSON(t *testing.T) {
	b, err := json.Marshal(Submodule{Name: "a", IdentNumber: 1, Subslot: 1})
	if err != nil || strings.Contains(string(b), "api") {
		t.Fatalf("%s %v", b, err)
	}
	b, err = json.Marshal(Submodule{Name: "a", IdentNumber: 1, Subslot: 1, API: 0x4E01})
	if err != nil || !strings.Contains(string(b), `"api":19969`) {
		t.Fatalf("%s %v", b, err)
	}
	var s Submodule
	if err := json.Unmarshal(b, &s); err != nil || s.API != 0x4E01 {
		t.Fatalf("%+v %v", s, err)
	}
}

// A module without submodules keeps its entry in API 0: the bytes are those
// go-profinet v0.19.0 (before APIs) encoded for the same model.
func TestEmptyModuleAPI0(t *testing.T) {
	d := validDevice()
	d.Slots = []Slot{
		{Number: 1, Module: Module{Name: "IO", IdentNumber: 0x40, Submodules: []Submodule{{Name: "io", IdentNumber: 0x41, Subslot: 1, InputLen: 2}}}},
		{Number: 3, Module: Module{Name: "Empty", IdentNumber: 0x60}},
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"0104003c010000010000000000000000010000000003000100000001000000010000010180000000800000000001000001018001000080010000000100000101",
		"010400200100000100000000000100000040000000010001000000410001000100020101",
		"01040012010000010000000000030000006000000000",
	}
	exp := d.Expected()
	if len(exp) != len(want) {
		t.Fatalf("%d blocks, want %d", len(exp), len(want))
	}
	for i, b := range exp {
		if got := b.Append(nil); !bytes.Equal(got, unhex(t, want[i])) {
			t.Errorf("block %d\n got %x\nwant %s", i, got, want[i])
		}
	}
	id := d.RealIdentification()
	if got, w := id.Append(nil), unhex(t, "0013003a01010001000000000003000000000100000300010000000180000000800080010000800100010000004000010001000000410003000000600000"); !bytes.Equal(got, w) {
		t.Errorf("identification\n got %x\nwant %x", got, w)
	}
}
