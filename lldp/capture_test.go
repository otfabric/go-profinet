// SPDX-License-Identifier: MIT

package lldp_test

// LLDP frames of real Siemens devices and a PC (testdata/captures,
// ics-pcaps excerpts): an S7-1200 CPU 1214C and a KTP700 Basic HMI (both
// name their order number and firmware in the frame), a SCALANCE X 200
// switch and an OptiPlex PC running an HMI. Every frame decodes to what
// tshark's lldp dissector shows and re-encodes to the recorded bytes.

import (
	"bytes"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/internal/capture"
	"github.com/otfabric/go-profinet/lldp"
)

func TestCaptureLLDP(t *testing.T) {
	cases := []struct {
		file             string
		chassisSub       uint8
		chassis, port    string
		portDesc, sysDes string
		sysName          string
		mgmt             string
		ifIndex          uint32
		chassisMAC       string
		pmd, mau         uint16
	}{
		{"ics-pcaps/lldp_s7-1200.hex", 7, "S7-1200                   6ES7 214-1AG40-0XB0  S V-K2C13707         7 V  4  2  1", "port-001.plcxb1d0ed",
			"Siemens, SIMATIC S7, Ethernet Port, X1 P1", "Siemens, SIMATIC S7, CPU-1200, 6ES7 214-1AG40-0XB0, HW: 7, FW: V.4.2.1, S V-K2C13707", "", "192.168.0.1", 1, "e0:dc:a0:34:21:d4", 0x6c00, 0x0010},
		{"ics-pcaps/lldp_ktp700.hex", 7, "hmixb110d0", "port-001",
			"Siemens, SIMATIC S7, Ethernet Port, X1 P1", "Siemens, SIMATIC HMI, KTP700 Basic, 6AV2 123-2GB03-0AX0, HW:0, SW.V 13.0.1", "", "192.168.0.2", 2, "e0:dc:a0:29:c6:e7", 0x6c01, 0x0010},
		{"ics-pcaps/lldp_scalance-x200.hex", 4, "00:1b:1b:34:34:cb", "port-003",
			"", "Siemens AG SIMATIC S7 MULTIPORT SCALANCE X 200 FAM", "", "0.0.0.0", 3, "00:1b:1b:34:34:cb", 0x6c00, 0x000f},
		{"ics-pcaps/lldp_pc-hmi.hex", 7, "scada-pc-060839", "port-001",
			"", "Dell Inc. OptiPlex 990,01,G22XB2S + HMI", "SCADA-PC-060839", "10.10.10.70", 1, "", 0, 0},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			e, err := ethernet.Decode(capture.MustHex(c.file))
			if err != nil {
				t.Fatal(err)
			}
			if e.EtherType != ethernet.EtherTypeLLDP || e.Dst.String() != "01:80:c2:00:00:0e" {
				t.Fatalf("%s to %s", e.EtherType, e.Dst)
			}
			f, err := lldp.Decode(e.Payload)
			if err != nil {
				t.Fatal(err)
			}
			if f.ChassisSubtype != c.chassisSub || f.ChassisString() != c.chassis || f.PortSubtype != 7 || f.PortString() != c.port || f.TTL != 20 {
				t.Errorf("chassis %d %q port %d %q ttl %d", f.ChassisSubtype, f.ChassisString(), f.PortSubtype, f.PortString(), f.TTL)
			}
			if f.PortDescription != c.portDesc || f.SystemDescription != c.sysDes || f.SystemName != c.sysName || f.ManagementAddr != netip.MustParseAddr(c.mgmt) {
				t.Errorf("port desc %q sys desc %q name %q mgmt %v", f.PortDescription, f.SystemDescription, f.SystemName, f.ManagementAddr)
			}
			if c.chassisMAC != "" {
				if f.PortStatus == nil || f.PortStatus.RTClass2 != 0 || f.PortStatus.RTClass3 != 0 {
					t.Errorf("PNO port status %+v", f.PortStatus)
				}
				if f.ChassisMAC == nil || f.ChassisMAC.String() != c.chassisMAC {
					t.Errorf("PNO chassis MAC %v", f.ChassisMAC)
				}
				if f.MACPHY == nil || f.MACPHY.Autoneg != 3 || f.MACPHY.PMDCapability != c.pmd || f.MACPHY.MAUType != c.mau {
					t.Errorf("MAC/PHY %+v", f.MACPHY)
				}
			}
			// Every device names the PNO enterprise OID 1.3.6.1.4.1.24686.
			if f.ManagementIfIndex != c.ifIndex || !bytes.Equal(f.ManagementOID, pnoOID) {
				t.Errorf("management ifIndex %d OID % x", f.ManagementIfIndex, f.ManagementOID)
			}
			// Re-encoding gives the recorded TLVs. The order differs: this
			// package writes the TLVs it decodes before the others (no
			// meaning in LLDP, IEEE 802.1AB 8.2).
			got, err := f.AppendTo(nil)
			if err != nil {
				t.Fatal(err)
			}
			if g, w := tlvSet(t, got), tlvSet(t, e.Payload); g != w {
				t.Errorf("re-encoded TLVs\n got %s\nwant %s", g, w)
			}
		})
	}
}

// pnoOID is 1.3.6.1.4.1.24686, BER encoded.
var pnoOID = []byte{0x2b, 0x06, 0x01, 0x04, 0x01, 0x81, 0xc0, 0x6e}

// tlvSet returns the TLVs of an LLDPDU up to the End TLV, sorted, as text.
func tlvSet(t *testing.T, b []byte) string {
	t.Helper()
	var tlvs []string
	for len(b) >= 2 {
		n := int(b[0]&1)<<8 | int(b[1])
		if 2+n > len(b) {
			t.Fatalf("TLV of %d octets in %d", n, len(b))
		}
		tlvs = append(tlvs, fmt.Sprintf("% x", b[:2+n]))
		if b[0]>>1 == 0 {
			break
		}
		b = b[2+n:]
	}
	sort.Strings(tlvs)
	return strings.Join(tlvs, " | ")
}

// A management address TLV whose OID length exceeds the TLV keeps the
// address and drops the OID; an over-long OID is refused when encoding.
func TestManagementOIDBounds(t *testing.T) {
	b := []byte{0x02, 0x02, 0x07, 'c', 0x04, 0x02, 0x07, 'p', 0x06, 0x02, 0, 20,
		0x10, 0x0c, 5, 1, 10, 0, 0, 1, 2, 0, 0, 0, 7, 9, 0, 0}
	f, err := lldp.Decode(b)
	if err != nil || f.ManagementAddr != netip.MustParseAddr("10.0.0.1") || f.ManagementIfIndex != 7 || f.ManagementOID != nil {
		t.Fatalf("%+v %v", f, err)
	}
	f.ManagementOID = make([]byte, 129)
	if _, err := f.AppendTo(nil); err == nil {
		t.Fatal("129-octet OID encoded")
	}
}
