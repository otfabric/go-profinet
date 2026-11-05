// SPDX-License-Identifier: MIT

package device

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/l2"
	"github.com/otfabric/go-profinet/netstack"
	"github.com/otfabric/go-profinet/pnio"
	"github.com/otfabric/go-profinet/rpc"
	"github.com/otfabric/go-profinet/rt"
	"github.com/otfabric/go-profinet/vnet"
)

// hostileOps are the operations of the device interface a controller
// calls, plus one the device does not implement.
var hostileOps = []pnio.OpNum{pnio.OpConnect, pnio.OpRelease, pnio.OpRead, pnio.OpWrite, pnio.OpControl, pnio.OpReadImplicit, pnio.OpNum(9)}

// fuzzDevice starts a device with the test model on its own virtual
// network, for targets that cannot use the *testing.T harness.
func fuzzDevice(tb testing.TB) *Device {
	tb.Helper()
	nw := vnet.New(1)
	ep, err := nw.NewEndpoint(vnet.EndpointConfig{Name: devIP, MAC: ethernet.MAC{0x02, 0, 0, 0, 0, 0x10}})
	if err != nil {
		tb.Fatal(err)
	}
	mux := l2.NewMux(ep)
	st, err := netstack.New(mux, netstack.Config{Addr: netip.MustParseAddr(devIP), Mask: mask})
	if err != nil {
		tb.Fatal(err)
	}
	d, err := New(Config{
		Model: testModel(), Mux: mux, StationName: "dut", IP: suite(devIP), ListenPacket: st.ListenPacket,
		NoResponseDelay: true, LLDPInterval: time.Hour, ApplicationReadyTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		_ = d.Close()
		_ = st.Close()
		_ = mux.Close()
		_ = ep.Close()
		_ = nw.Close()
	})
	return d
}

// hostileRequest wraps an NDR body in the RPC request of op, little endian
// as the RPC client sends it.
func hostileRequest(d *Device, op pnio.OpNum, body []byte) *rpc.Request {
	return &rpc.Request{
		Header: rpc.Header{Type: rpc.TypeRequest, DRep: [3]byte{rpc.DRepLittleEndian}, Object: d.ObjectUUID(), Iface: pnio.DeviceInterface,
			IfaceVersion: pnio.InterfaceVersion, OpNum: uint16(op), BodyLen: uint16(len(body))},
		Body: body,
		Addr: &net.UDPAddr{IP: net.ParseIP("192.168.0.2").To4(), Port: 49152},
	}
}

// dropARs aborts every AR of d, so that one input does not leave state
// for the next.
func dropARs(d *Device) {
	d.mu.Lock()
	ars := make([]*ar, 0, len(d.ars))
	for _, a := range d.ars {
		ars = append(ars, a)
	}
	d.mu.Unlock()
	for _, a := range ars {
		d.abort(a, pnio.AbortUser, false)
	}
}

