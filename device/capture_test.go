// SPDX-License-Identifier: MIT

package device

// Replays the requests a real SIMATIC S7-1500 CPU 1515-2 PN (firmware
// V2.9.4) sent to an IO-Device, recorded by pnio-rs (testdata/captures,
// source pnio-rs), to this package's device, frame by frame on the wire,
// and compares the device's answers with the answers of the recorded
// IO-Device. That IO-Device was rt-labs p-net v0.2.0, a software stack: its
// answers are one valid implementation, not the reference; where this
// device answers differently the difference is classified below.

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/alarm"
	"github.com/otfabric/go-profinet/dcp"
	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/internal/capture"
	"github.com/otfabric/go-profinet/l2"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/netstack"
	"github.com/otfabric/go-profinet/pnio"
	"github.com/otfabric/go-profinet/rpc"
	"github.com/otfabric/go-profinet/rt"
	"github.com/otfabric/go-profinet/vnet"
)

// The addresses of the recording (ar-connect-2026-08-27-164334.pcapng).
var (
	recCPUMAC = ethernet.MAC{0xec, 0x1c, 0x5d, 0x61, 0xe7, 0x3f}
	recDevMAC = ethernet.MAC{0x8c, 0xf3, 0x19, 0xcd, 0x19, 0xf8}
	recCPUIP  = netip.MustParseAddr("172.16.2.100")
	recDevIP  = netip.MustParseAddr("172.16.2.10")
)

// pnetModel is the module set the CPU's Connect request expects: the
// p-net sample application's DAP (one port) and its four modules
// (slot 1 8 inputs, slot 2 8 outputs, slot 3 8 in + 8 out, slot 4 64 in +
// 64 out), with the records the CPU writes at start-up (0x7B, 0x7C on
// slot 3, 0x7D on slot 4: four octets each).
func pnetModel() *model.Device {
	rec := func(i pnio.Index) model.Record { return model.Record{Index: i, Length: 4} }
	return &model.Device{
		Identity: model.Identity{
			VendorID: 0x0493, DeviceID: 0x0002, VendorName: "rt-labs", TypeOfStation: "P-Net Sample App",
			OrderID: "12345 Abcdefghijk", SerialNumber: "007", HardwareRevision: 3,
			SoftwareRevision: pnio.SoftwareRevision{Prefix: 'V', BugFix: 2},
		},
		Capabilities: model.Capabilities{MaxARs: 1, MinDeviceInterval: 32, ConformanceClass: "B", Ports: 1},
		DAP:          model.StandardDAP("DAP 1", 0x00000001, 1),
		Slots: []model.Slot{
			{Number: 1, Module: model.Module{Name: "DI 8xLogicLevel", IdentNumber: 0x30, Submodules: []model.Submodule{{Name: "DI", IdentNumber: 0x130, Subslot: 1, InputLen: 1}}}},
			{Number: 2, Module: model.Module{Name: "DO 8xLogicLevel", IdentNumber: 0x31, Submodules: []model.Submodule{{Name: "DO", IdentNumber: 0x131, Subslot: 1, OutputLen: 1}}}},
			{Number: 3, Module: model.Module{Name: "DIO 8xLogicLevel", IdentNumber: 0x32, Submodules: []model.Submodule{{Name: "DIO", IdentNumber: 0x132, Subslot: 1, InputLen: 1, OutputLen: 1,
				Records: []model.Record{rec(0x7B), rec(0x7C)}}}}},
			{Number: 4, Module: model.Module{Name: "Echo module", IdentNumber: 0x40, Submodules: []model.Submodule{{Name: "Echo", IdentNumber: 0x140, Subslot: 1, InputLen: 8, OutputLen: 8,
				Records: []model.Record{rec(0x7D)}}}}},
		},
	}
}

// recordedCPU plays the CPU: a raw port with the CPU's MAC that sends the
// recorded frames as they are, answers ARP for the CPU's address and hands
// every other frame it receives to the test.
type recordedCPU struct {
	t      *testing.T
	ep     *vnet.Endpoint
	frames chan []byte
}

func newRecordedCPU(t *testing.T, n *vnet.Network) *recordedCPU {
	t.Helper()
	ep, err := n.NewEndpoint(vnet.EndpointConfig{Name: "cpu", MAC: recCPUMAC})
	if err != nil {
		t.Fatal(err)
	}
	c := &recordedCPU{t: t, ep: ep, frames: make(chan []byte, 256)}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 2048)
		for {
			n, err := ep.ReadFrame(ctx, buf)
			if err != nil {
				return
			}
			f := append([]byte(nil), buf[:n]...)
			if c.answerARP(ctx, f) {
				continue
			}
			select {
			case c.frames <- f:
			default:
			}
		}
	}()
	t.Cleanup(func() { cancel(); wg.Wait(); _ = ep.Close() })
	return c
}

// answerARP answers an ARP request for the CPU's address.
func (c *recordedCPU) answerARP(ctx context.Context, f []byte) bool {
	e, err := ethernet.Decode(f)
	if err != nil || e.EtherType != ethernet.EtherTypeARP {
		return false
	}
	p := e.Payload
	if len(p) < 28 || binary.BigEndian.Uint16(p[6:]) != 1 || !bytes.Equal(p[24:28], recCPUIP.AsSlice()) {
		return true
	}
	r := make([]byte, 28)
	copy(r, p[:6])
	binary.BigEndian.PutUint16(r[6:], 2)
	copy(r[8:], recCPUMAC[:])
	copy(r[14:], recCPUIP.AsSlice())
	copy(r[18:], p[8:14])
	copy(r[24:], p[14:18])
	out := (&ethernet.Frame{Dst: e.Src, Src: recCPUMAC, EtherType: ethernet.EtherTypeARP, Payload: r}).Encode()
	_ = c.ep.WriteFrame(ctx, out)
	return true
}

func (c *recordedCPU) send(frame []byte) {
	c.t.Helper()
	if err := c.ep.WriteFrame(context.Background(), frame); err != nil {
		c.t.Fatal(err)
	}
}

// next returns the next frame the CPU receives that match accepts.
func (c *recordedCPU) next(what string, match func([]byte) bool) []byte {
	c.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case f := <-c.frames:
			if match(f) {
				return f
			}
		case <-timeout:
			c.t.Fatalf("no %s from the device", what)
			return nil
		}
	}
}

