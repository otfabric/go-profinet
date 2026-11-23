// SPDX-License-Identifier: MIT

package pnio

import (
	"fmt"

	"github.com/otfabric/go-profinet"
)

// APIData is the APIData block (BlockAPIData, 0x001A), the record of index
// IndexAPIData (0xF821): the APIs of a device. Its layout, block version
// 1.0 with NumberOfAPIs (Unsigned16) and one API (Unsigned32) each and no
// padding, is the one Wireshark's PNIO dissector reads
// (dissect_APIData_block), and p-net answers the read of 0xF821 with it,
// both outside and inside an AR (interop TestPnetController).
type APIData struct {
	APIs []uint32
}

// Append appends the APIData block.
func (d *APIData) Append(b []byte) []byte {
	b, s := beginBlock(b, BlockAPIData, 1, 0)
	b = appendLen16(b, "APIs", len(d.APIs))
	for _, a := range d.APIs {
		b = appendU32(b, a)
	}
	return endBlock(b, s)
}

// DecodeAPIData decodes an APIData block of version 1.0.
func DecodeAPIData(blk RawBlock) (APIData, error) {
	var d APIData
	if blk.Header.Type != BlockAPIData {
		return d, fmt.Errorf("pnio: %w: %s instead of APIData", profinet.ErrMalformed, blk.Header.Type)
	}
	if blk.Header.VersionHigh != 1 || blk.Header.VersionLow != 0 {
		return d, fmt.Errorf("pnio: %w: APIData version %d.%d", profinet.ErrUnsupported, blk.Header.VersionHigh, blk.Header.VersionLow)
	}
	r := newReader(blk.Body, "APIData")
	n := r.count(4)
	for i := 0; i < n && r.err == nil; i++ {
		d.APIs = append(d.APIs, r.u32())
	}
	return d, r.end()
}
