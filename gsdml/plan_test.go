// SPDX-License-Identifier: MIT

package gsdml

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/model"
)

// planFixture returns the parameter fixture with edits applied, and the
// model of the fixture without record 3, which ToModel refuses (a field
// without a verified encoding).
func planFixture(t *testing.T, edits ...string) (*Document, *model.Device) {
	t.Helper()
	b := parameterFixture(t)
	for i := 0; i+1 < len(edits); i += 2 {
		if !bytes.Contains(b, []byte(edits[i])) {
			t.Fatalf("%q not in the fixture", edits[i])
		}
		b = bytes.Replace(b, []byte(edits[i]), []byte(edits[i+1]), 1)
	}
	doc, err := ParseBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(b, []byte(`<ParameterRecordDataItem Index="3"`))
	j := i + bytes.Index(b[i:], []byte(`</ParameterRecordDataItem>`)) + len(`</ParameterRecordDataItem>`)
	mdoc, err := ParseBytes(append(append([]byte(nil), b[:i]...), b[j:]...))
	if err != nil {
		t.Fatal(err)
	}
	m, err := ToModel(mdoc, "")
	if err != nil {
		t.Fatal(err)
	}
	return doc, m
}

// The fixture's record 2 has TransferSequence 2 and records 1 and 3 none;
// sequenced makes it 1.
var sequenced = []string{`TransferSequence="2"`, `TransferSequence="1"`}

func TestPlanParametersDefaults(t *testing.T) {
	t.Parallel()
	doc, m := planFixture(t, sequenced...)
	plan, err := PlanParameters(doc, m, PlanOptions{Defaults: true})
	if err != nil {
		t.Fatalf("%v %v", err, plan)
	}
	// Record 2 (sequence 1) before record 1 (0, file order); record 3 is
	// left out.
	if len(plan.Writes) != 2 {
		t.Fatalf("writes %+v", plan.Writes)
	}
	w2, w1 := plan.Writes[0], plan.Writes[1]
	if w2.Write.Slot != 1 || w2.Write.Subslot != 1 || w2.Write.Index != 2 || !bytes.Equal(w2.Write.Data, fixtureDefault) ||
		w2.TransferSequence != 1 || w2.FilePosition != 1 || w2.Values != 0 || w2.ModuleIdent != 0x50 || w2.SubmoduleIdent != 0x51 || w2.Name != "Every encodable type" {
		t.Errorf("record 2 %+v", w2)
	}
	if w1.Write.Index != 1 || !bytes.Equal(w1.Write.Data, []byte{0x00, 0x64, 0x00, 0x01}) || w1.TransferSequence != 0 || w1.FilePosition != 0 {
		t.Errorf("record 1 %+v", w1)
	}
	rules := map[string]Severity{}
	for _, is := range plan.Issues {
		rules[is.Rule] = is.Severity
		if !strings.HasPrefix(is.Path, "Slot[1]/Subslot[0x0001]/") {
			t.Errorf("path %q", is.Path)
		}
	}
	want := map[string]Severity{
		RuleTransferSequence:  SeverityWarning,     // 0 among sequenced
		RuleParameterEncoding: SeverityUnvalidated, // record 3's field
		RuleParameterPlan:     SeverityUnvalidated, // record 3 not written
	}
	if len(rules) != len(want) {
		t.Errorf("issues %v", plan.Issues)
	}
	for r, s := range want {
		if rules[r] != s {
			t.Errorf("rule %s: %v, want %v (%v)", r, rules[r], s, plan.Issues)
		}
	}
}

