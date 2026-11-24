// SPDX-License-Identifier: MIT

package device

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/l2/bridge"
	"github.com/otfabric/go-profinet/lldp"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/mrp"
	"github.com/otfabric/go-profinet/pnio"
)

// mrpNeighbor is an LLDP neighbor on the test segment that announces the
// MRP domain in dom, or no MRP port status while it is nil.
func mrpNeighbor(t *testing.T, n *tnet, dom *atomic.Pointer[[16]byte]) {
	t.Helper()
	nd := n.node("")
	a, err := lldp.NewAgent(nd.mux, lldp.AgentOptions{Interval: 50 * time.Millisecond, TTLFactor: 4,
		Local: func() lldp.LocalInfo {
			li := lldp.LocalInfo{StationName: "sw", PortName: "port-002"}
			if p := dom.Load(); p != nil {
				li.MRP = &lldp.MRPPortStatus{Domain: *p}
			}
			return li
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
}

// portMismatch returns the remote mismatch extended error types of a port.
func portMismatch(d *Device, subslot uint16) []uint16 {
	var out []uint16
	for _, it := range d.Diagnoses(0, subslot) {
		if it.ErrorType == pnio.ChannelErrRemoteMismat && it.ChannelNumber == pdevChannel {
			out = append(out, it.ExtErrorType)
		}
	}
	return out
}

// The MRP_DomainUUID check of PDInterfaceMrpDataCheck: a ring port whose
// neighbor announces another MRP domain reports a remote mismatch
// diagnosis with extended error type 0x8004; the same domain, a neighbor
// without MRP port status and a check without the bit report none. Before,
// the record was refused.
func TestMRPDomainCheck(t *testing.T) {
	dom := [16]byte{0xD0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	other := [16]byte{0xEE, 1}
	// Port 2 is on the test segment and on the ring.
	d, n := mrpRingDevice(t, &MRPConfig{Role: mrp.RoleClient, Domain: dom, DomainName: "ring-x", RingPorts: [2]int{1, 2}})
	const port = model.SubslotPort1 + 1
	var peerDom atomic.Pointer[[16]byte]
	peerDom.Store(&dom)
	mrpNeighbor(t, n, &peerDom)
	eventually(t, 5*time.Second, "neighbor", func() bool { nb, ok := d.ports[1].lldpA.Latest(); return ok && nb.Frame.MRPPortStatus != nil })
	x := newPDevPeer(t, n, d)

	if b, err := x.read(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataCheck); err != nil || len(b) != 0 {
		t.Fatalf("check before a write: % x %v", b, err)
	}
	chk := pnio.PDInterfaceMrpDataCheck{Domain: dom, Check: pnio.MrpCheckDomainUUID}
	if err := x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataCheck, chk.Append(nil)); err != nil {
		t.Fatalf("domain check: %v", err)
	}
	if b, err := x.read(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataCheck); err != nil || string(b) != string(chk.Append(nil)) {
		t.Fatalf("check read back: % x %v", b, err)
	}
	wait := func(what string, want ...uint16) {
		t.Helper()
		eventually(t, 5*time.Second, what, func() bool { return equalTypes(portMismatch(d, port), want) })
	}
	time.Sleep(200 * time.Millisecond)
	wait("same domain")

	peerDom.Store(&other)
	wait("other domain", extPeerMRPDomainMismatch)
	// Port 1, without a neighbor, reports nothing.
	if got := portMismatch(d, model.SubslotPort1); len(got) != 0 {
		t.Fatalf("port 1: %#x", got)
	}

	peerDom.Store(nil)
	wait("neighbor without MRP port status")

	peerDom.Store(&other)
	wait("other domain again", extPeerMRPDomainMismatch)
	chk.Check = 0
	if err := x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataCheck, chk.Append(nil)); err != nil {
		t.Fatalf("check off: %v", err)
	}
	wait("check off")
}

// A PDInterfaceMrpDataCheck the device cannot honour is refused and leaves
// the stored check as it was: the MediaRedundancyManager check, reserved
// bits, another domain, block version 1.1 (several MRP instances), and a
// device without MRP.
func TestMRPCheckRefused(t *testing.T) {
	dom := mrp.DefaultDomain
	d, n := mrpRingDevice(t, &MRPConfig{Role: mrp.RoleClient})
	x := newPDevPeer(t, n, d)
	ok := pnio.PDInterfaceMrpDataCheck{Domain: dom, Check: pnio.MrpCheckDomainUUID}
	if err := x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataCheck, ok.Append(nil)); err != nil {
		t.Fatal(err)
	}
	v11 := ok.Append(nil)
	v11[5] = 1
	for _, c := range []struct {
		name string
		data []byte
		code uint8
	}{
		{"manager", (&pnio.PDInterfaceMrpDataCheck{Domain: dom, Check: pnio.MrpCheckManager | pnio.MrpCheckDomainUUID}).Append(nil), pnio.RWFeatureNotSupport},
		{"reserved", (&pnio.PDInterfaceMrpDataCheck{Domain: dom, Check: 0x04}).Append(nil), pnio.RWInvalidParameter},
		{"other domain", (&pnio.PDInterfaceMrpDataCheck{Domain: [16]byte{1}, Check: pnio.MrpCheckDomainUUID}).Append(nil), pnio.RWInvalidParameter},
		{"version 1.1", v11, pnio.RWFeatureNotSupport},
		{"short", []byte{0x02, 0x13, 0x00, 0x02, 1, 0}, pnio.RWInvalidParameter},
	} {
		t.Run(c.name, func(t *testing.T) {
			wantStatus(t, x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataCheck, c.data), c.code, 0)
			if b, err := x.read(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataCheck); err != nil || string(b) != string(ok.Append(nil)) {
				t.Fatalf("stored check changed: % x %v", b, err)
			}
		})
	}
	// Only the interface submodule carries the check.
	if err := x.write(model.SubslotPort1, pnio.IndexPDInterfaceMrpDataCheck, ok.Append(nil)); err == nil {
		t.Fatal("check accepted on a port")
	}
}

