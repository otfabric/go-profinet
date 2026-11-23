// SPDX-License-Identifier: MIT

package gsdml

import (
	"bytes"
	"errors"
	"testing"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/model"
)

// apiModel is testModel with slot 1's module carrying a second submodule
// of the IO-Link API 0x4E01 (19969).
func apiModel() *model.Device {
	m := testModel()
	m.Slots[0].Module.Submodules = append(m.Slots[0].Module.Submodules,
		model.Submodule{Name: "IOL", IdentNumber: 0x99, API: 0x4E01, Subslot: 2, InputLen: 4})
	return m
}

// The API survives FromModel and ToModel, written as GSDML's decimal
// Unsigned32; API 0 is not written.
func TestAPIRoundTrip(t *testing.T) {
	doc, err := FromModel(apiModel(), GenerateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(b, []byte(`API="19969"`)) != 1 || bytes.Contains(b, []byte(`API="0"`)) {
		t.Fatalf("API attributes in\n%s", b)
	}
	doc2, err := ParseBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(doc2); err != nil {
		t.Fatal(err)
	}
	m2, err := ToModel(doc2, "")
	if err != nil {
		t.Fatal(err)
	}
	subs := m2.Slots[0].Module.Submodules
	if len(subs) != 2 || subs[0].API != 0 || subs[1].API != 0x4E01 || subs[1].Subslot != 2 {
		t.Fatalf("slot 1 %+v", subs)
	}
	if a := m2.APIs(); len(a) != 2 || a[1] != 0x4E01 {
		t.Fatalf("APIs %v", a)
	}
	exp := m2.Expected()
	if mods := exp[1].Modules; len(mods) != 2 || mods[1].API != 0x4E01 || mods[1].Submodules[0].Subslot != 2 {
		t.Fatalf("expected %+v", mods)
	}
}

// An API that is no Unsigned32 is malformed; one other than 0 on a DAP
// submodule is not supported by the model (its DAP is API 0).
func TestAPIMalformed(t *testing.T) {
	doc, err := FromModel(apiModel(), GenerateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{`API="x"`, `API=""`, `API="4294967296"`} {
		d, err := ParseBytes(bytes.Replace(b, []byte(`API="19969"`), []byte(v), 1))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ToModel(d, ""); !errors.Is(err, profinet.ErrMalformed) {
			t.Fatalf("%s: err %v", v, err)
		}
	}
	d, err := ParseBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	dap := d.ProfileBody.ApplicationProcess.DeviceAccessPointList.DeviceAccessPointItems[0]
	dap.VirtualSubmoduleList.VirtualSubmoduleItems[0].API = "15872"
	if _, err := ToModel(d, ""); !errors.Is(err, profinet.ErrUnsupported) {
		t.Fatalf("DAP submodule of API 0x3E00: err %v", err)
	}
}
