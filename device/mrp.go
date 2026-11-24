// SPDX-License-Identifier: MIT

package device

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/l2"
	"github.com/otfabric/go-profinet/lldp"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/mrp"
	"github.com/otfabric/go-profinet/pnio"
)

// Media redundancy: a multiport device runs an MRP client or manager
// (package mrp) on two of its ports, configured by Config.MRP and by the
// PROFINET MRP records a controller writes (PDInterfaceMrpDataAdjust
// 0x8052, PDPortMrpDataAdjust 0x8053), and reported in
// PDInterfaceMrpDataReal (0x8050) and PDPortMrpDataReal (0x8054).
//
// The timers of a written MrpManagerParams and MrpClientParams select the
// timer profile: they must be those of mrp.Profile200 or mrp.Profile500
// (writtenProfile); others are refused, as faster profiles need timers a
// general-purpose OS does not keep.
//
// PDInterfaceMrpDataCheck (0x8051) is served for its MRP_DomainUUID check
// only: the ring ports compare the MRP domain their LLDP neighbor
// announces (the MRP port status TLV, which the device sends on its ring
// ports) with the device's, and report a difference as a remote mismatch
// diagnosis (extended error type 0x8004). The MediaRedundancyManager
// check is refused: its diagnoses are not established from public
// sources.

// MRPConfig configures media redundancy.
type MRPConfig struct {
	// Role is the initial role; a controller may change it.
	// mrp.RoleAuto (MRA) is a role of this configuration only: PROFINET's
	// MRP_Role has no value for it, and PDInterfaceMrpDataReal reports
	// what the entity acts as (client or manager).
	Role mrp.Role
	// Domain is the MRP domain UUID (zero: mrp.DefaultDomain) and
	// DomainName its name.
	Domain     [16]byte
	DomainName string
	// Prio is the manager priority; zero means mrp.DefaultManagerPrio.
	Prio uint16
	// Profile is the timer profile; zero means mrp.Profile200.
	Profile mrp.Profile
	// RingPorts are the ring ports (1-based); zero means ports 1 and 2.
	RingPorts [2]int
}

// mrpState is the device's MRP state, guarded by Device.mu.
type mrpState struct {
	cfg     MRPConfig
	ent     *mrp.Entity
	adjust  *pnio.PDInterfaceMrpDataAdjust
	portAdj map[uint16]*pnio.PDPortMrpData
	// check is the written PDInterfaceMrpDataCheck, nil before a write.
	check *pnio.PDInterfaceMrpDataCheck
	// closed is set by stopMRP: no entity starts after it.
	closed bool
}

// startMRP checks the configuration and starts the entity of the
// configured role.
func (d *Device) startMRP() error {
	c := *d.cfg.MRP
	// As for a written PDInterfaceMrpDataAdjust: at most 240 octets.
	if len(c.DomainName) > 240 {
		return fmt.Errorf("device: MRP domain name of %d octets (at most 240): %w", len(c.DomainName), profinet.ErrInvalidConfig)
	}
	if d.cfg.Bridge == nil {
		return fmt.Errorf("device: MRP needs a multiport device (Config.Bridge): %w", profinet.ErrInvalidConfig)
	}
	if c.RingPorts == [2]int{} {
		c.RingPorts = [2]int{1, 2}
	}
	// The default of mrp.New, made explicit so that a written timer
	// profile compares with what the entity runs.
	if c.Profile.TestInterval == 0 {
		c.Profile = mrp.Profile200
	}
	for _, p := range c.RingPorts {
		if p < 1 || p > len(d.ports) {
			return fmt.Errorf("device: MRP ring port %d of %d ports: %w", p, len(d.ports), profinet.ErrInvalidConfig)
		}
	}
	if c.RingPorts[0] == c.RingPorts[1] {
		return fmt.Errorf("device: MRP ring ports %v: %w", c.RingPorts, profinet.ErrInvalidConfig)
	}
	if c.Domain == ([16]byte{}) {
		c.Domain = mrp.DefaultDomain
	}
	if c.DomainName == "" {
		c.DomainName = "mrpdomain-1"
	}
	// The LLDP agents, already sending, read it (mrpLLDP).
	d.mu.Lock()
	d.mrpSt = &mrpState{cfg: c, portAdj: map[uint16]*pnio.PDPortMrpData{}}
	d.mu.Unlock()
	d.mrpMu.Lock()
	defer d.mrpMu.Unlock()
	return d.setMRPRole(c.Role, c.Domain, c.DomainName)
}

