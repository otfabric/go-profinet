// SPDX-License-Identifier: MIT

package model

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet"
)

// allocated returns the octets fn allocates (runtime.MemStats.TotalAlloc).
func allocated(fn func()) uint64 {
	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	fn()
	runtime.ReadMemStats(&m1)
	return m1.TotalAlloc - m0.TotalAlloc
}

// noIO returns a module with n submodules without IO data in the
// subslots 1 to n.
func noIO(ident uint32, n int) Module {
	m := Module{IdentNumber: ident}
	for i := range n {
		m.Submodules = append(m.Submodules, Submodule{IdentNumber: 1, Subslot: uint16(i + 1)})
	}
	return m
}

// shared returns a device whose slots 1 to slots all hold the module m:
// the slots share its submodules.
func shared(m Module, slots int) *Device {
	d := validDevice()
	d.Slots, d.Catalog = nil, nil
	for i := range slots {
		d.Slots = append(d.Slots, Slot{Number: uint16(i + 1), Module: m})
	}
	return d
}

// TestValidatePluggedBound: Validate takes as many plugged submodules as a
// C_SDU carries, and refuses more before it works on them.
func TestValidatePluggedBound(t *testing.T) {
	// The DAP has three submodules; a submodule without IO data takes one
	// octet of either C_SDU, so the bound itself is a valid model.
	const dap = 3
	repeat := func(n, slots int) []int {
		out := make([]int, slots)
		for i := range out {
			out[i] = n
		}
		return out
	}
	for _, tc := range []struct {
		name string
		// slots are the submodules of the module in each slot; slots of
		// the same number share one module.
		slots []int
		dap   int
		ok    bool
	}{
		{"no slot", nil, dap, true},
		{"one submodule", []int{1}, dap, true},
		{"one below the bound", []int{479, 479, 478}, dap, true},
		{"the bound", []int{479, 479, 479}, dap, true},
		{"one above the bound", []int{479, 479, 479, 1}, dap, false},
		{"many slots of one module", repeat(2000, 64), dap, false},
		{"one large module", []int{60000}, dap, false},
		{"a device access point at the bound", nil, maxPlugged, true},
		{"a device access point above the bound", nil, maxPlugged + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := validDevice()
			d.Slots, d.Catalog = nil, nil
			mods := map[int]Module{}
			for i, n := range tc.slots {
				m, ok := mods[n]
				if !ok {
					m = noIO(uint32(0x50+len(mods)), n)
					mods[n] = m
				}
				d.Slots = append(d.Slots, Slot{Number: uint16(i + 1), Module: m})
			}
			for i := len(d.DAP.Submodules); i < tc.dap; i++ {
				d.DAP.Submodules = append(d.DAP.Submodules, Submodule{IdentNumber: 2, Subslot: uint16(0x100 + i)})
			}
			plugged := len(d.DAP.Submodules)
			for _, s := range d.Slots {
				plugged += len(s.Module.Submodules)
			}
			var err error
			a := allocated(func() { err = d.Validate() })
			if tc.ok {
				if plugged > maxPlugged || err != nil {
					t.Fatalf("%d plugged submodules: %v", plugged, err)
				}
				return
			}
			if !errors.Is(err, profinet.ErrInvalidConfig) || !strings.Contains(err.Error(), "more than 1440 plugged submodules") {
				t.Fatalf("%d plugged submodules: err %v, want the bound on plugged submodules", plugged, err)
			}
			if budget := uint64(64 << 10); a > budget {
				t.Fatalf("Validate allocated %d octets to refuse %d plugged submodules, budget %d", a, plugged, budget)
			}
		})
	}
}

