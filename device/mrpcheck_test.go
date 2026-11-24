// SPDX-License-Identifier: MIT

package device

import (
	"sync/atomic"
	"testing"
	"time"

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
