// SPDX-License-Identifier: MIT

package controller

import (
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/pnio"
)

// apiIOLink is the API of the IO-Link profile submodules the tests use.
const apiIOLink uint32 = 0x4E01

// apiModel is testModel with a module in slot 4 whose submodules belong
// to two APIs: subslot 1 to API 0, subslot 2 (IO data and a parameter
// record) to apiIOLink.
func apiModel() *model.Device {
	m := testModel()
	m.Slots = append(m.Slots, model.Slot{Number: 4, Module: model.Module{Name: "IOL", IdentNumber: 0x40, Submodules: []model.Submodule{
		{Name: "Port", IdentNumber: 0x41, Subslot: 1},
		{Name: "IOL data", IdentNumber: 0x42, API: apiIOLink, Subslot: 2, InputLen: 4, OutputLen: 2,
			Records: []model.Record{{Index: 0x20, Name: "Param", Length: 1}}},
	}}})
	return m
}

// The Connect request, the IO layout and the parameter writes address a
// submodule by its API: what the device receives on the wire.
func TestConnectAPIAddressing(t *testing.T) {
	n := newNet(t)
	c, _ := n.controller()
	f := n.fake("fake")
	var mu sync.Mutex
	var writes []pnio.WriteReq
	f.set(func(f *fakeDev) {
		f.raw = func(op pnio.OpNum, blocks []byte) []byte {
			if op != pnio.OpWrite {
				return nil
			}
			blk, _, err := pnio.ReadBlock(blocks)
			if err != nil {
				t.Error(err)
				return nil
			}
			q, err := pnio.DecodeWriteReq(blk)
			if err != nil {
				t.Error(err)
				return nil
			}
			mu.Lock()
			writes = append(writes, q)
			mu.Unlock()
			res := pnio.WriteRes{Seq: q.Seq, ARUUID: q.ARUUID, API: q.API, Slot: q.Slot, Subslot: q.Subslot, Index: q.Index, Length: q.Length}
			return pnio.AppendNDRResponse(nil, order, pnio.Status{}, 0, res.Append(nil))
		}
	})
	cd, err := c.AddDevice(DeviceConfig{StationName: "fake", Model: apiModel(), ReconnectMin: time.Hour, ReductionRatio: 512, DataHoldFactor: 0x1E00,
		Parameters: []Parameter{{Slot: 2, Subslot: 1, Index: 0x10, Data: []byte{0, 2}}, {Slot: 4, Subslot: 2, Index: 0x20, Data: []byte{7}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cd.Close() }()
	waitState(t, cd, StateWaitAppReady, 5*time.Second)
	req := <-f.requests

	// ExpectedSubmoduleBlockReq: slot 4 has one API entry per API.
	var slot4 []pnio.ExpectedModule
	for _, b := range req.Expected {
		for _, m := range b.Modules {
			if m.Slot == 4 {
				slot4 = append(slot4, m)
			} else if m.API != 0 {
				t.Errorf("slot %d in API %#x", m.Slot, m.API)
			}
		}
	}
	if len(slot4) != 2 || slot4[0].API != 0 || slot4[1].API != apiIOLink || slot4[0].IdentNumber != 0x40 || slot4[1].IdentNumber != 0x40 ||
		len(slot4[0].Submodules) != 1 || slot4[0].Submodules[0].Subslot != 1 || len(slot4[1].Submodules) != 1 || slot4[1].Submodules[0].Subslot != 2 {
		t.Fatalf("slot 4 %+v", slot4)
	}

	// The IOCRs list the IO data object of 4/2 under its API.
	for _, cr := range req.IOCRs {
		found := false
		for _, a := range cr.APIs {
			for _, o := range append(append([]pnio.IODataObject(nil), a.IODataObjects...), a.IOCS...) {
				if o.Slot == 4 && o.Subslot == 2 {
					found = true
					if a.API != apiIOLink {
						t.Errorf("IOCR %v: 4/2 in API %#x", cr.Type, a.API)
					}
				}
				if o.Slot == 4 && o.Subslot == 1 && a.API != 0 {
					t.Errorf("IOCR %v: 4/1 in API %#x", cr.Type, a.API)
				}
			}
		}
		if !found {
			t.Errorf("IOCR %v without 4/2: %+v", cr.Type, cr.APIs)
		}
	}

	// The parameter writes: each header carries its submodule's API.
	mu.Lock()
	defer mu.Unlock()
	if len(writes) != 2 {
		t.Fatalf("writes %+v", writes)
	}
	for _, w := range writes {
		want := uint32(0)
		if w.Slot == 4 {
			want = apiIOLink
		}
		if w.API != want {
			t.Errorf("write %d/%d %s: API %#x, want %#x", w.Slot, w.Subslot, w.Index, w.API, want)
		}
	}
}

// A parameter for a subslot the model does not have is written at API 0.
func TestParameterRecordAPI(t *testing.T) {
	d := &Device{cfg: DeviceConfig{Model: apiModel()}}
	for _, c := range []struct {
		p    Parameter
		want uint32
	}{
		{Parameter{Slot: 4, Subslot: 2, Index: 0x20}, apiIOLink},
		{Parameter{Slot: 4, Subslot: 1, Index: 0x20}, 0},
		{Parameter{Slot: 0, Subslot: 0x8001, Index: 0x802B}, 0},
		{Parameter{Slot: 9, Subslot: 1, Index: 1}, 0},
	} {
		if r := d.record(c.p); r.API != c.want || r.Slot != c.p.Slot || r.Subslot != c.p.Subslot || r.Index != c.p.Index {
			t.Errorf("%+v: %+v", c.p, r)
		}
	}
}

// A ModuleDiffBlock that reports a module missing in one API leaves the
// module's submodules of other APIs proper.
func TestProperSubmodulesByAPI(t *testing.T) {
	d := &Device{cfg: DeviceConfig{Model: apiModel()}}
	diff := &pnio.ModuleDiffBlock{APIs: []pnio.DiffAPI{{API: apiIOLink, Modules: []pnio.DiffModule{{Slot: 4, IdentNumber: 0x40, State: pnio.ModuleStateNoModule}}}}}
	p := d.properSubmodules(diff)
	if p[subKey{4, 2}] || !p[subKey{4, 1}] || !p[subKey{1, 1}] {
		t.Fatalf("proper %v", p)
	}
	diff.APIs[0].API = 0
	p = d.properSubmodules(diff)
	if !p[subKey{4, 2}] || p[subKey{4, 1}] {
		t.Fatalf("proper (API 0 missing) %v", p)
	}
}
