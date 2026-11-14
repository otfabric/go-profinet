// SPDX-License-Identifier: MIT

package device

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/dcp"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/mrp"
	"github.com/otfabric/go-profinet/pnio"
	"github.com/otfabric/go-profinet/rpc"
	"github.com/otfabric/go-profinet/store"
)

// liveARs counts the ARs that reached Parameterizing and have not closed.
type liveARs struct {
	NopHandler
	live, peak atomic.Int32
}

func (h *liveARs) StateChanged(_ ARInfo, from, to ARState) {
	if to == ARStateParameterizing && from != ARStateData {
		if n := h.live.Add(1); n > h.peak.Load() {
			h.peak.Store(n)
		}
	}
	if to == ARStateClosed {
		h.live.Add(-1)
	}
}

// Concurrent Connects with one ARUUID: one is accepted, the AR count stays
// within MaxARs.
func TestConcurrentConnectsOneARUUID(t *testing.T) {
	n := newNet(t)
	h := &liveARs{}
	d, _ := n.device(h)
	p1 := n.peer(d, "192.168.0.100", false)
	p2 := n.peer(d, "192.168.0.101", false)
	req := p1.ioCAR()
	for range 10 {
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, p := range []*peer{p1, p2} {
			wg.Go(func() {
				<-start
				_, _ = p.connect(req)
			})
		}
		close(start)
		wg.Wait()
	}
	time.Sleep(50 * time.Millisecond)
	if peak, maxARs := int(h.peak.Load()), d.model.Capabilities.MaxARs; peak > maxARs {
		t.Fatalf("%d ARs at once, MaxARs %d", peak, maxARs)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.connecting) != 0 {
		t.Fatalf("%d Connects still marked in progress", len(d.connecting))
	}
}

func mrpEntities() int {
	buf := make([]byte, 1<<22)
	n := runtime.Stack(buf, true)
	return strings.Count(string(buf[:n]), "mrp.(*Entity).run(")
}

// Concurrent MRP role changes leave one entity, and none after Close.
func TestConcurrentMRPRoleWrites(t *testing.T) {
	base := mrpEntities()
	d, n := mrpRingDevice(t, &MRPConfig{Role: mrp.RoleClient, RingPorts: [2]int{1, 3}})
	x := newPDevPeer(t, n, d)
	y := &pdevPeer{t: t, p: n.peer(d, "192.168.0.3", false), ar: x.ar}
	adj := func(role uint16) []byte {
		a := pnio.PDInterfaceMrpDataAdjust{Domain: mrp.DefaultDomain, Role: role, DomainName: "mrpdomain-1"}
		return a.Append(nil)
	}
	for i := range 20 {
		r1, r2 := uint16(pnio.MrpRoleClient), uint16(pnio.MrpRoleManager)
		if i%2 == 1 {
			r1, r2 = r2, r1
		}
		var wg sync.WaitGroup
		wg.Go(func() { _ = x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust, adj(r1)) })
		wg.Go(func() { _ = y.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust, adj(r2)) })
		wg.Wait()
	}
	// Other tests of the package may run MRP entities too: the count is
	// bounded, not exact.
	if got := mrpEntities() - base; got > 1 {
		t.Fatalf("%d MRP entities running, want at most 1", got)
	}
	d.mu.Lock()
	running := d.mrpSt.ent != nil
	d.mu.Unlock()
	if !running {
		t.Fatal("no MRP entity after the role changes")
	}
	_ = d.Close()
	eventually(t, 3*time.Second, "no MRP entity after Close", func() bool { return mrpEntities() <= base })
}

// A permanent DCP Set the application refuses is not stored either.
func TestRefusedPermanentSetNotStored(t *testing.T) {
	n := newNet(t)
	st := store.NewMemory()
	refuse := func(c *Config) {
		c.SetDHCP = func(dcp.DHCPParameter, bool) dcp.BlockError { return dcp.BlockErrorSetNotPossible }
		c.ApplyIP = func(dcp.IPSuite, bool) error { return errors.New("refused") }
	}
	d, _ := n.device(nil, withStoreCfg(st, "unit"), refuse)
	s := station{d}
	if c := s.SetDHCP(dcp.DHCPControlParameter(dcp.DHCPControlOn), true); c != dcp.BlockErrorSetNotPossible {
		t.Fatalf("SetDHCP: %v", c)
	}
	if c := s.SetIP(suite("192.168.0.99"), true); c != dcp.BlockErrorSetNotPossible {
		t.Fatalf("SetIP: %v", c)
	}
	p := stored(t, st, "unit")
	if len(p.DHCP) != 0 || p.IP != nil {
		t.Fatalf("refused values stored: DHCP %v, IP %+v", p.DHCP, p.IP)
	}
}