// TestValidateRecordOctets: Validate bounds the parameter records of the
// plugged submodules together; a module in several slots counts once per
// slot.
func TestValidateRecordOctets(t *testing.T) {
	// A module with records of 65536 octets, which 256 slots bring to
	// the bound exactly.
	module := func(last uint16) Module {
		return Module{IdentNumber: 0x60, Submodules: []Submodule{{IdentNumber: 1, Subslot: 1, Records: []Record{
			{Index: 0x10, Length: 65535}, {Index: 0x11, Length: last},
		}}}}
	}
	const slots = maxRecordOctets / 65536
	for _, tc := range []struct {
		name   string
		slots  int
		dapLen uint16
		ok     bool
	}{
		{"no record", 0, 0, true},
		{"one slot", 1, 0, true},
		{"one octet below the bound", slots - 1, 65535, true},
		{"the bound", slots, 0, true},
		{"one octet above the bound", slots, 1, false},
		{"one slot above the bound", slots + 1, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := shared(module(1), tc.slots)
			if tc.dapLen > 0 {
				d.DAP.Submodules[0].Records = []Record{{Index: 0x20, Length: tc.dapLen}}
			}
			err := d.Validate()
			if tc.ok && err != nil {
				t.Fatalf("%d octets: %v", tc.slots*65536+int(tc.dapLen), err)
			}
			if !tc.ok && (!errors.Is(err, profinet.ErrInvalidConfig) || !strings.Contains(err.Error(), "parameter records of more than 16777216 octets")) {
				t.Fatalf("%d octets: err %v, want the bound on parameter records", tc.slots*65536+int(tc.dapLen), err)
			}
		})
	}
}

// TestEachPositions: Each visits the submodules of a module in subslot
// order through their positions: it hands out the model's own submodules,
// allocates no copy of them, and lets the first of several submodules of
// one subslot stand for all of them.
func TestEachPositions(t *testing.T) {
	const n = 20000
	d := validDevice()
	d.Slots = []Slot{{Number: 1, Module: Module{IdentNumber: 0x70}}}
	subs := make([]Submodule, n)
	for i := range subs {
		// Descending subslots, so that every one moves.
		subs[i] = Submodule{IdentNumber: uint32(i), Subslot: uint16(n - i)}
	}
	d.Slots[0].Module.Submodules = subs
	var seen int
	var prev uint16
	a := allocated(func() {
		d.Each(func(p PluggedSubmodule) {
			if p.Slot != 1 {
				return
			}
			seen++
			if p.Submodule.Subslot <= prev || p.Submodule != &subs[n-int(p.Submodule.Subslot)] {
				t.Fatalf("subslot %d after %d, or not the model's submodule", p.Submodule.Subslot, prev)
			}
			prev = p.Submodule.Subslot
		})
	})
	if seen != n {
		t.Fatalf("%d submodules visited, want %d", seen, n)
	}
	// The positions of the submodules, and nothing per submodule beyond.
	if budget := uint64(16 * n); a > budget {
		t.Fatalf("Each allocated %d octets for %d submodules, budget %d", a, n, budget)
	}

	// Several submodules of one subslot (Validate refuses them): the first
	// in the model stands for each.
	d = validDevice()
	d.Slots = []Slot{{Number: 1, Module: Module{IdentNumber: 0x71, Submodules: []Submodule{
		{Name: "c", Subslot: 7}, {Name: "a", Subslot: 3}, {Name: "d", Subslot: 7}, {Name: "b", Subslot: 3}, {Name: "e", Subslot: 7}, {Name: "f", Subslot: 1},
	}}}}
	var names []string
	d.Each(func(p PluggedSubmodule) {
		if p.Slot == 1 {
			names = append(names, p.Submodule.Name)
			if sm, _ := d.Submodule(1, p.Submodule.Subslot); sm != p.Submodule {
				t.Errorf("subslot %d: not the submodule Device.Submodule finds", p.Submodule.Subslot)
			}
		}
	})
	if got := strings.Join(names, ""); got != "faaccc" {
		t.Fatalf("visited %q, want %q", got, "faaccc")
	}
	if d.Slots[0].Module.Submodules[0].Name != "c" || d.Slots[0].Module.Submodules[5].Name != "f" {
		t.Fatal("Each reordered the model")
	}
	// An empty module.
	d.Slots = []Slot{{Number: 1, Module: Module{IdentNumber: 0x72}}}
	count := 0
	d.Each(func(PluggedSubmodule) { count++ })
	if count != len(d.DAP.Submodules) {
		t.Fatalf("%d submodules", count)
	}
}
