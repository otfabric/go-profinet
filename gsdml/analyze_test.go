// SPDX-License-Identifier: MIT

package gsdml

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet"
)

// analyzeVariant analyzes the modular fixture with edits (old, new, …)
// applied, each once.
func analyzeVariant(t *testing.T, edits ...string) (*Document, []ModelReport) {
	t.Helper()
	b, err := os.ReadFile("testdata/fixtures/GSDML-V2.43-OTFabric-ModularFixture-20261027.xml")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(edits); i += 2 {
		if !bytes.Contains(b, []byte(edits[i])) {
			t.Fatalf("the fixture no longer holds %s", edits[i])
		}
		b = bytes.Replace(b, []byte(edits[i]), []byte(edits[i+1]), 1)
	}
	doc, err := ParseBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	reps, err := AnalyzeModels(doc)
	if err != nil {
		t.Fatal(err)
	}
	return doc, reps
}

func moduleOf(t *testing.T, r ModelReport, target string) ModuleReport {
	t.Helper()
	for _, m := range r.Modules {
		if m.Target == target {
			return m
		}
	}
	t.Fatalf("%s: no module %s", r.DAP, target)
	return ModuleReport{}
}

// A converted report holds ToModel's model, structurally and in JSON, and
// its modules' configured slots are the model's slots.
func TestAnalyzeModelsEquivalence(t *testing.T) {
	t.Parallel()
	for _, f := range []string{"GSDML-V2.43-OTFabric-ModularFixture-20261027.xml", "GSDML-V2.35-OTFabric-CompactFixture-20261027.xml"} {
		b, err := os.ReadFile("testdata/fixtures/" + f)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := ParseBytes(b)
		if err != nil {
			t.Fatal(err)
		}
		reps, err := AnalyzeModels(doc)
		if err != nil {
			t.Fatal(err)
		}
		daps := doc.ProfileBody.ApplicationProcess.DeviceAccessPointList.DeviceAccessPointItems
		if len(reps) != len(daps) {
			t.Fatalf("%s: %d reports for %d DAPs", f, len(reps), len(daps))
		}
		for i, r := range reps {
			want, err := ToModel(doc, daps[i].ID)
			if err != nil {
				t.Fatal(err)
			}
			if r.DAP != daps[i].ID || r.Outcome != OutcomeConverted || r.Err != nil || r.Error != "" || r.DAPErr != nil || r.DAPError != "" || r.Blocked || !reflect.DeepEqual(r.Model, want) {
				t.Fatalf("%s %s: %+v", f, daps[i].ID, r)
			}
			gotJSON, _ := json.Marshal(r.Model)
			wantJSON, _ := json.Marshal(want)
			if !bytes.Equal(gotJSON, wantJSON) {
				t.Fatalf("%s %s: JSON differs", f, r.DAP)
			}
			var slots, modelSlots []uint16
			for _, m := range r.Modules {
				if m.ModuleOutcome != OutcomeConverted || m.ModuleErr != nil || m.ReferenceOutcome != OutcomeConverted || m.ReferenceErr != nil || m.ModuleIdent == 0 || m.ModulePath == "" {
					t.Fatalf("%s %s: %+v", f, r.DAP, m)
				}
				slots = append(slots, m.ConfiguredSlots...)
			}
			for _, s := range want.Slots {
				modelSlots = append(modelSlots, s.Number)
			}
			slices.Sort(slots)
			if !slices.Equal(slots, modelSlots) {
				t.Fatalf("%s %s: slots %v, the model's %v", f, r.DAP, slots, modelSlots)
			}
		}
	}
	// The modular fixture: DAP_1P plugs MOD_DI16 in slot 1 and lists
	// MOD_DIO8 in the catalog only.
	_, reps := analyzeVariant(t)
	if m := moduleOf(t, reps[0], "MOD_DI16"); !slices.Equal(m.ConfiguredSlots, []uint16{1}) || m.ModuleIdent != 0x10 || m.RecordOctets == 0 {
		t.Fatalf("MOD_DI16 %+v", m)
	}
	if m := moduleOf(t, reps[0], "MOD_DIO8"); len(m.ConfiguredSlots) != 0 {
		t.Fatalf("MOD_DIO8 %+v", m)
	}
	// A slot an earlier reference took, and slot 0: left out, as ToModel
	// leaves them out.
	doc, reps := analyzeVariant(t, `<ModuleItemRef ModuleItemTarget="MOD_DIO8" AllowedInSlots="1..16" UsedInSlots="4"/>`,
		`<ModuleItemRef ModuleItemTarget="MOD_DIO8" AllowedInSlots="1..16" UsedInSlots="0 1 5"/>`)
	want, err := ToModel(doc, "DAP_2P")
	if err != nil {
		t.Fatal(err)
	}
	if m := moduleOf(t, reps[1], "MOD_DIO8"); !slices.Equal(m.ConfiguredSlots, []uint16{5}) || !reflect.DeepEqual(reps[1].Model, want) {
		t.Fatalf("taken slots %+v", m)
	}
	b, err := json.Marshal(reps[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`"outcome":"converted"`, `"blocked":false`, `"reference_outcome":"converted"`, `"module_outcome":"converted"`, `"configured_slots":[1]`} {
		if !strings.Contains(string(b), s) {
			t.Fatalf("JSON without %s: %s", s, b)
		}
	}
	for _, s := range []string{`"Model"`, `"Err"`, `"ModuleErr"`, `"ReferenceErr"`, `"DAPErr"`} {
		if strings.Contains(string(b), s) {
			t.Fatalf("JSON with %s: %s", s, b)
		}
	}
}

// A module that does not convert, or a reference that is not usable,
// blocks the DAP; the outcome stays ToModel's and the other modules are
// still reported.
func TestAnalyzeModelsBlocked(t *testing.T) {
	t.Parallel()
	_, reps := analyzeVariant(t, `<ModuleItem ID="MOD_DO16" ModuleIdentNumber="0x00000020">`, `<ModuleItem ID="MOD_DO16" ModuleIdentNumber="x">`)
	for _, r := range reps {
		if r.Outcome != OutcomeMalformed || !r.Blocked || r.Model != nil || r.DAPErr != nil || !errors.Is(r.Err, profinet.ErrMalformed) {
			t.Fatalf("%s: %+v", r.DAP, r)
		}
		// An invalid module, validly referenced: the reference is fine.
		bad := moduleOf(t, r, "MOD_DO16")
		if bad.ModuleOutcome != OutcomeMalformed || !errors.Is(bad.ModuleErr, profinet.ErrMalformed) || !strings.Contains(bad.ModuleError, "ModuleIdentNumber") ||
			bad.ReferenceOutcome != OutcomeConverted || bad.ReferenceErr != nil {
			t.Fatalf("%s MOD_DO16 %+v", r.DAP, bad)
		}
		for _, other := range []string{"MOD_DI16", "MOD_DIO8", "MOD_AI4"} {
			if m := moduleOf(t, r, other); m.ModuleOutcome != OutcomeConverted || m.ReferenceOutcome != OutcomeConverted {
				t.Fatalf("%s %s %+v", r.DAP, other, m)
			}
		}
	}
	// The slots it asks for are requested positions, not a model.
	if m := moduleOf(t, reps[0], "MOD_DO16"); !slices.Equal(m.ConfiguredSlots, []uint16{2}) {
		t.Fatalf("requested slots %+v", m)
	}
	// A field without a verified encoding: unsupported, not malformed.
	_, reps = analyzeVariant(t, `DataType="Unsigned16" DefaultValue="50"`, `DataType="Integer16_S" DefaultValue="50"`)
	if r := reps[0]; r.Outcome != OutcomeUnsupported || !r.Blocked || !errors.Is(r.Err, profinet.ErrUnsupported) || moduleOf(t, r, "MOD_AI4").ModuleOutcome != OutcomeUnsupported {
		t.Fatalf("unsupported %+v", r)
	}
	// A reference to no module, and a valid module referenced with a slot
	// list that does not parse: typed reference errors.
	_, reps = analyzeVariant(t, `<ModuleItemRef ModuleItemTarget="MOD_DIO8" AllowedInSlots="1..8"/>`, `<ModuleItemRef ModuleItemTarget="MOD_NONE" AllowedInSlots="1..8"/>`,
		`<ModuleItemRef ModuleItemTarget="MOD_DI16" AllowedInSlots="1..16" UsedInSlots="1"/>`, `<ModuleItemRef ModuleItemTarget="MOD_DI16" AllowedInSlots="1..16" UsedInSlots="x"/>`)
	if m := moduleOf(t, reps[0], "MOD_NONE"); reps[0].Outcome != OutcomeMalformed || !reps[0].Blocked || m.ReferenceOutcome != OutcomeMalformed ||
		!errors.Is(m.ReferenceErr, profinet.ErrMalformed) || m.ModuleOutcome != "" || m.ModulePath != "" || m.ModuleIdent != 0 {
		t.Fatalf("no module %+v", reps[0])
	}
	if m := moduleOf(t, reps[1], "MOD_DI16"); reps[1].Outcome != OutcomeMalformed || !reps[1].Blocked || m.ModuleOutcome != OutcomeConverted ||
		m.ReferenceOutcome != OutcomeMalformed || !errors.Is(m.ReferenceErr, profinet.ErrMalformed) || m.ReferenceError == "" || len(m.ConfiguredSlots) != 0 {
		t.Fatalf("slots %+v", reps[1])
	}
}

// A DAP whose own content does not convert has that outcome; the other
// DAP is not affected.
func TestAnalyzeModelsDAP(t *testing.T) {
	t.Parallel()
	_, reps := analyzeVariant(t, `ModuleIdentNumber="0x00000101" MinDeviceInterval="32"`, `ModuleIdentNumber="0x00000101" MinDeviceInterval="x"`)
	if r := reps[0]; r.Outcome != OutcomeMalformed || r.DAPError == "" || !errors.Is(r.DAPErr, profinet.ErrMalformed) || r.Blocked || r.Model != nil || len(r.Modules) != 4 {
		t.Fatalf("DAP_1P %+v", r)
	}
	if r := reps[1]; r.Outcome != OutcomeConverted || r.Model == nil {
		t.Fatalf("DAP_2P %+v", r)
	}
	// No document, no DeviceAccessPointList, and one without items.
	empty := &Document{ProfileBody: &ProfileBody{ApplicationProcess: &ApplicationProcess{DeviceAccessPointList: &DeviceAccessPointList{}}}}
	for _, doc := range []*Document{nil, {}, empty} {
		if reps, err := AnalyzeModels(doc); !errors.Is(err, profinet.ErrMalformed) || reps != nil {
			t.Fatalf("%+v: %v %v", doc, reps, err)
		}
	}
}

// Modules that convert on their own, but whose records together exceed
// MaxModelRecordOctets: the DAP is resource_exhausted, and each module
// keeps its octets.
func TestAnalyzeModelsAggregate(t *testing.T) {
	t.Parallel()
	big := func(first string) string {
		var b strings.Builder
		const n = 130 // 130 × 65535 octets: about 8.1 MiB a module
		for i := range n {
			b.WriteString(`<ParameterRecordDataItem Index="` + strconv.Itoa(1000+i) + `" Length="65535"><Name TextId="T_DI16"/></ParameterRecordDataItem>`)
		}
		return b.String() + first
	}
	di, do := `<ParameterRecordDataItem Index="100" Length="2">`, `<ParameterRecordDataItem Index="101" Length="1">`
	_, reps := analyzeVariant(t, di, big(di), do, big(do))
	for _, r := range reps {
		if r.Outcome != OutcomeResourceExhausted || r.Blocked || !errors.Is(r.Err, profinet.ErrResourceExhausted) || r.DAPErr != nil || r.Model != nil {
			t.Fatalf("%s: %v %v", r.DAP, r.Outcome, r.Err)
		}
		total := 0
		for _, m := range r.Modules {
			if m.ModuleOutcome != OutcomeConverted || m.ReferenceOutcome != OutcomeConverted {
				t.Fatalf("%s %s: %v %v", r.DAP, m.Target, m.ModuleErr, m.ReferenceErr)
			}
			total += m.RecordOctets
		}
		if total <= MaxModelRecordOctets {
			t.Fatalf("%s: %d octets", r.DAP, total)
		}
	}
	// The records exceed the limit in slot 2 of DAP_1P, before ToModel
	// reaches the malformed MOD_AI4 of slot 3: the outcome stays
	// resource_exhausted, and MOD_AI4 blocks the DAP as well.
	_, reps = analyzeVariant(t, di, big(di), do, big(do),
		`<ModuleItem ID="MOD_AI4" ModuleIdentNumber="0x00000040">`, `<ModuleItem ID="MOD_AI4" ModuleIdentNumber="x">`)
	if r := reps[0]; r.Outcome != OutcomeResourceExhausted || !errors.Is(r.Err, profinet.ErrResourceExhausted) || !r.Blocked || moduleOf(t, r, "MOD_AI4").ModuleOutcome != OutcomeMalformed {
		t.Fatalf("%s: %v %v blocked %v", r.DAP, r.Outcome, r.Err, r.Blocked)
	}
}

func TestOutcomeOf(t *testing.T) {
	t.Parallel()
	for err, want := range map[error]Outcome{
		nil:                           OutcomeConverted,
		profinet.ErrMalformed:         OutcomeMalformed,
		profinet.ErrUnsupported:       OutcomeUnsupported,
		profinet.ErrResourceExhausted: OutcomeResourceExhausted,
		errors.New("other"):           OutcomeMalformed,
		errors.Join(profinet.ErrMalformed, profinet.ErrUnsupported): OutcomeMalformed,
	} {
		if got := outcomeOf(err); got != want {
			t.Errorf("%v: %v, want %v", err, got, want)
		}
	}
}

// Edits of the modular fixture: configurable submodules (refs, the
// SubmoduleItemRef elements) on DAP_1P, on MOD_DI16 (plugged in both
// DAPs) and on MOD_DIO8 (only in DAP_1P's catalog, plugged in DAP_2P).
func onDAP1(refs string) []string {
	return []string{"</SystemDefinedSubmoduleList>", "</SystemDefinedSubmoduleList><UseableSubmodules>" + refs + "</UseableSubmodules>"}
}

func onDI16(refs string) []string {
	return []string{"</VirtualSubmoduleList>\n        </ModuleItem>\n        <ModuleItem ID=\"MOD_DO16\"",
		"</VirtualSubmoduleList><UseableSubmodules>" + refs + "</UseableSubmodules>\n        </ModuleItem>\n        <ModuleItem ID=\"MOD_DO16\""}
}

func onDIO8(refs string) []string {
	return []string{"</VirtualSubmoduleList>\n        </ModuleItem>\n        <ModuleItem ID=\"MOD_AI4\"",
		"</VirtualSubmoduleList><UseableSubmodules>" + refs + "</UseableSubmodules>\n        </ModuleItem>\n        <ModuleItem ID=\"MOD_AI4\""}
}

// Optional configurable submodules (allowed in several subslots) are
// listed and left out: the DAPs convert to the model of the file without
// them.
func TestAnalyzeModelsOptionalSubmodules(t *testing.T) {
	t.Parallel()
	plainDoc, plain := analyzeVariant(t)
	for _, r := range plain {
		if r.OmitsSubmodules() || r.OmitsModules() || r.ConfigurableSubmodules != nil {
			t.Fatalf("%s: %+v", r.DAP, r.ConfigurableSubmodules)
		}
	}
	edits := append(onDAP1(`<SubmoduleItemRef SubmoduleItemTarget="SM_OPT" AllowedInSubslots="2..3"/>`),
		onDI16(`<SubmoduleItemRef SubmoduleItemTarget="SM_CH1" AllowedInSubslots="2..9"/><SubmoduleItemRef SubmoduleItemTarget="SM_CH2" AllowedInSubslots="10 12"/>`)...)
	doc, reps := analyzeVariant(t, edits...)
	dap1, dap2 := reps[0], reps[1]
	wantDAP := []SubmoduleRef{{Target: "SM_OPT", Path: dap1.Path + "/UseableSubmodules/SubmoduleItemRef[SM_OPT]", Outcome: OutcomeOmitted}}
	if !reflect.DeepEqual(dap1.ConfigurableSubmodules, wantDAP) || dap2.ConfigurableSubmodules != nil {
		t.Fatalf("DAP refs %+v %+v", dap1.ConfigurableSubmodules, dap2.ConfigurableSubmodules)
	}
	for i, r := range reps {
		// Optional submodules leave every module in the catalog.
		if r.Outcome != OutcomeConverted || r.Blocked || !r.OmitsSubmodules() || r.OmitsModules() {
			t.Fatalf("%s: %s blocked %v omits %v, %v", r.DAP, r.Outcome, r.Blocked, r.OmitsSubmodules(), r.OmitsModules())
		}
		di := moduleOf(t, r, "MOD_DI16")
		if len(di.ConfigurableSubmodules) != 2 || di.ConfigurableSubmodules[1].Target != "SM_CH2" || di.ConfigurableSubmodules[1].Required ||
			di.ConfigurableSubmodules[1].Path != di.ModulePath+"/UseableSubmodules/SubmoduleItemRef[SM_CH2]" {
			t.Fatalf("%s MOD_DI16 %+v", r.DAP, di.ConfigurableSubmodules)
		}
		if do := moduleOf(t, r, "MOD_DO16"); do.ConfigurableSubmodules != nil {
			t.Fatalf("%s MOD_DO16 %+v", r.DAP, do.ConfigurableSubmodules)
		}
		got, err := ToModel(doc, r.DAP)
		if err != nil {
			t.Fatal(err)
		}
		want, err := ToModel(plainDoc, r.DAP)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(r.Model, want) || !reflect.DeepEqual(plain[i].Model, want) {
			t.Fatalf("%s: optional configurable submodules changed the model", r.DAP)
		}
	}
	b, err := json.Marshal(dap1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"configurable_submodules":[{"target":"SM_OPT","path":`)) || !bytes.Contains(b, []byte(`"required":false,"outcome":"omitted"`)) {
		t.Fatalf("%s", b)
	}
	if b, _ := json.Marshal(plain[0]); bytes.Contains(b, []byte("configurable_submodules")) {
		t.Fatalf("%s", b)
	}
}

// A configurable submodule a configuration needs (fixed or used) on the
// DAP or on a plugged module fails ToModel with ErrUnsupported;
// AnalyzeModels says which and why. An unreferenced SubmoduleList alone
// changes nothing.
func TestToModelRequiredSubmodules(t *testing.T) {
	t.Parallel()
	type want struct {
		dap1, dap2 Outcome
	}
	for _, tc := range []struct {
		name  string
		edits []string
		want  want
		// where the refusal shows: "dap" (DAP_1P's own), or a module
		where    string
		subslots []uint32
	}{
		{"used in a plugged module", onDI16(`<SubmoduleItemRef SubmoduleItemTarget="SM_CH1" AllowedInSubslots="2..9" UsedInSubslots="2 3"/>`), want{OutcomeUnsupported, OutcomeUnsupported}, "MOD_DI16", []uint32{2, 3}},
		{"fixed in a plugged module", onDI16(`<SubmoduleItemRef SubmoduleItemTarget="SM_CH1" AllowedInSubslots="2" FixedInSubslots="2"/>`), want{OutcomeUnsupported, OutcomeUnsupported}, "MOD_DI16", []uint32{2}},
		{"on the DAP", onDAP1(`<SubmoduleItemRef SubmoduleItemTarget="SM_OPT" AllowedInSubslots="2..3" FixedInSubslots="2"/>`), want{OutcomeUnsupported, OutcomeConverted}, "dap", []uint32{2}},
		{"empty attribute", onDI16(`<SubmoduleItemRef SubmoduleItemTarget="SM_CH1" UsedInSubslots=""/>`), want{OutcomeMalformed, OutcomeMalformed}, "MOD_DI16", nil},
		{"no ValueList", onDI16(`<SubmoduleItemRef SubmoduleItemTarget="SM_CH1" AllowedInSubslots="x"/>`), want{OutcomeMalformed, OutcomeMalformed}, "MOD_DI16", nil},
		{"unreferenced SubmoduleList", []string{"<ModuleList>", `<SubmoduleList><SubmoduleItem ID="SM_X" SubmoduleIdentNumber="0x00000099"/></SubmoduleList><ModuleList>`}, want{OutcomeConverted, OutcomeConverted}, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc, reps := analyzeVariant(t, tc.edits...)
			for i, w := range []Outcome{tc.want.dap1, tc.want.dap2} {
				r := reps[i]
				_, err := ToModel(doc, r.DAP)
				if r.Outcome != w || outcomeOf(err) != w {
					t.Fatalf("%s: %s (ToModel %v), want %s", r.DAP, r.Outcome, err, w)
				}
				if w == OutcomeUnsupported && (!errors.Is(err, profinet.ErrUnsupported) || !strings.Contains(err.Error(), "SM_CH1") && !strings.Contains(err.Error(), "SM_OPT")) {
					t.Fatalf("%s: %v", r.DAP, err)
				}
			}
			r := reps[0]
			var refs []SubmoduleRef
			switch tc.where {
			case "":
				if r.OmitsSubmodules() || reps[1].OmitsSubmodules() {
					t.Fatal("an unreferenced SubmoduleList is reported")
				}
				return
			case "dap":
				refs = r.ConfigurableSubmodules
				if !errors.Is(r.DAPErr, profinet.ErrUnsupported) || r.Blocked {
					t.Fatalf("DAP error %v blocked %v", r.DAPErr, r.Blocked)
				}
			default:
				m := moduleOf(t, r, tc.where)
				refs = m.ConfigurableSubmodules
				if m.ModuleOutcome != tc.want.dap1 || !r.Blocked || r.DAPErr != nil {
					t.Fatalf("module %s %s blocked %v DAP %v", tc.where, m.ModuleOutcome, r.Blocked, r.DAPErr)
				}
			}
			if len(refs) != 1 || !slices.Equal(refs[0].ConfiguredSubslots, tc.subslots) || refs[0].Required != (tc.subslots != nil) || refs[0].Outcome != tc.want.dap1 || refs[0].Error == "" {
				t.Fatalf("refs %+v", refs)
			}
		})
	}
}

// A submodule only allowed, even in a single subslot, is optional.
func TestToModelSingleAllowedSubmodule(t *testing.T) {
	t.Parallel()
	doc, reps := analyzeVariant(t, onDI16(`<SubmoduleItemRef SubmoduleItemTarget="SM_CH1" AllowedInSubslots="4"/>`)...)
	for _, r := range reps {
		if _, err := ToModel(doc, r.DAP); err != nil || r.Outcome != OutcomeConverted || r.Blocked {
			t.Fatalf("%s: %s %v", r.DAP, r.Outcome, err)
		}
		if refs := moduleOf(t, r, "MOD_DI16").ConfigurableSubmodules; len(refs) != 1 || refs[0].Required || refs[0].ConfiguredSubslots != nil || refs[0].Outcome != OutcomeOmitted {
			t.Fatalf("%s: %+v", r.DAP, refs)
		}
	}
}

// A module that needs a configurable submodule is left out of the catalog
// of a DAP that only lists it (DAP_1P, MOD_DIO8), which converts, and
// refuses a DAP that plugs it (DAP_2P, slot 4).
func TestToModelOmitsCatalogModule(t *testing.T) {
	t.Parallel()
	plainDoc, _ := analyzeVariant(t)
	doc, reps := analyzeVariant(t, onDIO8(`<SubmoduleItemRef SubmoduleItemTarget="SM_CH1" UsedInSubslots="2"/>`)...)
	dap1, dap2 := reps[0], reps[1]
	got, err := ToModel(doc, "DAP_1P")
	if err != nil || dap1.Outcome != OutcomeConverted || dap1.Blocked || !dap1.OmitsModules() || dap1.OmitsSubmodules() != true {
		t.Fatalf("DAP_1P: %s blocked %v omits %v (%v)", dap1.Outcome, dap1.Blocked, dap1.OmitsModules(), err)
	}
	plain, err := ToModel(plainDoc, "DAP_1P")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Slots, plain.Slots) || !reflect.DeepEqual(got.DAP, plain.DAP) || !reflect.DeepEqual(dap1.Model, got) {
		t.Fatal("DAP_1P: the plugged configuration changed")
	}
	var idents []uint32
	for _, m := range got.Catalog {
		idents = append(idents, m.IdentNumber)
	}
	var plainIdents []uint32
	for _, m := range plain.Catalog {
		if m.IdentNumber != 0x30 {
			plainIdents = append(plainIdents, m.IdentNumber)
		}
	}
	if !slices.Equal(idents, plainIdents) || len(plainIdents) == len(plain.Catalog) {
		t.Fatalf("catalog %#x, want %#x", idents, plainIdents)
	}
	dio := moduleOf(t, dap1, "MOD_DIO8")
	if !dio.Omitted || dio.ModuleOutcome != OutcomeUnsupported || !errors.Is(dio.ModuleErr, profinet.ErrUnsupported) || !strings.Contains(dio.ModuleError, "SM_CH1") ||
		len(dio.ConfigurableSubmodules) != 1 || !dio.ConfigurableSubmodules[0].Required {
		t.Fatalf("DAP_1P MOD_DIO8 %+v", dio)
	}
	if moduleOf(t, dap1, "MOD_DI16").Omitted {
		t.Fatal("MOD_DI16 omitted")
	}
	_, err = ToModel(doc, "DAP_2P")
	if !errors.Is(err, profinet.ErrUnsupported) || dap2.Outcome != OutcomeUnsupported || !dap2.Blocked || dap2.Model != nil {
		t.Fatalf("DAP_2P: %s blocked %v (%v)", dap2.Outcome, dap2.Blocked, err)
	}
	if m := moduleOf(t, dap2, "MOD_DIO8"); m.Omitted || m.ModuleOutcome != OutcomeUnsupported {
		t.Fatalf("DAP_2P MOD_DIO8 %+v", m)
	}
	if b, _ := json.Marshal(dio); !bytes.Contains(b, []byte(`"omitted":true`)) {
		t.Fatalf("%s", b)
	}
	// Malformed subslot attributes of a catalog module fail every DAP, as
	// any malformed module does.
	bdoc, breps := analyzeVariant(t, onDIO8(`<SubmoduleItemRef SubmoduleItemTarget="SM_CH1" UsedInSubslots="y"/>`)...)
	for _, r := range breps {
		if _, err := ToModel(bdoc, r.DAP); !errors.Is(err, profinet.ErrMalformed) || r.Outcome != OutcomeMalformed || !r.Blocked || moduleOf(t, r, "MOD_DIO8").Omitted {
			t.Fatalf("%s: %s %v", r.DAP, r.Outcome, err)
		}
	}
}