// validConnect is a valid IOCAR Connect request for the test model from
// the controller at mac, built without a peer (as peer.ioCAR).
func validConnect(mac ethernet.MAC) *pnio.ConnectRequest {
	m := testModel()
	subs := subIO(m)
	inL, _ := rt.ComputeLayout(pnio.IOCRInput, subs)
	outL, _ := rt.ComputeLayout(pnio.IOCROutput, subs)
	iocr := func(t pnio.IOCRType, frameID uint16, l rt.Layout, ref uint16) pnio.IOCRBlockReq {
		return pnio.IOCRBlockReq{
			Type: t, Reference: ref, LT: pnio.LT, Properties: uint32(pnio.RTClass1), DataLength: l.DataLength, FrameID: frameID,
			SendClockFactor: 32, ReductionRatio: 128, Phase: 1, FrameSendOffset: 0xFFFFFFFF,
			WatchdogFactor: 3, DataHoldFactor: 0x1E00, TagHeader: 6 << 13, APIs: l.IOCRAPIs(),
		}
	}
	return &pnio.ConnectRequest{
		AR: pnio.ARBlockReq{
			Type: pnio.ARTypeIOCARSingle, ARUUID: rpc.NewRandomUUID(), SessionKey: 7,
			InitiatorMAC: mac, InitiatorObject: pnio.ObjectUUID(1, 1, 0xF000),
			Properties: pnio.ARPropStateActive | pnio.ARPropPrmServerCMInit, ActivityTimeoutFactor: 200,
			UDPRTPort: pnio.LT, StationName: "plc",
		},
		IOCRs: []pnio.IOCRBlockReq{iocr(pnio.IOCRInput, 0xC000, inL, 1), iocr(pnio.IOCROutput, pnio.FrameIDAssign, outL, 2)},
		AlarmCR: &pnio.AlarmCRBlockReq{
			Type: 1, LT: pnio.LT, RTATimeoutFactor: 5, RTARetries: 3,
			LocalAlarmReference: 1, MaxAlarmDataLength: 200, TagHeaderHigh: 6 << 13, TagHeaderLow: 5 << 13,
		},
		Expected: m.Expected(),
	}
}

// validSAR is a device access Connect request (as peer.ioSAR).
func validSAR(mac ethernet.MAC) *pnio.ConnectRequest {
	return &pnio.ConnectRequest{AR: pnio.ARBlockReq{
		Type: pnio.ARTypeIOSAR, ARUUID: rpc.NewRandomUUID(), SessionKey: 1, InitiatorMAC: mac,
		InitiatorObject: pnio.ObjectUUID(1, 2, 3), Properties: pnio.ARPropStateActive | pnio.ARPropDeviceAccess,
		ActivityTimeoutFactor: 200, UDPRTPort: pnio.LT, StationName: "tool",
	}}
}

// FuzzServeRPC feeds the device's PNIO interface with arbitrary NDR bodies
// for every operation. Invariants: no panic; every answer that is not a
// reject is an NDR response in the request's byte order;
// the device never holds more ARs than MaxARs; no answer exceeds the RPC
// message bound.
func FuzzServeRPC(f *testing.F) {
	d := fuzzDevice(f)
	o := binary.LittleEndian
	ar := rpc.NewRandomUUID()
	ndr := func(blocks []byte) []byte { return pnio.AppendNDRRequest(nil, o, pnio.DefaultArgsMaximum, blocks) }
	req := validConnect(ethernet.MAC{0x02, 0, 0, 0, 0, 2})
	f.Add(uint8(0), ndr(req.Append(nil)))
	f.Add(uint8(0), ndr(validSAR(ethernet.MAC{0x02, 0, 0, 0, 0, 2}).Append(nil)))
	cb := pnio.ControlBlock{Type: pnio.BlockPrmEndReq, ARUUID: ar, SessionKey: 1, Command: pnio.CmdPrmEnd}
	f.Add(uint8(4), ndr(cb.Append(nil)))
	rel := pnio.ControlBlock{Type: pnio.BlockReleaseReq, ARUUID: ar, SessionKey: 1, Command: pnio.CmdRelease}
	f.Add(uint8(1), ndr(rel.Append(nil)))
	q := pnio.ReadReq{ARUUID: ar, Slot: 0, Subslot: 1, Index: pnio.IndexIM0, Length: 4096}
	f.Add(uint8(2), ndr(q.Append(nil)))
	f.Add(uint8(5), ndr((&pnio.ReadReq{Index: pnio.IndexIM0Filter, Length: 0xFFFFFFFF}).Append(nil)))
	w := pnio.WriteReq{ARUUID: ar, Slot: 2, Subslot: 1, Index: 0x10, Length: 2}
	f.Add(uint8(3), ndr(append(w.Append(nil), 0, 2)))
	wm := pnio.WriteReq{ARUUID: ar, Index: pnio.IndexWriteMultiple, Length: 0xFFFFFFFF}
	f.Add(uint8(3), ndr(append(wm.Append(nil), pnio.AppendWriteMultiple(nil, []pnio.WriteMultipleItem{{Data: []byte{1}}})...)))
	f.Add(uint8(6), ndr(nil))
	f.Add(uint8(0), []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0, 0, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF})
	f.Fuzz(func(t *testing.T, opi uint8, body []byte) {
		op := hostileOps[int(opi)%len(hostileOps)]
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		out, err := d.serveRPC(ctx, hostileRequest(d, op, body))
		if op == pnio.OpNum(9) && err != nil {
			// An unknown operation with a valid NDR header is rejected;
			// with a malformed one it is answered with a status.
			return
		}
		if err != nil {
			t.Fatalf("op %d: %v", op, err)
		}
		if len(out) > rpc.DefaultMaxMessage {
			t.Fatalf("op %d: answer of %d octets", op, len(out))
		}
		if _, _, err := pnio.DecodeNDRResponse(out, o); err != nil {
			t.Fatalf("op %d: answer is not an NDR response: %v", op, err)
		}
		if n := len(d.ARs()); n > d.model.Capabilities.MaxARs {
			t.Fatalf("%d ARs, MaxARs %d", n, d.model.Capabilities.MaxARs)
		}
		dropARs(d)
	})
}