// The ring ports announce the device's MRP domain in their LLDP frames
// while MRP runs; a port off the ring, and every port once MRP is off,
// announce none.
func TestMRPLLDPPortStatus(t *testing.T) {
	dom := [16]byte{0xD0, 9}
	d, _ := mrpRingDevice(t, &MRPConfig{Role: mrp.RoleClient, Domain: dom, DomainName: "ring-x", RingPorts: [2]int{1, 2}})
	d.mu.Lock()
	ring, off := d.mrpLLDP(d.ports[1]), d.mrpLLDP(d.ports[2])
	d.mu.Unlock()
	if ring == nil || ring.Domain != dom || ring.MRRT != lldp.MRRTOff || off != nil {
		t.Fatalf("ring port %+v, port off the ring %+v", ring, off)
	}
	if li := d.lldpInfo(d.ports[1]); li.MRP == nil || li.MRP.Domain != dom {
		t.Fatalf("LocalInfo %+v", li)
	}
	d.mrpMu.Lock()
	err := d.setMRPRole(mrp.RoleDisabled, dom, "ring-x")
	d.mrpMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if li := d.lldpInfo(d.ports[1]); li.MRP != nil {
		t.Fatalf("MRP off: %+v", li.MRP)
	}
}

// The timers of a written MrpManagerParams and MrpClientParams select the
// 200 ms or the 500 ms profile in Wireshark's units; any other set is
// refused; all-zero timers keep the profile.
func TestWrittenProfile(t *testing.T) {
	m200 := &pnio.MrpManagerParams{Prio: 0x8000, TopChangeT: 1, TopNRmax: 3, TestShortT: 10, TestDefaultT: 20, TestNRmax: 3}
	c200 := &pnio.MrpClientParams{LinkDownT: 20, LinkUpT: 20, LinkNRmax: 4}
	m500 := &pnio.MrpManagerParams{TopChangeT: 2, TopNRmax: 3, TestShortT: 30, TestDefaultT: 50, TestNRmax: 5}
	c500 := &pnio.MrpClientParams{LinkDownT: 100, LinkUpT: 100, LinkNRmax: 4}
	with := func(f func(*pnio.MrpManagerParams)) *pnio.MrpManagerParams { m := *m200; f(&m); return &m }
	for _, c := range []struct {
		name string
		m    *pnio.MrpManagerParams
		c    *pnio.MrpClientParams
		want mrp.Profile
		ok   bool
	}{
		{"none", nil, nil, mrp.Profile500, true},
		{"manager 200", m200, nil, mrp.Profile200, true},
		{"client 200", nil, c200, mrp.Profile200, true},
		{"both 200", m200, c200, mrp.Profile200, true},
		{"manager 500", m500, nil, mrp.Profile500, true},
		{"client 500", nil, c500, mrp.Profile500, true},
		{"both 500", m500, c500, mrp.Profile500, true},
		{"mixed", m200, c500, mrp.Profile{}, false},
		{"zero manager timers", &pnio.MrpManagerParams{Prio: 0x9000}, nil, mrp.Profile500, true},
		{"zero client timers", nil, &pnio.MrpClientParams{}, mrp.Profile500, true},
		{"zero manager, client 200", &pnio.MrpManagerParams{Prio: 0x9000}, c200, mrp.Profile200, true},
		{"TOPchgT in ms", with(func(m *pnio.MrpManagerParams) { m.TopChangeT = 10 }), nil, mrp.Profile{}, false},
		{"TOPNRmax", with(func(m *pnio.MrpManagerParams) { m.TopNRmax = 4 }), nil, mrp.Profile{}, false},
		{"TSTshortT", with(func(m *pnio.MrpManagerParams) { m.TestShortT = 20 }), nil, mrp.Profile{}, false},
		{"TSTdefaultT 10 ms profile", with(func(m *pnio.MrpManagerParams) { m.TestDefaultT = 1 }), nil, mrp.Profile{}, false},
		{"TSTNRmax", with(func(m *pnio.MrpManagerParams) { m.TestNRmax = 0 }), nil, mrp.Profile{}, false},
		{"max values", &pnio.MrpManagerParams{TopChangeT: 0xFFFF, TopNRmax: 0xFFFF, TestShortT: 0xFFFF, TestDefaultT: 0xFFFF, TestNRmax: 0xFFFF}, nil, mrp.Profile{}, false},
		{"LNKupT differs", nil, &pnio.MrpClientParams{LinkDownT: 20, LinkUpT: 100, LinkNRmax: 4}, mrp.Profile{}, false},
		{"LNKNRmax", nil, &pnio.MrpClientParams{LinkDownT: 20, LinkUpT: 20, LinkNRmax: 0}, mrp.Profile{}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := writtenProfile(&pnio.PDInterfaceMrpDataAdjust{Manager: c.m, Client: c.c}, mrp.Profile500)
			if ok != c.ok || got != c.want {
				t.Fatalf("%+v %v, want %+v %v", got, ok, c.want, c.ok)
			}
		})
	}
}

