// SPDX-License-Identifier: MIT

package pnio

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/otfabric/go-profinet"
)

// goldenAPIDataPnet is p-net's answer to a read of 0xF821 (interop
// TestPnetController): one API, API 0.
const goldenAPIDataPnet = `001a 0008 0100 0001 00000000`

// goldenAPIData2 has two APIs, laid out as Wireshark's dissect_APIData_block
// reads them: BlockHeader (type 0x001A, length 12, version 1.0),
// NumberOfAPIs 2, API 0, API 0x4E01.
const goldenAPIData2 = `001a 000c 0100 0002 00000000 00004e01`

func TestAPIDataGolden(t *testing.T) {
	for _, c := range []struct {
		golden string
		apis   []uint32
	}{
		{goldenAPIDataPnet, []uint32{0}},
		{goldenAPIData2, []uint32{0, 0x4E01}},
	} {
		d := APIData{APIs: c.apis}
		if got := d.Append(nil); !bytes.Equal(got, hx(c.golden)) {
			t.Errorf("encode %v\n got %x\nwant %x", c.apis, got, hx(c.golden))
		}
		blk, rest, err := ReadBlock(hx(c.golden))
		if err != nil || len(rest) != 0 {
			t.Fatalf("%s: %v, %d trailing", c.golden, err, len(rest))
		}
		got, err := DecodeAPIData(blk)
		if err != nil || !reflect.DeepEqual(got.APIs, c.apis) {
			t.Errorf("decode %s: %+v %v", c.golden, got, err)
		}
	}
	// No API at all: a count of 0.
	if got := (&APIData{}).Append(nil); !bytes.Equal(got, hx(`001a 0004 0100 0000`)) {
		t.Errorf("empty: %x", got)
	}
}

func TestAPIDataMalformed(t *testing.T) {
	for _, c := range []struct {
		name string
		blk  string
		want error
	}{
		{"another block type", `0013 0008 0100 0001 00000000`, profinet.ErrMalformed},
		{"version 1.1", `001a 0008 0101 0001 00000000`, profinet.ErrUnsupported},
		{"version 2.0", `001a 0008 0200 0001 00000000`, profinet.ErrUnsupported},
		{"count beyond the body", `001a 0008 0100 0002 00000000`, profinet.ErrMalformed},
		{"no count", `001a 0002 0100`, profinet.ErrMalformed},
		{"truncated API", `001a 0006 0100 0001 0000`, profinet.ErrMalformed},
		{"trailing octets", `001a 000a 0100 0001 00000000 0000`, profinet.ErrMalformed},
	} {
		blk, _, err := ReadBlock(hx(c.blk))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if _, err := DecodeAPIData(blk); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	if s := BlockAPIData.String(); s != "APIData" {
		t.Errorf("String %q", s)
	}
}
