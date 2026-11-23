// SPDX-License-Identifier: MIT

// Package model describes a PROFINET IO device in Go: its identity, the
// device access point, the modules plugged into its slots and the
// submodules with their IO data and parameter records.
//
// One model drives three things: the real configuration an IO-Device
// (package device) serves, the expected configuration an IO-Controller
// (package controller) connects with, and the GSDML file package gsdml
// generates for engineering tools. Package gsdml also derives models from
// GSDML files.
package model

import (
	"cmp"
	"fmt"
	"slices"
	"sort"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/pnio"
)

// Well-known subslots of the device access point.
const (
	// SubslotDAP is the subslot of the DAP's identity submodule.
	SubslotDAP uint16 = 1
	// SubslotInterface is the subslot of the first interface.
	SubslotInterface uint16 = 0x8000
	// SubslotPort1 is the subslot of the first port of the first
	// interface.
	SubslotPort1 uint16 = 0x8001
)

// maxMinDeviceInterval is the largest MinDeviceInterval, 128 ms. The GSDML
// schema types the attribute only as Unsigned16; the bound is the one p-net
// (pf_fspm_validate_configuration) and Hilscher's firmware tag
// (usMinDeviceInterval) apply.
const maxMinDeviceInterval = 4096

// maxCSDU is the largest C_SDU of an RT frame: each direction of an
// application relationship carries at most this many octets.
const maxCSDU = 1440

// maxPlugged is the largest number of plugged submodules Validate
// accepts: each takes at least one octet of the input C_SDU and one of
// the output C_SDU (its data and provider status, or its consumer
// status), so more cannot be carried.
const maxPlugged = maxCSDU

// maxRecordOctets bounds the octets of the parameter records of all
// plugged submodules together, a module in several slots counting once
// per slot: what a device keeps and a controller writes for the model.
// It is the bound package gsdml applies to the models it converts
// (gsdml.MaxModelRecordOctets).
const maxRecordOctets = 16 << 20

// Record is a parameter record of a submodule.
type Record struct {
	// Index is the record index, 0..0x7FFF for user records.
	Index pnio.Index `json:"index"`
	// Name names the record in GSDML.
	Name string `json:"name,omitempty"`
	// Length is the exact length a write must have.
	Length uint16 `json:"length"`
	// Default is the value before the first write (zeros when nil).
	Default []byte `json:"default,omitempty"`
	// ReadOnly rejects writes.
	ReadOnly bool `json:"readOnly,omitempty"`
}

// Submodule is a submodule type: its ident number and IO data shape.
type Submodule struct {
	Name        string `json:"name"`
	IdentNumber uint32 `json:"identNumber"`
	// API is the application process identifier the submodule belongs
	// to (GSDML VirtualSubmoduleItem API): zero, the default, is the
	// profile-independent API 0. A submodule is addressed by (API, slot,
	// subslot) on the wire: in the expected and real identification, in
	// the read and write headers, in diagnoses and alarms.
	API uint32 `json:"api,omitempty"`
	// Subslot is where the submodule is plugged in its module.
	Subslot uint16 `json:"subslot"`
	// InputLen and OutputLen are the IO data lengths; both zero make a
	// NO_IO submodule.
	InputLen  uint16   `json:"inputLength,omitempty"`
	OutputLen uint16   `json:"outputLength,omitempty"`
	Records   []Record `json:"records,omitempty"`
	// Info is a free text for GSDML.
	Info string `json:"info,omitempty"`
}

// Type returns the submodule type implied by the data lengths.
func (s *Submodule) Type() pnio.SubmoduleType {
	switch {
	case s.InputLen > 0 && s.OutputLen > 0:
		return pnio.SubmoduleInputOutput
	case s.InputLen > 0:
		return pnio.SubmoduleInput
	case s.OutputLen > 0:
		return pnio.SubmoduleOutput
	default:
		return pnio.SubmoduleNoIO
	}
}

// Record returns the record with an index.
func (s *Submodule) Record(i pnio.Index) (*Record, bool) {
	for k := range s.Records {
		if s.Records[k].Index == i {
			return &s.Records[k], true
		}
	}
	return nil, false
}

