// SPDX-License-Identifier: MIT

package gsdml

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/model"
)

// configList is a SubmoduleList for the modular fixture: submodules a
// UseableSubmodules can name.
const configList = `<SubmoduleList>
  <SubmoduleItem ID="SM_CH" SubmoduleIdentNumber="0x00000051" MayIssueProcessAlarm="false">
    <IOData><Input><DataItem DataType="Unsigned8" TextId="T_DIO8_In"/></Input></IOData>
    <RecordDataList>
      <ParameterRecordDataItem Index="200" Length="2">
        <Name TextId="T_DI_Par"/>
        <Ref ByteOffset="0" DataType="Unsigned8" DefaultValue="5" AllowedValues="1..9" TextId="T_Filter"/>
        <Ref ByteOffset="1" DataType="Unsigned8" DefaultValue="1" Changeable="false" TextId="T_Range"/>
      </ParameterRecordDataItem>
    </RecordDataList>
    <ModuleInfo><Name TextId="T_DIO8"/><InfoText TextId="T_DIO8_Info"/></ModuleInfo>
  </SubmoduleItem>
  <SubmoduleItem ID="SM_CHO" SubmoduleIdentNumber="0x00000052" MayIssueProcessAlarm="false">
    <IOData><Output><DataItem DataType="Unsigned16" TextId="T_DIO8_Out"/></Output></IOData>
    <ModuleInfo><Name TextId="T_DO16"/><InfoText TextId="T_DO16_Info"/></ModuleInfo>
  </SubmoduleItem>
  <SubmoduleItem ID="SM_NOIO" SubmoduleIdentNumber="0x00000053" MayIssueProcessAlarm="false">
    <IOData/>
    <ModuleInfo><Name TextId="T_AI4"/><InfoText TextId="T_AI4_Info"/></ModuleInfo>
  </SubmoduleItem>
  <SubmoduleItem ID="SM_BAD" SubmoduleIdentNumber="0x00000054" MayIssueProcessAlarm="false">
    <IOData/>
    <RecordDataList><ParameterRecordDataItem Index="x" Length="1"><Name TextId="T_DI_Par"/></ParameterRecordDataItem></RecordDataList>
    <ModuleInfo><Name TextId="T_AI4"/><InfoText TextId="T_AI4_Info"/></ModuleInfo>
  </SubmoduleItem>
  <SubmoduleItem ID="SM_API" SubmoduleIdentNumber="0x00000055" API="15872" MayIssueProcessAlarm="false">
    <IOData/>
    <ModuleInfo><Name TextId="T_AI4"/><InfoText TextId="T_AI4_Info"/></ModuleInfo>
  </SubmoduleItem>
  <SubmoduleItem ID="SM_BIG" SubmoduleIdentNumber="0x00000056" MayIssueProcessAlarm="false">
    <IOData/>
    <RecordDataList><ParameterRecordDataItem Index="300" Length="65535"><Name TextId="T_DI_Par"/><Const Data="0x01"/></ParameterRecordDataItem></RecordDataList>
    <ModuleInfo><Name TextId="T_AI4"/><InfoText TextId="T_AI4_Info"/></ModuleInfo>
  </SubmoduleItem>
  <PortSubmoduleItem ID="PORT_2" TextId="T_P2" SubmoduleIdentNumber="0x00008002">
    <MAUTypeList><MAUTypeItem Value="16"/></MAUTypeList>
    <ModuleInfo><Name TextId="T_P2"/><InfoText TextId="T_P2"/></ModuleInfo>
  </PortSubmoduleItem>
</SubmoduleList>`

// withList adds configList to the edits of the modular fixture.
func withList(edits ...string) []string {
	return append([]string{"</ModuleList>", "</ModuleList>" + configList}, edits...)
}

// configOf is the default configuration of a DAP of the modular fixture
// with edits.
func configOf(t *testing.T, dap string, edits ...string) (*Document, *Configuration) {
	t.Helper()
	doc, _ := analyzeVariant(t, withList(edits...)...)
	c, err := NewConfiguration(doc, dap)
	if err != nil {
		t.Fatal(err)
	}
	return doc, c
}

func resolve(t *testing.T, c *Configuration) *model.Device {
	t.Helper()
	m, err := c.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// subslots lists the subslots and ident numbers of a module's submodules.
func subslots(m model.Module) string {
	var b strings.Builder
	for _, s := range m.Submodules {
		fmt.Fprintf(&b, "%d:%#x ", s.Subslot, s.IdentNumber)
	}
	return strings.TrimSpace(b.String())
}

func isErr(t *testing.T, err error, target error, contains ...string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error %v, want %v", err, target)
	}
	for _, s := range contains {
		if !strings.Contains(err.Error(), s) {
			t.Fatalf("error %q without %q", err, s)
		}
	}
}

// Without configurable submodules, the default configuration is ToModel's
// model, and so is a SubmoduleList no reference names.
func TestConfigurationIsToModel(t *testing.T) {
	t.Parallel()
	for _, edits := range [][]string{nil, withList()} {
		doc, _ := analyzeVariant(t, edits...)
		for _, dap := range []string{"DAP_1P", "DAP_2P", ""} {
			want, err := ToModel(doc, dap)
			if err != nil {
				t.Fatal(err)
			}
			c, err := NewConfiguration(doc, dap)
			if err != nil {
				t.Fatal(err)
			}
			if got := resolve(t, c); !reflect.DeepEqual(got, want) {
				t.Fatalf("%s: %+v, want %+v", dap, got, want)
			}
			if len(c.DAP()) != 0 || len(c.Modules()) != 4 {
				t.Fatalf("%s: %d DAP options, %d modules", dap, len(c.DAP()), len(c.Modules()))
			}
		}
	}
	// The compact fixture, whose DAP has no modules.
	doc := parseFixture(t, "GSDML-V2.35-OTFabric-CompactFixture-20261027.xml")
	want, err := ToModel(doc, "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewConfiguration(doc, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := resolve(t, c); !reflect.DeepEqual(got, want) {
		t.Fatalf("compact: %+v, want %+v", got, want)
	}
}

