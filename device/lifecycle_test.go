// SPDX-License-Identifier: MIT

package device

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/otfabric/go-profinet/controller"
	"github.com/otfabric/go-profinet/dcp"
	"github.com/otfabric/go-profinet/internal/leakcheck"
	"github.com/otfabric/go-profinet/pnio"
)

// sockets is a ListenPacket that fails its failAt-th call (0: never) and
// knows which of the sockets it opened are closed.
type sockets struct {
	listen func(ctx context.Context, network, address string) (net.PacketConn, error)
	failAt int32
	calls  atomic.Int32
	mu     sync.Mutex
	open   map[*trackedConn]bool
}

type trackedConn struct {
	net.PacketConn
	s *sockets
}

func (c *trackedConn) Close() error {
	c.s.mu.Lock()
	delete(c.s.open, c)
	c.s.mu.Unlock()
	return c.PacketConn.Close()
}

func (s *sockets) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	if n := s.calls.Add(1); n == s.failAt {
		return nil, errors.New("no socket")
	}
	c, err := s.listen(ctx, network, address)
	if err != nil {
		return nil, err
	}
	tc := &trackedConn{PacketConn: c, s: s}
	s.mu.Lock()
	if s.open == nil {
		s.open = map[*trackedConn]bool{}
	}
	s.open[tc] = true
	s.mu.Unlock()
	return tc, nil
}

func (s *sockets) left() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.open)
}

// TestNewFailureReleases: a device that cannot open one of its sockets
// (RPC client, RPC server, SNMP) fails and keeps nothing: the sockets it
// had opened are closed and no goroutine is left (contract row L14).
func TestNewFailureReleases(t *testing.T) {
	n := newNet(t)
	nd := n.node(devIP)
	config := func(s *sockets) Config {
		return Config{Model: testModel(), Mux: nd.mux, StationName: "dut", IP: suite(devIP), ListenPacket: s.ListenPacket,
			NoResponseDelay: true, SNMP: &SNMPConfig{Community: "public"}}
	}
	// How many sockets a device opens.
	all := &sockets{listen: nd.stack.ListenPacket}
	d, err := New(config(all))
	if err != nil {
		t.Fatal(err)
	}
	total := all.calls.Load()
	if err := d.Close(); err != nil || all.left() != 0 {
		t.Fatalf("Close: %v, %d sockets open", err, all.left())
	}
	if total != 3 {
		t.Fatalf("%d sockets opened, want the RPC client's, the RPC server's and the SNMP agent's", total)
	}
	for failAt := int32(1); failAt <= total; failAt++ {
		check := leakcheck.Check(t)
		s := &sockets{listen: nd.stack.ListenPacket, failAt: failAt}
		if d, err := New(config(s)); err == nil {
			_ = d.Close()
			t.Fatalf("socket %d of %d failed and New succeeded", failAt, total)
		}
		if s.left() != 0 {
			t.Errorf("socket %d of %d failed: %d sockets left open", failAt, total, s.left())
		}
		check()
	}
	// The ports are free again: a device starts.
	again, err := New(config(&sockets{listen: nd.stack.ListenPacket}))
	if err != nil {
		t.Fatalf("New after the failures: %v", err)
	}
	_ = again.Close()
}

// TestCloseConcurrent: Close of a device with an AR from several
// goroutines at once (contract row L2): all return and the AR is gone.
func TestCloseConcurrent(t *testing.T) {
	n := newNet(t)
	h := newRecHandler()
	d, _ := n.device(h)
	p := n.peer(d, "192.168.0.2", true)
	req := p.ioCAR()
	p.establish(req)
	leakcheck.Together(8, func(int) {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if ars := d.ARs(); len(ars) != 0 {
		t.Fatalf("%d ARs after Close", len(ars))
	}
	h.waitReason(t, req.AR.ARUUID, pnio.AbortDeactivated)
	if _, err := p.connect(p.ioCAR()); err == nil {
		t.Fatal("a Connect was accepted after Close")
	}
}

// usingHandler is a Handler whose methods use the device they are called
// by (contract row L9).
type usingHandler struct {
	*recHandler
	d     atomic.Pointer[Device]
	calls atomic.Int64
}

func (h *usingHandler) use() {
	d := h.d.Load()
	if d == nil {
		return
	}
	_ = d.ARs()
	_ = d.StationName()
	_ = d.IP()
	_ = d.SetInput(1, 1, []byte{1, 2, 3, 4, 5, 6, 7, 8}, pnio.IOxSGood)
	_, _, _ = d.Output(2, 1)
	h.calls.Add(1)
}

func (h *usingHandler) StateChanged(ar ARInfo, from, to ARState) {
	h.use()
	h.recHandler.StateChanged(ar, from, to)
}

func (h *usingHandler) PrmEnd(ctx context.Context, ar ARInfo) error {
	h.use()
	return h.recHandler.PrmEnd(ctx, ar)
}

func (h *usingHandler) ReadRecord(ctx context.Context, r RecordRequest) ([]byte, error) {
	h.use()
	return h.recHandler.ReadRecord(ctx, r)
}

func (h *usingHandler) NameChanged(name string, permanent bool) {
	h.use()
	h.recHandler.NameChanged(name, permanent)
}

// TestHandlerMayUseTheDevice: the methods of a Handler may call the
// methods of the Device that read or set its data.
func TestHandlerMayUseTheDevice(t *testing.T) {
	n := newNet(t)
	h := &usingHandler{recHandler: newRecHandler()}
	d, _ := n.device(h)
	h.d.Store(d)
	p := n.peer(d, "192.168.0.2", true)
	req := p.ioCAR()
	p.establish(req)
	// Parameterizing, PrmEnd, the states up to Data.
	if h.calls.Load() < 3 {
		t.Fatalf("%d handler calls returned", h.calls.Load())
	}
	before := h.calls.Load()
	// A user record the model does not have is the handler's to answer.
	_, _ = p.rc.Read(context.Background(), req.AR.ARUUID, controller.Record{Slot: 1, Subslot: 1, Index: 0x7000}, 0)
	if c := (station{d}).SetName("renamed", false); c != dcp.BlockErrorNone {
		t.Fatalf("SetName: %v", c)
	}
	if h.calls.Load() <= before {
		t.Fatal("the handler was not called for the record and the name")
	}
	if d.StationName() != "renamed" {
		t.Fatalf("station name %q", d.StationName())
	}
}
