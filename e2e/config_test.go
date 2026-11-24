// SPDX-License-Identifier: MIT

package e2e

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/controller"
	"github.com/otfabric/go-profinet/gsdml"
	"github.com/otfabric/go-profinet/pnio"
)

// configFixture is the modular fixture with edits (old, new, …) applied,
// each once.
func configFixture(t *testing.T, edits ...string) *gsdml.Document {
	t.Helper()
	b, err := os.ReadFile("../gsdml/testdata/fixtures/GSDML-V2.43-OTFabric-ModularFixture-20261027.xml")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(edits); i += 2 {
		if !bytes.Contains(b, []byte(edits[i])) {
			t.Fatalf("the fixture no longer holds %s", edits[i])
		}
		b = bytes.Replace(b, []byte(edits[i]), []byte(edits[i+1]), 1)
	}
	doc, err := gsdml.ParseBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// configurableList holds the configurable submodules of the variants: an
// input with a parameter record, an output, and a port.
const configurableList = `</ModuleList><SubmoduleList>
  <SubmoduleItem ID="SM_CH" SubmoduleIdentNumber="0x00000051" MayIssueProcessAlarm="false">
    <IOData><Input><DataItem DataType="Unsigned8" TextId="T_DIO8_In"/></Input></IOData>
    <RecordDataList>
      <ParameterRecordDataItem Index="200" Length="2">
        <Name TextId="T_DI_Par"/>
        <Ref ByteOffset="0" DataType="Unsigned8" DefaultValue="5" AllowedValues="1..9" TextId="T_Filter"/>
        <Const ByteOffset="1" Data="0x01"/>
      </ParameterRecordDataItem>
    </RecordDataList>
    <ModuleInfo><Name TextId="T_DIO8"/><InfoText TextId="T_DIO8_Info"/></ModuleInfo>
  </SubmoduleItem>
  <SubmoduleItem ID="SM_CHO" SubmoduleIdentNumber="0x00000052" MayIssueProcessAlarm="false">
    <IOData><Output><DataItem DataType="Unsigned16" TextId="T_DIO8_Out"/></Output></IOData>
    <ModuleInfo><Name TextId="T_DO16"/><InfoText TextId="T_DO16_Info"/></ModuleInfo>
  </SubmoduleItem>
  <PortSubmoduleItem ID="PORT_2" TextId="T_P2" SubmoduleIdentNumber="0x00008002">
    <MAUTypeList><MAUTypeItem Value="16"/></MAUTypeList>
    <ModuleInfo><Name TextId="T_P2"/><InfoText TextId="T_P2"/></ModuleInfo>
  </PortSubmoduleItem>
</SubmoduleList>`

// TestConfigurableSubmodules: a configuration gsdml.ToModel refuses (the
// module in slot 1 needs configurable submodules) is resolved with
// gsdml.Configuration, with a second slot of the same module holding
// other submodules; a go-profinet device built from the resolved model
// and a controller configured with it connect without a ModuleDiffBlock,
// exchange every input and output and write the planned records,
// including one of a configurable submodule.
func TestConfigurableSubmodules(t *testing.T) {
	const dap = "DAP_1P"
	doc := configFixture(t, "</ModuleList>", configurableList,
		"</VirtualSubmoduleList>\n        </ModuleItem>\n        <ModuleItem ID=\"MOD_DO16\"",
		"</VirtualSubmoduleList><UseableSubmodules>"+
			`<SubmoduleItemRef SubmoduleItemTarget="SM_CH" AllowedInSubslots="2" FixedInSubslots="2"/>`+
			`<SubmoduleItemRef SubmoduleItemTarget="SM_CHO" AllowedInSubslots="3..4" UsedInSubslots="3"/>`+
			"</UseableSubmodules>\n        </ModuleItem>\n        <ModuleItem ID=\"MOD_DO16\"")
	if _, err := gsdml.ToModel(doc, dap); err == nil {
		t.Fatal("ToModel converts a configuration with configurable submodules")
	}
	cfg, err := gsdml.NewConfiguration(doc, dap)
	if err != nil {
		t.Fatal(err)
	}
	// Slot 5: the same module, its output in subslot 4 instead of 3.
	if err := cfg.Plug(5, "MOD_DI16"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.ClearSubslot(5, 3); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Select(5, 4, "SM_CHO"); err != nil {
		t.Fatal(err)
	}
	m, err := cfg.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if s1, _ := m.Submodule(1, 3); s1 == nil || s1.IdentNumber != 0x52 {
		t.Fatalf("slot 1 subslot 3 %+v", s1)
	}
	if s5, _ := m.Submodule(5, 4); s5 == nil || s5.IdentNumber != 0x52 {
		t.Fatalf("slot 5 subslot 4 %+v", s5)
	}
	if _, ok := m.Submodule(5, 3); ok {
		t.Fatal("slot 5 subslot 3 is plugged")
	}
	plan, err := gsdml.PlanParameters(doc, m, gsdml.PlanOptions{DAP: dap, Defaults: true,
		Values: []gsdml.ParameterValues{{Slot: 5, Subslot: 2, Index: 200, Values: map[string]string{"Filter": "8"}}}})
	if err != nil {
		t.Fatalf("%v %v", err, plan.Issues)
	}
	var params []controller.Parameter
	configurable := 0
	for _, w := range plan.Writes {
		params = append(params, controller.Parameter(w.Write))
		if w.Write.Index == 200 {
			configurable++
		}
	}
	if configurable != 2 {
		t.Fatalf("plan %+v", plan.Writes)
	}
	e := newEnv(t, 44)
	dev, _ := e.device("configured", "192.168.0.10", m, nil)
	ctrl, _ := e.controller("192.168.0.1")
	expected, err := cfg.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	cd, err := ctrl.AddDevice(controller.DeviceConfig{StationName: "configured", Model: expected, Parameters: params})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, cd, controller.StateRunning, 10*time.Second)
	if diff := cd.ModuleDiff(); diff != nil {
		t.Fatalf("module diff %+v", diff)
	}
	exerciseFixture(t, cd, m, func(slot, subslot uint16, data []byte) { _ = dev.SetInput(slot, subslot, data, pnio.IOxSGood) })
	for _, p := range params {
		b, err := cd.ReadRecord(context.Background(), controller.Record{Slot: p.Slot, Subslot: p.Subslot, Index: p.Index}, 0)
		if err != nil || !bytes.Equal(b, p.Data) {
			t.Fatalf("parameter %d/%d %s: % x %v, want % x", p.Slot, p.Subslot, p.Index, b, err, p.Data)
		}
	}
}

// TestConfigurablePort: the two-port DAP with its second port as a
// configurable submodule (a PortSubmoduleItem of the SubmoduleList used in
// subslot 0x8002), which ToModel refuses, resolves to the plugged
// configuration of the fixture's built-in ports; a two-port device built
// from it serves a controller on its second port, which expects the
// port submodule (no ModuleDiffBlock).
func TestConfigurablePort(t *testing.T) {
	const dap = "DAP_2P"
	doc := configFixture(t, "</ModuleList>", configurableList,
		`<PortSubmoduleItem ID="P2_2P" SubslotNumber="32770" TextId="T_P2" SubmoduleIdentNumber="0x00008002">
              <MAUTypeList>
                <MAUTypeItem Value="16"/>
              </MAUTypeList>
            </PortSubmoduleItem>
          </SystemDefinedSubmoduleList>`,
		`</SystemDefinedSubmoduleList><UseableSubmodules><SubmoduleItemRef SubmoduleItemTarget="PORT_2" AllowedInSubslots="32770" UsedInSubslots="32770"/></UseableSubmodules>`)
	if _, err := gsdml.ToModel(doc, dap); err == nil {
		t.Fatal("ToModel converts a DAP with a configurable port")
	}
	cfg, err := gsdml.NewConfiguration(doc, dap)
	if err != nil {
		t.Fatal(err)
	}
	m, err := cfg.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	// The same plugged configuration as the fixture's built-in ports.
	want := fixtureModel(t, "GSDML-V2.43-OTFabric-ModularFixture-20261027.xml", dap)
	if m.Capabilities.Ports != want.Capabilities.Ports || len(m.DAP.Submodules) != len(want.DAP.Submodules) {
		t.Fatalf("DAP %+v, want %+v", m.DAP, want.DAP)
	}
	for _, s := range want.DAP.Submodules {
		if got, ok := m.DAP.Submodule(s.Subslot); !ok || got.IdentNumber != s.IdentNumber {
			t.Fatalf("DAP subslot %#x: %+v", s.Subslot, got)
		}
	}
	e := newEnv(t, 45)
	segs := e.segments(2)
	dev := e.multiportDeviceModel("configured-2p", "192.168.0.10", m, nil, segs[0], segs[1])
	ctrl, _ := e.controllerOn(segs[1], "192.168.0.1")
	expected, err := cfg.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	cd, err := ctrl.AddDevice(controller.DeviceConfig{StationName: "configured-2p", Model: expected})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, cd, controller.StateRunning, 15*time.Second)
	if diff := cd.ModuleDiff(); diff != nil {
		t.Fatalf("module diff %+v", diff)
	}
	exerciseFixture(t, cd, m, func(slot, subslot uint16, data []byte) {
		_ = dev.dev.SetInput(slot, subslot, data, pnio.IOxSGood)
	})
}
