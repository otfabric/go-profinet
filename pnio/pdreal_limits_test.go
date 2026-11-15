// SPDX-License-Identifier: MIT

package pnio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"testing"

	"github.com/otfabric/go-profinet"
)

// maxBlock is the longest block: its 16-bit length counts what follows
// the type and length fields.
const maxBlock = 4 + 0xFFFF

// mbhOverhead is what a MultipleBlockHeader adds around its blocks: the
// block header, padding, API, slot and subslot.
const mbhOverhead = BlockHeaderLen + 10

// wrapMBH puts body in a MultipleBlockHeader for a port submodule. It is
// built by hand so that a body of any length the block holds can be
// wrapped.
func wrapMBH(body []byte) []byte {
	b := make([]byte, 0, mbhOverhead+len(body))
	b = binary.BigEndian.AppendUint16(b, uint16(BlockMultipleBlockHeader))
	b = binary.BigEndian.AppendUint16(b, uint16(len(body)+mbhOverhead-4))
	b = append(b, 1, 0, 0, 0)
	b = binary.BigEndian.AppendUint32(b, 0)
	b = binary.BigEndian.AppendUint16(b, 0)
	b = binary.BigEndian.AppendUint16(b, 0x8001)
	return append(b, body...)
}

// nestMBH puts body in n MultipleBlockHeaders, one inside the other.
func nestMBH(body []byte, n int) []byte {
	for range n {
		body = wrapMBH(body)
	}
	return body
}

// TestPDRealDataNesting: both decoders of the PDRealData record take
// MultipleBlockHeaders nested up to maxPDRealDataNesting deep and refuse
// a deeper one.
func TestPDRealDataNesting(t *testing.T) {
	port := PDPortDataReal{Subslot: 0x8001, OwnPortID: "port-001", LinkState: LinkStateUp}
	iface := PDInterfaceDataReal{ChassisID: "io-1"}
	inner := append(port.Append(nil), iface.Append(nil)...)
	for _, depth := range []int{0, 1, 2, maxPDRealDataNesting - 1, maxPDRealDataNesting, maxPDRealDataNesting + 1, maxPDRealDataNesting + 2, 64} {
		t.Run(fmt.Sprintf("depth %d", depth), func(t *testing.T) {
			rec := nestMBH(inner, depth)
			ports, perr := DecodePDRealData(rec)
			rd, rerr := DecodePDRealDataRecord(rec)
			if depth > maxPDRealDataNesting {
				if !errors.Is(perr, profinet.ErrMalformed) || len(ports) != 0 {
					t.Errorf("DecodePDRealData: %d ports, %v; want none and ErrMalformed", len(ports), perr)
				}
				if !errors.Is(rerr, profinet.ErrMalformed) || len(rd.Ports) != 0 || rd.Interface != nil {
					t.Errorf("DecodePDRealDataRecord: %+v, %v; want nothing and ErrMalformed", rd, rerr)
				}
				return
			}
			if perr != nil || len(ports) != 1 || ports[0].OwnPortID != port.OwnPortID {
				t.Errorf("DecodePDRealData: %+v, %v", ports, perr)
			}
			if rerr != nil || len(rd.Ports) != 1 || rd.Ports[0].OwnPortID != port.OwnPortID || rd.Interface == nil || rd.Interface.ChassisID != iface.ChassisID {
				t.Errorf("DecodePDRealDataRecord: %+v, %v", rd, rerr)
			}
		})
	}

	// The bound is on the depth of one header, not on their number: a
	// record with many headers side by side, each as deep as allowed.
	var wide []byte
	for range 3 * maxPDRealDataNesting {
		wide = append(wide, nestMBH(port.Append(nil), maxPDRealDataNesting)...)
	}
	if ports, err := DecodePDRealData(wide); err != nil || len(ports) != 3*maxPDRealDataNesting {
		t.Errorf("headers side by side: %d ports, %v", len(ports), err)
	}
	if rd, err := DecodePDRealDataRecord(wide); err != nil || len(rd.Ports) != 3*maxPDRealDataNesting {
		t.Errorf("headers side by side: %d ports, %v", len(rd.Ports), err)
	}

	// What was decoded before a header that is too deep is returned with
	// the error.
	mixed := append(port.Append(nil), nestMBH(port.Append(nil), maxPDRealDataNesting+1)...)
	if ports, err := DecodePDRealData(mixed); !errors.Is(err, profinet.ErrMalformed) || len(ports) != 1 {
		t.Errorf("a port, then a header too deep: %d ports, %v", len(ports), err)
	}
	if rd, err := DecodePDRealDataRecord(mixed); !errors.Is(err, profinet.ErrMalformed) || len(rd.Ports) != 1 {
		t.Errorf("a port, then a header too deep: %d ports, %v", len(rd.Ports), err)
	}
}