// ndrRequest wraps blocks in an NDR request whose MaximumCount covers
// them.
func ndrRequest(blocks []byte) []byte {
	return pnio.AppendNDRRequest(nil, rpcOrder, max(pnio.DefaultArgsMaximum, uint32(len(blocks))), blocks)
}

// hostileCall serves one request and returns the status and blocks of the answer.
func hostileCall(t *testing.T, d *Device, op pnio.OpNum, blocks []byte) (pnio.Status, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := d.serveRPC(ctx, hostileRequest(d, op, ndrRequest(blocks)))
	if err != nil {
		t.Fatalf("op %d: %v", op, err)
	}
	st, res, err := pnio.DecodeNDRResponse(out, rpcOrder)
	if err != nil {
		t.Fatalf("op %d: %v", op, err)
	}
	return st, res
}

func frameIDsInUse(d *Device) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, used := range d.frameIDs {
		if used {
			n++
		}
	}
	return n
}

// TestHostileARFlood: a controller that opens ARs without end gets
// MaxARs of them and OutOfARs for the rest; the device keeps no state
// for the refused ones, and releases every FrameID when the ARs end.
func TestHostileARFlood(t *testing.T) {
	d := fuzzDevice(t)
	accepted := 0
	for i := range 64 {
		var req *pnio.ConnectRequest
		if i%2 == 0 {
			req = validSAR(ethernet.MAC{0x02, 0, 0, 0, 1, byte(i)})
		} else {
			// Every IOCAR from another controller, so that none replaces
			// another.
			req = validConnect(ethernet.MAC{0x02, 0, 0, 0, 1, byte(i)})
			req.AR.InitiatorObject[11] = byte(i)
		}
		st, _ := hostileCall(t, d, pnio.OpConnect, req.Append(nil))
		switch {
		case st.OK():
			accepted++
		case st.Code1 != pnio.Code1CMRPC || st.Code2 != pnio.CMRPCOutOfARs:
			t.Fatalf("connect %d: status %s", i, st)
		}
		if n := len(d.ARs()); n > d.model.Capabilities.MaxARs {
			t.Fatalf("connect %d: %d ARs", i, n)
		}
	}
	if accepted != d.model.Capabilities.MaxARs {
		t.Fatalf("%d accepted, MaxARs %d", accepted, d.model.Capabilities.MaxARs)
	}
	if n := frameIDsInUse(d); n > d.model.Capabilities.MaxARs {
		t.Fatalf("%d FrameIDs in use", n)
	}
	dropARs(d)
	eventually(t, 5*time.Second, "no ARs and no FrameIDs", func() bool { return len(d.ARs()) == 0 && frameIDsInUse(d) == 0 })
	d.mu.Lock()
	reserved := d.reserved
	d.mu.Unlock()
	if reserved != 0 {
		t.Fatalf("%d reservations left", reserved)
	}
}