// Module is a module type with the submodules it carries.
type Module struct {
	Name        string      `json:"name"`
	IdentNumber uint32      `json:"identNumber"`
	Submodules  []Submodule `json:"submodules"`
	Info        string      `json:"info,omitempty"`
}

// Submodule returns the submodule in a subslot.
func (m *Module) Submodule(subslot uint16) (*Submodule, bool) {
	for i := range m.Submodules {
		if m.Submodules[i].Subslot == subslot {
			return &m.Submodules[i], true
		}
	}
	return nil, false
}

// Slot is a module plugged into a slot.
type Slot struct {
	Number uint16 `json:"slot"`
	Module Module `json:"module"`
}

// Identity is the identification of the device type and instance.
type Identity struct {
	VendorID   uint16 `json:"vendorId"`
	DeviceID   uint16 `json:"deviceId"`
	VendorName string `json:"vendorName"`
	// TypeOfStation is the device type name (DCP Device/Vendor).
	TypeOfStation string `json:"typeOfStation"`
	Family        string `json:"family,omitempty"`
	OrderID       string `json:"orderId"`
	SerialNumber  string `json:"serialNumber,omitempty"`
	// HardwareRevision and SoftwareRevision are reported in I&M0.
	HardwareRevision uint16                `json:"hardwareRevision"`
	SoftwareRevision pnio.SoftwareRevision `json:"softwareRevision"`
	// Instance is the DeviceInstance (DCP, object UUID).
	Instance uint16 `json:"instance"`
}

// Capabilities are the communication properties the device declares.
type Capabilities struct {
	// MaxARs is the number of application relationships served at once.
	MaxARs int `json:"maxArs"`
	// MinDeviceInterval is the shortest cycle, SendClockFactor ×
	// ReductionRatio, in units of 31.25 µs (32 = 1 ms, at most 4096 =
	// 128 ms). Zero sets no lower bound on the cycle.
	MinDeviceInterval uint16 `json:"minDeviceInterval"`
	// ReductionRatios are the supported reduction ratios; empty means
	// 1, 2, 4, ..., 512.
	ReductionRatios []uint16 `json:"reductionRatios,omitempty"`
	// SupportedRTClasses lists the RT classes ("RT_CLASS_1").
	SupportedRTClasses []string `json:"supportedRtClasses,omitempty"`
	// ConformanceClass is "A", "B" or "C".
	ConformanceClass string `json:"conformanceClass"`
	// LLDP reports whether the device sends LLDP (conformance class B).
	LLDP bool `json:"lldp"`
	// Ports is the number of Ethernet ports.
	Ports int `json:"ports"`
	// DCPHello reports whether the device can send DCP Hello (GSDML
	// DCP_HelloSupported; device.Config.Hello enables it).
	DCPHello bool `json:"dcpHello,omitempty"`
}

// Device is the description of an IO-Device.
type Device struct {
	Identity     Identity     `json:"identity"`
	Capabilities Capabilities `json:"capabilities"`
	// DAP is the device access point plugged in slot 0. Its submodules
	// include the identity (subslot 1), the interface (0x8000) and the
	// ports (0x8001...).
	DAP Module `json:"dap"`
	// Slots are the modules plugged in slots 1 and up.
	Slots []Slot `json:"slots"`
	// Catalog lists further module types the device accepts (for GSDML);
	// the plugged ones are added automatically.
	Catalog []Module `json:"catalog,omitempty"`
}

// API is the profile-independent application process identifier 0: the
// default of Submodule.API, and the API of the DAP's submodules (the
// identity, the interface and the ports), which Validate requires.
const API uint32 = 0

// Module returns the module in a slot (slot 0 is the DAP).
func (d *Device) Module(slot uint16) (*Module, bool) {
	if slot == 0 {
		return &d.DAP, true
	}
	for i := range d.Slots {
		if d.Slots[i].Number == slot {
			return &d.Slots[i].Module, true
		}
	}
	return nil, false
}

// Submodule returns a plugged submodule.
func (d *Device) Submodule(slot, subslot uint16) (*Submodule, bool) {
	m, ok := d.Module(slot)
	if !ok {
		return nil, false
	}
	return m.Submodule(subslot)
}

