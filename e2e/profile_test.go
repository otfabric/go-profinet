// SPDX-License-Identifier: MIT

package e2e

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/controller"
	"github.com/otfabric/go-profinet/device"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/pnio"
)

// profileModel is testModel with an IO-Link master module in slot 4 whose
// submodule (API 0x4E01) has parameter records in the profile range
// 0xB000-0xBFFF at the indexes GSDML files of IO-Link Wireless masters use
// (0xB600 master configuration, 0xB701-0xB703 track configuration, 0xB900
// port parameters), PROFIdrive's 0xB02E as a read-only record, and a user
// record next to them.
func profileModel() *model.Device {
	m := testModel()
	master := append([]byte{0x80, 0x01, 1, 0x10, 0xFF, 0xFF}, make([]byte, 10)...)
	m.Slots = append(m.Slots, model.Slot{Number: 4, Module: model.Module{Name: "IOLM", IdentNumber: 0x40, Submodules: []model.Submodule{
		{Name: "IOLM", IdentNumber: 0x42, API: apiIOLink, Subslot: 1, InputLen: 2, Records: []model.Record{
			{Index: 0x20, Name: "User", Length: 2, Default: []byte{0, 1}},
			{Index: 0xB600, Name: "Master configuration", Length: 16, Default: master},
			{Index: 0xB701, Name: "Track 1 configuration", Length: 4, Default: []byte{0x80, 0x01, 4, 31}},
			{Index: 0xB702, Name: "Track 2 configuration", Length: 4, Default: []byte{0x80, 0x01, 4, 31}},
			{Index: 0xB703, Name: "Track 3 configuration", Length: 4, Default: []byte{0x80, 0x01, 4, 31}},
			{Index: 0xB900, Name: "Port parameters", Length: 34, Default: append([]byte{0x80, 0x01, 0x00, 0x11}, make([]byte, 30)...)},
			{Index: 0xB02E, Name: "Profile read-only", Length: 4, Default: []byte{1, 2, 3, 4}, ReadOnly: true},
		}},
	}}})
	return m
}

// profileHandler records the writes it sees and accepts one record the
// model does not have (0xB7FF), as an application implementing a profile
// would; it refuses the others like NopHandler.
type profileHandler struct {
	device.NopHandler
	mu     sync.Mutex
	writes []device.RecordRequest
}

func (h *profileHandler) WriteRecord(ctx context.Context, r device.RecordRequest, data []byte) error {
	h.mu.Lock()
	h.writes = append(h.writes, r)
	h.mu.Unlock()
	if r.Index == 0xB7FF {
		return nil
	}
	return h.NopHandler.WriteRecord(ctx, r, data)
}

func (h *profileHandler) seen(index pnio.Index) (device.RecordRequest, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, w := range h.writes {
		if w.Index == index {
			return w, true
		}
	}
	return device.RecordRequest{}, false
}

