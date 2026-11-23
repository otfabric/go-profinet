// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet/gsdml"
	"github.com/otfabric/go-profinet/model"
)

// The modular fixture's DAP_1P: slot 0 with the DAP's submodule (subslot
// 1), its interface (0x8000) and port (0x8001); MOD_DI16, MOD_DO16 and
// MOD_AI4 in slots 1, 2 and 3, each with one submodule in subslot 1;
// MOD_DIO8 only in the catalog.
func TestAddressCheck(t *testing.T) {
	t.Parallel()
	r := analyze(fixture(t, modular))
	a := r.DAPs[0].Addresses
	if a == nil || a.Failed() || a.Addresses != 6 || a.Records != 3 || a.Collisions != 0 || len(a.Samples) != 0 {
		t.Fatalf("%+v", a)
	}
	// DAP_2P also plugs MOD_DIO8, in slot 4, and has a second port.
	if a := r.DAPs[1].Addresses; a == nil || a.Failed() || a.Addresses != 8 {
		t.Fatalf("DAP_2P %+v", a)
	}
	// A submodule of API 0x4E01 with a record of the profile range: the
	// model keeps its address and record index.
	b := fixture(t, modular,
		`<VirtualSubmoduleItem ID="SM_DI16"`, `<VirtualSubmoduleItem API="19969" ID="SM_DI16"`,
		`<ParameterRecordDataItem Index="100" Length="2">`, `<ParameterRecordDataItem Index="46592" Length="2">`)
	doc, reps := analyzeDoc(t, b)
	want, ok := expectedAddresses(indexOf(doc), reps[0])
	if e, found := want.addrs[slotSubslot{1, 1}]; !ok || !found || e.api != 0x4E01 || e.ident != 0x11 || !slices.Equal(e.records, []uint16{0xB600}) {
		t.Fatalf("expected %+v %v", want.addrs, ok)
	}
	if a := checkAddresses(indexOf(doc), reps[0]); a.Failed() || a.Addresses != 6 {
		t.Fatalf("%+v", a)
	}
	// FixedInSubslots places a module's submodule (MOD_DIO8, slot 4 of
	// DAP_2P) in subslot 3.
	two := fixture(t, modular, `<VirtualSubmoduleItem ID="SM_DIO8" SubmoduleIdentNumber="0x00000031"`, `<VirtualSubmoduleItem ID="SM_DIO8" SubmoduleIdentNumber="0x00000031" FixedInSubslots="3"`)
	doc, reps = analyzeDoc(t, two)
	want, _ = expectedAddresses(indexOf(doc), reps[1])
	if _, found := want.addrs[slotSubslot{4, 3}]; !found {
		t.Fatalf("FixedInSubslots: %+v", want.addrs)
	}
	if a := checkAddresses(indexOf(doc), reps[1]); a.Failed() {
		t.Fatalf("%+v", a)
	}
}

func analyzeDoc(t *testing.T, b []byte) (*gsdml.Document, []gsdml.ModelReport) {
	t.Helper()
	doc, err := gsdml.ParseBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	reps, err := gsdml.AnalyzeModels(doc)
	if err != nil {
		t.Fatal(err)
	}
	return doc, reps
}

