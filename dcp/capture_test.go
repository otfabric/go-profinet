// SPDX-License-Identifier: MIT

package dcp_test

// DCP frames recorded from real devices and controllers (testdata/captures):
// every frame decodes to what Wireshark's pn_dcp dissector shows, and
// re-encodes to the recorded bytes.

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"testing"

	"github.com/otfabric/go-profinet/dcp"
	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/internal/capture"
)

// dcpFrame is one recorded DCP frame and what tshark 4.6 decodes from it.
type dcpFrame struct {
	file    string
	frame   []byte // when not from a hex fixture
	tagged  bool
	service dcp.ServiceID
	typ     dcp.ServiceType
	xid     uint32
	delay   uint16
	dataLen int
	// For Identify responses: the identity tshark shows.
	id *dcp.Identity
	// For Set requests: the blocks; for Set responses the Response blocks'
	// errors.
	set    []dcp.BlockID
	errors []dcp.BlockError
}

func ip(s string) netip.Addr { return netip.MustParseAddr(s) }

func dcpFrames(t *testing.T) []dcpFrame {
	t.Helper()
	ws, err := capture.ReadPcap("wireshark/unknown_scalance-x208_dcp-set-ip.pcap")
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 6 {
		t.Fatalf("unknown_scalance-x208_dcp-set-ip.pcap: %d frames, want 6", len(ws))
	}
	return []dcpFrame{
		// CPU 1515-2 PN (bench 1): Identify with a NameOfStation filter.
		{file: "pnio-rs/s7-1500_plcsim_dcp-identify-req.hex", service: dcp.ServiceIdentify, typ: dcp.TypeRequest, xid: 0x03000152, delay: 1, dataLen: 12},
		// PLCSIM Advanced (software) answering it.
		{file: "pnio-rs/s7-1500_plcsim_dcp-identify-res.hex", service: dcp.ServiceIdentify, typ: dcp.TypeResponseSuccess, xid: 0x03000152, dataLen: 88,
			id: &dcp.Identity{NameOfStation: "i-device", TypeOfStation: "S7-1500 (PLCSIM)", VendorID: 0x002A, DeviceID: 0x010E, Role: 0, Instance: 0x1064, HasInstance: true,
				IP: dcp.IPSuite{Addr: ip("192.168.1.61"), Mask: ip("255.255.255.0"), Gateway: ip("192.168.1.61")}, IPInfo: 1}},
		// p-net (software) answering the CPU (bench 2).
		{file: "pnio-rs/s7-1500_p-net_dcp-identify-res.hex", service: dcp.ServiceIdentify, typ: dcp.TypeResponseSuccess, xid: 0x0300012C, dataLen: 118,
			id: &dcp.Identity{NameOfStation: "rt-labs-dev", TypeOfStation: "P-Net Sample Application", VendorID: 0x0493, DeviceID: 0x0002, Role: dcp.RoleIODevice,
				IP: dcp.IPSuite{Addr: ip("0.0.0.0"), Mask: ip("0.0.0.0"), Gateway: ip("0.0.0.0")}}},
		// The CPU assigning p-net its address: VLAN tagged (priority 0, VID 0).
		{file: "pnio-rs/s7-1500_p-net_dcp-set-ip-req.hex", tagged: true, service: dcp.ServiceSet, typ: dcp.TypeRequest, xid: 0x0300012D, dataLen: 18, set: []dcp.BlockID{dcp.IPParameter}},
		{file: "pnio-rs/s7-1500_p-net_dcp-set-ip-res.hex", service: dcp.ServiceSet, typ: dcp.TypeResponseSuccess, xid: 0x0300012D, dataLen: 8, errors: []dcp.BlockError{dcp.BlockErrorNone}},
		// A SCALANCE switch checking that its name is unique.
		{file: "ics-pcaps/scalance_unknown_dcp-identify.pcap#1", service: dcp.ServiceIdentify, typ: dcp.TypeRequest, xid: 0x0300BF9A, delay: 1, dataLen: 12},
		// An S7-300 (role IO-Controller) with a hostile station name in a
		// NUL-padded 108-octet field, and a tool renaming it.
		{file: "ics-pcaps/unknown_s7-300ec_dcp-set-name.pcap#1", tagged: true, service: dcp.ServiceIdentify, typ: dcp.TypeRequest, xid: 0x01000001, delay: 1, dataLen: 4},
		{file: "ics-pcaps/unknown_s7-300ec_dcp-set-name.pcap#2", tagged: true, service: dcp.ServiceIdentify, typ: dcp.TypeResponseSuccess, xid: 0x01000001, dataLen: 184,
			id: &dcp.Identity{NameOfStation: "%n%n%n ", TypeOfStation: "S7-300EC", VendorID: 0x002A, DeviceID: 0x0101, Role: dcp.RoleIOController,
				IP: dcp.IPSuite{Addr: ip("192.168.0.11"), Mask: ip("255.255.255.0"), Gateway: ip("192.168.0.11")}, IPInfo: 1}},
		{file: "ics-pcaps/unknown_s7-300ec_dcp-set-name.pcap#3", tagged: true, service: dcp.ServiceSet, typ: dcp.TypeRequest, xid: 0x01000001, dataLen: 20, set: []dcp.BlockID{dcp.DeviceName, dcp.CtrlStop}},
		{file: "ics-pcaps/unknown_s7-300ec_dcp-set-name.pcap#4", tagged: true, service: dcp.ServiceSet, typ: dcp.TypeResponseSuccess, xid: 0x01000001, dataLen: 16, errors: []dcp.BlockError{dcp.BlockErrorNone, dcp.BlockErrorNone}},
		// Wireshark wiki sample: a tool and a SCALANCE X208.
		{file: "unknown_scalance-x208_dcp-set-ip.pcap#1", frame: ws[0], service: dcp.ServiceIdentify, typ: dcp.TypeRequest, xid: 0x01000001, delay: 1, dataLen: 4},
		{file: "unknown_scalance-x208_dcp-set-ip.pcap#2", frame: ws[1], service: dcp.ServiceIdentify, typ: dcp.TypeResponseSuccess, xid: 0x01000001, dataLen: 94,
			id: &dcp.Identity{NameOfStation: "X208-BORD", TypeOfStation: "INC", VendorID: 0x002A, DeviceID: 0x0A01, Role: dcp.RoleIODevice,
				IP: dcp.IPSuite{Addr: ip("192.168.0.6"), Mask: ip("255.255.255.0"), Gateway: ip("192.168.0.1")}, IPInfo: 1}},
		{file: "unknown_scalance-x208_dcp-set-ip.pcap#3", frame: ws[2], service: dcp.ServiceSet, typ: dcp.TypeRequest, xid: 0x01000001, dataLen: 18, set: []dcp.BlockID{dcp.IPParameter}},
		{file: "unknown_scalance-x208_dcp-set-ip.pcap#4", frame: ws[3], service: dcp.ServiceSet, typ: dcp.TypeResponseSuccess, xid: 0x01000001, dataLen: 8, errors: []dcp.BlockError{dcp.BlockErrorNone}},
	}
}