func parseFixture(t *testing.T, name string) *Document {
	t.Helper()
	b, err := os.ReadFile("testdata/fixtures/" + name)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ParseBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// di16Refs makes MOD_DI16 need configurable submodules: SM_CH fixed in
// subslot 2, SM_CHO used in 3 (allowed in 3 and 4), SM_NOIO allowed in 3
// to 5.
var di16Refs = onDI16(`<SubmoduleItemRef SubmoduleItemTarget="SM_CH" AllowedInSubslots="2" FixedInSubslots="2"/>` +
	`<SubmoduleItemRef SubmoduleItemTarget="SM_CHO" AllowedInSubslots="3..4" UsedInSubslots="3"/>` +
	`<SubmoduleItemRef SubmoduleItemTarget="SM_NOIO" AllowedInSubslots="3..5"/>`)

// A configuration ToModel refuses (MOD_DI16, plugged in slot 1, needs
// configurable submodules) resolves by default, to a model that passes
// Validate and whose records PlanParameters plans.
func TestConfigurationDefault(t *testing.T) {
	t.Parallel()
	doc, c := configOf(t, "DAP_1P", di16Refs...)
	if _, err := ToModel(doc, "DAP_1P"); !errors.Is(err, profinet.ErrUnsupported) {
		t.Fatalf("ToModel %v", err)
	}
	m := resolve(t, c)
	di, ok := m.Module(1)
	if !ok || subslots(*di) != "1:0x11 2:0x51 3:0x52" {
		t.Fatalf("slot 1: %s", subslots(*di))
	}
	ch := di.Submodules[1]
	if ch.Name != "DIO 8" || ch.InputLen != 1 || ch.OutputLen != 0 || len(ch.Records) != 1 || ch.Records[0].Index != 200 ||
		!slices.Equal(ch.Records[0].Default, []byte{5, 1}) || di.Submodules[2].OutputLen != 2 {
		t.Fatalf("SM_CH %+v, SM_CHO %+v", ch, di.Submodules[2])
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	// The other slots are ToModel's, and the catalog has MOD_DI16 with its
	// default submodules.
	plain, err := ToModel(parseFixture(t, "GSDML-V2.43-OTFabric-ModularFixture-20261027.xml"), "DAP_1P")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Slots) != len(plain.Slots) || !reflect.DeepEqual(m.Slots[1:], plain.Slots[1:]) || !reflect.DeepEqual(m.DAP, plain.DAP) {
		t.Fatalf("slots %+v", m.Slots)
	}
	if len(m.Catalog) != 4 || subslots(m.Catalog[0]) != "1:0x11 2:0x51 3:0x52" {
		t.Fatalf("catalog %+v", m.Catalog)
	}
	// The options.
	mods := c.Modules()
	opts := mods[0].Submodules
	if mods[0].ID != "MOD_DI16" || mods[0].AllowedInSlots != "1..8" || mods[0].UsedInSlots != "1" || mods[0].Err != nil || len(opts) != 3 {
		t.Fatalf("modules %+v", mods[0])
	}
	if o := opts[0]; o.ID != "SM_CH" || !o.Fixed || !slices.Equal(o.Defaults, []uint16{2}) || o.Submodule.Subslot != 0 || o.Submodule.IdentNumber != 0x51 || !o.Allows(2) || o.Allows(3) {
		t.Fatalf("SM_CH %+v", o)
	}
	if o := opts[1]; o.Fixed || !slices.Equal(o.Defaults, []uint16{3}) || o.UsedInSubslots != "3" || o.AllowedInSubslots != "3..4" || !o.Allows(4) {
		t.Fatalf("SM_CHO %+v", o)
	}
	if o := opts[2]; o.Defaults != nil || !o.Allows(5) || o.Allows(6) {
		t.Fatalf("SM_NOIO %+v", o)
	}
	// What the accessors return is a copy.
	mods[0].Module.Submodules[0].Name = "changed"
	opts[0].Submodule.Records[0].Default[0] = 99
	c.DAP()
	if m2 := resolve(t, c); !reflect.DeepEqual(m2, m) {
		t.Fatal("an accessor's result changed the configuration")
	}
	// The parameters: the configurable submodule's record is described
	// and planned, a value set by name.
	recs, err := SubmoduleParameters(doc, "DAP_1P", 0x10, 0x51)
	if err != nil || len(recs) != 1 || recs[0].Index != 200 || len(recs[0].Fields) != 2 {
		t.Fatalf("records %+v %v", recs, err)
	}
	plan, err := PlanParameters(doc, m, PlanOptions{DAP: "DAP_1P", Defaults: true,
		Values: []ParameterValues{{Slot: 1, Subslot: 2, Index: 200, Values: map[string]string{"Filter": "7"}}}})
	if err != nil {
		t.Fatalf("%v %+v", err, plan.Issues)
	}
	found := false
	for _, w := range plan.Writes {
		if w.Write.Slot == 1 && w.Write.Subslot == 2 && w.Write.Index == 200 {
			found = slices.Equal(w.Write.Data, []byte{7, 1}) && w.SubmoduleIdent == 0x51
		}
	}
	if !found {
		t.Fatalf("plan %+v", plan.Writes)
	}
}

// Two slots of one module hold different submodules; plugging a module
// again starts from its defaults.
func TestConfigurationInstances(t *testing.T) {
	t.Parallel()
	doc, c := configOf(t, "DAP_1P", di16Refs...)
	if err := c.Plug(5, "MOD_DI16"); err != nil {
		t.Fatal(err)
	}
	for _, step := range []error{c.Select(1, 4, "SM_CHO"), c.Select(1, 3, "SM_NOIO"), c.Select(5, 5, "SM_NOIO"), c.ClearSubslot(5, 3), c.Select(1, 2, "SM_CH")} {
		if step != nil {
			t.Fatal(step)
		}
	}
	m := resolve(t, c)
	s1, _ := m.Module(1)
	s5, _ := m.Module(5)
	if subslots(*s1) != "1:0x11 2:0x51 3:0x53 4:0x52" || subslots(*s5) != "1:0x11 2:0x51 5:0x53" {
		t.Fatalf("slot 1 %s, slot 5 %s", subslots(*s1), subslots(*s5))
	}
	// The instances share no records.
	s1.Submodules[1].Records[0].Default[0] = 9
	if s5.Submodules[1].Records[0].Default[0] != 5 {
		t.Fatal("slots 1 and 5 share a record")
	}
	if _, err := PlanParameters(doc, m, PlanOptions{DAP: "DAP_1P", Defaults: true}); err != nil {
		t.Fatal(err)
	}
	if id, ok := c.Module(5); !ok || id != "MOD_DI16" || !slices.Equal(c.Slots(), []uint16{1, 2, 3, 5}) {
		t.Fatalf("slot 5 %q %v, slots %v", id, ok, c.Slots())
	}
	if err := c.Plug(5, "MOD_DI16"); err != nil {
		t.Fatal(err)
	}
	m = resolve(t, c)
	if s5, _ := m.Module(5); subslots(*s5) != "1:0x11 2:0x51 3:0x52" {
		t.Fatalf("replugged slot 5 %s", subslots(*s5))
	}
	if err := c.Unplug(5); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Module(5); ok {
		t.Fatal("slot 5 still plugged")
	}
	if err := c.PlugIdent(6, 0x30); err != nil {
		t.Fatal(err)
	}
	if id, _ := c.Module(6); id != "MOD_DIO8" {
		t.Fatalf("slot 6 %q", id)
	}
}

// The DAP's own configurable submodule: a port of the SubmoduleList used
// in subslot 0x8002, which ToModel refuses.
func TestConfigurationDAPPort(t *testing.T) {
	t.Parallel()
	edits := onDAP1(`<SubmoduleItemRef SubmoduleItemTarget="PORT_2" AllowedInSubslots="32770" UsedInSubslots="32770"/>`)
	doc, c := configOf(t, "DAP_1P", edits...)
	if _, err := ToModel(doc, "DAP_1P"); !errors.Is(err, profinet.ErrUnsupported) {
		t.Fatalf("ToModel %v", err)
	}
	if o := c.DAP(); len(o) != 1 || !o[0].Port || o[0].Submodule.IdentNumber != 0x8002 || o[0].Submodule.Name != "X1 P2" {
		t.Fatalf("options %+v", o)
	}
	m := resolve(t, c)
	if subslots(m.DAP) != "1:0x1 32768:0x8000 32769:0x8001 32770:0x8002" || m.Capabilities.Ports != 2 {
		t.Fatalf("DAP %s, %d ports", subslots(m.DAP), m.Capabilities.Ports)
	}
	if _, err := PlanParameters(doc, m, PlanOptions{DAP: "DAP_1P", Defaults: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.ClearSubslot(0, 0x8002); err != nil {
		t.Fatal(err)
	}
	if m := resolve(t, c); m.Capabilities.Ports != 1 || len(m.DAP.Submodules) != 3 {
		t.Fatalf("cleared: %s, %d ports", subslots(m.DAP), m.Capabilities.Ports)
	}
	if err := c.Select(0, 0x8002, "PORT_2"); err != nil {
		t.Fatal(err)
	}
	isErr(t, c.Select(0, 0x8001, "PORT_2"), profinet.ErrInvalidConfig, "built-in", "X1 P1")
	isErr(t, c.Select(0, 0x8003, "PORT_2"), profinet.ErrInvalidConfig, "not allowed in subslot 32771")
	// PhysicalSubslots of the DAP bound the configurable subslots.
	_, c = configOf(t, "DAP_1P", append(edits, `PhysicalSlots="0..8" ModuleIdentNumber="0x00000101"`, `PhysicalSlots="0..8" PhysicalSubslots="1 32768..32770" ModuleIdentNumber="0x00000101"`)...)
	resolve(t, c)
	doc, _ = analyzeVariant(t, withList(append(edits, `PhysicalSlots="0..8" ModuleIdentNumber="0x00000101"`, `PhysicalSlots="0..8" PhysicalSubslots="1 32768 32769" ModuleIdentNumber="0x00000101"`)...)...)
	_, err := NewConfiguration(doc, "DAP_1P")
	isErr(t, err, profinet.ErrMalformed, "PhysicalSubslots")
	// A DAP submodule of an API other than 0 cannot be chosen.
	_, c = configOf(t, "DAP_1P", onDAP1(`<SubmoduleItemRef SubmoduleItemTarget="SM_API" AllowedInSubslots="2"/>`)...)
	if o := c.DAP(); !errors.Is(o[0].Err, profinet.ErrUnsupported) || o[0].Error == "" {
		t.Fatalf("SM_API %+v", o[0])
	}
	isErr(t, c.Select(0, 2, "SM_API"), profinet.ErrUnsupported, "API")
	resolve(t, c)
	// The DAP's own collisions refuse the configuration.
	doc, _ = analyzeVariant(t, withList(onDAP1(`<SubmoduleItemRef SubmoduleItemTarget="SM_NOIO" FixedInSubslots="1"/>`)...)...)
	_, err = NewConfiguration(doc, "DAP_1P")
	isErr(t, err, profinet.ErrMalformed, "built-in")
}

// Every choice that does not fit the file is refused, and leaves the
// configuration as it was.
func TestConfigurationChoiceErrors(t *testing.T) {
	t.Parallel()
	_, c := configOf(t, "DAP_1P", onDI16(`<SubmoduleItemRef SubmoduleItemTarget="SM_CH" AllowedInSubslots="2" FixedInSubslots="2"/>`+
		`<SubmoduleItemRef SubmoduleItemTarget="SM_CHO" AllowedInSubslots="3..4" UsedInSubslots="3"/>`+
		`<SubmoduleItemRef SubmoduleItemTarget="SM_NOIO" AllowedInSubslots="3..5"/>`+
		`<SubmoduleItemRef SubmoduleItemTarget="SM_BAD" AllowedInSubslots="6"/>`+
		`<SubmoduleItemRef SubmoduleItemTarget="PORT_2" AllowedInSubslots="7"/>`)...)
	before := resolve(t, c)
	for _, tc := range []struct {
		name     string
		err      error
		target   error
		contains []string
	}{
		{"no module", c.Select(4, 2, "SM_NOIO"), profinet.ErrInvalidConfig, []string{"slot 4 holds no module"}},
		{"unknown submodule", c.Select(1, 3, "SM_X"), profinet.ErrInvalidConfig, []string{"SM_X", "module MOD_DI16"}},
		{"another module's submodule", c.Select(2, 3, "SM_NOIO"), profinet.ErrInvalidConfig, []string{"module MOD_DO16"}},
		{"subslot 0", c.Select(1, 0, "SM_NOIO"), profinet.ErrInvalidConfig, []string{"subslot 0"}},
		{"virtual subslot", c.Select(1, 1, "SM_NOIO"), profinet.ErrInvalidConfig, []string{"built-in", "DI 16"}},
		{"fixed subslot", c.Select(1, 2, "SM_NOIO"), profinet.ErrInvalidConfig, []string{"SM_CH", "fixed"}},
		{"not allowed", c.Select(1, 9, "SM_NOIO"), profinet.ErrInvalidConfig, []string{"not allowed in subslot 9", "3..5"}},
		{"fixed one elsewhere", c.Select(1, 3, "SM_CH"), profinet.ErrInvalidConfig, []string{"not allowed in subslot 3"}},
		{"malformed item", c.Select(1, 6, "SM_BAD"), profinet.ErrMalformed, []string{"SM_BAD"}},
		{"port of a module", c.Select(1, 7, "PORT_2"), profinet.ErrUnsupported, []string{"ports in the DAP only"}},
		{"clear fixed", c.ClearSubslot(1, 2), profinet.ErrInvalidConfig, []string{"fixed"}},
		{"clear virtual", c.ClearSubslot(1, 1), profinet.ErrInvalidConfig, []string{"built-in"}},
		{"clear subslot 0", c.ClearSubslot(1, 0), profinet.ErrInvalidConfig, []string{"subslot 0"}},
		{"clear no module", c.ClearSubslot(4, 3), profinet.ErrInvalidConfig, []string{"slot 4"}},
		{"plug unknown", c.Plug(4, "MOD_X"), profinet.ErrInvalidConfig, []string{"MOD_X"}},
		{"plug slot 0", c.Plug(0, "MOD_DI16"), profinet.ErrInvalidConfig, []string{"slot 0"}},
		{"plug above 0x7FFF", c.Plug(0x8000, "MOD_DI16"), profinet.ErrInvalidConfig, []string{"0x7FFF"}},
		{"plug outside PhysicalSlots", c.Plug(9, "MOD_DI16"), profinet.ErrInvalidConfig, []string{"PhysicalSlots"}},
		{"plug ident unknown", c.PlugIdent(4, 0x99), profinet.ErrInvalidConfig, []string{"0x00000099"}},
		{"unplug empty", c.Unplug(4), profinet.ErrInvalidConfig, []string{"slot 4 holds no module"}},
	} {
		if tc.err == nil {
			t.Fatalf("%s: no error", tc.name)
		}
		if !errors.Is(tc.err, tc.target) {
			t.Fatalf("%s: %v, want %v", tc.name, tc.err, tc.target)
		}
		for _, s := range tc.contains {
			if !strings.Contains(tc.err.Error(), s) {
				t.Fatalf("%s: %q without %q", tc.name, tc.err, s)
			}
		}
	}
	if after := resolve(t, c); !reflect.DeepEqual(after, before) {
		t.Fatal("a refused choice changed the configuration")
	}
	if o := c.Modules()[0].Submodules[3]; !errors.Is(o.Err, profinet.ErrMalformed) || o.Error == "" {
		t.Fatalf("SM_BAD %+v", o)
	}
	// Module slot rules: AllowedInSlots, FixedInSlots, two modules of one
	// ident.
	_, c = configOf(t, "DAP_1P", `<ModuleItemRef ModuleItemTarget="MOD_DO16" AllowedInSlots="1..8" UsedInSlots="2"/>`, `<ModuleItemRef ModuleItemTarget="MOD_DO16" AllowedInSlots="2..3" FixedInSlots="2"/>`,
		`ModuleItem ID="MOD_AI4" ModuleIdentNumber="0x00000040"`, `ModuleItem ID="MOD_AI4" ModuleIdentNumber="0x00000030"`)
	isErr(t, c.Plug(5, "MOD_DO16"), profinet.ErrInvalidConfig, "AllowedInSlots")
	isErr(t, c.Plug(2, "MOD_DIO8"), profinet.ErrInvalidConfig, "fixed there")
	isErr(t, c.Unplug(2), profinet.ErrInvalidConfig, "fixed there")
	isErr(t, c.PlugIdent(5, 0x30), profinet.ErrInvalidConfig, "MOD_DIO8", "MOD_AI4")
	if err := c.Plug(2, "MOD_DO16"); err != nil {
		t.Fatal(err)
	}
	if err := c.Plug(3, "MOD_DO16"); err != nil {
		t.Fatal(err)
	}
}

// Several submodules used in one subslot by default: the configuration
// must choose, and the error names them.
func TestConfigurationAmbiguous(t *testing.T) {
	t.Parallel()
	_, c := configOf(t, "DAP_1P", onDI16(`<SubmoduleItemRef SubmoduleItemTarget="SM_CHO" AllowedInSubslots="3" UsedInSubslots="3"/>`+
		`<SubmoduleItemRef SubmoduleItemTarget="SM_NOIO" AllowedInSubslots="3" UsedInSubslots="3"/>`)...)
	_, err := c.Resolve()
	isErr(t, err, profinet.ErrInvalidConfig, "slot 1 subslot 3", "SM_CHO", "SM_NOIO", "Select one")
	if err := c.Select(1, 3, "SM_NOIO"); err != nil {
		t.Fatal(err)
	}
	m := resolve(t, c)
	if s, _ := m.Module(1); subslots(*s) != "1:0x11 3:0x53" {
		t.Fatalf("slot 1 %s", subslots(*s))
	}
	if err := c.ClearSubslot(1, 3); err != nil {
		t.Fatal(err)
	}
	if s, _ := resolve(t, c).Module(1); subslots(*s) != "1:0x11" {
		t.Fatalf("cleared slot 1 %s", subslots(*s))
	}
	// The ambiguous module stays out of the catalog.
	if m := resolve(t, c); len(m.Catalog) != 3 {
		t.Fatalf("catalog of %d", len(m.Catalog))
	}
}

// A module whose configurable submodules do not agree with the file
// cannot be plugged; one plugged by default refuses Resolve; one only in
// the catalog is left out of it.
func TestConfigurationModuleErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, refs, physical string
		contains             string
	}{
		{"fixed on a virtual submodule", `<SubmoduleItemRef SubmoduleItemTarget="SM_NOIO" FixedInSubslots="1"/>`, "", "built-in"},
		{"used on a virtual submodule", `<SubmoduleItemRef SubmoduleItemTarget="SM_NOIO" UsedInSubslots="1"/>`, "", "built-in"},
		{"two fixed", `<SubmoduleItemRef SubmoduleItemTarget="SM_NOIO" FixedInSubslots="2"/><SubmoduleItemRef SubmoduleItemTarget="SM_CHO" FixedInSubslots="2"/>`, "", "both fixed"},
		{"used where fixed", `<SubmoduleItemRef SubmoduleItemTarget="SM_NOIO" FixedInSubslots="2"/><SubmoduleItemRef SubmoduleItemTarget="SM_CHO" UsedInSubslots="2"/>`, "", "is used where"},
		{"outside PhysicalSubslots", `<SubmoduleItemRef SubmoduleItemTarget="SM_NOIO" UsedInSubslots="5"/>`, "1..4", "PhysicalSubslots"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			edits := onDIO8(tc.refs)
			if tc.physical != "" {
				edits = append(edits, `ModuleItem ID="MOD_DIO8" ModuleIdentNumber="0x00000030"`, `ModuleItem ID="MOD_DIO8" ModuleIdentNumber="0x00000030" PhysicalSubslots="`+tc.physical+`"`)
			}
			_, c := configOf(t, "DAP_1P", edits...)
			mo := c.Modules()[2]
			if mo.ID != "MOD_DIO8" || !errors.Is(mo.Err, profinet.ErrMalformed) || !strings.Contains(mo.Error, tc.contains) {
				t.Fatalf("%+v", mo)
			}
			m := resolve(t, c)
			if len(m.Catalog) != 3 {
				t.Fatalf("catalog of %d", len(m.Catalog))
			}
			err := c.Plug(4, "MOD_DIO8")
			isErr(t, err, profinet.ErrInvalidConfig, tc.contains)
			isErr(t, err, profinet.ErrMalformed)
			// DAP_2P plugs it in slot 4.
			_, c2 := configOf(t, "DAP_2P", edits...)
			_, err = c2.Resolve()
			isErr(t, err, profinet.ErrMalformed, tc.contains)
		})
	}
	// A reference to no item of the SubmoduleList: its option says so;
	// used by default, Resolve fails.
	_, c := configOf(t, "DAP_1P", onDI16(`<SubmoduleItemRef SubmoduleItemTarget="SM_NONE" UsedInSubslots="2"/>`)...)
	if o := c.Modules()[0].Submodules[0]; !errors.Is(o.Err, profinet.ErrMalformed) || !strings.Contains(o.Error, "SM_NONE") {
		t.Fatalf("SM_NONE %+v", o)
	}
	_, err := c.Resolve()
	isErr(t, err, profinet.ErrMalformed, "slot 1 subslot 2", "SM_NONE")
	if err := c.ClearSubslot(1, 2); err != nil {
		t.Fatal(err)
	}
	resolve(t, c)
}