// setMRPRole stops the running entity and starts one of the new role.
// The caller holds d.mrpMu, so one entity runs at a time.
func (d *Device) setMRPRole(role mrp.Role, domain [16]byte, name string) error {
	d.mu.Lock()
	st := d.mrpSt
	if st.closed {
		d.mu.Unlock()
		return profinet.ErrClosed
	}
	old := st.ent
	st.ent = nil
	st.cfg.Role, st.cfg.Domain, st.cfg.DomainName = role, domain, name
	c := st.cfg
	d.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	if role == mrp.RoleDisabled {
		// No ring: both ports forward.
		for _, p := range c.RingPorts {
			d.cfg.Bridge.SetBlocked(p-1, false)
		}
		return nil
	}
	ports := [2]*l2.Mux{d.ports[c.RingPorts[0]-1].mux, d.ports[c.RingPorts[1]-1].mux}
	newEntity := mrp.New
	if d.mrpNew != nil {
		newEntity = d.mrpNew
	}
	ent, err := newEntity(mrp.Config{Role: role, Ports: ports, Index: [2]int{c.RingPorts[0] - 1, c.RingPorts[1] - 1},
		Switch: d.cfg.Bridge, MAC: d.mux.HardwareAddr(), Domain: c.Domain, Prio: c.Prio, Profile: c.Profile, Logger: d.log})
	if err != nil {
		return err
	}
	d.mu.Lock()
	st.ent = ent
	d.mu.Unlock()
	d.log.Infof("device: MRP %s on ports %d and %d", role, c.RingPorts[0], c.RingPorts[1])
	return nil
}

func (d *Device) stopMRP() {
	d.mrpMu.Lock()
	defer d.mrpMu.Unlock()
	d.mu.Lock()
	var ent *mrp.Entity
	if d.mrpSt != nil {
		ent, d.mrpSt.ent = d.mrpSt.ent, nil
		d.mrpSt.closed = true
	}
	d.mu.Unlock()
	if ent != nil {
		_ = ent.Close()
	}
}

// MRPStatus returns the status of the device's MRP entity; ok is false
// when it runs none.
func (d *Device) MRPStatus() (mrp.Status, bool) {
	d.mu.Lock()
	var ent *mrp.Entity
	if d.mrpSt != nil {
		ent = d.mrpSt.ent
	}
	d.mu.Unlock()
	if ent == nil {
		return mrp.Status{}, false
	}
	return ent.Status(), true
}

// isRingPort reports whether a port submodule is an MRP ring port: mu
// held.
func (d *Device) isRingPort(subslot uint16) bool {
	if d.mrpSt == nil {
		return false
	}
	for _, p := range d.mrpSt.cfg.RingPorts {
		if int(subslot)-int(model.SubslotPort1)+1 == p {
			return true
		}
	}
	return false
}

func isMRPIndex(i pnio.Index) bool {
	return i >= pnio.IndexPDInterfaceMrpDataReal && i <= pnio.IndexPDPortMrpDataReal
}