func TestCaptureDCPFrames(t *testing.T) {
	for _, c := range dcpFrames(t) {
		t.Run(c.file, func(t *testing.T) {
			raw := c.frame
			if raw == nil {
				raw = capture.MustLoad(c.file)
			}
			e, err := ethernet.Decode(raw)
			if err != nil {
				t.Fatal(err)
			}
			if e.EtherType != ethernet.EtherTypeProfinet || e.Tagged != c.tagged || (e.Tagged && e.VLAN.TCI() != 0) {
				t.Fatalf("Ethernet type %s tagged %v TCI %#04x", e.EtherType, e.Tagged, e.VLAN.TCI())
			}
			f, err := dcp.Decode(e.Payload)
			if err != nil {
				t.Fatal(err)
			}
			if f.Service != c.service || f.Type != c.typ || f.Xid != c.xid || f.ResponseDelay != c.delay {
				t.Errorf("service/type/xid/delay %v/%v/%#08x/%d, tshark %v/%v/%#08x/%d", f.Service, f.Type, f.Xid, f.ResponseDelay, c.service, c.typ, c.xid, c.delay)
			}
			n := int(binary.BigEndian.Uint16(e.Payload[10:]))
			if n != c.dataLen {
				t.Errorf("DCPDataLength %d, tshark %d", n, c.dataLen)
			}
			// Re-encoding gives the recorded PDU byte for byte (the
			// Ethernet padding after DCPDataLength excluded).
			enc, err := f.AppendTo(nil)
			if err != nil {
				t.Fatal(err)
			}
			if want := e.Payload[:12+n]; !bytes.Equal(enc, want) {
				t.Errorf("re-encoded\n got % x\nwant % x", enc, want)
			}
			if c.id != nil {
				id, err := dcp.ParseIdentity(e.Src, f.Blocks)
				if err != nil {
					t.Fatal(err)
				}
				w := *c.id
				if id.NameOfStation != w.NameOfStation || id.TypeOfStation != w.TypeOfStation || id.VendorID != w.VendorID || id.DeviceID != w.DeviceID ||
					id.Role != w.Role || id.Instance != w.Instance || id.HasInstance != w.HasInstance || id.IP != w.IP || id.IPInfo != w.IPInfo {
					t.Errorf("identity\n got %+v\nwant %+v", id, w)
				}
			}
			if c.set != nil {
				var ids []dcp.BlockID
				for _, b := range f.Blocks {
					ids = append(ids, b.ID)
				}
				if !equalIDs(ids, c.set) {
					t.Errorf("Set blocks %v, tshark %v", ids, c.set)
				}
			}
			if c.errors != nil {
				var errs []dcp.BlockError
				for _, b := range f.Blocks {
					r, err := dcp.DecodeControlResponse(b.Data)
					if err != nil || b.ID != dcp.CtrlResponse {
						t.Fatalf("block %s: %v", b.ID, err)
					}
					errs = append(errs, r.Error)
				}
				if len(errs) != len(c.errors) {
					t.Errorf("Set response errors %v, tshark %v", errs, c.errors)
				}
			}
		})
	}
}