// Content that does not convert fails NewConfiguration, as it fails
// ToModel.
func TestNewConfigurationErrors(t *testing.T) {
	t.Parallel()
	many := strings.Repeat(`<SubmoduleItemRef SubmoduleItemTarget="SM_NOIO" AllowedInSubslots="2"/>`, maxModuleSubmodules+1)
	for _, tc := range []struct {
		name   string
		edits  []string
		target error
	}{
		{"not a ValueList", onDIO8(`<SubmoduleItemRef SubmoduleItemTarget="SM_NOIO" UsedInSubslots="x"/>`), profinet.ErrMalformed},
		{"empty attribute", onDIO8(`<SubmoduleItemRef SubmoduleItemTarget="SM_NOIO" AllowedInSubslots=""/>`), profinet.ErrMalformed},
		{"subslot 0", onDIO8(`<SubmoduleItemRef SubmoduleItemTarget="SM_NOIO" UsedInSubslots="0"/>`), profinet.ErrMalformed},
		{"subslot above 65535", onDIO8(`<SubmoduleItemRef SubmoduleItemTarget="SM_NOIO" FixedInSubslots="65536"/>`), profinet.ErrMalformed},
		{"too many subslots", onDIO8(`<SubmoduleItemRef SubmoduleItemTarget="SM_NOIO" UsedInSubslots="1..5000"/>`), profinet.ErrResourceExhausted},
		{"too many references", onDIO8(many), profinet.ErrMalformed},
		{"PhysicalSubslots", []string{`ModuleItem ID="MOD_DIO8" ModuleIdentNumber="0x00000030"`, `ModuleItem ID="MOD_DIO8" ModuleIdentNumber="0x00000030" PhysicalSubslots="y"`}, profinet.ErrMalformed},
		{"empty PhysicalSubslots", []string{`ModuleItem ID="MOD_DIO8" ModuleIdentNumber="0x00000030"`, `ModuleItem ID="MOD_DIO8" ModuleIdentNumber="0x00000030" PhysicalSubslots=""`}, profinet.ErrMalformed},
		{"no such module", []string{`ModuleItemTarget="MOD_DIO8" AllowedInSlots="1..8"`, `ModuleItemTarget="MOD_NONE" AllowedInSlots="1..8"`}, profinet.ErrMalformed},
		{"module does not convert", []string{`ModuleItem ID="MOD_DIO8" ModuleIdentNumber="0x00000030"`, `ModuleItem ID="MOD_DIO8" ModuleIdentNumber="z"`}, profinet.ErrMalformed},
		{"module slots", []string{`ModuleItemTarget="MOD_DIO8" AllowedInSlots="1..8"`, `ModuleItemTarget="MOD_DIO8" AllowedInSlots="1..8" UsedInSlots="q"`}, profinet.ErrMalformed},
		{"DAP subslot attribute", onDAP1(`<SubmoduleItemRef SubmoduleItemTarget="SM_NOIO" UsedInSubslots="q"/>`), profinet.ErrMalformed},
		{"DAP PhysicalSubslots", []string{`PhysicalSlots="0..8" ModuleIdentNumber="0x00000101"`, `PhysicalSlots="0..8" PhysicalSubslots="w" ModuleIdentNumber="0x00000101"`}, profinet.ErrMalformed},
	} {
		doc, _ := analyzeVariant(t, withList(tc.edits...)...)
		if _, err := NewConfiguration(doc, "DAP_1P"); !errors.Is(err, tc.target) {
			t.Fatalf("%s: %v, want %v", tc.name, err, tc.target)
		}
	}
	doc, _ := analyzeVariant(t)
	if _, err := NewConfiguration(doc, "DAP_X"); !errors.Is(err, profinet.ErrNotFound) {
		t.Fatalf("unknown DAP: %v", err)
	}
	if _, err := NewConfiguration(&Document{}, ""); !errors.Is(err, profinet.ErrMalformed) {
		t.Fatalf("empty document: %v", err)
	}
}