// TestHostileConnect: Connect requests a malicious controller can build
// within one RPC message are refused without an AR, a FrameID or a
// reservation left behind.
func TestHostileConnect(t *testing.T) {
	d := fuzzDevice(t)
	mac := ethernet.MAC{0x02, 0, 0, 0, 0, 2}
	cases := []struct {
		name string
		mod  func(r *pnio.ConnectRequest)
	}{
		{"hundreds of IOCRs", func(r *pnio.ConnectRequest) {
			for len(r.IOCRs) < 300 {
				r.IOCRs = append(r.IOCRs, r.IOCRs[0])
			}
		}},
		{"two input IOCRs", func(r *pnio.ConnectRequest) { r.IOCRs[1] = r.IOCRs[0] }},
		{"no IOCR", func(r *pnio.ConnectRequest) { r.IOCRs = nil }},
		{"maximal DataLength", func(r *pnio.ConnectRequest) { r.IOCRs[0].DataLength = 0xFFFF }},
		{"zero DataLength", func(r *pnio.ConnectRequest) { r.IOCRs[1].DataLength = 0 }},
		{"no alarm CR", func(r *pnio.ConnectRequest) { r.AlarmCR = nil }},
		{"huge alarm data", func(r *pnio.ConnectRequest) { r.AlarmCR.MaxAlarmDataLength = 0xFFFF }},
		{"no expected submodules", func(r *pnio.ConnectRequest) { r.Expected = nil }},
		{"a thousand expected modules", func(r *pnio.ConnectRequest) {
			e := r.Expected[len(r.Expected)-1]
			for i := range 1000 {
				e2 := e
				e2.Modules = append([]pnio.ExpectedModule(nil), e.Modules...)
				e2.Modules[0].Slot = uint16(100 + i)
				r.Expected = append(r.Expected, e2)
			}
		}},
		{"activity timeout 0", func(r *pnio.ConnectRequest) { r.AR.ActivityTimeoutFactor = 0 }},
		{"activity timeout max", func(r *pnio.ConnectRequest) { r.AR.ActivityTimeoutFactor = 0xFFFF }},
		{"nil AR UUID", func(r *pnio.ConnectRequest) { r.AR.ARUUID = rpc.NilUUID }},
		{"unknown AR type", func(r *pnio.ConnectRequest) { r.AR.Type = 0xFFFF }},
		{"maximal station name", func(r *pnio.ConnectRequest) {
			b := make([]byte, 240)
			for i := range b {
				b[i] = 'a'
			}
			r.AR.StationName = string(b)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := validConnect(mac)
			c.mod(req)
			blocks := req.Append(nil)
			start := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if len(blocks) > rpc.DefaultMaxMessage-pnio.NDRHeaderLen {
				t.Fatalf("request of %d octets does not fit an RPC message", len(blocks))
			}
			out, err := d.serveRPC(ctx, hostileRequest(d, pnio.OpConnect, ndrRequest(blocks)))
			if err != nil {
				t.Fatal(err)
			}
			if el := time.Since(start); el > 2*time.Second {
				t.Fatalf("connect took %v", el)
			}
			st, _, err := pnio.DecodeNDRResponse(out, rpcOrder)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("status %s", st)
			// What the device accepts must leave consistent state; what it
			// refuses none.
			if st.OK() {
				if len(d.ARs()) != 1 {
					t.Fatalf("accepted with %d ARs", len(d.ARs()))
				}
				dropARs(d)
			}
			eventually(t, 5*time.Second, "no AR and no FrameID", func() bool { return len(d.ARs()) == 0 && frameIDsInUse(d) == 0 })
		})
	}
	// The unmodified request is accepted, so that the cases above test
	// the field they change.
	if st, _ := hostileCall(t, d, pnio.OpConnect, validConnect(mac).Append(nil)); !st.OK() {
		t.Fatalf("valid connect: %s", st)
	}
	dropARs(d)
}