func equalIDs(a, b []dcp.BlockID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestCaptureDCPEncodeLikeRecorded builds the requests the recorded
// controllers sent with this package's encoder: the CPU's Identify by name
// and Set IP, and the tool's Identify All.
func TestCaptureDCPEncodeLikeRecorded(t *testing.T) {
	cases := []struct {
		file string
		f    dcp.Frame
	}{
		{"pnio-rs/s7-1500_plcsim_dcp-identify-req.hex", dcp.Frame{FrameID: dcp.FrameIDIdentifyRequest, Service: dcp.ServiceIdentify, Type: dcp.TypeRequest, Xid: 0x03000152, ResponseDelay: 1,
			Blocks: []dcp.Block(dcp.FilterName("i-device"))}},
		{"ics-pcaps/unknown_s7-300ec_dcp-set-name.pcap#1", dcp.Frame{FrameID: dcp.FrameIDIdentifyRequest, Service: dcp.ServiceIdentify, Type: dcp.TypeRequest, Xid: 0x01000001, ResponseDelay: 1,
			Blocks: []dcp.Block(dcp.FilterAll())}},
		{"pnio-rs/s7-1500_p-net_dcp-set-ip-req.hex", dcp.Frame{FrameID: dcp.FrameIDGetSet, Service: dcp.ServiceSet, Type: dcp.TypeRequest, Xid: 0x0300012D,
			Blocks: []dcp.Block{{ID: dcp.IPParameter, HasInfo: true, Info: 0, Data: dcp.AppendIPSuite(nil, dcp.IPSuite{Addr: ip("172.16.2.10"), Mask: ip("255.255.255.0"), Gateway: ip("172.16.2.10")})}}}},
	}
	for _, c := range cases {
		e, err := ethernet.Decode(capture.MustLoad(c.file))
		if err != nil {
			t.Fatal(err)
		}
		n := int(binary.BigEndian.Uint16(e.Payload[10:]))
		got, err := c.f.AppendTo(nil)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, e.Payload[:12+n]) {
			t.Errorf("%s\n got % x\nwant % x", c.file, got, e.Payload[:12+n])
		}
	}
}

// A NameOfStation padded with NULs (the S7-300 above) is the name without
// them.
func TestParseIdentityTrimsNULPadding(t *testing.T) {
	id, err := dcp.ParseIdentity(ethernet.MAC{}, []dcp.Block{
		{ID: dcp.DeviceName, HasInfo: true, Data: []byte("plc-1\x00\x00\x00")},
		{ID: dcp.DeviceVendor, HasInfo: true, Data: []byte("S7-300EC\x00")},
	})
	if err != nil || id.NameOfStation != "plc-1" || id.TypeOfStation != "S7-300EC" {
		t.Fatalf("%q %q %v", id.NameOfStation, id.TypeOfStation, err)
	}
}