// PluggedSubmodule is a submodule with its position.
type PluggedSubmodule struct {
	Slot        uint16
	ModuleIdent uint32
	Submodule   *Submodule
}

// Each calls fn for every plugged submodule in slot and subslot order,
// the DAP first. Submodule points into the model.
func (d *Device) Each(fn func(p PluggedSubmodule)) {
	var idx []int
	for _, m := range d.modules() {
		subs := m.mod.Submodules
		// The order is a sorted list of positions: the submodules are
		// neither copied nor searched.
		if cap(idx) < len(subs) {
			idx = make([]int, 0, len(subs))
		}
		idx = idx[:0]
		for i := range subs {
			idx = append(idx, i)
		}
		slices.SortStableFunc(idx, func(a, b int) int { return cmp.Compare(subs[a].Subslot, subs[b].Subslot) })
		for k, i := range idx {
			// Of submodules that share a subslot (Validate refuses
			// them), the first one stands for all, as Module.Submodule
			// finds it.
			if k > 0 && subs[idx[k-1]].Subslot == subs[i].Subslot {
				i = idx[k-1]
				idx[k] = i
			}
			fn(PluggedSubmodule{Slot: m.slot, ModuleIdent: m.mod.IdentNumber, Submodule: &subs[i]})
		}
	}
}

type slotModule struct {
	slot uint16
	mod  *Module
}

func (d *Device) modules() []slotModule {
	out := []slotModule{{0, &d.DAP}}
	idx := make([]int, len(d.Slots))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return d.Slots[idx[a]].Number < d.Slots[idx[b]].Number })
	for _, i := range idx {
		out = append(out, slotModule{d.Slots[i].Number, &d.Slots[i].Module})
	}
	return out
}

// ModuleTypes returns the distinct module types of the slots: the plugged
// modules and the catalog, by ident number. The DAP is not included.
func (d *Device) ModuleTypes() []Module {
	seen := map[uint32]bool{}
	var out []Module
	add := func(m Module) {
		if !seen[m.IdentNumber] {
			seen[m.IdentNumber] = true
			out = append(out, m)
		}
	}
	for _, s := range d.Slots {
		add(s.Module)
	}
	for _, m := range d.Catalog {
		add(m)
	}
	return out
}

