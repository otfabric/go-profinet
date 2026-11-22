// SPDX-License-Identifier: MIT

package gsdml_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	profinet "github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/gsdml"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/pnio"
)

// recordModel is a device with one parameter record of the given length
// and default in a plugged module.
func recordModel(length uint16, def []byte) *model.Device {
	return &model.Device{
		Identity: model.Identity{VendorID: 0xF000, DeviceID: 0x42, VendorName: "OT Fabric", TypeOfStation: "go-profinet Record Device",
			OrderID: "OTF-R", HardwareRevision: 1, SoftwareRevision: pnio.SoftwareRevision{Prefix: 'V', FunctionalEnhance: 1}},
		Capabilities: model.Capabilities{MaxARs: 1, MinDeviceInterval: 32, ConformanceClass: "A", Ports: 1},
		DAP:          model.StandardDAP("DAP", 0x100, 1),
		Slots: []model.Slot{{Number: 1, Module: model.Module{Name: "P", IdentNumber: 0x10, Submodules: []model.Submodule{{
			Name: "P", IdentNumber: 0x11, Subslot: 1, InputLen: 1,
			Records: []model.Record{{Index: 0x20, Name: "Param", Length: length, Default: def}}}}}}},
	}
}

func generate(t *testing.T, m *model.Device, version string) []byte {
	t.Helper()
	doc, err := gsdml.FromModel(m, gsdml.GenerateOptions{PNIOVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	b, err := gsdml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A record without a default is described by a Const of zeros, which the
// schemas require from V2.31 (a Const or a Ref in every
// ParameterRecordDataItem); its value reads back as zeros, as before.
func TestFromModelRecordWithoutDefault(t *testing.T) {
	b := generate(t, recordModel(3, nil), "")
	if !strings.Contains(string(b), `Data="0x00,0x00,0x00"`) {
		t.Fatalf("no zero Const:\n%s", b)
	}
	rep, err := gsdml.CheckBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range rep.Issues {
		if i.Rule == gsdml.RuleSchema || i.Rule == gsdml.RuleStructure {
			t.Errorf("%s %s", i.Rule, i)
		}
	}
	doc, err := gsdml.ParseBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	m, err := gsdml.ToModel(doc, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Slots[0].Module.Submodules[0].Records[0].Default; !bytes.Equal(got, []byte{0, 0, 0}) {
		t.Fatalf("default %x", got)
	}
}

// A record with a default keeps it as its Const.
func TestFromModelRecordDefault(t *testing.T) {
	b := generate(t, recordModel(2, []byte{0xAB, 0x01}), "")
	if !strings.Contains(string(b), `Data="0xAB,0x01"`) {
		t.Fatalf("default not written:\n%s", b)
	}
}

// A record of no octets has no form in V2.31 and later (a Const has at
// least one octet within the record) and is refused; before V2.31 it is
// written without a Const, as the schemas allow.
func TestFromModelEmptyRecord(t *testing.T) {
	for _, v := range []string{"", "V2.31", "V2.43"} {
		if _, err := gsdml.FromModel(recordModel(0, nil), gsdml.GenerateOptions{PNIOVersion: v}); !errors.Is(err, profinet.ErrUnsupported) {
			t.Errorf("%q: %v", v, err)
		}
	}
	m := recordModel(0, nil)
	m.DAP.Submodules[0].Records = []model.Record{{Index: 0x21, Length: 0}}
	m.Slots[0].Module.Submodules[0].Records = nil
	if _, err := gsdml.FromModel(m, gsdml.GenerateOptions{}); !errors.Is(err, profinet.ErrUnsupported) {
		t.Errorf("DAP record: %v", err)
	}
	b := generate(t, recordModel(0, nil), "V2.3")
	if strings.Contains(string(b), "<Const") {
		t.Fatalf("Const for a record of no octets:\n%s", b)
	}
}
