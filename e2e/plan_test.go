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
	"github.com/otfabric/go-profinet/model"
)

// TestParameterPlan: the records gsdml.PlanParameters builds for a
// fixture, one field set by name and the others at their defaults, are
// written by a controller and read back from a go-profinet device.
func TestParameterPlan(t *testing.T) {
	const file, dap = "GSDML-V2.43-OTFabric-ModularFixture-20261027.xml", "DAP_1P"
	b, err := os.ReadFile("../gsdml/testdata/fixtures/" + file)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := gsdml.ParseBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	m := fixtureModel(t, file, dap)
	// The first plugged submodule with record 100: its first field, by
	// name, set to 7 (default 3, AllowedValues 1..20).
	var values []gsdml.ParameterValues
	m.Each(func(p model.PluggedSubmodule) {
		if _, ok := p.Submodule.Record(100); !ok || values != nil {
			return
		}
		recs, err := gsdml.SubmoduleParameters(doc, dap, p.ModuleIdent, p.Submodule.IdentNumber)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range recs {
			if r.Index == 100 {
				values = []gsdml.ParameterValues{{Slot: p.Slot, Subslot: p.Submodule.Subslot, Index: 100, Values: map[string]string{r.Fields[0].Name: "7"}}}
			}
		}
	})
	if values == nil {
		t.Fatal("no submodule with record 100")
	}
	plan, err := gsdml.PlanParameters(doc, m, gsdml.PlanOptions{DAP: dap, Defaults: true, Values: values})
	if err != nil {
		t.Fatalf("%v %v", err, plan.Issues)
	}
	var params []controller.Parameter
	set := false
	for _, w := range plan.Writes {
		params = append(params, controller.Parameter(w.Write))
		if w.Values == 1 {
			set = w.Write.Data[0] == 7
		}
	}
	if !set || len(params) < 2 {
		t.Fatalf("plan %+v", plan.Writes)
	}
	e := newEnv(t, 43)
	e.device("fixture", "192.168.0.10", m, nil)
	ctrl, _ := e.controller("192.168.0.1")
	cd, err := ctrl.AddDevice(controller.DeviceConfig{StationName: "fixture", Model: fixtureModel(t, file, dap), Parameters: params})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, cd, controller.StateRunning, 10*time.Second)
	for _, p := range params {
		got, err := cd.ReadRecord(context.Background(), controller.Record{Slot: p.Slot, Subslot: p.Subslot, Index: p.Index}, 0)
		if err != nil || !bytes.Equal(got, p.Data) {
			t.Fatalf("record %d/%d %s: % x %v, want % x", p.Slot, p.Subslot, p.Index, got, err, p.Data)
		}
	}
}