// A model that loses, adds or changes an address or a record index fails
// the check, with the address named.
func TestAddressCheckMismatch(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		mutate func(m *model.Device)
		count  func(a *AddressCheck) int
		sample string
	}{
		{"lost submodule", func(m *model.Device) { m.Slots = m.Slots[1:] }, func(a *AddressCheck) int { return a.Lost }, "lost: API 0x0 slot 1 subslot 0x0001 ident 0x11"},
		{"extra submodule", func(m *model.Device) {
			m.Slots = append(m.Slots, model.Slot{Number: 7, Module: model.Module{IdentNumber: 0x70, Submodules: []model.Submodule{{Subslot: 1, IdentNumber: 0x71}}}})
		}, func(a *AddressCheck) int { return a.Extra }, "extra: API 0x0 slot 7 subslot 0x0001 ident 0x71"},
		{"changed API", func(m *model.Device) { m.Slots[0].Module.Submodules[0].API = 0x4E01 }, func(a *AddressCheck) int { return a.Changed },
			"changed: API 0x0 slot 1 subslot 0x0001 ident 0x11 is API 0x4E01 ident 0x11 in the model"},
		{"changed ident", func(m *model.Device) { m.Slots[1].Module.Submodules[0].IdentNumber = 0x99 }, func(a *AddressCheck) int { return a.Changed },
			"changed: API 0x0 slot 2 subslot 0x0001 ident 0x21 is API 0x0 ident 0x99 in the model"},
		{"moved subslot", func(m *model.Device) { m.Slots[2].Module.Submodules[0].Subslot = 2 }, func(a *AddressCheck) int { return a.Lost + a.Extra }, "lost: API 0x0 slot 3 subslot 0x0001 ident 0x41"},
		{"lost record", func(m *model.Device) { m.Slots[0].Module.Submodules[0].Records = nil }, func(a *AddressCheck) int { return a.RecordsLost },
			"record lost: API 0x0 slot 1 subslot 0x0001 record 0x0064"},
		{"changed record index", func(m *model.Device) { m.Slots[2].Module.Submodules[0].Records[0].Index = 0xB600 }, func(a *AddressCheck) int { return a.RecordsLost + a.RecordsExtra },
			"record extra: API 0x0 slot 3 subslot 0x0001 record 0xB600"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			doc, reps := analyzeDoc(t, fixture(t, modular))
			want, ok := expectedAddresses(indexOf(doc), reps[0])
			if !ok {
				t.Fatal("unread")
			}
			// The modules of the slots share their submodules with the
			// catalog: copy them before the change.
			m := reps[0].Model
			for i := range m.Slots {
				subs := slices.Clone(m.Slots[i].Module.Submodules)
				for j := range subs {
					subs[j].Records = slices.Clone(subs[j].Records)
				}
				m.Slots[i].Module.Submodules = subs
			}
			c.mutate(m)
			a := compareAddresses(want, modelAddresses(m))
			if !a.Failed() || c.count(a) == 0 || !slices.Contains(a.Samples, c.sample) {
				t.Fatalf("%+v", a)
			}
		})
	}
}

// Two submodules the file puts at one address, or one record index twice
// in a submodule: a collision, reported and not a failure, since the
// model serves the first as the file's first.
func TestAddressCollision(t *testing.T) {
	t.Parallel()
	r := analyze(fixture(t, modular, `<PortSubmoduleItem ID="P1_1P" SubslotNumber="32769"`, `<PortSubmoduleItem ID="P1_1P" SubslotNumber="32768"`))
	a := r.DAPs[0].Addresses
	if a == nil || a.Failed() || a.Collisions != 1 || a.Addresses != 5 || !slices.Contains(a.Samples, "collision: API 0x0 slot 0 subslot 0x8000 twice") {
		t.Fatalf("%+v", a)
	}
	inv := newInventory()
	inv.add(1, 1, addrEntry{records: []uint16{0xB600, 0x10, 0xB600}})
	if len(inv.collisions) != 1 || inv.collisions[0] != "API 0x0 slot 1 subslot 0x0001 record 0xB600 twice" || !slices.Equal(inv.addrs[slotSubslot{1, 1}].records, []uint16{0x10, 0xB600}) {
		t.Fatalf("%+v", inv)
	}
	if s := Summarize([]FileResult{r}).Fidelity; s.AddressCollisions != 1 || s.DAPsAddressCollide != 1 || s.DAPsAddressFailed != 0 {
		t.Fatalf("%+v", s)
	}
}