// A written PDInterfaceMrpDataAdjust applies its timer profile and its
// manager priority, restarting the entity; one with timers of no profile
// is refused and changes nothing (role, priority, timers, stored record).
// Before, the timers were ignored and a new priority alone did not reach
// the entity.
func TestMRPAdjustTimers(t *testing.T) {
	d, n := mrpRingDevice(t, &MRPConfig{Role: mrp.RoleClient})
	x := newPDevPeer(t, n, d)
	cfg := func() MRPConfig {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.mrpSt.cfg
	}
	ent := func() *mrp.Entity {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.mrpSt.ent
	}
	if c := cfg(); c.Profile != mrp.Profile200 {
		t.Fatalf("initial profile %+v", c.Profile)
	}
	a := pnio.PDInterfaceMrpDataAdjust{Domain: mrp.DefaultDomain, Role: pnio.MrpRoleClient, DomainName: "mrpdomain-1",
		Client: &pnio.MrpClientParams{LinkDownT: 100, LinkUpT: 100, LinkNRmax: 4}}
	before := ent()
	if err := x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust, a.Append(nil)); err != nil {
		t.Fatal(err)
	}
	if c := cfg(); c.Profile != mrp.Profile500 || ent() == before || ent() == nil {
		t.Fatalf("profile %+v, entity restarted %v", c.Profile, ent() != before)
	}
	stored, _ := x.read(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust)

	// A 10 ms profile's timers: refused, nothing changes.
	bad := pnio.PDInterfaceMrpDataAdjust{Domain: mrp.DefaultDomain, Role: pnio.MrpRoleManager, DomainName: "mrpdomain-1",
		Manager: &pnio.MrpManagerParams{Prio: 0x1000, TopChangeT: 1, TopNRmax: 3, TestShortT: 1, TestDefaultT: 1, TestNRmax: 3}}
	running := ent()
	wantStatus(t, x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust, bad.Append(nil)), pnio.RWFeatureNotSupport, 0)
	if c := cfg(); c.Role != mrp.RoleClient || c.Prio == 0x1000 || c.Profile != mrp.Profile500 || ent() != running {
		t.Fatalf("refused write changed %+v", c)
	}
	if b, _ := x.read(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust); string(b) != string(stored) {
		t.Fatal("refused write stored")
	}

	// A manager with the priority alone (all timers zero, as the
	// go-profinet controller writes): the priority reaches the entity,
	// the timers stay.
	m := pnio.PDInterfaceMrpDataAdjust{Domain: mrp.DefaultDomain, Role: pnio.MrpRoleManager, DomainName: "mrpdomain-1",
		Manager: &pnio.MrpManagerParams{Prio: 0x9000}}
	if err := x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust, m.Append(nil)); err != nil {
		t.Fatal(err)
	}
	if c := cfg(); c.Role != mrp.RoleManager || c.Prio != 0x9000 || c.Profile != mrp.Profile500 {
		t.Fatalf("%+v", c)
	}
	running = ent()
	m.Manager.Prio = 0xA000
	if err := x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust, m.Append(nil)); err != nil {
		t.Fatal(err)
	}
	if c := cfg(); c.Prio != 0xA000 || ent() == running {
		t.Fatalf("new priority not applied: %+v, restarted %v", c, ent() != running)
	}
	// The same write again changes nothing.
	running = ent()
	if err := x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust, m.Append(nil)); err != nil {
		t.Fatal(err)
	}
	if ent() != running {
		t.Fatal("identical write restarted the entity")
	}
}