// A Connect still running when Close begins leaves no AR and no callback
// after Close returns.
func TestCloseDuringConnect(t *testing.T) {
	n := newNet(t)
	h := newRecHandler()
	d, _ := n.device(h)
	p := n.peer(d, "192.168.0.2", true)
	req := p.ioCAR()
	req.AR.ActivityTimeoutFactor = 3
	u := req.AR.ARUUID
	// imgMu stops the Connect in claimOwnership, after its closed check.
	d.imgMu.Lock()
	go func() { _, _ = p.connect(req) }()
	eventually(t, 3*time.Second, "connect reserved", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.reserved > 0
	})
	closed := make(chan struct{})
	go func() { _ = d.Close(); close(closed) }()
	eventually(t, 3*time.Second, "close started", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.closed
	})
	d.imgMu.Unlock()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}
	d.mu.Lock()
	nARs := len(d.ars)
	d.mu.Unlock()
	if nARs != 0 {
		t.Fatalf("%d ARs after Close", nARs)
	}
	before := len(h.states(u))
	time.Sleep(500 * time.Millisecond)
	if after := len(h.states(u)); after != before {
		t.Fatalf("state changes after Close: %v", h.states(u))
	}
}

type appReadySeen struct {
	act  rpc.UUID
	seq  uint32
	from net.Addr
}

func appReadyFaultRun(t *testing.T, mode string) {
	n := newNet(t)
	h := newRecHandler()
	d, _ := n.device(h)

	pa := n.peer(d, "192.168.0.20", false)
	var mu sync.Mutex
	var seen []appReadySeen
	sc, err := pa.stack.ListenPacket(context.Background(), "udp4", ":34964")
	if err != nil {
		t.Fatal(err)
	}
	srv := rpc.NewServer(sc, true, rpc.ServerOptions{})
	srv.Register(pnio.ControllerInterface, pnio.InterfaceVersion, rpc.HandlerFunc(func(ctx context.Context, req *rpc.Request) ([]byte, error) {
		mu.Lock()
		seen = append(seen, appReadySeen{req.Header.Activity, req.Header.SeqNum, req.Addr})
		mu.Unlock()
		return pa.serve(ctx, req)
	}))
	t.Cleanup(func() { _ = srv.Close() })
	raw, err := pa.stack.ListenUDP(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })

	for i := 0; i < 2; i++ {
		req := pa.ioCAR()
		pa.establish(req)
		if err := pa.release(req); err != nil {
			t.Fatal(err)
		}
		waitAR(t, d, req.AR.ARUUID, ARStateClosed)
	}
	mu.Lock()
	s := append([]appReadySeen(nil), seen...)
	mu.Unlock()
	if len(s) != 2 {
		t.Fatalf("A saw %d ApplicationReady calls", len(s))
	}

	pb := n.peer(d, "192.168.0.30", true)
	got := make(chan struct{}, 4)
	pb.setOnReady(func(cb pnio.ControlBlock) []byte {
		got <- struct{}{}
		time.Sleep(300 * time.Millisecond)
		return nil
	})
	reqB := pb.ioCAR()
	if _, err := pb.connect(reqB); err != nil {
		t.Fatal(err)
	}
	if err := pb.prmEnd(reqB); err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("B got no ApplicationReady")
	}
	send := func(act rpc.UUID, seq uint32, to net.Addr) {
		hd := rpc.Header{Type: rpc.TypeFault, DRep: [3]byte{rpc.DRepLittleEndian}, Activity: act, SeqNum: seq,
			IfaceHint: 0xFFFF, ActivityHint: 0xFFFF, BodyLen: 4}
		b := binary.LittleEndian.AppendUint32(hd.AppendTo(nil), rpc.StatusFaultUnspec)
		if _, err := raw.WriteTo(b, to); err != nil {
			t.Fatal(err)
		}
	}
	switch mode {
	case "learned":
		for _, e := range s {
			send(e.act, e.seq+1, e.from)
		}
	case "random":
		for _, e := range s {
			send(rpc.NewRandomUUID(), e.seq+1, e.from)
		}
	}
	time.Sleep(time.Second)
	st, ok := arState(d, reqB.AR.ARUUID)
	r, closed := h.reason(reqB.AR.ARUUID)
	switch mode {
	default:
		if !ok || st != ARStateData || closed {
			t.Fatalf("mode %s: B's AR in %v (present %v, closed %v, reason %v), want Data", mode, st, ok, closed, r)
		}
	}
}

// A controller's FAULT on the activities of its own ApplicationReady calls
// does not end another controller's AR.
func TestApplicationReadyFaultFromOtherController(t *testing.T) {
	for _, mode := range []string{"learned", "random", "none"} {
		t.Run(mode, func(t *testing.T) { appReadyFaultRun(t, mode) })
	}
}
