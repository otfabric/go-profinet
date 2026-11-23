// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/otfabric/go-profinet/gsdml"
	"github.com/otfabric/go-profinet/model"
)

// maxSamples bounds the addresses an AddressCheck lists of each kind; the
// counts are complete.
const maxSamples = 10

// AddressCheck compares, for a converted DAP, the addresses of its plugged
// configuration as the GSDML file states them with the addresses of the
// model ToModel builds, as a device built from the model serves them
// (model.Device.Each).
//
// An address is a submodule's slot and subslot, with its API and
// SubmoduleIdentNumber, and the record indexes of its parameter records.
// The expected side is read from the document's attributes here, not
// from the model:
//
//   - the DAP is slot 0; its virtual submodules, interfaces and ports are
//     its submodules;
//   - a module is in the slots AnalyzeModels reports for its reference
//     (ModuleReport.ConfiguredSlots, the rule ToModel plugs by);
//   - a virtual submodule's subslot is the first value of its
//     FixedInSubslots; without one, ToModel's rule: 1 for the DAP's,
//     the subslot after the previous submodule's for a module's;
//   - an interface's subslot is its SubslotNumber, 32768 when absent (the
//     schema's default); a port's is its SubslotNumber;
//   - an API that is absent is 0.
//
// Two submodules the document puts at one slot and subslot, or two
// records of one index in a submodule, are a collision: the model keeps
// both, and a device built from it serves the first only.
type AddressCheck struct {
	// Addresses and Records count the expected submodule addresses and
	// their record indexes.
	Addresses int `json:"addresses"`
	Records   int `json:"records"`
	// Lost counts the expected addresses the model does not serve, Extra
	// the addresses it serves that the document does not state, Changed
	// those it serves with another API or SubmoduleIdentNumber.
	Lost    int `json:"lost"`
	Extra   int `json:"extra"`
	Changed int `json:"changed"`
	// RecordsLost and RecordsExtra count the record indexes missing from,
	// or added to, an address the model serves.
	RecordsLost  int `json:"records_lost"`
	RecordsExtra int `json:"records_extra"`
	// Collisions counts the addresses and record indexes the document
	// states twice.
	Collisions int `json:"collisions"`
	// Unread is set when the document's addresses of a DAP ToModel
	// converts cannot be read here: an attribute this check parses and
	// ToModel does not.
	Unread bool `json:"unread,omitempty"`
	// Samples names up to maxSamples addresses of each kind.
	Samples []string `json:"samples,omitempty"`
}

// Failed reports whether the model does not keep the document's addresses.
func (c *AddressCheck) Failed() bool {
	return c != nil && (c.Unread || c.Lost+c.Extra+c.Changed+c.RecordsLost+c.RecordsExtra > 0)
}

// checkAddresses is the AddressCheck of a DAP ToModel converts, nil for
// one it does not.
func checkAddresses(ix docIndex, rep gsdml.ModelReport) *AddressCheck {
	if rep.Outcome != gsdml.OutcomeConverted || rep.Model == nil {
		return nil
	}
	want, ok := expectedAddresses(ix, rep)
	if !ok {
		return &AddressCheck{Unread: true}
	}
	return compareAddresses(want, modelAddresses(rep.Model))
}

// collapse collapses the white space of an xsd:token, as ToModel compares
// module IDs.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

type slotSubslot struct{ slot, subslot uint16 }

type addrEntry struct {
	api, ident uint32
	records    []uint16
}

// inventory is a set of addresses, with the collisions found building it.
type inventory struct {
	addrs      map[slotSubslot]addrEntry
	collisions []string
}

func newInventory() *inventory { return &inventory{addrs: map[slotSubslot]addrEntry{}} }

// add puts a submodule at an address; of two at one address the first
// stays, as model.Device.Each serves it.
func (inv *inventory) add(slot, subslot uint16, e addrEntry) {
	k := slotSubslot{slot, subslot}
	seen := map[uint16]bool{}
	var recs []uint16
	for _, r := range e.records {
		if seen[r] {
			inv.collisions = append(inv.collisions, fmt.Sprintf("%s record 0x%04X twice", addrName(k, e.api), r))
			continue
		}
		seen[r] = true
		recs = append(recs, r)
	}
	slices.Sort(recs)
	e.records = recs
	if _, ok := inv.addrs[k]; ok {
		inv.collisions = append(inv.collisions, fmt.Sprintf("%s twice", addrName(k, e.api)))
		return
	}
	inv.addrs[k] = e
}

func addrName(k slotSubslot, api uint32) string {
	return fmt.Sprintf("API 0x%X slot %d subslot 0x%04X", api, k.slot, k.subslot)
}