// TestHostileRecords: reads and writes whose lengths do not match their
// payload, and control calls for ARs that do not exist or in the wrong
// state.
func TestHostileRecords(t *testing.T) {
	d := fuzzDevice(t)
	req := validSAR(ethernet.MAC{0x02, 0, 0, 0, 0, 2})
	if st, _ := hostileCall(t, d, pnio.OpConnect, req.Append(nil)); !st.OK() {
		t.Fatalf("connect: %s", st)
	}
	ar := req.AR.ARUUID
	// A write that claims 4 GiB of record data with two octets present.
	w := pnio.WriteReq{ARUUID: ar, Slot: 2, Subslot: 1, Index: 0x10, Length: 0xFFFFFFFF}
	st, out := hostileCall(t, d, pnio.OpWrite, append(w.Append(nil), 0, 1))
	if st.Code1 != pnio.RWWriteLength || len(out) == 0 {
		t.Fatalf("4 GiB write: %s", st)
	}
	// One octet short, one octet long (the extra octet is ignored).
	w.Length = 3
	if st, _ := hostileCall(t, d, pnio.OpWrite, append(w.Append(nil), 0, 1)); st.Code1 != pnio.RWWriteLength {
		t.Fatalf("short write: %s", st)
	}
	w.Length = 2
	if st, _ := hostileCall(t, d, pnio.OpWrite, append(w.Append(nil), 0, 1, 2)); !st.OK() {
		t.Fatalf("write with trailing octet: %s", st)
	}
	// A record of the wrong length.
	w.Length = 1
	if st, _ := hostileCall(t, d, pnio.OpWrite, append(w.Append(nil), 0)); st.OK() {
		t.Fatal("write of 1 octet to a 2-octet record accepted")
	}
	// A WriteMultiple whose items claim more data than they carry, and one
	// that nests a WriteMultiple.
	inner := pnio.WriteReq{Slot: 2, Subslot: 1, Index: 0x10, Length: 0xFFFF}
	data := append(inner.Append(nil), 0, 1)
	wm := pnio.WriteReq{ARUUID: ar, Index: pnio.IndexWriteMultiple, Length: uint32(len(data))}
	if st, _ := hostileCall(t, d, pnio.OpWrite, append(wm.Append(nil), data...)); st.OK() {
		t.Fatal("lying WriteMultiple item accepted")
	}
	nested := pnio.AppendWriteMultiple(nil, []pnio.WriteMultipleItem{{Header: pnio.WriteReq{Index: pnio.IndexWriteMultiple}, Data: []byte{1, 2, 3, 4}}})
	wm.Length = uint32(len(nested))
	if st, _ := hostileCall(t, d, pnio.OpWrite, append(wm.Append(nil), nested...)); st.OK() {
		t.Fatal("nested WriteMultiple accepted")
	}
	// A WriteMultiple of many items is answered with one header each.
	var items []pnio.WriteMultipleItem
	for range 200 {
		items = append(items, pnio.WriteMultipleItem{Header: pnio.WriteReq{Slot: 2, Subslot: 1, Index: 0x10}, Data: []byte{0, 1}})
	}
	data = pnio.AppendWriteMultiple(nil, items)
	wm.Length = uint32(len(data))
	st, out = hostileCall(t, d, pnio.OpWrite, append(wm.Append(nil), data...))
	if !st.OK() {
		t.Fatalf("200 item WriteMultiple: %s", st)
	}
	if blocks, err := pnio.SplitBlocks(out); err != nil || len(blocks) != 201 {
		t.Fatalf("%d answers, %v", len(blocks), err)
	}
	// Reads that bound the answer to nothing, and to 4 GiB.
	for _, l := range []uint32{0, 1, 0xFFFFFFFF} {
		q := pnio.ReadReq{ARUUID: ar, Subslot: 1, Index: pnio.IndexIM0, Length: l}
		st, out := hostileCall(t, d, pnio.OpRead, q.Append(nil))
		if !st.OK() {
			t.Fatalf("read of %d: %s", l, st)
		}
		blk, rest, err := pnio.ReadBlock(out)
		if err != nil {
			t.Fatal(err)
		}
		h, err := pnio.DecodeReadRes(blk)
		if err != nil || h.Length > l || int(h.Length) != len(rest) {
			t.Fatalf("read of %d: header %+v, %d octets, %v", l, h, len(rest), err)
		}
	}
	// Control calls with a wrong session key, for an unknown AR, and a
	// PrmEnd of an IOSAR that is in Data already.
	for _, cb := range []pnio.ControlBlock{
		{Type: pnio.BlockPrmEndReq, ARUUID: ar, SessionKey: 99, Command: pnio.CmdPrmEnd},
		{Type: pnio.BlockReleaseReq, ARUUID: rpc.NewRandomUUID(), SessionKey: 1, Command: pnio.CmdRelease},
	} {
		op := pnio.OpControl
		if cb.Type == pnio.BlockReleaseReq {
			op = pnio.OpRelease
		}
		if st, _ := hostileCall(t, d, op, cb.Append(nil)); st.Code1 != pnio.Code1CMRPC || st.Code2 != pnio.CMRPCARUUIDUnknown {
			t.Fatalf("%+v: %s", cb, st)
		}
	}
	cb := pnio.ControlBlock{Type: pnio.BlockPrmEndReq, ARUUID: ar, SessionKey: 1, Command: pnio.CmdPrmEnd}
	if st, _ := hostileCall(t, d, pnio.OpControl, cb.Append(nil)); st.OK() {
		t.Fatal("PrmEnd of an IOSAR in Data accepted")
	}
	// The AR survived all of it, and a release ends it.
	if s, ok := arState(d, ar); !ok || s != ARStateData {
		t.Fatalf("AR %v %v", s, ok)
	}
	rel := pnio.ControlBlock{Type: pnio.BlockReleaseReq, ARUUID: ar, SessionKey: 1, Command: pnio.CmdRelease}
	if st, _ := hostileCall(t, d, pnio.OpRelease, rel.Append(nil)); !st.OK() {
		t.Fatalf("release: %s", st)
	}
	eventually(t, 5*time.Second, "AR gone", func() bool { return len(d.ARs()) == 0 })
}

