// SPDX-License-Identifier: MIT

package gsdml

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/model"
)

// dap1P converts DAP_1P of the modular fixture with old replaced by new.
func dap1P(t *testing.T, old, new string) (*model.Device, error) {
	t.Helper()
	doc, err := ParseBytes(fixtureVariant(t, old, new))
	if err != nil {
		t.Fatal(err)
	}
	return ToModel(doc, "DAP_1P")
}

// TestOptionalNumbersMalformed: a numeric attribute ToModel reads that is
// present but no number, or a ValueList that is no ValueList, is refused
// with ErrMalformed; before v0.12.0 ToModel went on without it (a
// MinDeviceInterval of 0, one AR, no reduction ratios, the default
// subslot, an unplugged module).
func TestOptionalNumbersMalformed(t *testing.T) {
	const dio8 = `<ModuleItemRef ModuleItemTarget="MOD_DIO8" AllowedInSlots="1..8"/>`
	const sm = `<VirtualSubmoduleItem ID="DAP_1P_SM"`
	for _, tc := range []struct{ name, old, new, attr string }{
		{"ObjectUUID_LocalIndex", `ObjectUUID_LocalIndex="1"`, `ObjectUUID_LocalIndex="one"`, "ObjectUUID_LocalIndex"},
		{"ObjectUUID_LocalIndex range", `ObjectUUID_LocalIndex="1"`, `ObjectUUID_LocalIndex="65536"`, "ObjectUUID_LocalIndex"},
		{"MinDeviceInterval", `MinDeviceInterval="32"`, `MinDeviceInterval="32ms"`, "MinDeviceInterval"},
		{"MinDeviceInterval negative", `MinDeviceInterval="32"`, `MinDeviceInterval="-32"`, "MinDeviceInterval"},
		{"NumberOfAR", `NumberOfAR="1"`, `NumberOfAR="x"`, "NumberOfAR"},
		{"NumberOfAR empty", `NumberOfAR="1"`, `NumberOfAR=""`, "NumberOfAR"},
		{"ReductionRatio", `ReductionRatio="1 2 4 8 16 32 64 128 256 512"`, `ReductionRatio="1 2 four"`, "ReductionRatio"},
		{"ReductionRatio empty", `ReductionRatio="1 2 4 8 16 32 64 128 256 512"`, `ReductionRatio=""`, "ReductionRatio"},
		{"ReductionRatio blank", `ReductionRatio="1 2 4 8 16 32 64 128 256 512"`, `ReductionRatio=" "`, "ReductionRatio"},
		{"AllowedInSlots", dio8, `<ModuleItemRef ModuleItemTarget="MOD_DIO8" AllowedInSlots="1..x"/>`, "AllowedInSlots"},
		{"AllowedInSlots empty", dio8, `<ModuleItemRef ModuleItemTarget="MOD_DIO8" AllowedInSlots=""/>`, "AllowedInSlots"},
		{"UsedInSlots empty", dio8, `<ModuleItemRef ModuleItemTarget="MOD_DIO8" AllowedInSlots="1..8" UsedInSlots=""/>`, "UsedInSlots"},
		{"FixedInSubslots", sm, sm + ` FixedInSubslots="a"`, "FixedInSubslots"},
		{"FixedInSubslots empty", sm, sm + ` FixedInSubslots=""`, "FixedInSubslots"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Validate reports it too (RELEASE.md v0.12.0 says so).
			doc, err := ParseBytes(fixtureVariant(t, tc.old, tc.new))
			if err != nil {
				t.Fatal(err)
			}
			if verr := Validate(doc); verr == nil || !strings.Contains(verr.Error(), tc.attr) {
				t.Errorf("Validate: %v", verr)
			}
			_, err = dap1P(t, tc.old, tc.new)
			if !errors.Is(err, profinet.ErrMalformed) || !strings.Contains(err.Error(), tc.attr) {
				t.Fatalf("ToModel: %v", err)
			}
		})
	}
	// A FixedInSubslots of more values than ToModel expands is refused as
	// too large, not taken for absent.
	if _, err := dap1P(t, sm, sm+` FixedInSubslots="1..100"`); !errors.Is(err, profinet.ErrResourceExhausted) {
		t.Fatalf("FixedInSubslots 1..100: %v", err)
	}
}

// TestOptionalNumbersRead: valid and absent values convert as before.
func TestOptionalNumbersRead(t *testing.T) {
	const dio8 = `<ModuleItemRef ModuleItemTarget="MOD_DIO8" AllowedInSlots="1..8"/>`
	m, err := dap1P(t, `NumberOfAR="1"`, `NumberOfAR="3"`)
	if err != nil {
		t.Fatal(err)
	}
	if m.Capabilities.MaxARs != 3 || m.Capabilities.MinDeviceInterval != 32 || m.Identity.Instance != 1 ||
		!slices.Equal(m.Capabilities.ReductionRatios, []uint16{1, 2, 4, 8, 16, 32, 64, 128, 256, 512}) {
		t.Fatalf("%+v %+v", m.Capabilities, m.Identity)
	}
	// Absent ones leave the defaults.
	m, err = dap1P(t, ` NumberOfAR="1"`, ``)
	if err != nil || m.Capabilities.MaxARs != 1 {
		t.Fatalf("absent NumberOfAR: %v %v", m, err)
	}
	// NumberOfAR="0" keeps the one AR every device serves.
	if m, err = dap1P(t, `NumberOfAR="1"`, `NumberOfAR="0"`); err != nil || m.Capabilities.MaxARs != 1 {
		t.Fatalf("NumberOfAR 0: %v %v", m, err)
	}
	// One allowed slot plugs the module there; a range of them leaves it
	// unplugged, as before.
	slotOf := func(m *model.Device) (slots []uint16) {
		for _, s := range m.Slots {
			slots = append(slots, s.Number)
		}
		return slots
	}
	one, err := dap1P(t, dio8, `<ModuleItemRef ModuleItemTarget="MOD_DIO8" AllowedInSlots="5"/>`)
	if err != nil {
		t.Fatal(err)
	}
	ranged, err := dap1P(t, dio8, dio8)
	if err != nil {
		t.Fatal(err)
	}
	if len(one.Slots) != len(ranged.Slots)+1 || !slices.Contains(slotOf(one), 5) || slices.Contains(slotOf(ranged), 5) {
		t.Fatalf("one allowed slot: %d slots %v, a range: %d", len(one.Slots), slotOf(one), len(ranged.Slots))
	}
	// FixedInSubslots places the DAP's submodule.
	if m, err = dap1P(t, `<VirtualSubmoduleItem ID="DAP_1P_SM"`, `<VirtualSubmoduleItem ID="DAP_1P_SM" FixedInSubslots="7"`); err != nil || m.DAP.Submodules[0].Subslot != 7 {
		t.Fatalf("FixedInSubslots 7: %v", err)
	}
}