// The bounds: parameter records beyond MaxModelRecordOctets in the plugged
// submodules, more submodules than a C_SDU carries, and default subslots
// beyond maxDefaultPlacements.
func TestConfigurationBounds(t *testing.T) {
	t.Parallel()
	// SM_BIG has a record of 65535 octets: 300 of them are beyond 16 MiB.
	_, c := configOf(t, "DAP_1P", onDI16(`<SubmoduleItemRef SubmoduleItemTarget="SM_BIG" UsedInSubslots="2..301"/>`)...)
	_, err := c.Resolve()
	isErr(t, err, profinet.ErrResourceExhausted)
	_, c = configOf(t, "DAP_1P", onDI16(`<SubmoduleItemRef SubmoduleItemTarget="SM_NOIO" UsedInSubslots="2..1441"/>`)...)
	_, err = c.Resolve()
	isErr(t, err, profinet.ErrInvalidConfig, "C_SDU")
	_, c = configOf(t, "DAP_1P", onDAP1(`<SubmoduleItemRef SubmoduleItemTarget="SM_NOIO" UsedInSubslots="2..1441"/>`)...)
	_, err = c.Resolve()
	isErr(t, err, profinet.ErrInvalidConfig, "C_SDU")
	refs := strings.Repeat(`<SubmoduleItemRef SubmoduleItemTarget="SM_NOIO" UsedInSubslots="1..4096"/>`, maxDefaultPlacements/4096+1)
	_, c = configOf(t, "DAP_1P", onDIO8(refs)...)
	if mo := c.Modules()[2]; !errors.Is(mo.Err, profinet.ErrResourceExhausted) {
		t.Fatalf("MOD_DIO8 %+v", mo.Err)
	}
	// A model Validate refuses: the outputs exceed the C_SDU.
	_, c = configOf(t, "DAP_1P", onDI16(`<SubmoduleItemRef SubmoduleItemTarget="SM_CHO" UsedInSubslots="2..800"/>`)...)
	_, err = c.Resolve()
	isErr(t, err, profinet.ErrInvalidConfig, "output CR")
}