// TestHostileNDRAllocation: an NDR header that claims gigabytes is
// refused before anything of that size is allocated.
func TestHostileNDRAllocation(t *testing.T) {
	d := fuzzDevice(t)
	body := make([]byte, pnio.NDRHeaderLen+8)
	for _, o := range []rpc.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		o.PutUint32(body, 0xFFFFFFFF)     // ArgsMaximum
		o.PutUint32(body[4:], 0xFFFFFFF0) // ArgsLength
		o.PutUint32(body[8:], 0xFFFFFFFF) // MaximumCount
		o.PutUint32(body[16:], 0xFFFFFFF0)
		if _, _, err := pnio.DecodeNDRRequest(body, o); err == nil {
			t.Fatal("4 GiB NDR request decoded")
		}
		if n := testing.AllocsPerRun(100, func() { _, _, _ = pnio.DecodeNDRRequest(body, o) }); n > 1 {
			t.Fatalf("DecodeNDRRequest: %v allocations", n)
		}
	}
	// The device answers with a status of a few octets, whatever the
	// ArgsMaximum claims.
	ctx := context.Background()
	for _, op := range hostileOps[:6] {
		req := hostileRequest(d, op, body)
		var out []byte
		allocs := testing.AllocsPerRun(20, func() { out, _ = d.serveRPC(ctx, req) })
		if len(out) > 64 {
			t.Fatalf("op %d: answer of %d octets", op, len(out))
		}
		if allocs > 20 {
			t.Fatalf("op %d: %v allocations", op, allocs)
		}
	}
}