// readMRP serves a read of an MRP record; handled is false for other
// indexes.
func (d *Device) readMRP(q pnio.ReadReq) (b []byte, handled bool, err error) {
	if !isMRPIndex(q.Index) {
		return nil, false, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	st := d.mrpSt
	if st == nil {
		return nil, true, errInvalidIndex
	}
	switch q.Index {
	case pnio.IndexPDInterfaceMrpDataReal, pnio.IndexPDInterfaceMrpDataAdjust, pnio.IndexPDInterfaceMrpDataCheck:
		if !isInterface(q.Slot, q.Subslot) {
			return nil, true, errInvalidIndex
		}
	default:
		if !d.isPort(q.Slot, q.Subslot) {
			return nil, true, errInvalidIndex
		}
	}
	switch q.Index {
	case pnio.IndexPDInterfaceMrpDataReal:
		// The role that runs: none (off) when the configured entity did
		// not start; PDInterfaceMrpDataAdjust keeps what was asked for.
		role := mrp.RoleDisabled
		var es mrp.Status
		if st.ent != nil {
			role = st.cfg.Role
			es = st.ent.Status()
			if role == mrp.RoleAuto {
				role = es.Acting
			}
		}
		r := pnio.PDInterfaceMrpDataReal{Domain: st.cfg.Domain, Role: uint16(min(max(role, mrp.RoleDisabled), mrp.RoleAuto)), DomainName: st.cfg.DomainName, Version: mrp.Version}
		if st.ent != nil && role == mrp.RoleManager {
			r.HasRingState = true
			if !es.RingOpen {
				r.RingState = uint16(mrp.RingClosed)
			}
		}
		return r.Append(nil), true, nil
	case pnio.IndexPDInterfaceMrpDataAdjust:
		if st.adjust != nil {
			return st.adjust.Append(nil), true, nil
		}
	case pnio.IndexPDPortMrpDataAdjust:
		if a := st.portAdj[q.Subslot]; a != nil {
			return a.Append(nil), true, nil
		}
	case pnio.IndexPDPortMrpDataReal:
		if d.isRingPort(q.Subslot) && st.cfg.Role != mrp.RoleDisabled && st.ent != nil {
			r := pnio.PDPortMrpData{Real: true, Domain: st.cfg.Domain}
			return r.Append(nil), true, nil
		}
	default:
		if st.check != nil {
			return st.check.Append(nil), true, nil
		}
	}
	return []byte{}, true, nil
}

// mrpLLDP returns the MRP port status a port sends in its LLDP frames:
// its MRP domain on a ring port while MRP runs, nil otherwise. MRRT is
// not implemented, so its state is off. mu held.
//
// rt-labs p-net sends the TLV on the ring ports of a client, and once
// after a role change (pf_lldp_get_mrp_port_status); go-profinet sends it
// while the role is client or manager.
//
// It is sent only while an entity runs: a configuration whose entity did
// not start (setMRPRole failed) advertises no MRP domain.
func (d *Device) mrpLLDP(p *port) *lldp.MRPPortStatus {
	st := d.mrpSt
	if st == nil || st.ent == nil || st.cfg.Role == mrp.RoleDisabled || !slices.Contains(st.cfg.RingPorts[:], p.n) {
		return nil
	}
	return &lldp.MRPPortStatus{Domain: st.cfg.Domain, MRRT: lldp.MRRTOff}
}

// mrpDomainMismatch reports whether the MRP_DomainUUID check applies to a
// port and the neighbor's MRP domain differs from the one the check names,
// which is the device's: a check is written only for the device's domain
// and dropped when an adjustment changes it. The check applies to a ring
// port while MRP runs. A neighbor that sends no MRP port status (not an
// MRP ring port, a device without the TLV, or no neighbor) is not a
// mismatch: nothing says it is in another domain. p-net instead compares
// with an empty domain (pf_pdport_check_mrp_domain_mismatch), so such a
// neighbor is a mismatch there; the standard's text is not read.
//
// The check belongs to the interface, not to an AR: a Connect's PDev
// reset (resetPDev) does not remove it, as p-net's pf_pdport_reset_ar
// resets only the port checks; the evaluator reports the mismatch again
// for the new AR. mu held.
func (d *Device) mrpDomainMismatch(subslot uint16, peer lldp.Neighbor, found bool) bool {
	st := d.mrpSt
	if st == nil || st.ent == nil || st.check == nil || st.check.Check&pnio.MrpCheckDomainUUID == 0 || st.cfg.Role == mrp.RoleDisabled || !d.isRingPort(subslot) {
		return false
	}
	return found && peer.Frame.MRPPortStatus != nil && peer.Frame.MRPPortStatus.Domain != st.check.Domain
}

// writeMRPCheck serves a write of PDInterfaceMrpDataCheck: version 1.0
// (one MRP instance), the device's own domain, and no check but the
// MRP_DomainUUID one.
func (d *Device) writeMRPCheck(st *mrpState, data []byte) error {
	blk, err := oneBlock(data)
	if err != nil {
		return err
	}
	c, err := pnio.DecodePDInterfaceMrpDataCheck(blk)
	switch {
	case errors.Is(err, profinet.ErrUnsupported):
		// Version 1.1, several MRP instances: one is implemented.
		return errUnsupportedPDev
	case err != nil:
		return rwErr(pnio.RWInvalidParameter)
	case c.Check&pnio.MrpCheckManager != 0:
		return errUnsupportedPDev
	case c.Check&^pnio.MrpCheckDomainUUID != 0:
		// Reserved bits: nothing is known that they would check.
		return rwErr(pnio.RWInvalidParameter)
	}
	// After an adjustment in progress, which may restore the check it
	// found (the RPC server runs calls of several ARs at once).
	d.mrpMu.Lock()
	defer d.mrpMu.Unlock()
	d.mu.Lock()
	if c.Domain != st.cfg.Domain {
		d.mu.Unlock()
		return rwErr(pnio.RWInvalidParameter)
	}
	st.check = &c
	d.mu.Unlock()
	d.pokePDev()
	return nil
}

// mrpParamProfiles are the timer profiles a controller can select.
var mrpParamProfiles = []mrp.Profile{mrp.Profile200, mrp.Profile500}

// writtenProfile returns the timer profile a PDInterfaceMrpDataAdjust
// selects; ok is false when its timers are not those of a profile in
// mrpParamProfiles. The units are Wireshark's (packet-dcerpc-pn-io.c): MRP_TOPchgT in
// 10 ms, MRP_TSTshortT, MRP_TSTdefaultT, MRP_LNKdownT and MRP_LNKupT in
// ms; MRP_TOPNRmax, MRP_TSTNRmax and MRP_LNKNRmax are counts. p-net's
// default MRP_LNKdownT, 20 (ms), is mrp.Profile200's link interval. A
// block whose timers are all zero selects nothing and keeps cur: the
// go-profinet controller sends MrpManagerParams with the priority only.
// That is an OT Fabric convention, not a meaning of zero known from the
// standard or another implementation. Every timer of the profile,
// MRP_TSTshortT included (mrp.Profile.TestShortInterval), is what the
// entity runs. The arithmetic is on time.Duration, which holds 65535 ×
// 10 ms without overflow.
func writtenProfile(a *pnio.PDInterfaceMrpDataAdjust, cur mrp.Profile) (mrp.Profile, bool) {
	m, c := a.Manager, a.Client
	if m != nil && m.TopChangeT == 0 && m.TopNRmax == 0 && m.TestShortT == 0 && m.TestDefaultT == 0 && m.TestNRmax == 0 {
		m = nil
	}
	if c != nil && *c == (pnio.MrpClientParams{}) {
		c = nil
	}
	if m == nil && c == nil {
		return cur, true
	}
	ms := time.Millisecond
	for _, p := range mrpParamProfiles {
		if m != nil && (time.Duration(m.TopChangeT)*10*ms != p.TopChangeInterval || int(m.TopNRmax) != p.TopChangeRepeat ||
			time.Duration(m.TestShortT)*ms != p.TestShortInterval || time.Duration(m.TestDefaultT)*ms != p.TestInterval || int(m.TestNRmax) != p.TestMax) {
			continue
		}
		if c != nil && (time.Duration(c.LinkDownT)*ms != p.LinkInterval || time.Duration(c.LinkUpT)*ms != p.LinkInterval || int(c.LinkNRmax) != p.LinkRepeat) {
			continue
		}
		return p, true
	}
	return mrp.Profile{}, false
}

// writeMRP serves a write of an MRP record; handled is false for other
// indexes.
func (d *Device) writeMRP(q pnio.WriteReq, data []byte) (handled bool, err error) {
	if !isMRPIndex(q.Index) {
		return false, nil
	}
	d.mu.Lock()
	st := d.mrpSt
	d.mu.Unlock()
	if st == nil {
		return true, errInvalidIndex
	}
	switch q.Index {
	case pnio.IndexPDInterfaceMrpDataReal, pnio.IndexPDPortMrpDataReal:
		return true, rwErr(pnio.RWAccessDenied)
	case pnio.IndexPDInterfaceMrpDataCheck:
		if !isInterface(q.Slot, q.Subslot) {
			return true, errInvalidIndex
		}
		return true, d.writeMRPCheck(st, data)
	default:
	}
	blk, err := oneBlock(data)
	if err != nil {
		return true, err
	}
	if q.Index == pnio.IndexPDPortMrpDataAdjust {
		if !d.isPort(q.Slot, q.Subslot) {
			return true, errInvalidIndex
		}
		p, err := pnio.DecodePDPortMrpData(blk)
		if err != nil || p.Real {
			return true, rwErr(pnio.RWInvalidParameter)
		}
		// After an adjustment in progress, whose domain may be refused and
		// restored (the RPC server runs calls of several ARs at once).
		d.mrpMu.Lock()
		defer d.mrpMu.Unlock()
		d.mu.Lock()
		defer d.mu.Unlock()
		if !d.isRingPort(q.Subslot) || p.Domain != st.cfg.Domain {
			return true, rwErr(pnio.RWInvalidParameter)
		}
		st.portAdj[q.Subslot] = &p
		return true, nil
	}
	if !isInterface(q.Slot, q.Subslot) {
		return true, errInvalidIndex
	}
	a, err := pnio.DecodePDInterfaceMrpDataAdjust(blk)
	if err != nil {
		return true, rwErr(pnio.RWInvalidParameter)
	}
	if a.Role > pnio.MrpRoleManager || len(a.DomainName) == 0 || len(a.DomainName) > 240 {
		return true, rwErr(pnio.RWInvalidParameter)
	}
	d.mrpMu.Lock()
	defer d.mrpMu.Unlock()
	d.mu.Lock()
	// Everything is checked before anything changes: a refused write
	// leaves the role, the priority and the timers as they were.
	prof, ok := writtenProfile(&a, st.cfg.Profile)
	if !ok {
		d.mu.Unlock()
		return true, errUnsupportedPDev
	}
	prio := st.cfg.Prio
	if a.Manager != nil && a.Manager.Prio != 0 {
		prio = a.Manager.Prio
	}
	// The same configuration changes nothing only when its entity runs:
	// after a failed start and restore it is rewritten to start it again.
	same := st.cfg.Role == mrp.Role(a.Role) && st.cfg.Domain == a.Domain && st.cfg.DomainName == a.DomainName &&
		st.cfg.Prio == prio && st.cfg.Profile == prof && (st.ent != nil) == (st.cfg.Role != mrp.RoleDisabled)
	prevCfg, prevAdjust, prevCheck := st.cfg, st.adjust, st.check
	st.adjust = &a
	st.cfg.Prio, st.cfg.Profile = prio, prof
	if a.Domain != st.cfg.Domain {
		// A check names the domain it checks, which must be the device's
		// (writeMRPCheck): it does not survive a change of domain.
		st.check = nil
	}
	d.mu.Unlock()
	if same {
		return true, nil
	}
	// The ring ports announce the new domain; the check compares with it.
	defer d.pokePDev()
	if err := d.setMRPRole(mrp.Role(a.Role), a.Domain, a.DomainName); err != nil {
		// The write is refused, so it changes nothing: the records, the
		// configuration and the entity that ran before.
		d.log.Infof("device: MRP: %v; the previous configuration is restored", err)
		d.mu.Lock()
		st.cfg, st.adjust, st.check = prevCfg, prevAdjust, prevCheck
		d.mu.Unlock()
		if rerr := d.setMRPRole(prevCfg.Role, prevCfg.Domain, prevCfg.DomainName); rerr != nil {
			// The configuration stays as it was asked for; nothing runs, so
			// nothing is advertised (mrpLLDP), reported as real or checked.
			// The ring ports keep the forwarding state the last entity left:
			// forwarding both could close a loop the manager had opened,
			// blocking one could cut the only path; which is right without
			// an entity is not established.
			d.log.Errorf("device: MRP: restoring %s: %v; no MRP entity runs", prevCfg.Role, rerr)
		}
		return true, rwErr(pnio.RWInvalidParameter)
	}
	return true, nil
}