// A PDInterfaceMrpDataAdjust whose entity does not start is refused and
// changes nothing: the stored records, the role, domain, priority and
// timers, and a running entity of the previous configuration. Before,
// the write was refused but left the new configuration recorded and no
// entity running.
func TestMRPAdjustRollback(t *testing.T) {
	dom := [16]byte{0xD0, 7}
	d, n := mrpRingDevice(t, &MRPConfig{Role: mrp.RoleClient, Domain: dom, DomainName: "ring-a"})
	x := newPDevPeer(t, n, d)
	chk := pnio.PDInterfaceMrpDataCheck{Domain: dom, Check: pnio.MrpCheckDomainUUID}
	if err := x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataCheck, chk.Append(nil)); err != nil {
		t.Fatal(err)
	}
	read := func(idx pnio.Index) string {
		b, err := x.read(model.SubslotInterface, idx)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	snap := func() (MRPConfig, string, string, string) {
		d.mu.Lock()
		c := d.mrpSt.cfg
		d.mu.Unlock()
		return c, read(pnio.IndexPDInterfaceMrpDataAdjust), read(pnio.IndexPDInterfaceMrpDataCheck), read(pnio.IndexPDInterfaceMrpDataReal)
	}
	cfg0, adj0, chk0, real0 := snap()
	var fails atomic.Int32
	fails.Store(1)
	d.mrpNew = func(c mrp.Config) (*mrp.Entity, error) {
		if fails.Add(-1) >= 0 {
			return nil, profinet.ErrInvalidConfig
		}
		return mrp.New(c)
	}
	a := pnio.PDInterfaceMrpDataAdjust{Domain: [16]byte{0xD0, 8}, Role: pnio.MrpRoleManager, DomainName: "ring-b",
		Manager: &pnio.MrpManagerParams{Prio: 0x9000, TopChangeT: 2, TopNRmax: 3, TestShortT: 30, TestDefaultT: 50, TestNRmax: 5}}
	wantStatus(t, x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust, a.Append(nil)), pnio.RWInvalidParameter, 0)
	cfg1, adj1, chk1, real1 := snap()
	if cfg1 != cfg0 || adj1 != adj0 || chk1 != chk0 || real1 != real0 {
		t.Fatalf("the refused write changed the state:\n%+v\n%+v", cfg0, cfg1)
	}
	if st, ok := d.MRPStatus(); !ok || st.Role != mrp.RoleClient {
		t.Fatalf("the previous entity does not run: %+v %v", st, ok)
	}
	// The same write succeeds once the entity starts.
	if err := x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust, a.Append(nil)); err != nil {
		t.Fatal(err)
	}
	if c, _, _, _ := snap(); c.Role != mrp.RoleManager || c.Prio != 0x9000 || c.Profile != mrp.Profile500 || c.Domain != a.Domain {
		t.Fatalf("%+v", c)
	}
}