// PlanParameters checks a configurable submodule's subslot against its
// reference and the PhysicalSubslots, as Select does.
func TestPlanConfigurableSubslot(t *testing.T) {
	t.Parallel()
	edits := append(di16Refs, `ModuleItem ID="MOD_DI16" ModuleIdentNumber="0x00000010"`, `ModuleItem ID="MOD_DI16" ModuleIdentNumber="0x00000010" PhysicalSubslots="1..3"`)
	doc, c := configOf(t, "DAP_1P", edits...)
	m := resolve(t, c)
	if _, err := PlanParameters(doc, m, PlanOptions{DAP: "DAP_1P", Defaults: true}); err != nil {
		t.Fatal(err)
	}
	moved := func(subslot uint16) *model.Device {
		c := *m
		c.Slots = slices.Clone(m.Slots)
		c.Slots[0].Module = cloneModule(m.Slots[0].Module)
		c.Slots[0].Module.Submodules[1].Subslot = subslot
		return &c
	}
	_, err := PlanParameters(doc, moved(9), PlanOptions{DAP: "DAP_1P", Defaults: true})
	isErr(t, err, profinet.ErrInvalidConfig, "SM_CH", "not allowed in subslot 9")
	// SM_CHO is allowed in 4, but 4 is not among the PhysicalSubslots.
	mm := *m
	mm.Slots = slices.Clone(m.Slots)
	mm.Slots[0].Module = cloneModule(m.Slots[0].Module)
	mm.Slots[0].Module.Submodules[2].Subslot = 4
	_, err = PlanParameters(doc, &mm, PlanOptions{DAP: "DAP_1P", Defaults: true})
	isErr(t, err, profinet.ErrInvalidConfig, "PhysicalSubslots")
	// Without AllowedInSubslots, the used subslots are the allowed ones.
	doc, c = configOf(t, "DAP_1P", onDI16(`<SubmoduleItemRef SubmoduleItemTarget="SM_CH" UsedInSubslots="2"/>`)...)
	m = resolve(t, c)
	if _, err := PlanParameters(doc, m, PlanOptions{DAP: "DAP_1P", Defaults: true}); err != nil {
		t.Fatal(err)
	}
	_, err = PlanParameters(doc, moved(3), PlanOptions{DAP: "DAP_1P", Defaults: true})
	isErr(t, err, profinet.ErrInvalidConfig, `"2"`)
	// A submodule no reference names is not in the file.
	if _, err := SubmoduleParameters(doc, "DAP_1P", 0x10, 0x52); !errors.Is(err, profinet.ErrNotFound) {
		t.Fatal(err)
	}
}

