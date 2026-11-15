// SPDX-License-Identifier: MIT

package capture

import (
	"encoding/binary"
	"errors"
	"testing"
)

// FuzzParsers gives any octets to the parsers of the package: a pcapng
// file, a frame with a UDP datagram, the text of a hex fixture and of a
// manifest. What they return is a part of what they were given.
func FuzzParsers(f *testing.F) {
	// One Section Header Block, one Interface Description Block for
	// Ethernet and one Enhanced Packet Block of four octets.
	le := binary.LittleEndian
	block := func(typ uint32, body []byte) []byte {
		b := le.AppendUint32(nil, typ)
		b = le.AppendUint32(b, uint32(12+len(body)))
		b = append(b, body...)
		return le.AppendUint32(b, uint32(12+len(body)))
	}
	ng := block(0x0A0D0D0A, append(le.AppendUint32(nil, 0x1A2B3C4D), make([]byte, 12)...))
	ng = append(ng, block(1, []byte{1, 0, 0, 0, 0, 0, 0, 0})...)
	epb := make([]byte, 20, 24)
	le.PutUint32(epb[12:], 4)
	le.PutUint32(epb[16:], 4)
	ng = append(ng, block(6, append(epb, 1, 2, 3, 4))...)
	f.Add(ng)
	f.Add(ng[:len(ng)-5])
	f.Add([]byte("0102 03 # comment\n04"))
	f.Add([]byte(`{"captures":[]}`))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		if pkts, err := readPcapng(b); err == nil {
			total := 0
			for _, p := range pkts {
				total += len(p)
			}
			if total > len(b) || len(pkts) > len(b)/32 {
				t.Fatalf("%d packets of %d octets from a file of %d", len(pkts), total, len(b))
			}
		} else if !errors.Is(err, ErrFormat) {
			t.Fatalf("readPcapng: %v", err)
		}
		if u, err := DecodeUDP(b); err == nil && len(u.Payload) > len(b) {
			t.Fatalf("a datagram of %d octets from a frame of %d", len(u.Payload), len(b))
		}
		if h, err := ParseHex(string(b)); err == nil {
			if len(h.Frame) == 0 || 2*len(h.Frame) > len(b) {
				t.Fatalf("a frame of %d octets from a text of %d", len(h.Frame), len(b))
			}
		} else if !errors.Is(err, ErrFormat) {
			t.Fatalf("ParseHex: %v", err)
		}
		if _, err := ParseManifest(b); err != nil && !errors.Is(err, ErrFormat) {
			t.Fatalf("ParseManifest: %v", err)
		}
	})
}