// rpcFrom matches a UDP datagram carrying a DCE-RPC PDU of type typ.
func rpcFrom(typ rpc.PacketType) func([]byte) bool {
	return func(f []byte) bool {
		u, err := capture.DecodeUDP(f)
		if err != nil {
			return false
		}
		h, _, err := rpc.DecodeHeader(u.Payload)
		return err == nil && h.Type == typ
	}
}

// decodeRPC splits a recorded or received RPC frame into its header and
// the PNIO blocks of its NDR body.
func decodeRPC(t *testing.T, frame []byte) (capture.UDP, rpc.Header, pnio.Status, uint32, []byte) {
	t.Helper()
	u, err := capture.DecodeUDP(frame)
	if err != nil {
		t.Fatal(err)
	}
	h, body, err := rpc.DecodeHeader(u.Payload)
	if err != nil {
		t.Fatal(err)
	}
	o := h.Order()
	if h.Type == rpc.TypeRequest {
		argsMax, blocks, err := pnio.DecodeNDRRequest(body, o)
		if err != nil {
			t.Fatal(err)
		}
		return u, h, pnio.Status{}, argsMax, blocks
	}
	st, blocks, err := pnio.DecodeNDRResponse(body, o)
	if err != nil {
		t.Fatal(err)
	}
	// The NDR response's max_count (after the status and args_length).
	var maxCount uint32
	if len(body) >= 12 {
		maxCount = o.Uint32(body[8:])
	}
	return u, h, st, maxCount, blocks
}

// udpFrame builds an untagged IPv4/UDP frame (UDP checksum 0, as RFC 768
// allows over IPv4).
func udpFrame(dst, src ethernet.MAC, from, to netip.AddrPort, payload []byte) []byte {
	ip := make([]byte, 28, 28+len(payload))
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:], uint16(28+len(payload)))
	ip[8], ip[9] = 64, 17
	copy(ip[12:], from.Addr().AsSlice())
	copy(ip[16:], to.Addr().AsSlice())
	var sum uint32
	for i := 0; i < 20; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(ip[i:]))
	}
	for sum > 0xFFFF {
		sum = sum&0xFFFF + sum>>16
	}
	binary.BigEndian.PutUint16(ip[10:], ^uint16(sum))
	binary.BigEndian.PutUint16(ip[20:], from.Port())
	binary.BigEndian.PutUint16(ip[22:], to.Port())
	binary.BigEndian.PutUint16(ip[24:], uint16(8+len(payload)))
	ip = append(ip, payload...)
	return (&ethernet.Frame{Dst: dst, Src: src, EtherType: ethernet.EtherTypeIPv4, Payload: ip}).Encode()
}

type captureHandler struct {
	NopHandler
	mu     sync.Mutex
	writes []RecordRequest
	acks   []pnio.AlarmAck
	states []ARState
}

func (h *captureHandler) WriteRecord(ctx context.Context, r RecordRequest, data []byte) error {
	h.mu.Lock()
	h.writes = append(h.writes, r)
	h.mu.Unlock()
	return h.NopHandler.WriteRecord(ctx, r, data)
}

func (h *captureHandler) AlarmAck(_ ARInfo, a pnio.AlarmAck) {
	h.mu.Lock()
	h.acks = append(h.acks, a)
	h.mu.Unlock()
}

func (h *captureHandler) StateChanged(_ ARInfo, _, to ARState) {
	h.mu.Lock()
	h.states = append(h.states, to)
	h.mu.Unlock()
}

