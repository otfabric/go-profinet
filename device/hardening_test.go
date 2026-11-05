// SPDX-License-Identifier: MIT

package device

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/pnio"
	"github.com/otfabric/go-profinet/rpc"
)

// An RPCPort or ControllerRPCPort outside 0..65535 used to be accepted
// (by a ListenPacket that does not parse the port itself) and the RPCPort
// truncated into the ARRPCBlockRes of every Connect response.
func TestNewRejectsPortsOutOfRange(t *testing.T) {
	n := newNet(t)
	nd := n.node("192.168.0.53")
	lp := func(ctx context.Context, network, _ string) (net.PacketConn, error) {
		return nd.stack.ListenPacket(ctx, network, ":0")
	}
	for _, c := range []Config{
		{RPCPort: -1}, {RPCPort: math.MaxUint16 + 1}, {RPCPort: math.MaxUint16 + 1 + rpc.Port},
		{ControllerRPCPort: -1}, {ControllerRPCPort: math.MaxUint16 + 1},
	} {
		c.Model, c.Mux, c.ListenPacket, c.DisableLLDP = testModel(), nd.mux, lp, true
		d, err := New(c)
		if !errors.Is(err, profinet.ErrInvalidConfig) {
			if d != nil {
				_ = d.Close()
			}
			t.Fatalf("RPCPort %d ControllerRPCPort %d: err %v", c.RPCPort, c.ControllerRPCPort, err)
		}
	}
}

func TestSNMPIndex(t *testing.T) {
	for _, c := range []struct {
		v  int64
		ok bool
	}{{1, true}, {math.MaxInt32, true}, {0, false}, {-1, false}, {math.MaxInt32 + 1, false}} {
		if v, ok := snmpIndex(c.v); ok != c.ok || ok && int64(v) != c.v {
			t.Errorf("%d: %d %v", c.v, v, ok)
		}
	}
	// A port without a valid index has no row (and is not touched).
	d := &Device{ports: []*port{{n: 0}}}
	if got := d.snmpInterfaces(); len(got) != 0 {
		t.Fatalf("ifTable %+v", got)
	}
	if got := d.snmpLLDPRemote(); len(got) != 0 {
		t.Fatalf("lldpRemTable %+v", got)
	}
}

func TestLen32(t *testing.T) {
	if n, ok := len32(10); !ok || n != 10 {
		t.Fatalf("10: %d %v", n, ok)
	}
	if _, ok := len32(-1); ok {
		t.Fatal("-1 accepted")
	}
	if strconv.IntSize == 64 {
		if _, ok := len32(int(^uint(0) >> 1)); ok {
			t.Fatal("MaxInt accepted")
		}
	}
}

// blockLenOK reports whether a block's BlockLength covers the rest of it.
func blockLenOK(b []byte) bool {
	return len(b) >= 6 && int(binary.BigEndian.Uint16(b[2:])) == len(b)-4
}

func TestFixedBlockLengths(t *testing.T) {
	n := newNet(t)
	d, _ := n.device(nil)
	b, err := d.readRecord(context.Background(), nil, pnio.ReadReq{Index: pnio.IndexLogBook})
	if err != nil || !blockLenOK(b) {
		t.Fatalf("logbook %x %v", b, err)
	}
	f := d.im0Filter()
	if len(f) != 56 || !blockLenOK(f[:28]) || !blockLenOK(f[28:]) || f[1] != 0x30 || f[29] != 0x32 {
		t.Fatalf("I&M0 filter %x", f)
	}
	if err := d.SetInput(1, 1, []byte{1, 2, 3, 4, 5, 6, 7, 8}, pnio.IOxSGood); err != nil {
		t.Fatal(err)
	}
	b, err = d.recordInput(nil, pnio.ReadReq{Slot: 1, Subslot: 1})
	if err != nil || !blockLenOK(b) || len(b) != 20 {
		t.Fatalf("record input %x %v", b, err)
	}
}

// Input data too long for a block is refused rather than given a
// truncated BlockLength (the model bounds it to 1439 octets, so only a
// broken invariant reaches this).
func TestRecordInputTooLong(t *testing.T) {
	d := &Device{inputs: map[key]*ioData{{1, 1}: {data: make([]byte, math.MaxUint16-7)}}}
	if _, err := d.recordInput(nil, pnio.ReadReq{Slot: 1, Subslot: 1}); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("err %v", err)
	}
	d.inputs[key{1, 1}].data = make([]byte, math.MaxUint16-8)
	if b, err := d.recordInput(nil, pnio.ReadReq{Slot: 1, Subslot: 1}); err != nil || !blockLenOK(b) {
		t.Fatalf("longest: %v", err)
	}
}