// fill returns as many copies of blk as n octets hold.
func fill(blk []byte, n int) []byte {
	var b []byte
	for len(b)+len(blk) <= n {
		b = append(b, blk...)
	}
	return b
}

// TestPDRealDataAllocation: decoding a record of the longest block there
// is allocates in proportion to the record, whatever it holds.
func TestPDRealDataAllocation(t *testing.T) {
	// The octets a decoder may allocate for each octet of the record.
	const perOctet = 48
	port := (&PDPortDataReal{Subslot: 0x8001}).Append(nil)
	other := (&PDInterfaceAdjust{}).Append(nil)
	empty := []byte{0x7F, 0xFF, 0, 2, 1, 0} // the shortest block, of a type the decoders skip
	// As many headers one inside the other as the longest block holds.
	deepest := (maxBlock - len(port)) / mbhOverhead
	for _, c := range []struct {
		name string
		rec  []byte
		ok   bool
	}{
		{"ports", wrapMBH(fill(port, maxBlock-mbhOverhead)), true},
		{"ports at the deepest level", nestMBH(fill(port, maxBlock-maxPDRealDataNesting*mbhOverhead), maxPDRealDataNesting), true},
		{"shortest blocks", wrapMBH(fill(empty, maxBlock-mbhOverhead)), true},
		{"blocks of another kind", wrapMBH(fill(other, maxBlock-mbhOverhead)), true},
		{"a header at every level", fill(nestMBH(nil, maxPDRealDataNesting), maxBlock), true},
		{"headers as deep as the block holds", nestMBH(port, deepest), false},
		{"ports in headers that take half of the block", nestMBH(fill(port, maxBlock/2), maxBlock/2/mbhOverhead), false},
		{"headers one deeper than allowed", fill(nestMBH(port, maxPDRealDataNesting+1), maxBlock), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if len(c.rec) > maxBlock || len(c.rec) < maxBlock-2*len(port)-maxPDRealDataNesting*mbhOverhead {
				t.Fatalf("record of %d octets, want about %d", len(c.rec), maxBlock)
			}
			budget := uint64(perOctet * len(c.rec))
			for name, decode := range map[string]func([]byte) error{
				"DecodePDRealData":       func(b []byte) error { _, err := DecodePDRealData(b); return err },
				"DecodePDRealDataRecord": func(b []byte) error { _, err := DecodePDRealDataRecord(b); return err },
			} {
				runtime.GC()
				var m0, m1 runtime.MemStats
				runtime.ReadMemStats(&m0)
				err := decode(c.rec)
				runtime.ReadMemStats(&m1)
				if c.ok && err != nil {
					t.Errorf("%s: %v", name, err)
				}
				if !c.ok && !errors.Is(err, profinet.ErrMalformed) {
					t.Errorf("%s: err %v, want ErrMalformed", name, err)
				}
				if a := m1.TotalAlloc - m0.TotalAlloc; a > budget {
					t.Errorf("%s: %d octets allocated for a record of %d, more than %d", name, a, len(c.rec), budget)
				} else {
					t.Logf("%s: %d octets allocated for a record of %d", name, a, len(c.rec))
				}
			}
		})
	}
}