// replayDevice starts the device under the recorded device's addresses
// and name.
func replayDevice(t *testing.T, h Handler) (*Device, *recordedCPU) {
	t.Helper()
	n := vnet.New(1)
	t.Cleanup(func() { _ = n.Close() })
	ep, err := n.NewEndpoint(vnet.EndpointConfig{Name: "dev", MAC: recDevMAC})
	if err != nil {
		t.Fatal(err)
	}
	mux := l2.NewMux(ep)
	st, err := netstack.New(mux, netstack.Config{Addr: recDevIP, Mask: mask})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(); _ = mux.Close(); _ = ep.Close() })
	cpu := newRecordedCPU(t, n)
	d, err := New(Config{
		Model: pnetModel(), Mux: mux, StationName: "rt-labs-dev",
		IP:           dcp.IPSuite{Addr: recDevIP, Mask: mask, Gateway: netip.IPv4Unspecified()},
		ListenPacket: st.ListenPacket, Handler: h, NoResponseDelay: true, DisableLLDP: true,
		ApplicationReadyTimeout: 3 * time.Second,
		IM:                      IMData{IM1: pnio.IM1{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, cpu
}

// TestCaptureReplayStartup sends the CPU's Connect, Write (MultipleWrite)
// and Control (PrmEnd) requests to the device, answers its
// ApplicationReady with the CPU's recorded answer and then feeds it the
// CPU's cyclic output frame.
func TestCaptureReplayStartup(t *testing.T) { replayStartup(t) }

// replayed is the state after replayStartup: the AR in Data.
type replayed struct {
	d   *Device
	cpu *recordedCPU
	h   *captureHandler
	res *pnio.ConnectResponse
}

func replayStartup(t *testing.T) replayed {
	t.Helper()
	h := &captureHandler{}
	d, cpu := replayDevice(t, h)

	// Connect (frame 50 -> 53).
	reqFrame := capture.MustLoad("pnio-rs/s7-1500_p-net_connect-req.hex")
	_, reqH, _, reqArgsMax, reqBlocks := decodeRPC(t, reqFrame)
	creq, err := pnio.DecodeConnectRequest(reqBlocks)
	if err != nil {
		t.Fatal(err)
	}
	cpu.send(reqFrame)
	got := cpu.next("Connect response", rpcFrom(rpc.TypeResponse))
	gu, gh, gst, gMax, gBlocks := decodeRPC(t, got)
	_, wh, wst, wMax, wBlocks := decodeRPC(t, capture.MustLoad("pnio-rs/s7-1500_p-net_connect-res.hex"))
	if gst != wst {
		t.Fatalf("Connect refused: %v", gst)
	}
	if gu.Dst.String() != "172.16.2.100:54766" || gu.Src.Port() != rpc.Port {
		t.Errorf("Connect response %v -> %v, recorded 172.16.2.10:34964 -> 172.16.2.100:54766", gu.Src, gu.Dst)
	}
	if gh.Activity != reqH.Activity || gh.SeqNum != reqH.SeqNum || gh.OpNum != wh.OpNum || gh.Object != wh.Object || gh.Iface != wh.Iface {
		t.Errorf("Connect response header: activity/seq/op/object/interface %v/%d/%d/%v/%v, recorded %v/%d/%d/%v/%v",
			gh.Activity, gh.SeqNum, gh.OpNum, gh.Object, gh.Iface, wh.Activity, wh.SeqNum, wh.OpNum, wh.Object, wh.Iface)
	}
	if gMax != wMax || gMax != reqArgsMax {
		t.Errorf("Connect response max_count %d, recorded %d (the request's args_max %d)", gMax, wMax, reqArgsMax)
	}
	gres, err := pnio.DecodeConnectResponse(gBlocks)
	if err != nil {
		t.Fatal(err)
	}
	wres, err := pnio.DecodeConnectResponse(wBlocks)
	if err != nil {
		t.Fatal(err)
	}
	if gres.AR != wres.AR {
		t.Errorf("ARBlockRes %+v, recorded %+v", gres.AR, wres.AR)
	}
	if len(gres.IOCRs) != len(wres.IOCRs) {
		t.Fatalf("IOCRBlockRes %+v, recorded %+v", gres.IOCRs, wres.IOCRs)
	}
	for i := range gres.IOCRs {
		if gres.IOCRs[i] != wres.IOCRs[i] {
			t.Errorf("IOCRBlockRes %d: %+v, recorded %+v", i, gres.IOCRs[i], wres.IOCRs[i])
		}
	}
	// Intentional difference: p-net sends no ModuleDiffBlock when every
	// submodule matches; this device reports every submodule of an IOCAR
	// as ApplicationReadyPending until ApplicationReady (IEC 61158-6-10
	// ARInfo). The recording shows only that the CPU accepts the former;
	// what matters here is that nothing is wrong, missing or locked.
	if wres.ModuleDiff != nil {
		t.Errorf("recorded ModuleDiffBlock %+v; the fixture expects none", wres.ModuleDiff)
	}
	if gres.ModuleDiff != nil {
		n := 0
		for _, a := range gres.ModuleDiff.APIs {
			for _, m := range a.Modules {
				if m.State != pnio.ModuleStateProper {
					t.Errorf("ModuleDiff slot %d state %#x, want proper: the model differs from the CPU's configuration", m.Slot, m.State)
				}
				for _, s := range m.Submodules {
					n++
					if s.State != pnio.SubmStateFormat|pnio.SubmStateARInfoAppReady {
						t.Errorf("ModuleDiff %d/%#x state %#x, want only ApplicationReadyPending", m.Slot, s.Subslot, s.State)
					}
				}
			}
		}
		if n != 7 {
			t.Errorf("ModuleDiff lists %d submodules, the CPU expects 7", n)
		}
	}
	if gres.AlarmCR == nil || gres.AlarmCR.Type != wres.AlarmCR.Type {
		t.Fatalf("AlarmCRBlockRes %+v, recorded %+v", gres.AlarmCR, wres.AlarmCR)
	}
	// Intentional difference: p-net answers MaxAlarmDataLength 200 (its
	// own maximum) to the CPU's 256; this device grants what the CPU asked
	// for when it can hold it. Both are below the request, as required.
	if gres.AlarmCR.MaxAlarmDataLength > creq.AlarmCR.MaxAlarmDataLength || gres.AlarmCR.MaxAlarmDataLength < 200 {
		t.Errorf("MaxAlarmDataLength %d, CPU asked %d (p-net answered %d)", gres.AlarmCR.MaxAlarmDataLength, creq.AlarmCR.MaxAlarmDataLength, wres.AlarmCR.MaxAlarmDataLength)
	}
	if gres.ARServer == nil || gres.ARServer.StationName != wres.ARServer.StationName {
		t.Errorf("ARServerBlock %+v, recorded %+v", gres.ARServer, wres.ARServer)
	}

	// Write, MultipleWrite of PDInterfaceAdjust and three module records
	// (frame 54 -> 56).
	cpu.send(capture.MustLoad("pnio-rs/s7-1500_p-net_write-multiple-req.hex"))
	got = cpu.next("Write response", rpcFrom(rpc.TypeResponse))
	_, gh, gst, gMax, gBlocks = decodeRPC(t, got)
	_, wh, wst, wMax, wBlocks = decodeRPC(t, capture.MustLoad("pnio-rs/s7-1500_p-net_write-multiple-res.hex"))
	if gst != wst || gMax != wMax || gh.SeqNum != wh.SeqNum || gh.OpNum != wh.OpNum {
		t.Errorf("Write response status/max_count/seq/op %v/%d/%d/%d, recorded %v/%d/%d/%d", gst, gMax, gh.SeqNum, gh.OpNum, wst, wMax, wh.SeqNum, wh.OpNum)
	}
	compareWriteRes(t, gBlocks, wBlocks)
	h.mu.Lock()
	nw := len(h.writes)
	h.mu.Unlock()
	if nw != 3 { // 0x7B, 0x7C, 0x7D; PDInterfaceAdjust is the device's own
		t.Errorf("application saw %d record writes, want the 3 module records", nw)
	}

	// Control PrmEnd (frame 57 -> 58).
	cpu.send(capture.MustLoad("pnio-rs/s7-1500_p-net_prmend-req.hex"))
	got = cpu.next("PrmEnd response", rpcFrom(rpc.TypeResponse))
	_, gh, gst, gMax, gBlocks = decodeRPC(t, got)
	_, wh, wst, wMax, wBlocks = decodeRPC(t, capture.MustLoad("pnio-rs/s7-1500_p-net_prmend-res.hex"))
	if gst != wst || gMax != wMax || gh.SeqNum != wh.SeqNum {
		t.Errorf("PrmEnd response status/max_count/seq %v/%d/%d, recorded %v/%d/%d", gst, gMax, gh.SeqNum, wst, wMax, wh.SeqNum)
	}
	if !bytes.Equal(gBlocks, wBlocks) {
		t.Errorf("IODControlRes\n got % x\nwant % x", gBlocks, wBlocks)
	}

	// ApplicationReady (device -> CPU, frame 59), answered with the CPU's
	// recorded answer (frame 60). Dynamic fields: the activity UUID and
	// sequence number are the device's, copied into the CPU's answer.
	got = cpu.next("ApplicationReady request", rpcFrom(rpc.TypeRequest))
	gu, gh, _, _, gBlocks = decodeRPC(t, got)
	_, wh, _, _, wBlocks = decodeRPC(t, capture.MustLoad("pnio-rs/s7-1500_p-net_appready-req.hex"))
	if gu.Dst.Addr() != recCPUIP || gu.Dst.Port() != rpc.Port {
		t.Errorf("ApplicationReady sent to %v, want the CPU's 172.16.2.100:34964", gu.Dst)
	}
	if gh.Object != wh.Object || gh.Iface != wh.Iface || gh.OpNum != wh.OpNum {
		t.Errorf("ApplicationReady header object/interface/op %v/%v/%d, recorded %v/%v/%d", gh.Object, gh.Iface, gh.OpNum, wh.Object, wh.Iface, wh.OpNum)
	}
	if !bytes.Equal(gBlocks, wBlocks) {
		t.Errorf("IOXControlReq\n got % x\nwant % x", gBlocks, wBlocks)
	}
	cpuRes := capture.MustLoad("pnio-rs/s7-1500_p-net_appready-res.hex")
	ru, err := capture.DecodeUDP(cpuRes)
	if err != nil {
		t.Fatal(err)
	}
	gotU, _ := capture.DecodeUDP(got)
	pdu := append([]byte(nil), ru.Payload...)
	if gotU.Payload[4] != pdu[4] {
		t.Fatalf("ApplicationReady drep %x; the CPU's answer is little endian", gotU.Payload[4:7])
	}
	copy(pdu[40:56], gotU.Payload[40:56]) // activity UUID, same drep
	copy(pdu[64:68], gotU.Payload[64:68]) // sequence number
	cpu.send(udpFrame(recDevMAC, recCPUMAC, ru.Src, gu.Src, pdu))

	waitState(t, d, ARStateData)

	// Cyclic: the CPU's output frame 0x8001 (rt-cyclic, frame 2): data
	// status 0x35, every IOPS and IOCS GOOD, outputs zero.
	cpu.send(capture.MustLoad("pnio-rs/s7-1500_p-net_rt-output-8001.hex"))
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, iops, ok := d.Output(2, 1)
		if ok && iops == 0x80 && len(data) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("output slot 2: %x iops %#x ok %v after the CPU's frame", data, iops, ok)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The echo frame: %Q0.0 = TRUE (QB0 = 0x01, slot 2) and, on the echo
	// module (QB2..QB9), QD2 = 16#12345678 and QD6 = 1.5: declaration
	// order, big endian, IEEE 754.
	cpu.send(capture.MustLoad("pnio-rs/s7-1500_p-net_rt-output-8001-echo.hex"))
	deadline = time.Now().Add(3 * time.Second)
	for {
		data, _, _ := d.Output(4, 1)
		q0, _, _ := d.Output(2, 1)
		if bytes.Equal(data, []byte{0x12, 0x34, 0x56, 0x78, 0x3f, 0xc0, 0, 0}) && bytes.Equal(q0, []byte{0x01}) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("echo module outputs % x after the CPU's echo frame", data)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The device's own input frame 0x8000 has the layout of the recorded
	// p-net frame: same length, IOPS/IOCS at the same offsets.
	in := cpu.next("input frame 0x8000", func(f []byte) bool {
		e, err := ethernet.Decode(f)
		return err == nil && e.EtherType == ethernet.EtherTypeProfinet && len(e.Payload) >= 2 && binary.BigEndian.Uint16(e.Payload) == 0x8000
	})
	compareInputFrame(t, in, capture.MustLoad("pnio-rs/s7-1500_p-net_rt-input-8000.hex"))
	return replayed{d: d, cpu: cpu, h: h, res: &gres}
}

// keepCyclic sends the CPU's echo frame every 8 ms, as the CPU sends it
// every 32 ms, so that the device's watchdog (3 x 32 ms) does not end the
// AR. Dynamic field: the cycle counter, which advances by 1024 (32 ms in
// 31.25 µs units) per frame as in the recording.
func (c *recordedCPU) keepCyclic() {
	f := append([]byte(nil), capture.MustLoad("pnio-rs/s7-1500_p-net_rt-output-8001-echo.hex")...)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(8 * time.Millisecond)
		defer tick.Stop()
		cc := binary.BigEndian.Uint16(f[len(f)-4:])
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			cc += 1024
			binary.BigEndian.PutUint16(f[len(f)-4:], cc)
			_ = c.ep.WriteFrame(ctx, f)
		}
	}()
	c.t.Cleanup(func() { cancel(); wg.Wait() })
}

func waitState(t *testing.T, d *Device, s ARState) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ars := d.ARs()
		if len(ars) == 1 && ars[0].State == s {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ARs %+v, want one in %v", ars, s)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// compareWriteRes compares the device's Write response blocks with
// p-net's: one IODWriteResHeader per record, the same index, slot,
// subslot and status. p-net's outer header has RecordDataLength 0.
func compareWriteRes(t *testing.T, got, want []byte) {
	t.Helper()
	g, err := pnio.SplitBlocks(got)
	if err != nil {
		t.Fatalf("device Write response: %v", err)
	}
	w, err := pnio.SplitBlocks(want)
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != len(w) {
		t.Fatalf("Write response holds %d blocks, recorded %d", len(g), len(w))
	}
	for i := range g {
		gr, err := pnio.DecodeWriteRes(g[i])
		if err != nil {
			t.Fatal(err)
		}
		wr, err := pnio.DecodeWriteRes(w[i])
		if err != nil {
			t.Fatal(err)
		}
		if gr.Index != wr.Index || gr.Slot != wr.Slot || gr.Subslot != wr.Subslot || gr.API != wr.API || gr.Status != wr.Status || gr.ARUUID != wr.ARUUID || gr.Seq != wr.Seq {
			t.Errorf("IODWriteResHeader %d: %+v, recorded %+v", i, gr, wr)
		}
	}
}

func compareInputFrame(t *testing.T, got, want []byte) {
	t.Helper()
	ge, err := ethernet.Decode(got)
	if err != nil {
		t.Fatal(err)
	}
	we, err := ethernet.Decode(want)
	if err != nil {
		t.Fatal(err)
	}
	if ge.Tagged != we.Tagged || ge.VLAN != we.VLAN {
		t.Errorf("input frame VLAN %v %+v, recorded %v %+v", ge.Tagged, ge.VLAN, we.Tagged, we.VLAN)
	}
	gf, err := rt.Decode(ge.Payload, 40)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := rt.Decode(we.Payload, 40)
	if err != nil {
		t.Fatal(err)
	}
	if gf.DataStatus != wf.DataStatus || gf.TransferStatus != wf.TransferStatus {
		t.Errorf("input DataStatus/TransferStatus %#x/%#x, recorded %#x/%#x", gf.DataStatus, gf.TransferStatus, wf.DataStatus, wf.TransferStatus)
	}
	// IOPS and IOCS positions: every octet that is 0x80 in p-net's frame.
	for i := range wf.Data {
		if wf.Data[i] == 0x80 && gf.Data[i] != 0x80 {
			t.Errorf("input frame octet %d = %#x, recorded GOOD (0x80)", i, gf.Data[i])
		}
	}
}

// alarmFrom matches an RTA frame of the given FrameID and PDU type.
func alarmFrom(frameID uint16, typ alarm.PDUType) func([]byte) bool {
	return func(f []byte) bool {
		e, err := ethernet.Decode(f)
		if err != nil || e.EtherType != ethernet.EtherTypeProfinet {
			return false
		}
		p, err := alarm.Decode(e.Payload)
		return err == nil && p.FrameID == frameID && p.Type == typ
	}
}

func decodeRTA(t *testing.T, frame []byte) (ethernet.Frame, alarm.PDU) {
	t.Helper()
	e, err := ethernet.Decode(frame)
	if err != nil {
		t.Fatal(err)
	}
	p, err := alarm.Decode(e.Payload)
	if err != nil {
		t.Fatal(err)
	}
	return e, p
}

// cpuRTA is a recorded RTA frame of the CPU, moved into this AR. The
// alarm recordings (plan5-alarm.pcapng, 2026-08-30) come from another
// session than the start-up: the CPU sent from 0x3C instead of 0x3F, and
// AlarmDstEndpoint must name this device's LocalAlarmReference (p-net's
// was 0, as the CPU's). Nothing else is changed.
func cpuRTA(t *testing.T, rel string, devRef uint16) []byte {
	t.Helper()
	f := append([]byte(nil), capture.MustLoad(rel)...)
	if !bytes.Equal(f[6:12], []byte{0xec, 0x1c, 0x5d, 0x61, 0xe7, 0x3c}) || !bytes.Equal(f[0:6], recDevMAC[:]) {
		t.Fatalf("%s: unexpected addresses % x", rel, f[:12])
	}
	copy(f[6:12], recCPUMAC[:])
	binary.BigEndian.PutUint16(f[20:], devRef) // AlarmDstEndpoint, after the VLAN tag and FrameID
	return f
}

// compareRTA compares the RTA header fields that do not depend on the
// endpoint references.
func compareRTA(t *testing.T, what string, got, want []byte) (alarm.PDU, alarm.PDU) {
	t.Helper()
	ge, gp := decodeRTA(t, got)
	we, wp := decodeRTA(t, want)
	if ge.Tagged != we.Tagged || ge.VLAN != we.VLAN {
		t.Errorf("%s: VLAN %v %+v, recorded %v %+v", what, ge.Tagged, ge.VLAN, we.Tagged, we.VLAN)
	}
	if gp.FrameID != wp.FrameID || gp.Type != wp.Type || gp.Version != wp.Version || gp.AddFlags != wp.AddFlags || gp.SendSeq != wp.SendSeq || gp.AckSeq != wp.AckSeq || gp.Dst != wp.Dst {
		t.Errorf("%s: frame/type/version/flags/send/ack/dst %#04x/%v/%d/%#02x/%#04x/%#04x/%d, recorded %#04x/%v/%d/%#02x/%#04x/%#04x/%d", what,
			gp.FrameID, gp.Type, gp.Version, gp.AddFlags, gp.SendSeq, gp.AckSeq, gp.Dst, wp.FrameID, wp.Type, wp.Version, wp.AddFlags, wp.SendSeq, wp.AckSeq, wp.Dst)
	}
	return gp, wp
}

// TestCaptureReplayAlarms raises the process alarm and the channel
// diagnosis p-net raised in the recording, answers them with the CPU's
// recorded ACK-RTA and AlarmAck, and ends the AR with the CPU's ERR-RTA
// "AR removed" (sent at a TIA download).
func TestCaptureReplayAlarms(t *testing.T) {
	r := replayStartup(t)
	r.cpu.keepCyclic()
	ref := r.res.AlarmCR.LocalAlarmReference

	// Process alarm, high priority, slot 1/1, USI 0x0010 with one octet
	// (frames 50893..50896).
	done := make(chan error, 1)
	go func() {
		done <- r.d.SendAlarm(context.Background(), Alarm{High: true, Type: pnio.AlarmProcess, Slot: 1, Subslot: 1, HasItem: true, USI: 0x0010, Data: []byte{0x01}})
	}()
	got := r.cpu.next("process alarm", alarmFrom(pnio.FrameIDAlarmHigh, alarm.PDUData))
	want := capture.MustLoad("pnio-rs/s7-1500_p-net_alarm-high-process.hex")
	gp, wp := compareRTA(t, "AlarmNotification", got, want)
	if !bytes.Equal(gp.VarPart, wp.VarPart) {
		t.Errorf("AlarmNotification\n got % x\nwant % x", gp.VarPart, wp.VarPart)
	}
	r.cpu.send(cpuRTA(t, "pnio-rs/s7-1500_p-net_alarm-high-ack-rta-cpu.hex", ref))
	r.cpu.send(cpuRTA(t, "pnio-rs/s7-1500_p-net_alarm-high-process-ack.hex", ref))
	got = r.cpu.next("ACK-RTA of the AlarmAck", alarmFrom(pnio.FrameIDAlarmHigh, alarm.PDUAck))
	compareRTA(t, "ACK-RTA", got, capture.MustLoad("pnio-rs/s7-1500_p-net_alarm-high-ack-rta-device.hex"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("process alarm: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("process alarm not acknowledged")
	}
	r.h.mu.Lock()
	acks := append([]pnio.AlarmAck(nil), r.h.acks...)
	r.h.mu.Unlock()
	_ = acks

	// Channel diagnosis, low priority, slot 1/1, channel 4, input,
	// appears, fault, error type 1 (frames 52813..52816).
	go func() {
		done <- r.d.AddDiagnosis(context.Background(), 1, 1, pnio.DiagnosisItem{ChannelNumber: 4, ChannelProperties: 0x2801, ErrorType: 0x0001})
	}()
	got = r.cpu.next("diagnosis alarm", alarmFrom(pnio.FrameIDAlarmLow, alarm.PDUData))
	want = capture.MustLoad("pnio-rs/s7-1500_p-net_alarm-low-diagnosis-appears.hex")
	gp, wp = compareRTA(t, "diagnosis AlarmNotification", got, want)
	compareNotification(t, gp.VarPart, wp.VarPart)
	r.cpu.send(cpuRTA(t, "pnio-rs/s7-1500_p-net_alarm-low-ack-rta-cpu.hex", ref))
	r.cpu.send(cpuRTA(t, "pnio-rs/s7-1500_p-net_alarm-low-diagnosis-ack.hex", ref))
	got = r.cpu.next("ACK-RTA of the diagnosis AlarmAck", alarmFrom(pnio.FrameIDAlarmLow, alarm.PDUAck))
	compareRTA(t, "low ACK-RTA", got, capture.MustLoad("pnio-rs/s7-1500_p-net_alarm-low-ack-rta-device.hex"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("diagnosis alarm: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("diagnosis alarm not acknowledged")
	}

	// The CPU's ERR-RTA "AR removed" (CF 81 FD 11). Its sequence numbers
	// are those of another session (FFFE/FFFE): an ERR-RTA is taken
	// whatever they are. p-net answered with its own ERR-RTA.
	r.cpu.send(cpuRTA(t, "pnio-rs/s7-1500_p-net_alarm-err-rta-cpu-ar-removed.hex", ref))
	got = r.cpu.next("ERR-RTA answer", alarmFrom(pnio.FrameIDAlarmLow, alarm.PDUErr))
	// p-net echoed the CPU's status (CF 81 FD 11); this device answers
	// "AR alarm.ind(err)" (CF 81 FD 0B), as the CPU itself answers a
	// device's ERR-RTA (s7-1500_p-net_alarm-err-rta-cpu-reply.hex). Its sequence numbers are
	// the low channel's counters, as in the CPU's ERR-RTA: the device's
	// one DATA (the diagnosis, 0xFFFF) and the CPU's one DATA accepted
	// (its AlarmAck, 0xFFFF).
	_, gp = decodeRTA(t, got)
	_, cp := decodeRTA(t, capture.MustLoad("pnio-rs/s7-1500_p-net_alarm-err-rta-cpu-reply.hex"))
	if !bytes.Equal(gp.VarPart, cp.VarPart[:4]) || gp.Dst != 0 || gp.SendSeq != 0xFFFF || gp.AckSeq != 0xFFFF {
		t.Errorf("ERR-RTA answer dst %d send/ack %#04x/%#04x status % x, want dst 0, FFFF/FFFF, % x", gp.Dst, gp.SendSeq, gp.AckSeq, gp.VarPart, cp.VarPart[:4])
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(r.d.ARs()) != 0 && r.d.ARs()[0].State != ARStateClosed {
		if time.Now().After(deadline) {
			t.Fatalf("AR after the CPU's ERR-RTA: %+v", r.d.ARs())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// compareNotification compares two AlarmNotification blocks field by
// field, the AlarmSpecifier included: its sequence number counts the
// alarms of one priority, so the first low-priority alarm has 0 although
// a high-priority one went before it.
func compareNotification(t *testing.T, got, want []byte) {
	t.Helper()
	gb, _, err := pnio.ReadBlock(got)
	if err != nil {
		t.Fatal(err)
	}
	wb, _, err := pnio.ReadBlock(want)
	if err != nil {
		t.Fatal(err)
	}
	g, err := pnio.DecodeAlarmNotification(gb)
	if err != nil {
		t.Fatal(err)
	}
	w, err := pnio.DecodeAlarmNotification(wb)
	if err != nil {
		t.Fatal(err)
	}
	if gb.Header.Type != wb.Header.Type || g.Type != w.Type || g.API != w.API || g.Slot != w.Slot || g.Subslot != w.Subslot ||
		g.ModuleIdentNumber != w.ModuleIdentNumber || g.SubmoduleIdentNumber != w.SubmoduleIdentNumber || g.HasItem != w.HasItem || g.Specifier != w.Specifier {
		t.Errorf("AlarmNotification\n got %+v\nwant %+v", g, w)
	}
	// Legitimate difference: p-net reports every channel diagnosis as
	// ExtChannelDiagnosis (USI 0x8002) with ExtChannelErrorType and
	// ExtChannelAddValue 0; this device sends ChannelDiagnosis (USI 0x8000)
	// when they are 0. The channel, its properties and the error type agree.
	gi, err := pnio.DecodeDiagnosisItems(g.USI, g.Data)
	if err != nil {
		t.Fatal(err)
	}
	wi, err := pnio.DecodeDiagnosisItems(w.USI, w.Data)
	if err != nil {
		t.Fatal(err)
	}
	if w.USI != pnio.USIExtChannelDiagnosis || g.USI != pnio.USIChannelDiagnosis || len(gi) != 1 || len(wi) != 1 || gi[0] != wi[0] {
		t.Errorf("diagnosis USI %#04x items %+v, recorded USI %#04x items %+v", g.USI, gi, w.USI, wi)
	}
}

// TestCaptureReplayDCP sends the CPU's DCP Identify (bench 1, name filter
// "i-device") and DCP Set IP (bench 2) to the device and compares its
// answers with those of the recorded devices (PLCSIM Advanced and p-net,
// both software).
func TestCaptureReplayDCP(t *testing.T) {
	d, cpu := replayDevice(t, NopHandler{})
	isDCP := func(id uint16) func([]byte) bool {
		return func(f []byte) bool {
			e, err := ethernet.Decode(f)
			return err == nil && e.EtherType == ethernet.EtherTypeProfinet && len(e.Payload) >= 2 && binary.BigEndian.Uint16(e.Payload) == id
		}
	}

	// Set IP: the CPU assigns 172.16.2.10/24, gateway 172.16.2.10,
	// temporary (BlockQualifier 0), in a frame tagged with VID 0.
	cpu.send(capture.MustLoad("pnio-rs/s7-1500_p-net_dcp-set-ip-req.hex"))
	got := cpu.next("Set response", isDCP(dcp.FrameIDGetSet))
	ge, _ := ethernet.Decode(got)
	we, _ := ethernet.Decode(capture.MustLoad("pnio-rs/s7-1500_p-net_dcp-set-ip-res.hex"))
	gf, err := dcp.Decode(ge.Payload)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := dcp.Decode(we.Payload)
	if err != nil {
		t.Fatal(err)
	}
	// The response is untagged, as p-net's (the request's VID 0 only
	// carries a priority).
	gb, _ := gf.AppendTo(nil)
	wb, _ := wf.AppendTo(nil)
	if ge.Tagged != we.Tagged || !bytes.Equal(gb, wb) {
		t.Errorf("Set response tagged %v\n got % x\nwant % x", ge.Tagged, gb, wb)
	}
	if s := d.IP(); s.Addr != recDevIP || s.Gateway != recDevIP {
		t.Errorf("IP after the CPU's Set: %v", s)
	}

	// The tool's Set NameOfStation "%n%n%n " with End-Transaction, which
	// the recorded S7-300 accepted (unknown_s7-300ec_dcp-set-name.pcap
	// frame 4: both blocks error 0). Intentional difference: this device refuses a name
	// that is no valid NameOfStation and keeps its own.
	hostile := append([]byte(nil), capture.MustLoad("ics-pcaps/unknown_s7-300ec_dcp-set-name.pcap#3")...)
	copy(hostile[0:6], recDevMAC[:])
	copy(hostile[6:12], recCPUMAC[:])
	cpu.send(hostile)
	got = cpu.next("Set name response", isDCP(dcp.FrameIDGetSet))
	ge, _ = ethernet.Decode(got)
	gf, err = dcp.Decode(ge.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var errs []dcp.BlockError
	for _, b := range gf.Blocks {
		r, err := dcp.DecodeControlResponse(b.Data)
		if err != nil {
			t.Fatal(err)
		}
		errs = append(errs, r.Error)
	}
	if len(errs) != 2 || errs[0] == dcp.BlockErrorNone || d.StationName() != "rt-labs-dev" {
		t.Errorf("hostile Set name: errors %v, name %q", errs, d.StationName())
	}

	// Identify by name: rename the device to the bench-1 name first.
	d2, cpu2 := replayDeviceNamed(t, "i-device")
	_ = d2
	cpu2.send(capture.MustLoad("pnio-rs/s7-1500_plcsim_dcp-identify-req.hex"))
	got = cpu2.next("Identify response", isDCP(dcp.FrameIDIdentifyResponse))
	ge, _ = ethernet.Decode(got)
	we, _ = ethernet.Decode(capture.MustLoad("pnio-rs/s7-1500_plcsim_dcp-identify-res.hex"))
	gf, err = dcp.Decode(ge.Payload)
	if err != nil {
		t.Fatal(err)
	}
	wf, err = dcp.Decode(we.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if ge.Dst != recCPUMAC || gf.Xid != wf.Xid || gf.Service != wf.Service || gf.Type != wf.Type {
		t.Errorf("Identify response to %v xid %#x %v/%v, recorded to %v xid %#x %v/%v", ge.Dst, gf.Xid, gf.Service, gf.Type, we.Dst, wf.Xid, wf.Service, wf.Type)
	}
	gid, err := dcp.ParseIdentity(ge.Src, gf.Blocks)
	if err != nil {
		t.Fatal(err)
	}
	if gid.NameOfStation != "i-device" || gid.VendorID != 0x0493 || gid.DeviceID != 0x0002 || gid.Role != dcp.RoleIODevice {
		t.Errorf("identity %+v", gid)
	}
	// Both answer the blocks PLCSIM answered (other identity values).
	have := map[dcp.BlockID]bool{}
	for _, b := range gf.Blocks {
		have[b.ID] = true
	}
	for _, b := range wf.Blocks {
		if !have[b.ID] {
			t.Errorf("Identify response lacks block %s that PLCSIM sent", b.ID)
		}
	}
}

// replayDeviceNamed is replayDevice with another station name.
func replayDeviceNamed(t *testing.T, name string) (*Device, *recordedCPU) {
	t.Helper()
	n := vnet.New(2)
	t.Cleanup(func() { _ = n.Close() })
	ep, err := n.NewEndpoint(vnet.EndpointConfig{Name: "dev", MAC: recDevMAC})
	if err != nil {
		t.Fatal(err)
	}
	mux := l2.NewMux(ep)
	t.Cleanup(func() { _ = mux.Close(); _ = ep.Close() })
	cpu := newRecordedCPU(t, n)
	d, err := New(Config{Model: pnetModel(), Mux: mux, StationName: name, NoResponseDelay: true, DisableLLDP: true, Handler: NopHandler{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, cpu
}

// TestCaptureReplayDCPRequests sends every recorded DCP Identify request
// of the corpus (the SCALANCE switches' name checks, two Identify All) to
// a device named "switch3": it answers Identify All and the two requests
// for its own name, with their Xid, and no other.
func TestCaptureReplayDCPRequests(t *testing.T) {
	_, cpu := replayDeviceNamed(t, "switch3")
	var reqs [][]byte
	for _, p := range []string{"ics-pcaps/scalance_unknown_dcp-identify.pcap", "ics-pcaps/unknown_s7-300ec_dcp-set-name.pcap", "wireshark/unknown_scalance-x208_dcp-set-ip.pcap"} {
		fr, err := capture.ReadPcap(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range fr {
			e, err := ethernet.Decode(f)
			if err != nil || e.EtherType != ethernet.EtherTypeProfinet {
				continue
			}
			if d, err := dcp.Decode(e.Payload); err == nil && d.Service == dcp.ServiceIdentify && d.Type == dcp.TypeRequest {
				// From the CPU's port, so that the answer reaches it; the
				// recorded destination (multicast or the device) is kept
				// unless it named another device.
				b := append([]byte(nil), f...)
				if b[0]&1 == 0 {
					copy(b[0:6], recDevMAC[:])
				}
				copy(b[6:12], recCPUMAC[:])
				reqs = append(reqs, b)
			}
		}
	}
	if len(reqs) != 16 {
		t.Fatalf("%d recorded Identify requests, tshark 16", len(reqs))
	}
	want := map[uint32]bool{0x0300BF9A: true, 0x0300BF9B: true, 0x01000001: true}
	for _, r := range reqs {
		cpu.send(r)
	}
	got := map[uint32]int{}
	deadline := time.After(2 * time.Second)
	for done := false; !done; {
		select {
		case f := <-cpu.frames:
			e, err := ethernet.Decode(f)
			if err != nil || e.EtherType != ethernet.EtherTypeProfinet {
				continue
			}
			if d, err := dcp.Decode(e.Payload); err == nil && d.Service == dcp.ServiceIdentify && d.Type == dcp.TypeResponseSuccess {
				got[d.Xid]++
			}
		case <-deadline:
			done = true
		}
	}
	for x := range want {
		if got[x] == 0 {
			t.Errorf("no answer to Xid %#08x", x)
		}
	}
	for x := range got {
		if !want[x] {
			t.Errorf("answered Xid %#08x, a request for another name", x)
		}
	}
	// The two Identify All requests share Xid 0x01000001.
	if got[0x01000001] != 2 {
		t.Errorf("%d answers to the two Identify All requests", got[0x01000001])
	}
}

// TestCaptureReplayOtherControllers sends the Connect request of a 2009
// Siemens controller and the Read Implicit of a tool (to a GE device) to
// the device and classifies its answers against the recording.
func TestCaptureReplayOtherControllers(t *testing.T) {
	_, cpu := replayDevice(t, NopHandler{})
	// The recorded addresses are those of other networks: move the
	// requests to the device's and the CPU's (MAC, IP, IP checksum).
	move := func(ref string) []byte {
		u, err := capture.DecodeUDP(capture.MustLoad(ref))
		if err != nil {
			t.Fatal(err)
		}
		return udpFrame(recDevMAC, recCPUMAC, netip.AddrPortFrom(recCPUIP, u.Src.Port()), netip.AddrPortFrom(recDevIP, u.Dst.Port()), u.Payload)
	}

	// Read Implicit I&M0FilterData: the GE device answered with a
	// submodule, a module (slot 1 carries its own I&M0) and a device
	// block. Legitimate difference: this device's only I&M0 carrier is the
	// DAP, so it sends no I&M0FilterDataModule, which IEC 61158-6-10 makes
	// optional; the other two are the same block types.
	cpu.send(move("ics-pcaps/unknown_ge-ip-device_read-implicit.pcap#1"))
	got := cpu.next("Read Implicit response", rpcFrom(rpc.TypeResponse))
	_, _, gst, _, gb := decodeRPC(t, got)
	_, _, wst, _, wb := decodeRPC(t, capture.MustLoad("ics-pcaps/unknown_ge-ip-device_read-implicit.pcap#2"))
	types := func(b []byte) []pnio.BlockType {
		blks, err := pnio.SplitBlocks(b)
		if err != nil {
			t.Fatal(err)
		}
		var ts []pnio.BlockType
		for _, x := range blks {
			if len(ts) == 0 || ts[len(ts)-1] != x.Header.Type {
				ts = append(ts, x.Header.Type)
			}
		}
		return ts
	}
	if gst != wst || fmt.Sprint(types(gb)) != "[IODReadResHeader Block(0x0030) Block(0x0032)]" || fmt.Sprint(types(wb)) != "[IODReadResHeader Block(0x0030) Block(0x0031) Block(0x0032)]" {
		t.Errorf("Read Implicit I&M0FilterData: %v blocks %v, GE device %v %v", gst, types(gb), wst, types(wb))
	}

	// Connect of the 2009 Siemens controller, for another device (DAP
	// 0x00020005, modules in the profile API 0x3A00). Accepted with a
	// ModuleDiffBlock: the DAP's module wrong, the API 0x3A00 modules
	// missing. What the recorded device answered is not in the file.
	cpu.send(move("ics-pcaps/s7-300_unknown_connect-req.pcap#1"))
	got = cpu.next("Connect response", rpcFrom(rpc.TypeResponse))
	_, _, gst, _, gb = decodeRPC(t, got)
	if gst != (pnio.Status{}) {
		t.Fatalf("Connect refused: %v", gst)
	}
	res, err := pnio.DecodeConnectResponse(gb)
	if err != nil {
		t.Fatal(err)
	}
	if res.ModuleDiff == nil {
		t.Fatal("no ModuleDiffBlock for a configuration of another device")
	}
	states := map[uint32][]uint16{}
	for _, a := range res.ModuleDiff.APIs {
		for _, m := range a.Modules {
			states[a.API] = append(states[a.API], m.State)
		}
	}
	if len(states[0x3A00]) != 2 || states[0x3A00][0] != pnio.ModuleStateNoModule || len(states[0]) != 1 || states[0][0] != pnio.ModuleStateWrong {
		t.Errorf("ModuleDiff module states %v", states)
	}
	if len(res.IOCRs) != 2 || res.IOCRs[1].FrameID == pnio.FrameIDAssign {
		t.Errorf("IOCRBlockRes %+v: the output FrameID the controller left to the device", res.IOCRs)
	}
}
