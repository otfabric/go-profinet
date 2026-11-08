// SPDX-License-Identifier: MIT

package pnio_test

// DCE-RPC / PNIO-CM PDUs recorded from a real CPU 1515-2 PN, from p-net and
// from older Siemens and GE devices (testdata/captures): the RPC header, the
// NDR header and the PNIO blocks decode to what Wireshark's pn_io dissector
// shows and re-encode to the recorded bytes, in both data representations.

import (
	"bytes"
	"testing"

	"github.com/otfabric/go-profinet/internal/capture"
	"github.com/otfabric/go-profinet/pnio"
	"github.com/otfabric/go-profinet/rpc"
)

var rpcFixtures = []string{
	"pnio-rs/s7-1500_p-net_connect-req.hex", "pnio-rs/s7-1500_p-net_connect-res.hex",
	"pnio-rs/s7-1500_p-net_write-multiple-req.hex", "pnio-rs/s7-1500_p-net_write-multiple-res.hex",
	"pnio-rs/s7-1500_p-net_prmend-req.hex", "pnio-rs/s7-1500_p-net_prmend-res.hex",
	"pnio-rs/s7-1500_p-net_appready-req.hex", "pnio-rs/s7-1500_p-net_appready-res.hex",
	"pnio-rs/s7-1500_p-net_read-im0-dap-req.hex", "pnio-rs/s7-1500_p-net_read-im0-dap-res.hex",
	"pnio-rs/s7-1500_p-net_read-im0-interface-req.hex", "pnio-rs/s7-1500_p-net_read-im0-interface-res.hex",
	"ics-pcaps/s7-300_unknown_connect-req.pcap#1",
	"ics-pcaps/unknown_ge-ip-device_read-implicit.pcap#1", "ics-pcaps/unknown_ge-ip-device_read-implicit.pcap#2",
}

type rpcPDU struct {
	h       rpc.Header
	header  []byte // the recorded 80 octets
	body    []byte
	argsMax uint32 // request
	status  pnio.Status
	blocks  []byte
}

