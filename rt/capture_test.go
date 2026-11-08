// SPDX-License-Identifier: MIT

package rt_test

// Cyclic RT_CLASS_1 frames recorded between a CPU 1515-2 PN and p-net
// (testdata/captures, pnio-rs rt/), and the CPU's IOCR layout.

import (
	"testing"

	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/internal/capture"
	"github.com/otfabric/go-profinet/pnio"
	"github.com/otfabric/go-profinet/rpc"
	"github.com/otfabric/go-profinet/rt"
)

// TestCaptureCyclicFrames decodes the four recorded frames to what tshark's
// pn_rt dissector shows: FrameID, cycle counter, data status, the 40-octet
// C_SDU and the VLAN priority 6 tag.
func TestCaptureCyclicFrames(t *testing.T) {
	cases := []struct {
		file    string
		frameID uint16
		cycle   uint16
		status  rt.DataStatus
		primary bool
	}{
		{"pnio-rs/rt/rtc_cpu_8001.hex", 0x8001, 47104, 0x35, true},
		{"pnio-rs/rt/rtc_dev_8000.hex", 0x8000, 7168, 0x35, true},
		{"pnio-rs/rt/echo_cpu_8001.hex", 0x8001, 58368, 0x35, true},
		// p-net reported "Backup" in this frame: State 0, Redundancy 1.
		{"pnio-rs/rt/echo_dev_8000.hex", 0x8000, 60416, 0x36, false},
	}
	for _, c := range cases {
		e, err := ethernet.Decode(capture.MustHex(c.file))
		if err != nil {
			t.Fatal(err)
		}
		if !e.Tagged || e.VLAN.Priority != 6 || e.VLAN.ID != 0 || e.EtherType != ethernet.EtherTypeProfinet {
			t.Errorf("%s: VLAN %v %+v type %s", c.file, e.Tagged, e.VLAN, e.EtherType)
		}
		f, err := rt.Decode(e.Payload, 40)
		if err != nil {
			t.Fatal(err)
		}
		if f.FrameID != c.frameID || f.CycleCounter != c.cycle || f.DataStatus != c.status || f.TransferStatus != 0 || len(f.Data) != 40 {
			t.Errorf("%s: %#04x cycle %d status %#02x ts %d len %d", c.file, f.FrameID, f.CycleCounter, uint8(f.DataStatus), f.TransferStatus, len(f.Data))
		}
		if f.DataStatus.Primary() != c.primary || !f.DataStatus.Valid() || !f.DataStatus.Running() || f.DataStatus.StationProblem() {
			t.Errorf("%s: status %v", c.file, f.DataStatus)
		}
		// The trailer re-encodes as recorded.
		back := rt.AppendPayload(nil, &f)
		if string(back) != string(e.Payload[:len(back)]) {
			t.Errorf("%s: re-encoded % x", c.file, back)
		}
	}
	// 32 ms between the CPU's frames: send clock factor 32 x reduction
	// ratio 32 x 31.25 µs; the cycle counter counts 31.25 µs.
	if got := rt.CycleNanos(32, 32); got != 32_000_000 {
		t.Errorf("CycleNanos(32, 32) = %d", got)
	}
}

// TestCaptureCPULayout reads the CPU's IOCRs: LayoutFromIOCR finds every
// IODataObject and IOCS where the CPU put them, and the CPU's output
// frame carries GOOD (0x80) at each of them.
//
// Intentional difference: the CPU puts each submodule's IOCS right after
// its data objects, in slot order; ComputeLayout, which this package's
// controller uses, puts all IOCS after all data. Either is valid: the
// IO controller chooses the layout and the device follows the IOCR block.
func TestCaptureCPULayout(t *testing.T) {
	u, err := capture.DecodeUDP(capture.MustHex("pnio-rs/cm/connect_req.hex"))
	if err != nil {
		t.Fatal(err)
	}
	h, body, err := rpc.DecodeHeader(u.Payload)
	if err != nil {
		t.Fatal(err)
	}
	_, blocks, err := pnio.DecodeNDRRequest(body, h.Order())
	if err != nil {
		t.Fatal(err)
	}
	req, err := pnio.DecodeConnectRequest(blocks)
	if err != nil {
		t.Fatal(err)
	}
	var subs []rt.SubmoduleIO
	for _, b := range req.Expected {
		for _, m := range b.Modules {
			for _, s := range m.Submodules {
				subs = append(subs, rt.SubmoduleIO{API: m.API, Slot: m.Slot, Subslot: s.Subslot, Type: s.Type(), InputLen: s.InputLength(), OutputLen: s.OutputLength()})
			}
		}
	}
	e, err := ethernet.Decode(capture.MustHex("pnio-rs/rt/rtc_cpu_8001.hex"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := rt.Decode(e.Payload, 40)
	if err != nil {
		t.Fatal(err)
	}
	for _, cr := range req.IOCRs {
		l, err := rt.LayoutFromIOCR(&cr, subs)
		if err != nil {
			t.Fatalf("%v IOCR: %v", cr.Type, err)
		}
		if l.DataLength != cr.DataLength {
			t.Errorf("%v DataLength %d, CPU %d", cr.Type, l.DataLength, cr.DataLength)
		}
		for _, a := range cr.APIs {
			for _, o := range a.IODataObjects {
				got, ok := l.Find(a.API, o.Slot, o.Subslot)
				if !ok || got.Offset != o.FrameOffset {
					t.Errorf("%v %d/%#x: offset %d (%v), CPU %d", cr.Type, o.Slot, o.Subslot, got.Offset, ok, o.FrameOffset)
				}
				if cr.Type == pnio.IOCROutput && out.Data[got.IOPSOffset()] != 0x80 {
					t.Errorf("CPU's IOPS of %d/%#x at %d: %#02x", o.Slot, o.Subslot, got.IOPSOffset(), out.Data[got.IOPSOffset()])
				}
			}
			for _, s := range a.IOCS {
				got, ok := l.FindIOCS(a.API, s.Slot, s.Subslot)
				if !ok || got.Offset != s.FrameOffset {
					t.Errorf("%v IOCS %d/%#x: offset %d (%v), CPU %d", cr.Type, s.Slot, s.Subslot, got.Offset, ok, s.FrameOffset)
				}
				if cr.Type == pnio.IOCROutput && out.Data[s.FrameOffset] != 0x80 {
					t.Errorf("CPU's IOCS of %d/%#x: %#02x", s.Slot, s.Subslot, out.Data[s.FrameOffset])
				}
			}
		}
		own, err := rt.ComputeLayout(cr.Type, subs)
		if err != nil {
			t.Fatal(err)
		}
		if own.DataLength != cr.DataLength {
			t.Errorf("ComputeLayout %v DataLength %d, CPU %d", cr.Type, own.DataLength, cr.DataLength)
		}
	}
}