// Check states the SubmoduleList rules the catalog cannot: at least one
// item, ports without a SubslotNumber; a built-in port needs one.
func TestCheckSubmoduleList(t *testing.T) {
	t.Parallel()
	errorsOf := func(doc *Document) []string {
		var out []string
		for _, i := range Check(doc).Issues {
			if i.Severity == SeverityError && (strings.Contains(i.Message, "PortSubmoduleItem") || strings.Contains(i.Message, "SubslotNumber")) {
				out = append(out, i.Message)
			}
		}
		return out
	}
	doc, _ := analyzeVariant(t, withList()...)
	if e := errorsOf(doc); e != nil {
		t.Fatalf("valid list: %v", e)
	}
	doc, _ = analyzeVariant(t, "</ModuleList>", "</ModuleList><SubmoduleList/>")
	if e := errorsOf(doc); len(e) != 1 || !strings.Contains(e[0], "SubmoduleItem or PortSubmoduleItem") {
		t.Fatalf("empty list: %v", e)
	}
	doc, _ = analyzeVariant(t, withList(`<PortSubmoduleItem ID="PORT_2"`, `<PortSubmoduleItem ID="PORT_2" SubslotNumber="32770"`)...)
	if e := errorsOf(doc); len(e) != 1 || !strings.Contains(e[0], "a port of the SubmoduleList has none") {
		t.Fatalf("list port with a subslot: %v", e)
	}
	doc, _ = analyzeVariant(t, `<PortSubmoduleItem ID="P1_1P" SubslotNumber="32769"`, `<PortSubmoduleItem ID="P1_1P"`)
	if e := errorsOf(doc); len(e) != 1 || e[0] != "missing required attribute SubslotNumber" {
		t.Fatalf("built-in port without a subslot: %v", e)
	}
}