func decodePDU(t *testing.T, file string) rpcPDU {
	t.Helper()
	u, err := capture.DecodeUDP(capture.MustLoad(file))
	if err != nil {
		t.Fatal(err)
	}
	h, body, err := rpc.DecodeHeader(u.Payload)
	if err != nil {
		t.Fatal(err)
	}
	p := rpcPDU{h: h, header: u.Payload[:len(u.Payload)-len(body)], body: body}
	if h.Type == rpc.TypeRequest {
		p.argsMax, p.blocks, err = pnio.DecodeNDRRequest(body, h.Order())
	} else {
		p.status, p.blocks, err = pnio.DecodeNDRResponse(body, h.Order())
	}
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestCaptureRPCRoundTrip: header and NDR header re-encode byte for byte,
// little endian (the CPU's, the older Siemens controller's) and big endian
// (p-net's).
func TestCaptureRPCRoundTrip(t *testing.T) {
	for _, f := range rpcFixtures {
		t.Run(f, func(t *testing.T) {
			p := decodePDU(t, f)
			if got := p.h.AppendTo(nil); !bytes.Equal(got, p.header) {
				t.Errorf("RPC header\n got % x\nwant % x", got, p.header)
			}
			var ndr []byte
			if p.h.Type == rpc.TypeRequest {
				ndr = pnio.AppendNDRRequest(nil, p.h.Order(), p.argsMax, p.blocks)
			} else {
				ndr = pnio.AppendNDRResponse(nil, p.h.Order(), p.status, maxCount(p), p.blocks)
			}
			if !bytes.Equal(ndr, p.body) {
				t.Errorf("NDR body\n got % x\nwant % x", ndr, p.body)
			}
		})
	}
}

// maxCount is the NDR response's max_count, which the responder echoes
// from the request's args_max.
func maxCount(p rpcPDU) uint32 { return p.h.Order().Uint32(p.body[8:]) }

func TestCaptureRPCHeaders(t *testing.T) {
	cases := []struct {
		file   string
		typ    rpc.PacketType
		op     pnio.OpNum
		le     bool
		flags1 uint8
		seq    uint32
	}{
		{"pnio-rs/s7-1500_p-net_connect-req.hex", rpc.TypeRequest, pnio.OpConnect, true, 0x20, 0},
		{"pnio-rs/s7-1500_p-net_connect-res.hex", rpc.TypeResponse, pnio.OpConnect, false, 0x28, 0},
		{"pnio-rs/s7-1500_p-net_write-multiple-req.hex", rpc.TypeRequest, pnio.OpWrite, true, 0x20, 1},
		{"pnio-rs/s7-1500_p-net_prmend-req.hex", rpc.TypeRequest, pnio.OpControl, true, 0x20, 2},
		{"pnio-rs/s7-1500_p-net_appready-req.hex", rpc.TypeRequest, pnio.OpControl, false, 0x20, 0},
		// The CPU answers with LastFrag | NoFack (0x0A), not idempotent.
		{"pnio-rs/s7-1500_p-net_appready-res.hex", rpc.TypeResponse, pnio.OpControl, true, 0x0A, 0},
		{"pnio-rs/s7-1500_p-net_read-im0-dap-req.hex", rpc.TypeRequest, pnio.OpRead, true, 0x20, 4},
		{"ics-pcaps/s7-300_unknown_connect-req.pcap#1", rpc.TypeRequest, pnio.OpConnect, true, 0x20, 0},
		{"ics-pcaps/unknown_ge-ip-device_read-implicit.pcap#1", rpc.TypeRequest, pnio.OpReadImplicit, true, 0x08, 0},
	}
	for _, c := range cases {
		p := decodePDU(t, c.file)
		le := p.h.DRep[0]&0x10 != 0
		if p.h.Type != c.typ || pnio.OpNum(p.h.OpNum) != c.op || le != c.le || p.h.Flags1 != c.flags1 || p.h.SeqNum != c.seq {
			t.Errorf("%s: type/op/le/flags1/seq %v/%d/%v/%#02x/%d, tshark %v/%d/%v/%#02x/%d", c.file, p.h.Type, p.h.OpNum, le, p.h.Flags1, p.h.SeqNum, c.typ, c.op, c.le, c.flags1, c.seq)
		}
		if p.h.Type == rpc.TypeRequest && p.h.Iface != pnio.DeviceInterface && p.h.Iface != pnio.ControllerInterface {
			t.Errorf("%s: interface %v", c.file, p.h.Iface)
		}
	}
}

// TestCaptureConnect: the Connect requests of the CPU 1515-2 PN and of an
// older Siemens controller, and p-net's Connect response, decode and
// re-encode block for block.
func TestCaptureConnect(t *testing.T) {
	for _, f := range []string{"pnio-rs/s7-1500_p-net_connect-req.hex", "ics-pcaps/s7-300_unknown_connect-req.pcap#1"} {
		p := decodePDU(t, f)
		req, err := pnio.DecodeConnectRequest(p.blocks)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		// Both Siemens controllers send the AlarmCRBlockReq last, after
		// the ExpectedSubmoduleBlockReqs; this package encodes it before
		// them, in the order of the IODConnectReq syntax (IEC 61158-6-10).
		// A legitimate difference: block order carries no meaning and the
		// decoder takes either. Apart from it the encoding is identical.
		if got, want := req.Append(nil), alarmCRFirst(t, p.blocks); !bytes.Equal(got, want) {
			t.Errorf("%s: re-encoded\n got % x\nwant % x", f, got, want)
		}
		if last := lastBlock(t, p.blocks); last != pnio.BlockAlarmCRReq {
			t.Errorf("%s: last block %v; the recording has the AlarmCRBlockReq last", f, last)
		}
	}
	p := decodePDU(t, "pnio-rs/s7-1500_p-net_connect-req.hex")
	req, _ := pnio.DecodeConnectRequest(p.blocks)
	// tshark: IOCARSingle, session key 2, CMInitiatorObjectUUID of a
	// Siemens (0x002A) device 0x010E instance 0x1064, StartupMode
	// advanced (bit 30), ParameterizationServer CM initiator, Active.
	if req.AR.Type != pnio.ARTypeIOCARSingle || req.AR.SessionKey != 2 || req.AR.InitiatorObject != pnio.ObjectUUID(0x1064, 0x010E, 0x002A) ||
		req.AR.Properties != 0x40000011 || req.AR.StationName != "plcxbbench.profinetxainterfacexb25fbd" || req.AR.ActivityTimeoutFactor != 200 {
		t.Errorf("ARBlockReq %+v", req.AR)
	}
	if len(req.IOCRs) != 2 || req.IOCRs[0].FrameID != 0x8000 || req.IOCRs[1].FrameID != 0x8001 || req.IOCRs[0].SendClockFactor != 32 ||
		req.IOCRs[0].ReductionRatio != 32 || req.IOCRs[0].DataLength != 40 || req.IOCRs[0].RTClass() != pnio.RTClass2 || req.IOCRs[0].TagHeader != 0xC000 {
		t.Errorf("IOCRBlockReq %+v", req.IOCRs)
	}
	if a := req.AlarmCR; a == nil || a.RTATimeoutFactor != 1 || a.RTARetries != 3 || a.MaxAlarmDataLength != 256 || a.TagHeaderHigh != 0xC000 || a.TagHeaderLow != 0xA000 {
		t.Errorf("AlarmCRBlockReq %+v", req.AlarmCR)
	}
	if len(req.Expected) != 5 {
		t.Errorf("%d ExpectedSubmoduleBlockReq, tshark 5", len(req.Expected))
	}

	r := decodePDU(t, "pnio-rs/s7-1500_p-net_connect-res.hex")
	res, err := pnio.DecodeConnectResponse(r.blocks)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Append(nil); !bytes.Equal(got, r.blocks) {
		t.Errorf("Connect response re-encoded\n got % x\nwant % x", got, r.blocks)
	}
	if res.AR.ARUUID != req.AR.ARUUID || res.AR.SessionKey != 2 || res.ARServer == nil || res.ARServer.StationName != "rt-labs-dev" || res.AlarmCR.MaxAlarmDataLength != 200 {
		t.Errorf("Connect response %+v", res)
	}

	// The older Siemens controller: two APIs (0 and the profile API
	// 0x3A00) and an output FrameID for the device to assign.
	s := decodePDU(t, "ics-pcaps/s7-300_unknown_connect-req.pcap#1")
	sreq, _ := pnio.DecodeConnectRequest(s.blocks)
	if sreq.AR.Properties != 0x00000011 || len(sreq.IOCRs[0].APIs) != 2 || sreq.IOCRs[0].APIs[1].API != 0x3A00 || sreq.IOCRs[1].FrameID != pnio.FrameIDAssign || sreq.IOCRs[0].RTClass() != pnio.RTClass2 {
		t.Errorf("Siemens Connect %+v %+v", sreq.AR, sreq.IOCRs)
	}
}

// TestCaptureRecords: the CPU's MultipleWrite, its PrmEnd and p-net's
// ApplicationReady and the CPU's answer, and the I&M0 reads.
func TestCaptureRecords(t *testing.T) {
	w := decodePDU(t, "pnio-rs/s7-1500_p-net_write-multiple-req.hex")
	blk, rest, err := pnio.ReadBlock(w.blocks)
	if err != nil {
		t.Fatal(err)
	}
	outer, err := pnio.DecodeWriteReq(blk)
	if err != nil {
		t.Fatal(err)
	}
	if outer.Index != pnio.IndexWriteMultiple || int(outer.Length) != len(rest) {
		t.Fatalf("MultipleWrite header %+v with %d octets after it", outer, len(rest))
	}
	items, err := pnio.SplitWriteMultiple(rest)
	if err != nil {
		t.Fatal(err)
	}
	type rec struct {
		slot, subslot uint16
		index         pnio.Index
		n             int
	}
	want := []rec{{0, 0x8000, pnio.IndexPDInterfaceAdjust, 12}, {3, 1, 0x7B, 4}, {3, 1, 0x7C, 4}, {4, 1, 0x7D, 4}}
	if len(items) != len(want) {
		t.Fatalf("%d records, tshark %d", len(items), len(want))
	}
	for i, it := range items {
		if it.Header.Slot != want[i].slot || it.Header.Subslot != want[i].subslot || it.Header.Index != want[i].index || len(it.Data) != want[i].n {
			t.Errorf("record %d: %+v (%d octets), tshark %+v", i, it.Header, len(it.Data), want[i])
		}
	}
	if got := pnio.AppendWriteMultiple(nil, items); !bytes.Equal(got, rest) {
		t.Errorf("MultipleWrite re-encoded\n got % x\nwant % x", got, rest)
	}
	pd, _, err := pnio.ReadBlock(items[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pnio.DecodePDInterfaceAdjust(pd); err != nil {
		t.Errorf("PDInterfaceAdjust: %v", err)
	}

	for _, c := range []struct {
		file string
		cmd  uint16
		typ  pnio.BlockType
	}{
		{"pnio-rs/s7-1500_p-net_prmend-req.hex", pnio.CmdPrmEnd, pnio.BlockPrmEndReq},
		{"pnio-rs/s7-1500_p-net_prmend-res.hex", pnio.CmdDone, pnio.BlockPrmEndRes},
		{"pnio-rs/s7-1500_p-net_appready-req.hex", pnio.CmdApplicationReady, pnio.BlockAppReadyReq},
		{"pnio-rs/s7-1500_p-net_appready-res.hex", pnio.CmdDone, pnio.BlockAppReadyRes},
	} {
		p := decodePDU(t, c.file)
		b, _, err := pnio.ReadBlock(p.blocks)
		if err != nil {
			t.Fatal(err)
		}
		cb, err := pnio.DecodeControlBlock(b)
		if err != nil {
			t.Fatal(err)
		}
		if cb.Type != c.typ || cb.Command != c.cmd || cb.SessionKey != 2 {
			t.Errorf("%s: %+v", c.file, cb)
		}
		if got := cb.Append(nil); !bytes.Equal(got, p.blocks) {
			t.Errorf("%s re-encoded\n got % x\nwant % x", c.file, got, p.blocks)
		}
	}

	for _, f := range []string{"pnio-rs/s7-1500_p-net_read-im0-dap-res.hex", "pnio-rs/s7-1500_p-net_read-im0-interface-res.hex"} {
		p := decodePDU(t, f)
		hb, rest, err := pnio.ReadBlock(p.blocks)
		if err != nil {
			t.Fatal(err)
		}
		rr, err := pnio.DecodeReadRes(hb)
		if err != nil || rr.Index != pnio.IndexIM0 || int(rr.Length) != len(rest) {
			t.Fatalf("%s: %+v %v", f, rr, err)
		}
		ib, _, err := pnio.ReadBlock(rest)
		if err != nil {
			t.Fatal(err)
		}
		im, err := pnio.DecodeIM0(ib)
		if err != nil {
			t.Fatal(err)
		}
		if im.VendorID != 0x0493 || im.OrderID != "12345 Abcdefghijk" || im.SerialNumber != "007" || im.HardwareRevision != 3 ||
			im.SoftwareRevision != (pnio.SoftwareRevision{Prefix: 'V', FunctionalEnhance: 0, BugFix: 2, InternalChange: 0}) ||
			im.ProfileID != 0x1234 || im.ProfileType != 0x5678 || im.VersionMajor != 1 || im.VersionMinor != 1 || im.Supported != 0x000E {
			t.Errorf("%s: I&M0 %+v", f, im)
		}
		if got := im.Append(nil); !bytes.Equal(got, rest) {
			t.Errorf("%s: I&M0 re-encoded\n got % x\nwant % x", f, got, rest)
		}
	}
}

// alarmCRFirst moves the AlarmCRBlockReq in front of the first
// ExpectedSubmoduleBlockReq.
func alarmCRFirst(t *testing.T, b []byte) []byte {
	t.Helper()
	blks, err := pnio.SplitBlocks(b)
	if err != nil {
		t.Fatal(err)
	}
	var alarmCR []byte
	var out [][]byte
	for _, blk := range blks {
		raw := blk.Append(nil)
		if blk.Header.Type == pnio.BlockAlarmCRReq {
			alarmCR = raw
			continue
		}
		out = append(out, raw)
	}
	var res []byte
	for _, raw := range out {
		if alarmCR != nil && pnio.BlockType(uint16(raw[0])<<8|uint16(raw[1])) == pnio.BlockExpectedSubmReq {
			res, alarmCR = append(res, alarmCR...), nil
		}
		res = append(res, raw...)
	}
	return append(res, alarmCR...)
}

func lastBlock(t *testing.T, b []byte) pnio.BlockType {
	t.Helper()
	blks, err := pnio.SplitBlocks(b)
	if err != nil || len(blks) == 0 {
		t.Fatal(err)
	}
	return blks[len(blks)-1].Header.Type
}