// TestHostileReadDuringAbort: reads and writes racing the abort of their
// AR end with an answer, never a panic or a hang.
func TestHostileReadDuringAbort(t *testing.T) {
	d := fuzzDevice(t)
	for round := range 20 {
		req := validSAR(ethernet.MAC{0x02, 0, 0, 0, 0, 2})
		if st, _ := hostileCall(t, d, pnio.OpConnect, req.Append(nil)); !st.OK() {
			t.Fatalf("round %d: connect: %s", round, st)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			q := pnio.ReadReq{ARUUID: req.AR.ARUUID, Subslot: 1, Index: pnio.IndexIM0, Length: 4096}
			w := pnio.WriteReq{ARUUID: req.AR.ARUUID, Slot: 2, Subslot: 1, Index: 0x10, Length: 2}
			for range 20 {
				_, _ = hostileCall(t, d, pnio.OpRead, q.Append(nil))
				_, _ = hostileCall(t, d, pnio.OpWrite, append(w.Append(nil), 0, 1))
			}
		}()
		dropARs(d)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("round %d: record calls hang", round)
		}
		eventually(t, 5*time.Second, "AR gone", func() bool { return len(d.ARs()) == 0 })
	}
}

// TestHostileLargeExpectedConfig: the largest expected configuration a
// Connect within one RPC message can carry. The device must answer the
// Connect and the reads of the records derived from it (expected
// identification, ModuleDiff) without an encoder overflowing a 16-bit
// block length: the encoders panic on that, and no request may reach it.
func TestHostileLargeExpectedConfig(t *testing.T) {
	d := fuzzDevice(t)
	for _, perBlock := range []int{1, 64, 2000} {
		req := validConnect(ethernet.MAC{0x02, 0, 0, 0, 0, 2})
		tmpl := req.Expected[len(req.Expected)-1].Modules[0]
		slot := 100
		limit := rpc.DefaultMaxMessage - pnio.NDRHeaderLen - 64
		size := len(req.Append(nil))
		for {
			blk := pnio.ExpectedSubmoduleBlockReq{}
			for range perBlock {
				m := tmpl
				m.Slot = uint16(slot)
				slot++
				blk.Modules = append(blk.Modules, m)
			}
			n := len(blk.Append(nil))
			if n > 0xFFFF || size+n > limit {
				break
			}
			req.Expected = append(req.Expected, blk)
			size += n
		}
		blocks := req.Append(nil)
		if len(blocks) != size {
			t.Fatalf("request of %d octets, counted %d", len(blocks), size)
		}
		t.Logf("%d modules per block: Connect of %d octets", perBlock, len(blocks))
		st, _ := hostileCall(t, d, pnio.OpConnect, blocks)
		if !st.OK() {
			t.Fatalf("refused: %s", st)
		}
		for _, idx := range []pnio.Index{pnio.IndexExpectedIdentAR, pnio.IndexRealIdentAR, pnio.IndexModuleDiffAR, pnio.IndexRealIdentAPI, pnio.IndexExpectedIdentSlot} {
			q := pnio.ReadReq{ARUUID: req.AR.ARUUID, Index: idx, Length: 0xFFFFFFFF}
			st, out := hostileCall(t, d, pnio.OpRead, q.Append(nil))
			t.Logf("read %s: %s, %d octets", idx, st, len(out))
			if !st.OK() || len(out) > rpc.DefaultMaxMessage {
				t.Fatalf("read %s: %s, %d octets", idx, st, len(out))
			}
		}
		dropARs(d)
		eventually(t, 5*time.Second, "AR gone", func() bool { return len(d.ARs()) == 0 })
	}
}