// Validate checks the description: unique slots and subslots (a subslot
// number is unique in its slot, whatever the submodules' APIs), a DAP with
// its identity submodule and its submodules in API 0, IO lengths the C_SDU can carry, records in the
// user range. Two bounds come before the work on the submodules: at most
// 1440 plugged submodules, which is what a C_SDU carries, and at most
// 16 MiB of parameter records in the plugged submodules together.
func (d *Device) Validate() error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("model: %w: "+format, append([]any{profinet.ErrInvalidConfig}, a...)...)
	}
	if d.Identity.VendorID == 0 && d.Identity.DeviceID == 0 {
		return bad("vendor and device ID are zero")
	}
	if len(d.Identity.OrderID) > pnio.IMOrderIDLen {
		return bad("order ID %q longer than %d", d.Identity.OrderID, pnio.IMOrderIDLen)
	}
	if len(d.Identity.SerialNumber) > pnio.IMSerialLen {
		return bad("serial number %q longer than %d", d.Identity.SerialNumber, pnio.IMSerialLen)
	}
	if _, ok := d.DAP.Submodule(SubslotDAP); !ok {
		return bad("the DAP has no submodule in subslot 1")
	}
	slots := map[uint16]bool{0: true}
	for _, s := range d.Slots {
		if s.Number == 0 {
			return bad("slot 0 is the DAP's")
		}
		if slots[s.Number] {
			return bad("slot %d used twice", s.Number)
		}
		slots[s.Number] = true
	}
	// The size of the configuration, before anything is done per
	// submodule: the slots share their modules, so a small model can
	// plug a large number of submodules and records.
	plugged := len(d.DAP.Submodules)
	for i := 0; i < len(d.Slots) && plugged <= maxPlugged; i++ {
		plugged += len(d.Slots[i].Module.Submodules)
	}
	if plugged > maxPlugged {
		return bad("more than %d plugged submodules: a C_SDU cannot carry them", maxPlugged)
	}
	mods := d.modules()
	octets := 0
	for _, m := range mods {
		for i := range m.mod.Submodules {
			for _, r := range m.mod.Submodules[i].Records {
				if octets += int(r.Length); octets > maxRecordOctets {
					return bad("parameter records of more than %d octets in the plugged submodules", maxRecordOctets)
				}
			}
		}
	}
	var err error
	for _, m := range mods {
		subslots := map[uint16]bool{}
		for _, s := range m.mod.Submodules {
			if s.Subslot == 0 {
				err = bad("slot %d: subslot 0 is not allowed", m.slot)
			}
			if subslots[s.Subslot] {
				err = bad("slot %d: subslot %d used twice", m.slot, s.Subslot)
			}
			subslots[s.Subslot] = true
			if m.slot == 0 && s.API != API {
				// The device serves the DAP's identity, interface and
				// port records (I&M0 filter data, PDEV) at API 0.
				err = bad("slot 0 subslot %d: API %#x, the DAP's submodules belong to API 0", s.Subslot, s.API)
			}
			if int(s.InputLen) > 1439 || int(s.OutputLen) > 1439 {
				err = bad("slot %d subslot %d: IO data longer than the C_SDU", m.slot, s.Subslot)
			}
			idx := map[pnio.Index]bool{}
			for _, r := range s.Records {
				if !r.Index.IsUserIndex() {
					err = bad("slot %d subslot %d: record index %s outside 0..0x7FFF", m.slot, s.Subslot, r.Index)
				}
				if idx[r.Index] {
					err = bad("slot %d subslot %d: record index %s twice", m.slot, s.Subslot, r.Index)
				}
				idx[r.Index] = true
				if r.Default != nil && len(r.Default) != int(r.Length) {
					err = bad("slot %d subslot %d: record %s default of %d octets, length %d", m.slot, s.Subslot, r.Index, len(r.Default), r.Length)
				}
			}
			if err != nil {
				return err
			}
		}
	}
	// Each direction must fit one C_SDU: the input CR carries the input
	// (and NO_IO) data with their IOPS and the IOCS of the outputs, the
	// output CR the output data with their IOPS and the IOCS of the
	// inputs.
	in, out := 0, 0
	d.Each(func(p PluggedSubmodule) {
		switch p.Submodule.Type() {
		case pnio.SubmoduleInputOutput:
			in += int(p.Submodule.InputLen) + 2
			out += int(p.Submodule.OutputLen) + 2
		case pnio.SubmoduleOutput:
			out += int(p.Submodule.OutputLen) + 1
			in++
		default:
			in += int(p.Submodule.InputLen) + 1
			out++
		}
	})
	if in > maxCSDU {
		return bad("input CR of %d octets exceeds the C_SDU maximum 1440", in)
	}
	if out > maxCSDU {
		return bad("output CR of %d octets exceeds the C_SDU maximum 1440", out)
	}
	if d.Capabilities.MaxARs < 0 {
		return bad("MaxARs %d", d.Capabilities.MaxARs)
	}
	if d.Capabilities.MinDeviceInterval > maxMinDeviceInterval {
		return bad("MinDeviceInterval %d above %d (128 ms)", d.Capabilities.MinDeviceInterval, maxMinDeviceInterval)
	}
	return nil
}

// WithSlots returns a copy of the device with the DAP and only the given
// slots: the expected configuration of an IO controller that shares the
// device with others and owns only these modules. Slots the device does
// not have are ignored.
func (d *Device) WithSlots(slots ...uint16) *Device {
	keep := map[uint16]bool{}
	for _, s := range slots {
		keep[s] = true
	}
	c := *d
	c.Slots = nil
	for _, s := range d.Slots {
		if keep[s.Number] {
			c.Slots = append(c.Slots, s)
		}
	}
	return &c
}

