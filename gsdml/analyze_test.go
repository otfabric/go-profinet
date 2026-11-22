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
// its modules' slots are the model's slots.
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
			if r.DAP != daps[i].ID || r.Outcome != OutcomeConverted || r.Err != nil || r.Error != "" || r.DAPError != "" || !reflect.DeepEqual(r.Model, want) {
				t.Fatalf("%s %s: %+v", f, daps[i].ID, r)
			}
			gotJSON, _ := json.Marshal(r.Model)
			wantJSON, _ := json.Marshal(want)
			if !bytes.Equal(gotJSON, wantJSON) {
				t.Fatalf("%s %s: JSON differs", f, r.DAP)
			}
			var slots, modelSlots []uint16
			for _, m := range r.Modules {
				if m.Outcome != OutcomeConverted || m.Err != nil || m.ModuleIdent == 0 || m.ModulePath == "" {
					t.Fatalf("%s %s: %+v", f, r.DAP, m)
				}
				slots = append(slots, m.Slots...)
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
	if m := moduleOf(t, reps[0], "MOD_DI16"); !m.Plugged() || !slices.Equal(m.Slots, []uint16{1}) || m.ModuleIdent != 0x10 || m.RecordOctets == 0 {
		t.Fatalf("MOD_DI16 %+v", m)
	}
	if m := moduleOf(t, reps[0], "MOD_DIO8"); m.Plugged() {
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
	if m := moduleOf(t, reps[1], "MOD_DIO8"); !slices.Equal(m.Slots, []uint16{5}) || !reflect.DeepEqual(reps[1].Model, want) {
		t.Fatalf("taken slots %+v", m)
	}
	if b, err := json.Marshal(reps[0]); err != nil || !strings.Contains(string(b), `"outcome":"converted"`) || strings.Contains(string(b), `"Model"`) {
		t.Fatalf("JSON %s %v", b, err)
	}
}

// One module that does not convert blocks the DAP, and leaves the other
// modules reported.
func TestAnalyzeModelsBlocked(t *testing.T) {
	t.Parallel()
	_, reps := analyzeVariant(t, `<ModuleItem ID="MOD_DO16" ModuleIdentNumber="0x00000020">`, `<ModuleItem ID="MOD_DO16" ModuleIdentNumber="x">`)
	for _, r := range reps {
		if r.Outcome != OutcomeBlocked || r.Model != nil || r.DAPError != "" || !errors.Is(r.Err, profinet.ErrMalformed) {
			t.Fatalf("%s: %+v", r.DAP, r)
		}
		bad := moduleOf(t, r, "MOD_DO16")
		if bad.Outcome != OutcomeMalformed || !errors.Is(bad.Err, profinet.ErrMalformed) || !strings.Contains(bad.Error, "ModuleIdentNumber") || !bad.Plugged() {
			t.Fatalf("%s MOD_DO16 %+v", r.DAP, bad)
		}
		for _, other := range []string{"MOD_DI16", "MOD_DIO8", "MOD_AI4"} {
			if m := moduleOf(t, r, other); m.Outcome != OutcomeConverted {
				t.Fatalf("%s %s %+v", r.DAP, other, m)
			}
		}
	}
	// A field without a verified encoding: unsupported, not malformed.
	_, reps = analyzeVariant(t, `DataType="Unsigned16" DefaultValue="50"`, `DataType="Integer16_S" DefaultValue="50"`)
	if r := reps[0]; r.Outcome != OutcomeBlocked || !errors.Is(r.Err, profinet.ErrUnsupported) || moduleOf(t, r, "MOD_AI4").Outcome != OutcomeUnsupported {
		t.Fatalf("unsupported %+v", r)
	}
	// A reference to no module, and a slot list that does not parse.
	_, reps = analyzeVariant(t, `<ModuleItemRef ModuleItemTarget="MOD_DIO8" AllowedInSlots="1..8"/>`, `<ModuleItemRef ModuleItemTarget="MOD_NONE" AllowedInSlots="1..8"/>`,
		`<ModuleItemRef ModuleItemTarget="MOD_DI16" AllowedInSlots="1..16" UsedInSlots="1"/>`, `<ModuleItemRef ModuleItemTarget="MOD_DI16" AllowedInSlots="1..16" UsedInSlots="x"/>`)
	if m := moduleOf(t, reps[0], "MOD_NONE"); reps[0].Outcome != OutcomeBlocked || m.Outcome != OutcomeMalformed || m.ModulePath != "" || m.ModuleIdent != 0 {
		t.Fatalf("no module %+v", reps[0])
	}
	if m := moduleOf(t, reps[1], "MOD_DI16"); reps[1].Outcome != OutcomeBlocked || m.Outcome != OutcomeConverted || m.SlotsError == "" || m.Plugged() {
		t.Fatalf("slots %+v", reps[1])
	}
}

// A DAP whose own content does not convert has that outcome; the other
// DAP is not affected.
func TestAnalyzeModelsDAP(t *testing.T) {
	t.Parallel()
	_, reps := analyzeVariant(t, `ModuleIdentNumber="0x00000101" MinDeviceInterval="32"`, `ModuleIdentNumber="0x00000101" MinDeviceInterval="x"`)
	if r := reps[0]; r.Outcome != OutcomeMalformed || r.DAPError == "" || r.Model != nil || len(r.Modules) != 4 {
		t.Fatalf("DAP_1P %+v", r)
	}
	if r := reps[1]; r.Outcome != OutcomeConverted || r.Model == nil {
		t.Fatalf("DAP_2P %+v", r)
	}
	if _, err := AnalyzeModels(&Document{}); !errors.Is(err, profinet.ErrMalformed) {
		t.Fatalf("empty: %v", err)
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
		if r.Outcome != OutcomeResourceExhausted || !errors.Is(r.Err, profinet.ErrResourceExhausted) || r.DAPError != "" || r.Model != nil {
			t.Fatalf("%s: %v %v", r.DAP, r.Outcome, r.Err)
		}
		total := 0
		for _, m := range r.Modules {
			if m.Outcome != OutcomeConverted {
				t.Fatalf("%s %s: %v", r.DAP, m.Target, m.Err)
			}
			total += m.RecordOctets
		}
		if total <= MaxModelRecordOctets {
			t.Fatalf("%s: %d octets", r.DAP, total)
		}
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