// goAbort runs abort in a goroutine Close waits for, and does nothing
// once Close has begun.
func TestGoAbortLifecycle(t *testing.T) {
	n := newNet(t)
	h := newRecHandler()
	d, _ := n.device(h)
	p := n.peer(d, "192.168.0.2", true)
	req := p.ioCAR()
	p.establish(req)
	d.mu.Lock()
	a := d.ars[req.AR.ARUUID]
	d.mu.Unlock()
	d.goAbort(a, pnio.AbortAlarmSend, true)
	_ = d.Close()
	// Close returned: the goroutine is done and the AR closed.
	a.mu.Lock()
	st := a.state
	a.mu.Unlock()
	if st != ARStateClosed {
		t.Fatalf("state %s after Close", st)
	}
	// After Close nothing is started (wg.Add would race with Wait).
	d.goAbort(a, pnio.AbortAlarmSend, true)
	d.wg.Wait()
}

func TestNewOversizeModel(t *testing.T) {
	n := newNet(t)
	nd := n.node("192.168.0.54")
	m := testModel()
	mod := model.Module{Name: "BIG", IdentNumber: 0x99}
	for i := range 11000 {
		mod.Submodules = append(mod.Submodules, model.Submodule{Name: "S", IdentNumber: 0x98, Subslot: uint16(i%0x7FFF) + 1})
	}
	m.Slots = append(m.Slots, model.Slot{Number: 9, Module: mod})
	if _, err := New(Config{Model: m, Mux: nd.mux, ListenPacket: nd.stack.ListenPacket, DisableLLDP: true}); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("11000 submodules: %v", err)
	}
	long := strings.Repeat("a", 241)
	_, err := New(Config{Model: testModel(), Mux: nd.mux, ListenPacket: nd.stack.ListenPacket, DisableLLDP: true, MRP: &MRPConfig{DomainName: long}})
	if !errors.Is(err, profinet.ErrInvalidConfig) || !strings.Contains(err.Error(), "domain name") {
		t.Fatalf("MRP domain name: %v", err)
	}
}

// Diagnoses the application keeps adding used to make the diagnosis
// record too large for its block: the encoder panicked inside the RPC
// server. The read is now answered with an error status.
func TestDiagnosisReadTooLarge(t *testing.T) {
	n := newNet(t)
	d, _ := n.device(nil)
	for i := range 12000 {
		if err := d.AddDiagnosis(context.Background(), 1, 1, pnio.DiagnosisItem{ChannelNumber: uint16(i), ErrorType: 1}); !errors.Is(err, profinet.ErrInvalidState) {
			t.Fatal(err)
		}
	}
	q := pnio.ReadReq{Slot: 1, Subslot: 1, Index: pnio.IndexDiagAllSubslot, Length: 0xFFFFFFFF}
	if _, st := d.read(context.Background(), true, q.Append(nil)); st.OK() {
		t.Fatal("oversize diagnosis record read OK")
	}
}

// Alarm data too long for the notification is refused with an error,
// not a panic.
func TestSendAlarmDataTooLarge(t *testing.T) {
	n := newNet(t)
	d, _ := n.device(newRecHandler())
	p := n.peer(d, "192.168.0.2", true)
	p.establish(p.ioCAR())
	err := d.SendAlarm(context.Background(), Alarm{Type: pnio.AlarmProcess, Slot: 1, Subslot: 1, HasItem: true, USI: 0x0001, Data: make([]byte, 1<<16)})
	if !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("err %v", err)
	}
}

func TestEncodeSafely(t *testing.T) {
	if _, err := encodeSafely(func() ([]byte, error) { panic(fmt.Errorf("x: %w", profinet.ErrInvalidConfig)) }); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("err %v", err)
	}
	for _, v := range []any{"bug", errors.New("other")} {
		func() {
			defer func() {
				if r := recover(); r != v {
					t.Errorf("recovered %v, want %v", r, v)
				}
			}()
			_, _ = encodeSafely(func() ([]byte, error) { panic(v) })
		}()
	}
}