// The document's addresses of a converted DAP that cannot be read fail
// the check; a DAP ToModel does not convert is not checked.
func TestAddressUnread(t *testing.T) {
	t.Parallel()
	conv := gsdml.ModelReport{DAP: "none", Outcome: gsdml.OutcomeConverted, Model: &model.Device{}}
	if a := checkAddresses(docIndex{}, conv); a == nil || !a.Unread || !a.Failed() {
		t.Fatalf("%+v", a)
	}
	if a := checkAddresses(docIndex{}, gsdml.ModelReport{Outcome: gsdml.OutcomeMalformed}); a != nil || a.Failed() {
		t.Fatalf("%+v", a)
	}
	// Converted without a model: inconsistent, so it fails.
	if a := checkAddresses(docIndex{}, gsdml.ModelReport{Outcome: gsdml.OutcomeConverted}); a == nil || !a.Unread || !a.Failed() {
		t.Fatalf("no model: %+v", a)
	}
	var ix docIndex
	for name, edit := range map[string]func(){
		"module":  func() { delete(ix.modules, "MOD_DI16") },
		"ident":   func() { ix.daps["DAP_1P"].VirtualSubmoduleList.VirtualSubmoduleItems[0].SubmoduleIdentNumber = "x" },
		"api":     func() { ix.modules["MOD_DO16"].VirtualSubmoduleList.VirtualSubmoduleItems[0].API = "x" },
		"subslot": func() { ix.modules["MOD_AI4"].VirtualSubmoduleList.VirtualSubmoduleItems[0].FixedInSubslots = "70000" },
		"record":  func() { recordsOf(ix.modules["MOD_AI4"].VirtualSubmoduleList.VirtualSubmoduleItems[0])[0].Index = "x" },
		"port":    func() { ix.daps["DAP_1P"].SystemDefinedSubmoduleList.PortSubmoduleItems[0].SubslotNumber = "x" },
		"interface": func() {
			ix.daps["DAP_1P"].SystemDefinedSubmoduleList.InterfaceSubmoduleItems[0].SubmoduleIdentNumber = "x"
		},
		"interface def": func() { ix.daps["DAP_1P"].SystemDefinedSubmoduleList.InterfaceSubmoduleItems[0].SubslotNumber = "" },
	} {
		doc, reps := analyzeDoc(t, fixture(t, modular))
		ix = indexOf(doc)
		edit()
		a := checkAddresses(ix, reps[0])
		if name == "interface def" {
			// absent: the schema's default, 32768
			if a.Failed() {
				t.Fatalf("%s: %+v", name, a)
			}
			continue
		}
		if !a.Unread {
			t.Fatalf("%s: %+v", name, a)
		}
	}
}

// The summary and the matrix carry the comparison; a failed one makes the
// run's exit status 1.
func TestAddressSummary(t *testing.T) {
	t.Parallel()
	r := analyze(fixture(t, modular))
	f := Summarize([]FileResult{r}).Fidelity
	if f.DAPsAddressChecked != 2 || f.Addresses != 14 || f.AddressRecords != 6 || f.DAPsAddressFailed != 0 {
		t.Fatalf("%+v", f)
	}
	if md := Markdown(Summarize([]FileResult{r}), ""); !strings.Contains(md, "### Addresses") || !strings.Contains(md, "| 2 | 14 | 6 | 0 |") {
		t.Fatal(md)
	}
	var stderr bytes.Buffer
	if addressGate(f, &stderr) != 0 || stderr.Len() != 0 {
		t.Fatal(stderr.String())
	}
	r.DAPs[0].Addresses = &AddressCheck{Addresses: 6, Lost: 1}
	f = Summarize([]FileResult{r}).Fidelity
	if addressGate(f, &stderr) != 1 || !strings.Contains(stderr.String(), "address fidelity: 1 of 2 converted DAPs: 1 lost") {
		t.Fatalf("%+v %s", f, stderr.String())
	}
}

// Each kind keeps its own ten samples: many lost addresses do not hide the
// extra ones, the changed ones or the records.
func TestAddressSamplesPerKind(t *testing.T) {
	t.Parallel()
	want, got := newInventory(), newInventory()
	for i := uint16(1); i <= 15; i++ {
		want.add(1, i, addrEntry{ident: 1})                          // lost
		got.add(2, i, addrEntry{ident: 2})                           // extra
		want.add(3, i, addrEntry{ident: 3})                          // changed
		got.add(3, i, addrEntry{ident: 4})                           //
		want.add(4, i, addrEntry{ident: 5, records: []uint16{i}})    // record lost
		got.add(4, i, addrEntry{ident: 5, records: []uint16{0x100}}) // record extra
		want.add(5, i, addrEntry{})
		want.add(5, i, addrEntry{}) // collision
		got.add(5, i, addrEntry{})
	}
	a := compareAddresses(want, got)
	if a.Lost != 15 || a.Extra != 15 || a.Changed != 15 || a.RecordsLost != 15 || a.RecordsExtra != 15 || a.Collisions != 15 {
		t.Fatalf("%+v", a)
	}
	kinds := map[string]int{}
	for _, s := range a.Samples {
		kinds[s[:strings.Index(s, ": ")]]++
	}
	for _, k := range []string{"lost", "extra", "changed", "record lost", "record extra", "collision"} {
		if kinds[k] != maxSamples {
			t.Fatalf("%s: %d samples, want %d: %v", k, kinds[k], maxSamples, kinds)
		}
	}
	if len(a.Samples) != 6*maxSamples {
		t.Fatalf("%d samples", len(a.Samples))
	}
}