// expectedAddresses reads the addresses of a DAP's plugged configuration
// from the document. It returns false when an attribute does not parse:
// ToModel converts no such DAP.
func expectedAddresses(ix docIndex, rep gsdml.ModelReport) (*inventory, bool) {
	inv := newInventory()
	dap := ix.daps[rep.DAP]
	if dap == nil {
		return nil, false
	}
	virtual := func(l *gsdml.VirtualSubmoduleList, slot, first uint16, follow bool) bool {
		if l == nil {
			return true
		}
		next := first
		for _, vs := range l.VirtualSubmoduleItems {
			e, subslot, ok := virtualEntry(vs, next)
			if !ok {
				return false
			}
			if follow {
				next = subslot + 1
			}
			inv.add(slot, subslot, e)
		}
		return true
	}
	if !virtual(dap.VirtualSubmoduleList, 0, 1, false) {
		return nil, false
	}
	if sd := dap.SystemDefinedSubmoduleList; sd != nil {
		for _, it := range sd.InterfaceSubmoduleItems {
			ss := it.SubslotNumber
			if ss == "" {
				ss = "32768"
			}
			if !systemEntry(inv, ss, it.SubmoduleIdentNumber) {
				return nil, false
			}
		}
		for _, it := range sd.PortSubmoduleItems {
			if !systemEntry(inv, it.SubslotNumber, it.SubmoduleIdentNumber) {
				return nil, false
			}
		}
	}
	for _, m := range rep.Modules {
		if len(m.ConfiguredSlots) == 0 {
			continue
		}
		mi := ix.modules[collapse(m.Target)]
		if mi == nil {
			return nil, false
		}
		for _, s := range m.ConfiguredSlots {
			if !virtual(mi.VirtualSubmoduleList, s, 1, true) {
				return nil, false
			}
		}
	}
	return inv, true
}

func virtualEntry(vs *gsdml.VirtualSubmoduleItem, def uint16) (addrEntry, uint16, bool) {
	var e addrEntry
	var err error
	if e.ident, err = gsdml.ParseUint32(vs.SubmoduleIdentNumber); err != nil {
		return e, 0, false
	}
	if vs.API != "" {
		if e.api, err = gsdml.ParseUint32(vs.API); err != nil {
			return e, 0, false
		}
	}
	subslot := def
	if vs.FixedInSubslots != "" {
		l, err := gsdml.ParseValueList(vs.FixedInSubslots, 64)
		if err != nil || len(l) == 0 || l[0] > 0xFFFF {
			return e, 0, false
		}
		subslot = uint16(l[0])
	}
	for _, r := range recordsOf(vs) {
		idx, err := gsdml.ParseUint16(r.Index)
		if err != nil {
			return e, 0, false
		}
		e.records = append(e.records, idx)
	}
	return e, subslot, true
}

func systemEntry(inv *inventory, subslot, ident string) bool {
	ss, err := gsdml.ParseUint16(subslot)
	if err != nil {
		return false
	}
	id, err := gsdml.ParseUint32(ident)
	if err != nil {
		return false
	}
	inv.add(0, ss, addrEntry{ident: id})
	return true
}

// modelAddresses are the addresses a device built from the model serves.
func modelAddresses(m *model.Device) *inventory {
	inv := newInventory()
	m.Each(func(p model.PluggedSubmodule) {
		e := addrEntry{api: p.Submodule.API, ident: p.Submodule.IdentNumber}
		for _, r := range p.Submodule.Records {
			e.records = append(e.records, uint16(r.Index))
		}
		inv.add(p.Slot, p.Submodule.Subslot, e)
	})
	return inv
}

// compareAddresses compares the expected addresses with the model's.
func compareAddresses(want, got *inventory) *AddressCheck {
	c := &AddressCheck{Addresses: len(want.addrs), Collisions: len(want.collisions)}
	sample := func(format string, a ...any) {
		if len(c.Samples) < 4*maxSamples {
			c.Samples = append(c.Samples, fmt.Sprintf(format, a...))
		}
	}
	for _, s := range want.collisions {
		if len(c.Samples) < maxSamples {
			c.Samples = append(c.Samples, "collision: "+s)
		}
	}
	for _, k := range sortedAddrs(want.addrs) {
		w := want.addrs[k]
		c.Records += len(w.records)
		g, ok := got.addrs[k]
		switch {
		case !ok:
			c.Lost++
			sample("lost: %s ident 0x%X", addrName(k, w.api), w.ident)
			continue
		case g.api != w.api || g.ident != w.ident:
			c.Changed++
			sample("changed: %s ident 0x%X is API 0x%X ident 0x%X in the model", addrName(k, w.api), w.ident, g.api, g.ident)
			continue
		}
		for _, r := range w.records {
			if _, found := slices.BinarySearch(g.records, r); !found {
				c.RecordsLost++
				sample("record lost: %s record 0x%04X", addrName(k, w.api), r)
			}
		}
		for _, r := range g.records {
			if _, found := slices.BinarySearch(w.records, r); !found {
				c.RecordsExtra++
				sample("record extra: %s record 0x%04X", addrName(k, w.api), r)
			}
		}
	}
	for _, k := range sortedAddrs(got.addrs) {
		if _, ok := want.addrs[k]; !ok {
			c.Extra++
			sample("extra: %s ident 0x%X", addrName(k, got.addrs[k].api), got.addrs[k].ident)
		}
	}
	return c
}

func sortedAddrs(m map[slotSubslot]addrEntry) []slotSubslot {
	out := make([]slotSubslot, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].slot != out[j].slot {
			return out[i].slot < out[j].slot
		}
		return out[i].subslot < out[j].subslot
	})
	return out
}