// The MRP_DomainUUID check names the device's domain: an adjustment that
// changes the domain drops it (and its diagnosis), and a Connect's PDev
// reset keeps it, the mismatch being reported again for the new AR.
// Before, the check stayed after a domain change and compared the
// neighbor with a domain it did not name.
func TestMRPCheckLifecycle(t *testing.T) {
	dom := [16]byte{0xD0, 1}
	other := [16]byte{0xEE, 1}
	d, n := mrpRingDevice(t, &MRPConfig{Role: mrp.RoleClient, Domain: dom, DomainName: "ring-x", RingPorts: [2]int{1, 2}})
	const port = model.SubslotPort1 + 1
	var peerDom atomic.Pointer[[16]byte]
	peerDom.Store(&other)
	mrpNeighbor(t, n, &peerDom)
	eventually(t, 5*time.Second, "neighbor", func() bool { nb, ok := d.ports[1].lldpA.Latest(); return ok && nb.Frame.MRPPortStatus != nil })
	x := newPDevPeer(t, n, d)
	chk := pnio.PDInterfaceMrpDataCheck{Domain: dom, Check: pnio.MrpCheckDomainUUID}
	if err := x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataCheck, chk.Append(nil)); err != nil {
		t.Fatal(err)
	}
	wait := func(what string, want ...uint16) {
		t.Helper()
		eventually(t, 5*time.Second, what, func() bool { return equalTypes(portMismatch(d, port), want) })
	}
	wait("other domain", extPeerMRPDomainMismatch)

	// An IOCAR takes the ports: the check stays, the mismatch returns.
	p := n.peer(d, "192.168.0.3", true)
	if _, err := p.connect(p.ioCAR()); err != nil {
		t.Fatal(err)
	}
	wait("mismatch after the Connect", extPeerMRPDomainMismatch)
	if b, _ := x.read(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataCheck); string(b) != string(chk.Append(nil)) {
		t.Fatalf("check after the Connect: % x", b)
	}

	// The device moves to the neighbor's domain: the check of the old
	// domain is dropped.
	a := pnio.PDInterfaceMrpDataAdjust{Domain: other, Role: pnio.MrpRoleClient, DomainName: "ring-y"}
	if err := x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust, a.Append(nil)); err != nil {
		t.Fatal(err)
	}
	if b, _ := x.read(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataCheck); len(b) != 0 {
		t.Fatalf("check of the old domain kept: % x", b)
	}
	wait("check dropped")
	// A check of the new domain applies to it.
	chk.Domain = other
	if err := x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataCheck, chk.Append(nil)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	wait("same domain")
	peerDom.Store(&dom)
	wait("old domain is now the other one", extPeerMRPDomainMismatch)
	// MRP off: the check does not apply.
	a.Role = pnio.MrpRoleOff
	if err := x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust, a.Append(nil)); err != nil {
		t.Fatal(err)
	}
	wait("MRP off")
}

// A PDInterfaceMrpDataCheck written while an adjustment replaces the
// entity is not lost when the adjustment is refused and rolled back: the
// check write waits for the adjustment (the RPC server runs the calls of
// several ARs at once). Before, the rollback restored the check the
// adjustment had found and discarded the write that succeeded meanwhile.
func TestMRPCheckDuringAdjustRollback(t *testing.T) {
	dom := [16]byte{0xD0, 7}
	d, n := mrpRingDevice(t, &MRPConfig{Role: mrp.RoleClient, Domain: dom, DomainName: "ring-a"})
	x := newPDevPeer(t, n, d)
	// A second supervisor, whose calls the RPC server runs alongside x's.
	p2 := n.peer(d, "192.168.0.5", true)
	sar2 := p2.ioSAR()
	if _, err := p2.connect(sar2); err != nil {
		t.Fatal(err)
	}
	x2 := &pdevPeer{t: t, p: p2, ar: sar2.AR.ARUUID}
	chk := pnio.PDInterfaceMrpDataCheck{Domain: dom, Check: pnio.MrpCheckDomainUUID}
	inEntity := make(chan struct{})
	checkDone := make(chan error, 1)
	var calls atomic.Int32
	d.mrpNew = func(c mrp.Config) (*mrp.Entity, error) {
		if calls.Add(1) == 1 {
			close(inEntity)
			// Give the concurrent check write the time to finish, if
			// nothing holds it back.
			select {
			case err := <-checkDone:
				checkDone <- err
			case <-time.After(300 * time.Millisecond):
			}
			return nil, profinet.ErrInvalidConfig
		}
		return mrp.New(c)
	}
	a := pnio.PDInterfaceMrpDataAdjust{Domain: dom, Role: pnio.MrpRoleManager, DomainName: "ring-a"}
	adjDone := make(chan error, 1)
	go func() { adjDone <- x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust, a.Append(nil)) }()
	<-inEntity
	go func() {
		checkDone <- x2.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataCheck, chk.Append(nil))
	}()
	wantStatus(t, <-adjDone, pnio.RWInvalidParameter, 0)
	if err := <-checkDone; err != nil {
		t.Fatalf("check write: %v", err)
	}
	if b, err := x.read(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataCheck); err != nil || string(b) != string(chk.Append(nil)) {
		t.Fatalf("the concurrent check write was lost: % x %v", b, err)
	}
}