// A module the DAP references twice is plugged by the reference that
// allows the slot, even when it is the second; the references are not
// merged, and a slot neither allows is refused without a change.
func TestConfigurationRepeatedModuleRef(t *testing.T) {
	t.Parallel()
	_, c := configOf(t, "DAP_1P", `<ModuleItemRef ModuleItemTarget="MOD_DIO8" AllowedInSlots="1..8"/>`,
		`<ModuleItemRef ModuleItemTarget="MOD_DIO8" AllowedInSlots="5"/><ModuleItemRef ModuleItemTarget="MOD_DIO8" AllowedInSlots="6"/>`)
	before := resolve(t, c)
	beforeSlots := c.Slots()
	for _, err := range []error{c.Plug(7, "MOD_DIO8"), c.PlugIdent(7, 0x30)} {
		isErr(t, err, profinet.ErrInvalidConfig, "MOD_DIO8", "slot 7", "2 references", `"5", "6"`)
	}
	if _, ok := c.Module(7); ok || !slices.Equal(c.Slots(), beforeSlots) {
		t.Fatalf("a refused plug changed the slots: %v", c.Slots())
	}
	if after := resolve(t, c); !reflect.DeepEqual(after, before) {
		t.Fatal("a refused plug changed the configuration")
	}
	if err := c.Plug(6, "MOD_DIO8"); err != nil {
		t.Fatal(err)
	}
	if err := c.PlugIdent(5, 0x30); err != nil {
		t.Fatal(err)
	}
	m := resolve(t, c)
	for _, slot := range []uint16{5, 6} {
		if id, _ := c.Module(slot); id != "MOD_DIO8" {
			t.Fatalf("slot %d holds %q", slot, id)
		}
		if s, ok := m.Module(slot); !ok || s.IdentNumber != 0x30 {
			t.Fatalf("slot %d of the model: %+v", slot, s)
		}
	}
}