// Expected returns the ExpectedSubmoduleBlockReq blocks (one per slot)
// that describe the plugged configuration, as a controller sends them.
// A slot's block has one API entry per API its submodules belong to, in
// API order, each with the module ident and that API's submodules; a
// module without submodules has one entry in API 0.
func (d *Device) Expected() []pnio.ExpectedSubmoduleBlockReq {
	var out []pnio.ExpectedSubmoduleBlockReq
	for _, m := range d.modules() {
		var blk pnio.ExpectedSubmoduleBlockReq
		for _, g := range byAPI(m.mod.Submodules) {
			em := pnio.ExpectedModule{API: g.api, Slot: m.slot, IdentNumber: m.mod.IdentNumber}
			for _, s := range g.subs {
				t := s.Type()
				em.Submodules = append(em.Submodules, pnio.ExpectedSubmodule{
					Subslot:      s.Subslot,
					IdentNumber:  s.IdentNumber,
					Properties:   uint16(t),
					Descriptions: pnio.NewDataDescriptions(t, s.InputLen, s.OutputLen),
				})
			}
			blk.Modules = append(blk.Modules, em)
		}
		out = append(out, blk)
	}
	return out
}

// RealIdentification returns the RealIdentificationData of the plugged
// configuration: one entry per API, in API order, with the slots (in slot
// order) that have submodules of that API; a module without submodules is
// in API 0.
func (d *Device) RealIdentification() pnio.IdentificationData {
	var id pnio.IdentificationData
	at := map[uint32]int{}
	for _, m := range d.modules() {
		for _, g := range byAPI(m.mod.Submodules) {
			i, ok := at[g.api]
			if !ok {
				i = len(id.APIs)
				at[g.api] = i
				id.APIs = append(id.APIs, pnio.IdentAPI{API: g.api})
			}
			sl := pnio.IdentSlot{Slot: m.slot, IdentNumber: m.mod.IdentNumber}
			for _, s := range g.subs {
				sl.Submodules = append(sl.Submodules, pnio.IdentSubmodule{Subslot: s.Subslot, IdentNumber: s.IdentNumber})
			}
			id.APIs[i].Slots = append(id.APIs[i].Slots, sl)
		}
	}
	sort.SliceStable(id.APIs, func(i, j int) bool { return id.APIs[i].API < id.APIs[j].API })
	return id
}

// APIs returns the APIs of the plugged submodules, in ascending order.
// API 0 is always among them: the DAP's submodules belong to it.
func (d *Device) APIs() []uint32 {
	seen := map[uint32]bool{API: true}
	out := []uint32{API}
	for _, m := range d.modules() {
		for i := range m.mod.Submodules {
			if a := m.mod.Submodules[i].API; !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
		}
	}
	slices.Sort(out)
	return out
}

type apiGroup struct {
	api  uint32
	subs []*Submodule
}

// byAPI groups a module's submodules by API, in API order, each group's
// submodules in subslot order. A module without submodules is one empty
// group in API 0, so that it keeps its entry in API 0 as before APIs.
func byAPI(subs []Submodule) []apiGroup {
	if len(subs) == 0 {
		return []apiGroup{{api: API}}
	}
	var gs []apiGroup
	for i := range subs {
		s := &subs[i]
		k := slices.IndexFunc(gs, func(g apiGroup) bool { return g.api == s.API })
		if k < 0 {
			k = len(gs)
			gs = append(gs, apiGroup{api: s.API})
		}
		gs[k].subs = append(gs[k].subs, s)
	}
	slices.SortFunc(gs, func(a, b apiGroup) int { return cmp.Compare(a.api, b.api) })
	for _, g := range gs {
		slices.SortStableFunc(g.subs, func(a, b *Submodule) int { return cmp.Compare(a.Subslot, b.Subslot) })
	}
	return gs
}

// StandardDAP returns a DAP module with the identity submodule (subslot
// 1), one interface (0x8000) and n ports (0x8001...). The ident numbers
// follow the common convention of 0x00000001 for the DAP identity,
// 0x00008000 for the interface and 0x00008001... for the ports.
func StandardDAP(name string, ident uint32, ports int) Module {
	m := Module{Name: name, IdentNumber: ident, Submodules: []Submodule{
		{Name: name, IdentNumber: 0x00000001, Subslot: SubslotDAP},
		{Name: "Interface", IdentNumber: 0x00008000, Subslot: SubslotInterface},
	}}
	for i := range ports {
		m.Submodules = append(m.Submodules, Submodule{Name: fmt.Sprintf("Port %d", i+1), IdentNumber: 0x00008001 + uint32(i), Subslot: SubslotPort1 + uint16(i)})
	}
	return m
}
