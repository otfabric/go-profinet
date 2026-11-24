// SPDX-License-Identifier: MIT

package device

import (
	"testing"
	"time"

	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/pnio"
)

// A port check, a port adjustment and the remote mismatch diagnosis they
// cause do not outlive the controller that wrote them: an IOCAR that takes
// the port starts without them, as in p-net v2.0.0 (pf_pdport_reset_ar),
// and its controller writes its own. Before, a stale check kept reporting
// a mismatch and a stale NoLLDP boundary kept LLDP off.
func TestPDevResetOnConnect(t *testing.T) {
	n := newNet(t)
	d, _ := n.device(nil)
	x := newPDevPeer(t, n, d)
	const port = model.SubslotPort1
	_ = lldpPeer(t, n, "sw", "port-003")
	eventually(t, 5*time.Second, "neighbor", func() bool { _, ok := d.ports[0].lldpA.Latest(); return ok })
	if err := x.write(port, pnio.IndexPDPortDataCheck, check(port, pnio.CheckPeer{PortID: "port-003", ChassisID: "plc"})); err != nil {
		t.Fatal(err)
	}
	waitMismatch(t, d, extPeerStationMismatch)
	adj := pnio.PDPortDataAdjust{Subslot: port, HasPeerToPeerBoundary: true, PeerToPeerBoundary: pnio.PeerToPeerNoLLDP}
	if err := x.write(port, pnio.IndexPDPortDataAdjust, adj.Append(nil)); err != nil {
		t.Fatal(err)
	}
	if d.ports[0].lldpA.Transmitting() {
		t.Fatal("LLDP sent despite the boundary")
	}
	// The supervisor's AR does not reset anything.
	if got := mismatch(d); len(got) != 1 {
		t.Fatalf("mismatch %#x", got)
	}
	// An IOCAR takes the DAP and its port.
	p := n.peer(d, "192.168.0.3", true)
	req := p.ioCAR()
	resp, err := p.connect(req)
	if err != nil {
		t.Fatal(err)
	}
	if got := mismatch(d); len(got) != 0 {
		t.Fatalf("stale mismatch %#x after the Connect", got)
	}
	// The ModuleDiffBlock of the Connect no longer reports the port.
	if resp.ModuleDiff != nil {
		for _, api := range resp.ModuleDiff.APIs {
			for _, m := range api.Modules {
				for _, sm := range m.Submodules {
					if m.Slot == 0 && sm.Subslot == port {
						t.Fatalf("port in the ModuleDiff %+v", sm)
					}
				}
			}
		}
	}
	if !d.ports[0].lldpA.Transmitting() {
		t.Fatal("stale NoLLDP boundary after the Connect")
	}
	for _, idx := range []pnio.Index{pnio.IndexPDPortDataCheck, pnio.IndexPDPortDataAdjust} {
		if b, err := x.read(port, idx); err != nil || len(b) != 0 {
			t.Fatalf("%v after the Connect: % x %v", idx, b, err)
		}
	}
	// The diagnosis stays gone once the evaluator ran again.
	time.Sleep(100 * time.Millisecond)
	if got := mismatch(d); len(got) != 0 {
		t.Fatalf("mismatch %#x", got)
	}
}
