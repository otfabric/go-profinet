// SPDX-License-Identifier: MIT

package e2e

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/controller"
	"github.com/otfabric/go-profinet/device"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/pnio"
)

// apiIOLink is the IO-Link profile API (0x4E01, 19969 in GSDML).
const apiIOLink uint32 = 0x4E01

// apiModel is testModel with an IO-Link-like module in slot 4: a port
// submodule of API 0 and a data submodule of apiIOLink with inputs,
// outputs and a parameter record.
func apiModel() *model.Device {
	m := testModel()
	m.Slots = append(m.Slots, model.Slot{Number: 4, Module: model.Module{Name: "IOL", IdentNumber: 0x40, Submodules: []model.Submodule{
		{Name: "Port", IdentNumber: 0x41, Subslot: 1},
		{Name: "IOL data", IdentNumber: 0x42, API: apiIOLink, Subslot: 2, InputLen: 4, OutputLen: 2,
			Records: []model.Record{{Index: 0x20, Name: "Param", Length: 2, Default: []byte{0, 1}}}},
	}}})
	return m
}

// A controller and a device that share a model with a submodule of API
// 0x4E01: the AR comes up without a module difference, IO data, the
// parameter write, record reads and the diagnosis alarm are addressed in
// that API.
func TestSubmoduleAPI(t *testing.T) {
	e := newEnv(t, 41)
	dev, _ := e.device("dut", "192.168.0.10", apiModel(), nil)
	ctrl, _ := e.controller("192.168.0.1")
	alarms := make(chan controller.AlarmEvent, 4)
	cd, err := ctrl.AddDevice(controller.DeviceConfig{StationName: "dut", Model: apiModel(),
		Parameters: []controller.Parameter{{Slot: 4, Subslot: 2, Index: 0x20, Data: []byte{0, 9}}},
		OnAlarm:    func(a controller.AlarmEvent) { alarms <- a }})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, cd, controller.StateRunning, 10*time.Second)
	eventually(t, 2*time.Second, "device AR in Data", func() bool {
		ars := dev.ARs()
		return len(ars) == 1 && ars[0].State == device.ARStateData
	})

	if err := dev.SetInput(4, 2, []byte{1, 2, 3, 4}, pnio.IOxSGood); err != nil {
		t.Fatal(err)
	}
	if err := cd.SetOutput(4, 2, []byte{5, 6}, pnio.IOxSGood); err != nil {
		t.Fatal(err)
	}
	eventually(t, 2*time.Second, "inputs at the controller", func() bool {
		in, iops, _ := cd.Input(4, 2)
		return bytes.Equal(in, []byte{1, 2, 3, 4}) && iops == pnio.IOxSGood
	})
	eventually(t, 2*time.Second, "outputs at the device", func() bool {
		out, iops, _ := dev.Output(4, 2)
		return bytes.Equal(out, []byte{5, 6}) && iops == pnio.IOxSGood
	})

	ctx := context.Background()
	// The parameter written at Connect, read back in its API.
	if v, err := cd.ReadRecord(ctx, controller.Record{API: apiIOLink, Slot: 4, Subslot: 2, Index: 0x20}, 0); err != nil || !bytes.Equal(v, []byte{0, 9}) {
		t.Fatalf("parameter %x %v", v, err)
	}
	b, err := cd.ReadRecord(ctx, controller.Record{API: apiIOLink, Index: pnio.IndexRealIdentAPI}, 0)
	if err != nil {
		t.Fatal(err)
	}
	blk, _, _ := pnio.ReadBlock(b)
	if id, err := pnio.DecodeIdentificationData(blk); err != nil || len(id.APIs) != 1 || id.APIs[0].API != apiIOLink || id.APIs[0].Slots[0].Slot != 4 {
		t.Fatalf("real identification %+v %v", id, err)
	}
	b, err = cd.ReadRecord(ctx, controller.Record{Index: pnio.IndexModuleDiffAR}, 0)
	if err != nil {
		t.Fatal(err)
	}
	blk, _, _ = pnio.ReadBlock(b)
	if md, err := pnio.DecodeModuleDiffBlock(blk); err != nil || len(md.APIs) != 0 {
		t.Fatalf("module diff %+v %v", md, err)
	}

	if err := dev.AddDiagnosis(ctx, 4, 2, pnio.DiagnosisItem{ChannelNumber: 1, ChannelProperties: pnio.ChanDirInput, ErrorType: pnio.ChannelErrLineBreak}); err != nil {
		t.Fatal(err)
	}
	if a := <-alarms; a.Notification.API != apiIOLink || a.Notification.Slot != 4 || a.Notification.Subslot != 2 || a.Notification.SubmoduleIdentNumber != 0x42 {
		t.Fatalf("alarm %+v", a.Notification)
	}
	diag, err := cd.ReadDiagnosis(ctx)
	if err != nil || len(diag) != 1 || diag[0].API != apiIOLink || diag[0].Slot != 4 || diag[0].Subslot != 2 {
		t.Fatalf("diagnosis %+v %v", diag, err)
	}
	if err := cd.Close(); err != nil {
		t.Fatal(err)
	}
}