// A submodule a module references twice is chosen by the reference that
// allows the subslot, even when it is the second; a subslot neither
// allows is refused without a change.
func TestConfigurationRepeatedSubmoduleRef(t *testing.T) {
	t.Parallel()
	_, c := configOf(t, "DAP_1P", onDI16(`<SubmoduleItemRef SubmoduleItemTarget="SM_CHO" AllowedInSubslots="3"/>`+
		`<SubmoduleItemRef SubmoduleItemTarget="SM_CHO" AllowedInSubslots="4"/>`)...)
	before := resolve(t, c)
	isErr(t, c.Select(1, 5, "SM_CHO"), profinet.ErrInvalidConfig, "SM_CHO", "subslot 5", "2 references", `"3", "4"`)
	if after := resolve(t, c); !reflect.DeepEqual(after, before) {
		t.Fatal("a refused choice changed the configuration")
	}
	if err := c.Select(1, 4, "SM_CHO"); err != nil {
		t.Fatal(err)
	}
	if s, _ := resolve(t, c).Module(1); subslots(*s) != "1:0x11 4:0x52" {
		t.Fatalf("slot 1 %s", subslots(*s))
	}
	// The first reference still applies to its own subslot.
	if err := c.Select(1, 3, "SM_CHO"); err != nil {
		t.Fatal(err)
	}
	if s, _ := resolve(t, c).Module(1); subslots(*s) != "1:0x11 3:0x52 4:0x52" {
		t.Fatalf("slot 1 %s", subslots(*s))
	}
	// Without AllowedInSubslots a reference allows only where it fixes or
	// uses the submodule, and the error says so.
	_, c = configOf(t, "DAP_1P", onDI16(`<SubmoduleItemRef SubmoduleItemTarget="SM_CHO" UsedInSubslots="3"/>`+
		`<SubmoduleItemRef SubmoduleItemTarget="SM_CHO" AllowedInSubslots="4"/>`+
		`<SubmoduleItemRef SubmoduleItemTarget="SM_NOIO" UsedInSubslots="5"/>`)...)
	isErr(t, c.Select(1, 6, "SM_CHO"), profinet.ErrInvalidConfig, "2 references", `"3", "4"`)
	isErr(t, c.Select(1, 6, "SM_NOIO"), profinet.ErrInvalidConfig, "not allowed in subslot 6", `"5"`)
	// A reference that uses the submodule in a subslot is preferred to an
	// earlier one that only allows it there.
	_, c = configOf(t, "DAP_1P", onDI16(`<SubmoduleItemRef SubmoduleItemTarget="SM_CHO" AllowedInSubslots="3..4"/>`+
		`<SubmoduleItemRef SubmoduleItemTarget="SM_CHO" AllowedInSubslots="4" UsedInSubslots="4"/>`)...)
	if err := c.ClearSubslot(1, 4); err != nil {
		t.Fatal(err)
	}
	if err := c.Select(1, 4, "SM_CHO"); err != nil {
		t.Fatal(err)
	}
	if s, _ := resolve(t, c).Module(1); subslots(*s) != "1:0x11 4:0x52" {
		t.Fatalf("slot 1 %s", subslots(*s))
	}
}
