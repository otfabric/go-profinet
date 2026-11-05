// SPDX-License-Identifier: MIT

package device

import (
	"net/netip"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/dcp"
	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/l2"
	"github.com/otfabric/go-profinet/l2/bridge"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/mrp"
	"github.com/otfabric/go-profinet/netstack"
	"github.com/otfabric/go-profinet/pnio"
	"github.com/otfabric/go-profinet/rpc"
	"github.com/otfabric/go-profinet/rt"
	"github.com/otfabric/go-profinet/vnet"
)

// mrpRingDevice starts a three-port device "dut" at devIP, one port on
// each of three separate segments, running MRP with cfg; it returns the
// device and a test network on the segment of port 2, for a peer.
func mrpRingDevice(t *testing.T, cfg *MRPConfig) (*Device, *tnet) {
	t.Helper()
	var segs [3]*vnet.Network
	var ports []l2.Port
	for i := range segs {
		segs[i] = vnet.New(uint64(700 + i))
		s := segs[i]
		t.Cleanup(func() { _ = s.Close() })
		ep, err := s.NewEndpoint(vnet.EndpointConfig{Name: "dut", MAC: ethernet.MAC{0x02, 0, 0, 0, 0x01, byte(i + 1)}, Promiscuous: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ep.Close() })
		ports = append(ports, ep)
	}
	br, err := bridge.New(bridge.Config{Ports: ports, MAC: ethernet.MAC{0x02, 0, 0, 0, 0x01, 0x10}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = br.Close() })
	mux := l2.NewMux(br.CPU())
	t.Cleanup(func() { _ = mux.Close() })
	st, err := netstack.New(mux, netstack.Config{Addr: netip.MustParseAddr(devIP), Mask: mask})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := testModel()
	m.Capabilities.Ports = 3
	m.DAP = model.StandardDAP("DAP", 0x00000100, 3)
	d, err := New(Config{
		Model: m, Mux: mux, Bridge: br, StationName: "dut", IP: suite(devIP), ListenPacket: st.ListenPacket,
		ApplyIP:         func(s dcp.IPSuite, _ bool) error { return st.SetAddr(s.Addr, s.Mask, s.Gateway) },
		NoResponseDelay: true, LLDPInterval: 200 * time.Millisecond, MRP: cfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, &tnet{t: t, net: segs[1]}
}

// mrpInterfaceReal reads and decodes PDInterfaceMrpDataReal.
func mrpInterfaceReal(t *testing.T, x *pdevPeer) pnio.PDInterfaceMrpDataReal {
	t.Helper()
	b, err := x.read(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataReal)
	if err != nil {
		t.Fatalf("PDInterfaceMrpDataReal: %v", err)
	}
	blk, _, err := pnio.ReadBlock(b)
	if err != nil {
		t.Fatal(err)
	}
	r, err := pnio.DecodePDInterfaceMrpDataReal(blk)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestMRPRecordsUnit: PDInterfaceMrpDataReal reports the configured
// domain and role (a manager its ring state, the ring being open on
// separate segments); PDPortMrpDataReal is filled on the configured ring
// ports only (1 and 3, port 2 off the ring), and a port adjust is taken
// on a ring port only.
func TestMRPRecordsUnit(t *testing.T) {
	dom := [16]byte{0xD0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	for _, tc := range []struct {
		name     string
		role     mrp.Role
		wantRole uint16
		ringSt   bool
	}{
		{"manager", mrp.RoleManager, pnio.MrpRoleManager, true},
		{"client", mrp.RoleClient, pnio.MrpRoleClient, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, n := mrpRingDevice(t, &MRPConfig{Role: tc.role, Domain: dom, DomainName: "ring-x", RingPorts: [2]int{1, 3}})
			if st, ok := d.MRPStatus(); !ok || st.Role != tc.role {
				t.Fatalf("MRPStatus %+v %v", st, ok)
			}
			x := newPDevPeer(t, n, d)
			r := mrpInterfaceReal(t, x)
			if r.Role != tc.wantRole || r.Domain != dom || r.DomainName != "ring-x" || r.Version != mrp.Version {
				t.Fatalf("PDInterfaceMrpDataReal %+v", r)
			}
			if r.HasRingState != tc.ringSt {
				t.Fatalf("HasRingState %v, want %v", r.HasRingState, tc.ringSt)
			}
			if tc.ringSt && r.RingState != uint16(mrp.RingOpen) {
				t.Fatalf("ring state %d on separate segments, want open", r.RingState)
			}
			// Only an interface submodule carries the interface record.
			if _, err := x.read(model.SubslotPort1, pnio.IndexPDInterfaceMrpDataReal); err == nil {
				t.Fatal("interface record read on a port")
			}

			for port, ring := range map[int]bool{1: true, 2: false, 3: true} {
				sub := model.SubslotPort1 + uint16(port-1)
				b, err := x.read(sub, pnio.IndexPDPortMrpDataReal)
				if err != nil {
					t.Fatalf("port %d: %v", port, err)
				}
				if !ring {
					if len(b) != 0 {
						t.Fatalf("port %d off the ring: % x", port, b)
					}
					continue
				}
				blk, _, err := pnio.ReadBlock(b)
				if err != nil {
					t.Fatalf("port %d: %v", port, err)
				}
				p, err := pnio.DecodePDPortMrpData(blk)
				if err != nil || !p.Real || p.Domain != dom {
					t.Fatalf("port %d: %+v %v", port, p, err)
				}
			}
			adj := pnio.PDPortMrpData{Domain: dom}
			if err := x.write(model.SubslotPort1+1, pnio.IndexPDPortMrpDataAdjust, adj.Append(nil)); err == nil {
				t.Fatal("port adjust accepted on port 2, off the ring")
			}
			if err := x.write(model.SubslotPort1+2, pnio.IndexPDPortMrpDataAdjust, adj.Append(nil)); err != nil {
				t.Fatalf("port adjust on ring port 3: %v", err)
			}
		})
	}
}

// TestMRPRecordsAutoRole: an MRA device reports what its entity acts as,
// never "auto", which MRP_Role has no value for.
func TestMRPRecordsAutoRole(t *testing.T) {
	d, n := mrpRingDevice(t, &MRPConfig{Role: mrp.RoleAuto})
	x := newPDevPeer(t, n, d)
	if st, ok := d.MRPStatus(); !ok || st.Role != mrp.RoleAuto {
		t.Fatalf("MRPStatus %+v %v", st, ok)
	}
	r := mrpInterfaceReal(t, x)
	if r.Role != pnio.MrpRoleClient && r.Role != pnio.MrpRoleManager {
		t.Fatalf("role %d", r.Role)
	}
	if r.Domain != mrp.DefaultDomain || r.DomainName != "mrpdomain-1" {
		t.Fatalf("defaults not reported: %+v", r)
	}
}

// srConnect returns an IOCARSR Connect request of AR set 0xA..0xF/1..8
// with AR number num and an SRInfoBlock of the hold factor.
func (p *peer) srConnect(num uint8, hold uint16) *pnio.ConnectRequest {
	p.t.Helper()
	req := p.ioCAR()
	req.AR.Type = pnio.ARTypeIOCARSR
	req.AR.ARUUID = pnio.ARSetID{Discriminator: [6]byte{0xA, 0xB, 0xC, 0xD, 0xE, 0xF}, ConfigID: [8]byte{1, 2, 3, 4, 5, 6, 7, 8}, ARNumber: num, Resource: 2}.UUID()
	info := pnio.SRInfoBlock{RedundancyDataHoldFactor: hold, Properties: pnio.SRPropInputValidOnBackupAR}
	blk, _, err := pnio.ReadBlock(info.Append(nil))
	if err != nil {
		p.t.Fatal(err)
	}
	req.Other = append(req.Other, blk)
	return req
}

// TestSRDataHoldFactorMinimum: an SRInfoBlock's RedundancyDataHoldFactor
// below the minimum (3 by default, 0-2 reserved; else the configured one)
// is refused as a faulty SRInfoBlock; at the minimum it is accepted.
func TestSRDataHoldFactorMinimum(t *testing.T) {
	for _, tc := range []struct {
		name   string
		min    uint16
		reject uint16
		accept uint16
	}{
		{"default", 0, 2, 3},
		{"below default", 1, 2, 3},
		{"configured", 100, 99, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newNet(t)
			d, _ := n.device(nil, func(c *Config) { c.SystemRedundancy = &SRConfig{MinDataHoldFactor: tc.min} })
			p := n.peer(d, "192.168.0.2", true)
			_, err := p.connect(p.srConnect(1, tc.reject))
			wantStatus(t, err, pnio.Code1SRInfo, 4)
			if len(d.ARs()) != 0 {
				t.Fatalf("refused AR kept: %+v", d.ARs())
			}
			req := p.srConnect(1, tc.accept)
			if _, err := p.connect(req); err != nil {
				t.Fatalf("hold factor %d: %v", tc.accept, err)
			}
			if _, ok := arState(d, req.AR.ARUUID); !ok {
				t.Fatal("accepted AR not in the table")
			}
		})
	}
}

// TestSRRDHTExpires: when an AR set loses its primary and none takes over
// within the redundancy data hold time, the device aborts all the set's
// ARs with reason 38 "IOCARSR RDHT expired"; a primary that comes back in
// time stops the hold timer.
func TestSRRDHTExpires(t *testing.T) {
	const hold = 60 // ms
	n := newNet(t)
	h := newRecHandler()
	d, _ := n.device(h, func(c *Config) { c.SystemRedundancy = &SRConfig{} })
	p1 := n.peer(d, "192.168.0.2", true)
	p2 := n.peer(d, "192.168.0.3", true)
	r1, r2 := p1.srConnect(1, hold), p2.srConnect(2, hold)
	r2.AR.StationName = "plc-b"
	p1.establish(r1)
	p2.establish(r2)
	d.mu.Lock()
	a1, a2 := d.ars[r1.AR.ARUUID], d.ars[r2.AR.ARUUID]
	d.mu.Unlock()
	if a1 == nil || a2 == nil || !sameSet(a1, a2) {
		t.Fatal("the two IOCARSRs are not one AR set")
	}

	// AR 1 primary, then backup with AR 2 taking over in time: no abort.
	if !d.srOutput(a1, rt.PrimaryRequest) {
		t.Fatal("AR 1 not primary")
	}
	if d.srOutput(a2, rt.BackupRequest) {
		t.Fatal("backup AR 2 applies outputs")
	}
	if d.srOutput(a1, rt.BackupRequest) {
		t.Fatal("AR 1 still primary after a backup request")
	}
	if !d.srOutput(a2, rt.PrimaryRequest) {
		t.Fatal("AR 2 not primary")
	}
	time.Sleep(3 * hold * time.Millisecond)
	for _, u := range []rpc.UUID{r1.AR.ARUUID, r2.AR.ARUUID} {
		if st, ok := arState(d, u); !ok || st != ARStateData {
			t.Fatalf("AR %s %v %v after a takeover in time", u, st, ok)
		}
	}

	// AR 2 steps back, nobody steps up: both ARs go after RDHT.
	start := time.Now()
	if d.srOutput(a2, rt.BackupRequest) {
		t.Fatal("AR 2 still primary")
	}
	for _, u := range []rpc.UUID{r1.AR.ARUUID, r2.AR.ARUUID} {
		h.waitReason(t, u, pnio.AbortSRRDHTExpired)
		waitAR(t, d, u, ARStateClosed)
	}
	if el := time.Since(start); el < hold*time.Millisecond {
		t.Fatalf("aborted after %s, before the hold time of %d ms", el, hold)
	}
	if st := d.SystemRedundancy(); len(st) != 0 {
		t.Fatalf("set kept after its ARs: %+v", st)
	}
}
