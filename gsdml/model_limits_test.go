// SPDX-License-Identifier: MIT

package gsdml

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet"
)

func slotListDoc(refs int) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><ISO15745Profile xmlns="` + Namespace + `"><ProfileBody><ApplicationProcess><DeviceAccessPointList>`)
	b.WriteString(`<DeviceAccessPointItem ID="D" PhysicalSlots="0" FixedInSlots="0"><UseableModules>`)
	for i := 0; i < refs; i++ {
		b.WriteString(`<ModuleItemRef ModuleItemTarget="M" UsedInSlots="0..65535"/>`)
	}
	b.WriteString(`</UseableModules></DeviceAccessPointItem></DeviceAccessPointList></ApplicationProcess></ProfileBody></ISO15745Profile>`)
	return []byte(b.String())
}

func recordDoc(records, refs int) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><ISO15745Profile xmlns="` + Namespace + `"><ProfileBody><DeviceIdentity VendorID="0x1" DeviceID="0x1"/><ApplicationProcess>`)
	b.WriteString(`<DeviceAccessPointList><DeviceAccessPointItem ID="D" ModuleIdentNumber="1" PhysicalSlots="0..1" FixedInSlots="0"><UseableModules>`)
	for i := 0; i < refs; i++ {
		b.WriteString(`<ModuleItemRef ModuleItemTarget="M"/>`)
	}
	b.WriteString(`</UseableModules></DeviceAccessPointItem></DeviceAccessPointList>`)
	b.WriteString(`<ModuleList><ModuleItem ID="M" ModuleIdentNumber="2"><VirtualSubmoduleList><VirtualSubmoduleItem ID="S" SubmoduleIdentNumber="3"><RecordDataList>`)
	for i := 0; i < records; i++ {
		fmt.Fprintf(&b, `<ParameterRecordDataItem Index="%d" Length="65535"/>`, i+1)
	}
	b.WriteString(`</RecordDataList></VirtualSubmoduleItem></VirtualSubmoduleList></ModuleItem></ModuleList></ApplicationProcess></ProfileBody></ISO15745Profile>`)
	return []byte(b.String())
}

// Long slot lists make a bounded number of warnings per reference.
func TestCheckSlotWarningsBounded(t *testing.T) {
	doc, err := ParseBytes(slotListDoc(8))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, is := range Check(doc).Issues {
		if is.Rule == RuleSlots {
			n++
		}
	}
	if n == 0 || n > 8*2*(maxSlotWarnings+1) {
		t.Fatalf("%d slot warnings", n)
	}
}

// A module several references name is converted once, and the octets of
// all records are bounded.
func TestToModelRecordOctets(t *testing.T) {
	doc, err := ParseBytes(recordDoc(48, 48))
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	if _, err := ToModel(doc, ""); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&m1)
	if a := m1.TotalAlloc - m0.TotalAlloc; a > 16<<20 {
		t.Fatalf("%d octets allocated", a)
	}
	doc, err = ParseBytes(recordDoc(MaxModelRecordOctets/MaxRecordLength+1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ToModel(doc, ""); !errors.Is(err, profinet.ErrResourceExhausted) {
		t.Fatalf("err %v", err)
	}
}