func TestPlanParametersValues(t *testing.T) {
	t.Parallel()
	doc, m := planFixture(t, sequenced...)
	plan, err := PlanParameters(doc, m, PlanOptions{Values: []ParameterValues{
		{Slot: 1, Subslot: 1, Index: 2, Values: map[string]string{"Integer16": "-3", "Unsigned8": "20"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), fixtureDefault...)
	want[0], want[1], want[2] = 20, 0xFF, 0xFD
	if len(plan.Writes) != 1 || plan.Writes[0].Values != 2 || !bytes.Equal(plan.Writes[0].Write.Data, want) {
		t.Fatalf("%+v", plan.Writes)
	}
	// Only the written record is checked: record 3 is not reported.
	for _, is := range plan.Issues {
		if is.Rule != RuleTransferSequence {
			t.Errorf("issue %v", is)
		}
	}
}

// Values may not set a field that is not Changeable, nor one field twice
// by its ID and its name; the record's own default stays writable.
func TestPlanParametersAuthorization(t *testing.T) {
	t.Parallel()
	doc, m := planFixture(t, append(sequenced, `TextId="T_I16"`, `ID="R_I16" TextId="T_I16"`)...)
	for name, values := range map[string]map[string]string{
		"not changeable":   {"R_FIXED": "513"},
		"by name":          {"Not changeable": "513"},
		"ID and name":      {"R_I16": "1", "Integer16": "2"},
		"ID and same name": {"R_I16": "1", "Integer16": "1"},
	} {
		for range 8 { // map order: refused every time
			_, err := PlanParameters(doc, m, PlanOptions{Values: []ParameterValues{{Slot: 1, Subslot: 1, Index: 2, Values: values}}})
			if !errors.Is(err, profinet.ErrInvalidConfig) {
				t.Fatalf("%s: %v", name, err)
			}
		}
	}
	plan, err := PlanParameters(doc, m, PlanOptions{Values: []ParameterValues{{Slot: 1, Subslot: 1, Index: 2, Values: map[string]string{}}}})
	if err != nil || len(plan.Writes) != 1 || !bytes.Equal(plan.Writes[0].Write.Data[20:22], []byte{0x02, 0x01}) {
		t.Fatalf("default of R_FIXED: %v %+v", err, plan)
	}
	// A value the field's ValueItem does not assign (no AllowedValues).
	_, err = PlanParameters(doc, m, PlanOptions{Values: []ParameterValues{{Slot: 1, Subslot: 1, Index: 1, Values: map[string]string{"Mode": "3"}}}})
	if !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("ValueItem: %v", err)
	}
}

// The model must be of the file's device, not only of its module ident
// numbers.
func TestPlanParametersIdentity(t *testing.T) {
	t.Parallel()
	doc, m := planFixture(t, sequenced...)
	for name, edit := range map[string]func(m *model.Device){
		"vendor": func(m *model.Device) { m.Identity.VendorID++ },
		"device": func(m *model.Device) { m.Identity.DeviceID++ },
	} {
		mm := cloneModel(t, m)
		edit(mm)
		if _, err := PlanParameters(doc, mm, PlanOptions{Defaults: true}); !errors.Is(err, profinet.ErrInvalidConfig) {
			t.Errorf("%s: %v", name, err)
		}
	}
	id := *doc.ProfileBody.DeviceIdentity
	for name, edit := range map[string]func(d *DeviceIdentity){
		"no vendor": func(d *DeviceIdentity) { d.VendorID = "x" },
		"no device": func(d *DeviceIdentity) { d.DeviceID = "x" },
	} {
		d := id
		edit(&d)
		doc.ProfileBody.DeviceIdentity = &d
		if _, err := PlanParameters(doc, m, PlanOptions{}); !errors.Is(err, profinet.ErrMalformed) {
			t.Errorf("%s: %v", name, err)
		}
	}
	doc.ProfileBody.DeviceIdentity = nil
	if _, err := PlanParameters(doc, m, PlanOptions{}); !errors.Is(err, profinet.ErrMalformed) {
		t.Errorf("no identity: %v", err)
	}
}

func TestPlanParametersTransferSequence(t *testing.T) {
	t.Parallel()
	// The fixture as it is: sequence 2 without 1.
	doc, m := planFixture(t)
	plan, err := PlanParameters(doc, m, PlanOptions{Defaults: true})
	if !errors.Is(err, profinet.ErrInvalidConfig) || plan == nil || plan.Writes != nil || !hasIssue(plan.Issues, RuleTransferSequence, SeverityError) {
		t.Fatalf("%v %+v", err, plan)
	}
	plan, err = PlanParameters(doc, m, PlanOptions{Defaults: true, AllowSequenceErrors: true})
	if err != nil || len(plan.Writes) != 2 || plan.Writes[0].Write.Index != 2 || !hasIssue(plan.Issues, RuleTransferSequence, SeverityError) {
		t.Fatalf("allowed: %v %+v", err, plan)
	}
	// Every record sequenced: 1, 2, 3 in that order, whatever the file
	// order.
	doc, m = planFixture(t, `TransferSequence="2"`, `TransferSequence="3"`,
		`<ParameterRecordDataItem Index="1"`, `<ParameterRecordDataItem Index="1" TransferSequence="2"`,
		`<ParameterRecordDataItem Index="3" Length="3"`, `<ParameterRecordDataItem Index="3" Length="3" TransferSequence="1"`)
	plan, err = PlanParameters(doc, m, PlanOptions{Values: []ParameterValues{
		{Slot: 1, Subslot: 1, Index: 1}, {Slot: 1, Subslot: 1, Index: 2},
	}})
	if err != nil || len(plan.Writes) != 2 || plan.Writes[0].Write.Index != 1 || plan.Writes[1].Write.Index != 2 || len(plan.Issues) != 0 {
		t.Fatalf("1..3: %v %+v", err, plan)
	}
}

func TestSortTransfer(t *testing.T) {
	t.Parallel()
	ws := []PlannedWrite{
		{TransferSequence: 0, FilePosition: 0}, {TransferSequence: 2, FilePosition: 1}, {TransferSequence: 0, FilePosition: 2},
		{TransferSequence: 1, FilePosition: 3}, {TransferSequence: 2, FilePosition: 4},
	}
	sortTransfer(ws)
	var got []int
	for _, w := range ws {
		got = append(got, w.FilePosition)
	}
	if want := []int{3, 1, 4, 0, 2}; !equalInts(got, want) {
		t.Fatalf("%v, want %v", got, want)
	}
}

func TestPlanParametersErrors(t *testing.T) {
	t.Parallel()
	doc, m := planFixture(t, sequenced...)
	for name, c := range map[string]struct {
		doc  *Document
		edit func(m *model.Device)
		opts PlanOptions
		err  error
	}{
		"no model":         {doc: doc, opts: PlanOptions{}, err: profinet.ErrInvalidConfig},
		"no document":      {doc: &Document{}, err: profinet.ErrMalformed},
		"no such DAP":      {doc: doc, opts: PlanOptions{DAP: "DAP_X"}, err: profinet.ErrNotFound},
		"other DAP":        {doc: doc, edit: func(m *model.Device) { m.DAP.IdentNumber = 0x999 }, err: profinet.ErrInvalidConfig},
		"not allowed slot": {doc: doc, edit: func(m *model.Device) { m.Slots[0].Number = 3; m.Slots[2].Number = 1 }, err: profinet.ErrInvalidConfig},
		"no such slot":     {doc: doc, edit: func(m *model.Device) { m.Slots[0].Number = 9 }, err: profinet.ErrInvalidConfig},
		"not useable":      {doc: doc, edit: func(m *model.Device) { m.Slots[0].Module.IdentNumber = 0x99 }, err: profinet.ErrInvalidConfig},
		"other submodule":  {doc: doc, edit: func(m *model.Device) { m.Slots[0].Module.Submodules[0].IdentNumber = 0x99 }, err: profinet.ErrInvalidConfig},
		"two sets":         {doc: doc, opts: PlanOptions{Values: []ParameterValues{{Slot: 1, Subslot: 1, Index: 1}, {Slot: 1, Subslot: 1, Index: 1}}}, err: profinet.ErrInvalidConfig},
		"no such record":   {doc: doc, opts: PlanOptions{Values: []ParameterValues{{Slot: 1, Subslot: 1, Index: 9}}}, err: profinet.ErrInvalidConfig},
		"no such position": {doc: doc, opts: PlanOptions{Values: []ParameterValues{{Slot: 2, Subslot: 1, Index: 1}}}, err: profinet.ErrInvalidConfig},
		"no such field":    {doc: doc, opts: PlanOptions{Values: []ParameterValues{{Slot: 1, Subslot: 1, Index: 2, Values: map[string]string{"Nope": "1"}}}}, err: profinet.ErrInvalidConfig},
		"not allowed":      {doc: doc, opts: PlanOptions{Values: []ParameterValues{{Slot: 1, Subslot: 1, Index: 2, Values: map[string]string{"R_FIXED": "x"}}}}, err: profinet.ErrInvalidConfig},
		"no encoding":      {doc: doc, opts: PlanOptions{Values: []ParameterValues{{Slot: 1, Subslot: 1, Index: 3, Values: map[string]string{}}}}, err: profinet.ErrUnsupported},
	} {
		var mm *model.Device
		if name != "no model" {
			mm = cloneModel(t, m)
			if c.edit != nil {
				c.edit(mm)
			}
		}
		if _, err := PlanParameters(c.doc, mm, c.opts); !errors.Is(err, c.err) {
			t.Errorf("%s: %v, want %v", name, err, c.err)
		}
	}
	// A Check error of a written record refuses the plan: two fields with
	// one ID.
	doc, m = planFixture(t, append(sequenced, `ID="R_FIXED"`, `ID="R_FIXED"`, `TextId="T_I16"`, `ID="R_FIXED" TextId="T_I16"`)...)
	plan, err := PlanParameters(doc, m, PlanOptions{Defaults: true})
	if !errors.Is(err, profinet.ErrInvalidConfig) || !hasIssue(plan.Issues, RuleParameterLayout, SeverityError) {
		t.Fatalf("check error: %v %+v", err, plan)
	}
}

func hasIssue(is []Issue, rule string, s Severity) bool {
	for _, i := range is {
		if i.Rule == rule && i.Severity == s {
			return true
		}
	}
	return false
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cloneModel(t *testing.T, m *model.Device) *model.Device {
	t.Helper()
	c := *m
	c.Slots = append([]model.Slot(nil), m.Slots...)
	for i := range c.Slots {
		c.Slots[i].Module.Submodules = append([]model.Submodule(nil), m.Slots[i].Module.Submodules...)
	}
	return &c
}

// A long sequence is clipped in a message.
func TestClipSeq(t *testing.T) {
	t.Parallel()
	s := make([]uint16, 20)
	if len(clipSeq(s)) != 16 || len(clipSeq(s[:3])) != 3 {
		t.Fatal(clipSeq(s))
	}
}
