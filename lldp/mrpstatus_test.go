// SPDX-License-Identifier: MIT

package lldp

import (
	"bytes"
	"testing"

	"github.com/otfabric/go-profinet/ethernet"
)

// The PROFINET MRP port status TLV: OUI 00-0E-CF, subtype 4, the 16-octet
// MRP_DomainUUID and the 2-octet MRRT_PortStatus (Wireshark packet-lldp.c,
// dissect_profinet_tlv). The golden TLV is that layout written out by
// hand.
func TestMRPPortStatusTLV(t *testing.T) {
	dom := [16]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	golden := append([]byte{0xFE, 0x16, 0x00, 0x0E, 0xCF, 0x04}, dom[:]...)
	golden = append(golden, 0x00, 0x02)

	f := Frame{ChassisID: []byte("c"), PortID: []byte("p"), TTL: 20, MRPPortStatus: &MRPPortStatus{Domain: dom, MRRT: MRRTUp}}
	b, err := f.AppendTo(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, golden) {
		t.Fatalf("% x lacks the TLV % x", b, golden)
	}
	g, err := Decode(b)
	if err != nil || g.MRPPortStatus == nil || *g.MRPPortStatus != *f.MRPPortStatus || len(g.Other) != 0 {
		t.Fatalf("%+v %v", g, err)
	}

	// Another length is not this TLV: kept undecoded, re-encoded as is.
	for _, n := range []int{17, 19} {
		v := append([]byte{0x00, 0x0E, 0xCF, 0x04}, make([]byte, n)...)
		f := Frame{ChassisID: []byte("c"), PortID: []byte("p"), Other: []TLV{{Type: TypeOrgSpecific, Value: v}}}
		b, _ := f.AppendTo(nil)
		g, err := Decode(b)
		if err != nil || g.MRPPortStatus != nil || len(g.Other) != 1 || !bytes.Equal(g.Other[0].Value, v) {
			t.Fatalf("%d octets: %+v %v", n, g, err)
		}
	}
}

// LocalInfo.MRP is sent; without it no MRP port status is.
func TestLocalInfoMRP(t *testing.T) {
	mac := ethernet.MAC{2, 0, 0, 0, 0, 1}
	st := &MRPPortStatus{Domain: [16]byte{1, 2}}
	f := LocalInfo{StationName: "dev", MRP: st}.Build(mac, 21)
	if f.MRPPortStatus == nil || *f.MRPPortStatus != *st || f.MRPPortStatus == st {
		t.Fatalf("%+v", f.MRPPortStatus)
	}
	if f := (LocalInfo{StationName: "dev"}).Build(mac, 21); f.MRPPortStatus != nil {
		t.Fatalf("%+v", f.MRPPortStatus)
	}
}
