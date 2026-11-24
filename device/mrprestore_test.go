// SPDX-License-Identifier: MIT

package device

import (
	"testing"
	"time"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/l2/bridge"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/mrp"
	"github.com/otfabric/go-profinet/pnio"
)

// TestMRPRestoreFailsBlocked: neither the new entity nor the previous one
// starts while the client holds ring port 2 blocked in its link-up wait
// (state PT). The configuration is restored, no entity runs, and the
// port stays blocked: forwarding state is preserved, not repaired. The
// controller is on port 3, outside the ring, so its replies do not cross
// the blocked port; the states are read from the bridge. Rewriting the
// configuration then starts the client again.
func TestMRPRestoreFailsBlocked(t *testing.T) {
	dom := [16]byte{0xD0, 2}
	slow := mrp.Profile200
	slow.Name, slow.LinkInterval, slow.LinkRepeat = "slow link-up", 30*time.Second, 4 // PT for 2 minutes
	d, n := mrpRingDeviceOn(t, &MRPConfig{Role: mrp.RoleClient, Domain: dom, DomainName: "ring-b", RingPorts: [2]int{1, 2}, Profile: slow}, 2)
	br := d.cfg.Bridge
	states := func() [2]bridge.State { return [2]bridge.State{br.State(0), br.State(1)} }
	held := [2]bridge.State{bridge.Forwarding, bridge.Blocked}
	eventually(t, 5*time.Second, "client in PT", func() bool {
		st, ok := d.MRPStatus()
		return ok && st.State == mrp.StatePT && states() == held
	})
	x := newPDevPeer(t, n, d)

	d.mrpNew = func(mrp.Config) (*mrp.Entity, error) { return nil, profinet.ErrInvalidConfig }
	a := pnio.PDInterfaceMrpDataAdjust{Domain: dom, Role: pnio.MrpRoleManager, DomainName: "ring-b"}
	wantStatus(t, x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust, a.Append(nil)), pnio.RWInvalidParameter, 0)

	// Configuration restored, no entity, forwarding state preserved.
	d.mu.Lock()
	c := d.mrpSt.cfg
	d.mu.Unlock()
	if c.Role != mrp.RoleClient || c.Domain != dom || c.Profile != slow {
		t.Fatalf("configuration %+v", c)
	}
	if st, ok := d.MRPStatus(); ok {
		t.Fatalf("MRPStatus reports an entity: %+v", st)
	}
	time.Sleep(200 * time.Millisecond)
	if got := states(); got != held {
		t.Fatalf("ring ports %v, want %v kept", got, held)
	}

	// The entity is restored by rewriting the configuration.
	d.mrpNew = nil
	orig := pnio.PDInterfaceMrpDataAdjust{Domain: dom, Role: pnio.MrpRoleClient, DomainName: "ring-b"}
	if err := x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust, orig.Append(nil)); err != nil {
		t.Fatal(err)
	}
	if st, ok := d.MRPStatus(); !ok || st.Role != mrp.RoleClient {
		t.Fatalf("after rewriting the configuration: %+v %v", st, ok)
	}
}