// When neither the new entity nor the previous one starts, no MRP entity
// runs and the device says so: MRPStatus reports none, the ring ports
// send no MRP port status in LLDP, PDInterfaceMrpDataReal reports the
// role off and PDPortMrpDataReal nothing, and the domain check reports no
// mismatch. The configuration asked for stays, and the ring ports keep
// the forwarding state the last entity left. Before, the device kept
// advertising its MRP domain with no entity running.
func TestMRPRestoreFails(t *testing.T) {
	dom := [16]byte{0xD0, 1}
	other := [16]byte{0xEE, 1}
	d, n := mrpRingDevice(t, &MRPConfig{Role: mrp.RoleClient, Domain: dom, DomainName: "ring-x", RingPorts: [2]int{1, 2}})
	const port = model.SubslotPort1 + 1
	var peerDom atomic.Pointer[[16]byte]
	peerDom.Store(&other)
	mrpNeighbor(t, n, &peerDom)
	eventually(t, 5*time.Second, "neighbor", func() bool { nb, ok := d.ports[1].lldpA.Latest(); return ok && nb.Frame.MRPPortStatus != nil })
	x := newPDevPeer(t, n, d)
	chk := pnio.PDInterfaceMrpDataCheck{Domain: dom, Check: pnio.MrpCheckDomainUUID}
	if err := x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataCheck, chk.Append(nil)); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "mismatch", func() bool { return equalTypes(portMismatch(d, port), []uint16{extPeerMRPDomainMismatch}) })
	br := d.cfg.Bridge
	states := func() [2]bridge.State { return [2]bridge.State{br.State(0), br.State(1)} }
	before := states()

	d.mrpNew = func(mrp.Config) (*mrp.Entity, error) { return nil, profinet.ErrInvalidConfig }
	a := pnio.PDInterfaceMrpDataAdjust{Domain: dom, Role: pnio.MrpRoleManager, DomainName: "ring-x"}
	wantStatus(t, x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust, a.Append(nil)), pnio.RWInvalidParameter, 0)

	if st, ok := d.MRPStatus(); ok {
		t.Fatalf("MRPStatus reports an entity: %+v", st)
	}
	d.mu.Lock()
	lp := d.mrpLLDP(d.ports[1])
	d.mu.Unlock()
	if lp != nil || d.lldpInfo(d.ports[1]).MRP != nil {
		t.Fatalf("MRP port status advertised without an entity: %+v", lp)
	}
	if r := mrpInterfaceReal(t, x); r.Role != pnio.MrpRoleOff || r.HasRingState {
		t.Fatalf("PDInterfaceMrpDataReal %+v", r)
	}
	if b, err := x.read(port, pnio.IndexPDPortMrpDataReal); err != nil || len(b) != 0 {
		t.Fatalf("PDPortMrpDataReal % x %v", b, err)
	}
	eventually(t, 5*time.Second, "no mismatch without an entity", func() bool { return len(portMismatch(d, port)) == 0 })
	d.mu.Lock()
	c := d.mrpSt.cfg
	d.mu.Unlock()
	if c.Role != mrp.RoleClient || c.Domain != dom {
		t.Fatalf("configuration %+v", c)
	}
	if got := states(); got != before {
		t.Fatalf("ring ports %v, were %v", got, before)
	}
	// A later write whose entity starts brings MRP back.
	d.mrpNew = nil
	if err := x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust, a.Append(nil)); err != nil {
		t.Fatal(err)
	}
	if st, ok := d.MRPStatus(); !ok || st.Role != mrp.RoleManager {
		t.Fatalf("%+v %v", st, ok)
	}
	d.mu.Lock()
	lp = d.mrpLLDP(d.ports[1])
	d.mu.Unlock()
	if lp == nil {
		t.Fatal("no MRP port status once the entity runs")
	}
}