// Parameter records in the profile range 0xB000-0xBFFF: the controller
// writes them at Connect in their API, the device keeps and serves them,
// reports them to the Handler as model records, and refuses a write to a
// read-only one, of the wrong length, in the wrong API, or to an index
// outside the user and profile ranges; an index of the profile range the
// model does not have goes to the Handler, as a user index does.
func TestProfileRecords(t *testing.T) {
	e := newEnv(t, 43)
	h := &profileHandler{}
	dev, _ := e.device("dut", "192.168.0.10", profileModel(), h)
	ctrl, _ := e.controller("192.168.0.1")
	master := append([]byte{0x80, 0x01, 7, 0x11, 0xFF, 0xFF}, make([]byte, 10)...)
	cd, err := ctrl.AddDevice(controller.DeviceConfig{StationName: "dut", Model: profileModel(),
		Parameters: []controller.Parameter{
			{Slot: 4, Subslot: 1, Index: 0xB600, Data: master},
			{Slot: 4, Subslot: 1, Index: 0xB702, Data: []byte{0x80, 0x01, 2, 9}},
		}})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, cd, controller.StateRunning, 10*time.Second)
	eventually(t, 2*time.Second, "device AR in Data", func() bool {
		ars := dev.ARs()
		return len(ars) == 1 && ars[0].State == device.ARStateData
	})
	ctx := context.Background()
	rec := func(api uint32, index pnio.Index) controller.Record {
		return controller.Record{API: api, Slot: 4, Subslot: 1, Index: index}
	}

	// Written at Connect (the Parameters), or the model's Default.
	for _, c := range []struct {
		index pnio.Index
		want  []byte
	}{
		{0xB600, master},
		{0xB701, []byte{0x80, 0x01, 4, 31}},
		{0xB702, []byte{0x80, 0x01, 2, 9}},
		{0xB703, []byte{0x80, 0x01, 4, 31}},
		{0xB900, append([]byte{0x80, 0x01, 0x00, 0x11}, make([]byte, 30)...)},
		{0xB02E, []byte{1, 2, 3, 4}},
		{0x20, []byte{0, 1}},
	} {
		if v, err := cd.ReadRecord(ctx, rec(apiIOLink, c.index), 0); err != nil || !bytes.Equal(v, c.want) {
			t.Fatalf("read %s: %x %v, want %x", c.index, v, err, c.want)
		}
	}
	if r, ok := h.seen(0xB600); !ok || !r.Modeled || r.API != apiIOLink || r.Slot != 4 || r.Subslot != 1 {
		t.Fatalf("handler 0xB600 %+v %v", r, ok)
	}

	// A write of the running AR, read back.
	if err := cd.WriteRecord(ctx, rec(apiIOLink, 0xB703), []byte{0x80, 0x01, 1, 5}); err != nil {
		t.Fatal(err)
	}
	if v, err := cd.ReadRecord(ctx, rec(apiIOLink, 0xB703), 0); err != nil || !bytes.Equal(v, []byte{0x80, 0x01, 1, 5}) {
		t.Fatalf("0xB703 %x %v", v, err)
	}

	var se *pnio.StatusError
	refused := func(what string, err error, code1 uint8) {
		t.Helper()
		if !errors.As(err, &se) || se.Status.Code1 != code1 {
			t.Fatalf("%s: %v, want code1 %#x", what, err, code1)
		}
	}
	refused("read-only 0xB02E", cd.WriteRecord(ctx, rec(apiIOLink, 0xB02E), []byte{9, 9, 9, 9}), pnio.RWAccessDenied)
	refused("length", cd.WriteRecord(ctx, rec(apiIOLink, 0xB600), []byte{1, 2}), pnio.RWWriteLength)
	refused("API 0", cd.WriteRecord(ctx, rec(0, 0xB600), master), pnio.RWInvalidArea)
	_, err = cd.ReadRecord(ctx, rec(0, 0xB600), 0)
	refused("read in API 0", err, pnio.RWInvalidArea)
	// 0x8100 is in PROFINET's subslot range, not a submodule's own record.
	refused("0x8100", cd.WriteRecord(ctx, rec(apiIOLink, 0x8100), []byte{1}), pnio.RWInvalidIndex)
	if _, ok := h.seen(0x8100); ok {
		t.Fatal("0x8100 reached the handler")
	}
	// Not in the model: the Handler decides.
	refused("unmodelled 0xB7FE", cd.WriteRecord(ctx, rec(apiIOLink, 0xB7FE), []byte{1}), pnio.RWInvalidIndex)
	if err := cd.WriteRecord(ctx, rec(apiIOLink, 0xB7FF), []byte{1}); err != nil {
		t.Fatalf("unmodelled 0xB7FF: %v", err)
	}
	if r, ok := h.seen(0xB7FF); !ok || r.Modeled {
		t.Fatalf("handler 0xB7FF %+v %v", r, ok)
	}
	_, err = cd.ReadRecord(ctx, rec(apiIOLink, 0xB7FF), 0)
	refused("read unmodelled 0xB7FF", err, pnio.RWInvalidIndex)
	if v, err := cd.ReadRecord(ctx, rec(apiIOLink, 0xB02E), 0); err != nil || !bytes.Equal(v, []byte{1, 2, 3, 4}) {
		t.Fatalf("0xB02E after the refused write %x %v", v, err)
	}
	if err := cd.Close(); err != nil {
		t.Fatal(err)
	}
}
