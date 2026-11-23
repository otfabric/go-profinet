// SPDX-License-Identifier: MIT

package device

import (
	"bytes"
	"context"
	"slices"
	"testing"

	"github.com/otfabric/go-profinet/alarm"
	"github.com/otfabric/go-profinet/controller"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/pnio"
	"github.com/otfabric/go-profinet/rpc"
)

// apiIOLink is the API of the IO-Link profile submodules of apiModel.
const apiIOLink uint32 = 0x4E01

// apiModel is testModel with a module in slot 5 whose subslot 1 belongs
// to API 0 and subslot 2 (input data and a parameter record) to
// apiIOLink.
func apiModel() *model.Device {
	m := testModel()
	m.Slots = append(m.Slots, model.Slot{Number: 5, Module: model.Module{Name: "IOL", IdentNumber: 0x50, Submodules: []model.Submodule{
		{Name: "Port", IdentNumber: 0x51, Subslot: 1},
		{Name: "IOL data", IdentNumber: 0x52, API: apiIOLink, Subslot: 2, InputLen: 4,
			Records: []model.Record{{Index: 0x20, Name: "Param", Length: 1, Default: []byte{5}}}},
	}}})
	return m
}

func decodeIdent(t *testing.T, b []byte) pnio.IdentificationData {
	t.Helper()
	blk, _, err := pnio.ReadBlock(b)
	if err != nil {
		t.Fatal(err)
	}
	id, err := pnio.DecodeIdentificationData(blk)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func decodeDiag(t *testing.T, b []byte) []pnio.DiagnosisData {
	t.Helper()
	blks, err := pnio.SplitBlocks(b)
	if err != nil {
		t.Fatal(err)
	}
	var out []pnio.DiagnosisData
	for _, blk := range blks {
		d, err := pnio.DecodeDiagnosisData(blk)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

// A device whose module has submodules of two APIs: the AR is set up with
// its IO data under its API, records, identification, diagnosis and
// alarms are addressed by (API, slot, subslot).
func TestAPIAddressing(t *testing.T) {
	n := newNet(t)
	d, _ := n.device(nil, func(c *Config) { c.Model = apiModel() })
	p := n.peer(d, "192.168.0.2", true)
	ctx := context.Background()
	req := p.ioCARFor(apiModel())
	resp := p.establish(req)
	if resp.ModuleDiff != nil {
		t.Fatalf("module diff %+v", resp.ModuleDiff)
	}
	ar := req.AR.ARUUID
	rec := func(api uint32, slot, subslot uint16, idx pnio.Index) controller.Record {
		return controller.Record{API: api, Slot: slot, Subslot: subslot, Index: idx}
	}

	// Records: the submodule's own API only.
	if b, err := p.rc.Read(ctx, ar, rec(apiIOLink, 5, 2, 0x20), 0); err != nil || !bytes.Equal(b, []byte{5}) {
		t.Fatalf("read %x %v", b, err)
	}
	if err := p.rc.Write(ctx, ar, rec(apiIOLink, 5, 2, 0x20), []byte{6}); err != nil {
		t.Fatal(err)
	}
	if b, err := p.rc.Read(ctx, ar, rec(apiIOLink, 5, 2, 0x20), 0); err != nil || !bytes.Equal(b, []byte{6}) {
		t.Fatalf("read back %x %v", b, err)
	}
	_, err := p.rc.Read(ctx, ar, rec(0, 5, 2, 0x20), 0)
	wantStatus(t, err, pnio.RWInvalidArea, 0)
	wantStatus(t, p.rc.Write(ctx, ar, rec(0, 5, 2, 0x20), []byte{6}), pnio.RWInvalidArea, 0)
	_, err = p.rc.Read(ctx, ar, rec(apiIOLink, 5, 1, pnio.IndexRealIdentSubslot), 0)
	wantStatus(t, err, pnio.RWInvalidArea, 0)
	// An API the device does not have, at the API level.
	_, err = p.rc.Read(ctx, ar, rec(0x1234, 0, 0, pnio.IndexRealIdentAPI), 0)
	wantStatus(t, err, pnio.RWInvalidArea, 0)

	// Identification: the AR level has both APIs, the API level one.
	read := func(r controller.Record) []byte {
		t.Helper()
		b, err := p.rc.Read(ctx, ar, r, 0)
		if err != nil {
			t.Fatalf("%v: %v", r, err)
		}
		return b
	}
	for _, idx := range []pnio.Index{pnio.IndexRealIdentAR, pnio.IndexExpectedIdentAR} {
		if id := decodeIdent(t, read(rec(0, 0, 1, idx))); len(id.APIs) != 2 || id.APIs[0].API != 0 || id.APIs[1].API != apiIOLink {
			t.Fatalf("%s %+v", idx, id)
		}
	}
	id := decodeIdent(t, read(rec(apiIOLink, 0, 0, pnio.IndexRealIdentAPI)))
	if len(id.APIs) != 1 || id.APIs[0].API != apiIOLink || len(id.APIs[0].Slots) != 1 || id.APIs[0].Slots[0].Slot != 5 ||
		len(id.APIs[0].Slots[0].Submodules) != 1 || id.APIs[0].Slots[0].Submodules[0].IdentNumber != 0x52 {
		t.Fatalf("API level %+v", id)
	}
	id = decodeIdent(t, read(rec(0, 0, 0, pnio.IndexRealIdentAPI)))
	if len(id.APIs) != 1 || id.APIs[0].API != 0 || len(id.APIs[0].Slots) != 6 || len(id.APIs[0].Slots[5].Submodules) != 1 {
		t.Fatalf("API 0 level %+v", id)
	}
	for _, idx := range []pnio.Index{pnio.IndexRealIdentSlot, pnio.IndexExpectedIdentSlot} {
		id = decodeIdent(t, read(rec(apiIOLink, 5, 1, idx)))
		if len(id.APIs) != 1 || id.APIs[0].API != apiIOLink || len(id.APIs[0].Slots) != 1 || len(id.APIs[0].Slots[0].Submodules) != 1 || id.APIs[0].Slots[0].Submodules[0].Subslot != 2 {
			t.Fatalf("%s %+v", idx, id)
		}
	}
	id = decodeIdent(t, read(rec(apiIOLink, 5, 2, pnio.IndexExpectedIdentSubslot)))
	if len(id.APIs) != 1 || id.APIs[0].API != apiIOLink || id.APIs[0].Slots[0].Submodules[0].Subslot != 2 {
		t.Fatalf("expected subslot %+v", id)
	}

	// A diagnosis: its alarm and its DiagnosisData carry the API.
	got := make(chan pnio.AlarmNotification, 4)
	p.openAlarms(req, resp, func(_ bool, an pnio.AlarmNotification, ep *alarm.Endpoint) {
		got <- an
		ack := pnio.AckFor(&an, pnio.Status{})
		go func() { _ = ep.Send(context.Background(), ack.Append(nil)) }()
	})
	if err := d.AddDiagnosis(ctx, 5, 2, pnio.DiagnosisItem{ChannelNumber: 1, ChannelProperties: pnio.ChanDirInput, ErrorType: pnio.ChannelErrLineBreak}); err != nil {
		t.Fatal(err)
	}
	if an := <-got; an.API != apiIOLink || an.Slot != 5 || an.Subslot != 2 || an.SubmoduleIdentNumber != 0x52 {
		t.Fatalf("alarm %+v", an)
	}
	if err := d.AddDiagnosis(ctx, 1, 1, pnio.DiagnosisItem{ChannelNumber: 1, ChannelProperties: pnio.ChanDirInput, ErrorType: pnio.ChannelErrLineBreak}); err != nil {
		t.Fatal(err)
	}
	if an := <-got; an.API != 0 || an.Slot != 1 {
		t.Fatalf("API 0 alarm %+v", an)
	}
	if err := d.SendAlarm(ctx, Alarm{Type: pnio.AlarmProcess, Slot: 5, Subslot: 2}); err != nil {
		t.Fatal(err)
	}
	if an := <-got; an.API != apiIOLink || an.Type != pnio.AlarmProcess {
		t.Fatalf("process alarm %+v", an)
	}
	diag := func(api uint32, slot, subslot uint16, idx pnio.Index) []pnio.DiagnosisData {
		t.Helper()
		return decodeDiag(t, read(rec(api, slot, subslot, idx)))
	}
	if dd := diag(0, 0, 0, pnio.IndexDiagDMQSDevice); len(dd) != 2 || dd[0].API != 0 || dd[1].API != apiIOLink || dd[1].Slot != 5 {
		t.Fatalf("device %+v", dd)
	}
	if dd := diag(0, 0, 0, pnio.IndexDiagAllAR); len(dd) != 2 {
		t.Fatalf("AR %+v", dd)
	}
	if dd := diag(apiIOLink, 0, 0, pnio.IndexDiagAllAPI); len(dd) != 1 || dd[0].API != apiIOLink {
		t.Fatalf("API %+v", dd)
	}
	if dd := diag(0, 0, 0, pnio.IndexDiagAllAPI); len(dd) != 1 || dd[0].API != 0 || dd[0].Slot != 1 {
		t.Fatalf("API 0 %+v", dd)
	}
	if dd := diag(apiIOLink, 5, 1, pnio.IndexDiagAllSlot); len(dd) != 1 || dd[0].Subslot != 2 {
		t.Fatalf("slot %+v", dd)
	}
	if dd := diag(0, 5, 1, pnio.IndexDiagAllSlot); len(dd) != 0 {
		t.Fatalf("slot in API 0 %+v", dd)
	}
	if dd := diag(apiIOLink, 5, 2, pnio.IndexDiagAllSubslot); len(dd) != 1 || dd[0].API != apiIOLink {
		t.Fatalf("subslot %+v", dd)
	}
	if dd, err := p.rc.ReadSubmoduleDiagnosis(ctx, ar, apiIOLink, 5, 2); err != nil || len(dd) != 1 || dd[0].API != apiIOLink || dd[0].Subslot != 2 {
		t.Fatalf("ReadSubmoduleDiagnosis %+v %v", dd, err)
	}
	_, err = p.rc.ReadDiagnosis(ctx, ar, 5, 2, true)
	wantStatus(t, err, pnio.RWInvalidArea, 0)
}

// The ModuleDiffBlock compares (API, slot, subslot): a submodule expected
// in another API is missing there, and so is a module none of whose
// submodules belongs to the expected API.
func TestModuleDiffAPI(t *testing.T) {
	n := newNet(t)
	d, _ := n.device(nil, func(c *Config) { c.Model = apiModel() })
	exp := func(api uint32, slot uint16, ident uint32, subslot uint16, subIdent uint32, in uint16) []pnio.ExpectedSubmoduleBlockReq {
		t := pnio.SubmoduleNoIO
		if in > 0 {
			t = pnio.SubmoduleInput
		}
		return []pnio.ExpectedSubmoduleBlockReq{{Modules: []pnio.ExpectedModule{{API: api, Slot: slot, IdentNumber: ident, Submodules: []pnio.ExpectedSubmodule{
			{Subslot: subslot, IdentNumber: subIdent, Properties: uint16(t), Descriptions: pnio.NewDataDescriptions(t, in, 0)},
		}}}}}
	}
	if diff, proper := d.moduleDiff(exp(apiIOLink, 5, 0x50, 2, 0x52, 4), nil); diff != nil || !proper[key{5, 2}] {
		t.Fatalf("matching %+v %v", diff, proper)
	}
	// 5/2 expected in API 0, where slot 5 has a module (subslot 1).
	diff, proper := d.moduleDiff(exp(0, 5, 0x50, 2, 0x52, 4), nil)
	if diff == nil || len(diff.APIs) != 1 || diff.APIs[0].API != 0 || diff.APIs[0].Modules[0].State != pnio.ModuleStateProper ||
		diff.APIs[0].Modules[0].Submodules[0].State&(0xF<<11) != pnio.SubmStateIdentNoSubmodule || proper[key{5, 2}] {
		t.Fatalf("wrong API submodule %+v %v", diff, proper)
	}
	// Slot 1 expected in the IO-Link API: no module there.
	diff, _ = d.moduleDiff(exp(apiIOLink, 1, 0x10, 1, 0x11, 8), nil)
	if diff == nil || len(diff.APIs) != 1 || diff.APIs[0].API != apiIOLink || diff.APIs[0].Modules[0].State != pnio.ModuleStateNoModule {
		t.Fatalf("wrong API module %+v", diff)
	}
}

// An IO controller that expects 5/2 in API 0 gets it reported missing in
// the Connect response, and the AR still comes up for the rest.
func TestConnectWrongAPI(t *testing.T) {
	n := newNet(t)
	d, _ := n.device(nil, func(c *Config) { c.Model = apiModel() })
	p := n.peer(d, "192.168.0.2", true)
	wrong := apiModel()
	wrong.Slots[4].Module.Submodules[1].API = 0
	req := p.ioCARFor(wrong)
	resp, err := p.connect(req)
	if err != nil {
		t.Fatal(err)
	}
	md := resp.ModuleDiff
	if md == nil || len(md.APIs) != 1 || md.APIs[0].API != 0 || len(md.APIs[0].Modules) != 1 || md.APIs[0].Modules[0].Slot != 5 ||
		md.APIs[0].Modules[0].Submodules[0].Subslot != 2 || md.APIs[0].Modules[0].Submodules[0].State&(0xF<<11) != pnio.SubmStateIdentNoSubmodule {
		t.Fatalf("module diff %+v", md)
	}
	if err := p.release(req); err != nil {
		t.Fatal(err)
	}
}

// apiOf gives the plugged submodule's API, and API 0 for a subslot the
// model does not have (a diagnosis or alarm of a channel without one).
func TestAPIOf(t *testing.T) {
	d := &Device{model: apiModel()}
	for _, c := range []struct {
		slot, subslot uint16
		want          uint32
	}{{5, 2, apiIOLink}, {5, 1, 0}, {0, 1, 0}, {9, 1, 0}, {5, 9, 0}} {
		if got := d.apiOf(c.slot, c.subslot); got != c.want {
			t.Errorf("apiOf(%d, %d) = %#x, want %#x", c.slot, c.subslot, got, c.want)
		}
	}
}

// A module without submodules is plugged in API 0, as before APIs, and in
// no other API.
func TestModuleDiffEmptyModule(t *testing.T) {
	n := newNet(t)
	m := apiModel()
	m.Slots = append(m.Slots, model.Slot{Number: 7, Module: model.Module{Name: "Empty", IdentNumber: 0x70}})
	d, _ := n.device(nil, func(c *Config) { c.Model = m })
	exp := func(api uint32) []pnio.ExpectedSubmoduleBlockReq {
		return []pnio.ExpectedSubmoduleBlockReq{{Modules: []pnio.ExpectedModule{{API: api, Slot: 7, IdentNumber: 0x70}}}}
	}
	if diff, _ := d.moduleDiff(exp(0), nil); diff != nil {
		t.Fatalf("API 0: %+v", diff)
	}
	diff, _ := d.moduleDiff(exp(apiIOLink), nil)
	if diff == nil || len(diff.APIs) != 1 || diff.APIs[0].API != apiIOLink || diff.APIs[0].Modules[0].State != pnio.ModuleStateNoModule {
		t.Fatalf("API %#x: %+v", apiIOLink, diff)
	}
}

// APIData (0xF821) lists the APIs of the device, API 0 first, within an AR
// and implicitly; a device of API 0 alone lists API 0.
func TestAPIData(t *testing.T) {
	n := newNet(t)
	d, _ := n.device(nil, func(c *Config) { c.Model = apiModel() })
	p := n.peer(d, "192.168.0.2", true)
	ctx := context.Background()
	if apis, err := p.rc.ReadAPIData(ctx, rpc.NilUUID); err != nil || !slices.Equal(apis, []uint32{0, apiIOLink}) {
		t.Fatalf("implicit %v %v", apis, err)
	}
	req := p.ioCARFor(apiModel())
	p.establish(req)
	if apis, err := p.rc.ReadAPIData(ctx, req.AR.ARUUID); err != nil || !slices.Equal(apis, []uint32{0, apiIOLink}) {
		t.Fatalf("in the AR %v %v", apis, err)
	}
	// The device-level index in an API the device does not have.
	_, err := p.rc.Read(ctx, rpc.NilUUID, controller.Record{API: 0x1234, Subslot: 1, Index: pnio.IndexAPIData}, 0)
	wantStatus(t, err, pnio.RWInvalidArea, 0)

	n2 := newNet(t)
	d2, _ := n2.device(nil)
	p2 := n2.peer(d2, "192.168.0.2", true)
	if apis, err := p2.rc.ReadAPIData(ctx, rpc.NilUUID); err != nil || !slices.Equal(apis, []uint32{0}) {
		t.Fatalf("API 0 only %v %v", apis, err)
	}
}
