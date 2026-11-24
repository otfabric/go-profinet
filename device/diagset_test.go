// SPDX-License-Identifier: MIT

package device

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/alarm"
	"github.com/otfabric/go-profinet/pnio"
)

// diagPeer is an IO controller in the Data state that acknowledges every
// alarm and hands it to the test.
func diagPeer(t *testing.T) (*Device, <-chan pnio.AlarmNotification) {
	t.Helper()
	n := newNet(t)
	d, _ := n.device(nil)
	p := n.peer(d, "192.168.0.2", true)
	req := p.ioCAR()
	resp := p.establish(req)
	got := make(chan pnio.AlarmNotification, 16)
	p.openAlarms(req, resp, func(_ bool, an pnio.AlarmNotification, ep *alarm.Endpoint) {
		got <- an
		ack := pnio.AckFor(&an, pnio.Status{})
		go func() { _ = ep.Send(context.Background(), ack.Append(nil)) }()
	})
	return d, got
}

// nextAlarm returns the next alarm and its single diagnosis item.
func nextAlarm(t *testing.T, got <-chan pnio.AlarmNotification) (pnio.AlarmNotification, pnio.DiagnosisItem) {
	t.Helper()
	select {
	case an := <-got:
		items, err := pnio.DecodeDiagnosisItems(an.USI, an.Data)
		if err != nil || len(items) != 1 {
			t.Fatalf("alarm %+v: items %+v %v", an, items, err)
		}
		return an, items[0]
	case <-time.After(5 * time.Second):
		t.Fatal("no alarm")
	}
	panic("unreachable")
}

// noAlarm checks that no alarm is sent before a process alarm the test
// sends as a marker.
func noAlarm(t *testing.T, d *Device, got <-chan pnio.AlarmNotification) {
	t.Helper()
	if err := d.SendAlarm(context.Background(), Alarm{Type: pnio.AlarmProcess, Slot: 1, Subslot: 1}); err != nil {
		t.Fatal(err)
	}
	if an := <-got; an.Type != pnio.AlarmProcess {
		t.Fatalf("unexpected alarm %+v", an)
	}
}

const faultSpec = pnio.AlarmSpecChannelDiag | pnio.AlarmSpecSubmoduleDiag | pnio.AlarmSpecARDiag

func spec(an pnio.AlarmNotification) uint16 { return an.Specifier &^ pnio.AlarmSpecSeqMask }

// An identical diagnosis added twice is recorded and reported once; one
// that changes its severity replaces the active one with an appears and
// a disappears-others-remain alarm; removing it leaves none (p-net issue
// 2729).
func TestDiagnosisIdempotent(t *testing.T) {
	d, got := diagPeer(t)
	ctx := context.Background()
	fault := pnio.DiagnosisItem{ChannelNumber: 1, ChannelProperties: pnio.ChanDirInput, ErrorType: pnio.ChannelErrLineBreak}
	if err := d.AddDiagnosis(ctx, 1, 1, fault); err != nil {
		t.Fatal(err)
	}
	an, it := nextAlarm(t, got)
	if an.Type != pnio.AlarmDiagnosis || spec(an) != faultSpec || !it.Appears() || it.ErrorType != pnio.ChannelErrLineBreak {
		t.Fatalf("first alarm %+v %+v", an, it)
	}
	for range 3 {
		if err := d.AddDiagnosis(ctx, 1, 1, fault); err != nil {
			t.Fatalf("repeated add: %v", err)
		}
	}
	noAlarm(t, d, got)
	if ds := d.Diagnoses(1, 1); len(ds) != 1 {
		t.Fatalf("diagnoses %+v", ds)
	}
	// Another direction or extended error type is another diagnosis.
	out := fault
	out.ChannelProperties = pnio.ChanDirOutput
	if err := d.AddDiagnosis(ctx, 1, 1, out); err != nil {
		t.Fatal(err)
	}
	nextAlarm(t, got)
	if ds := d.Diagnoses(1, 1); len(ds) != 2 {
		t.Fatalf("diagnoses %+v", ds)
	}
	// The input diagnosis becomes a maintenance demand: it replaces the
	// fault, which disappears while the output fault remains.
	maint := fault
	maint.ChannelProperties |= pnio.ChanMaintDemanded
	if err := d.AddDiagnosis(ctx, 1, 1, maint); err != nil {
		t.Fatal(err)
	}
	an, it = nextAlarm(t, got)
	if !it.Appears() || it.ChannelProperties&pnio.ChanMaintQualified != pnio.ChanMaintDemanded || spec(an) != faultSpec {
		t.Fatalf("appears %+v %+v", an, it)
	}
	an, it = nextAlarm(t, got)
	if it.ChannelProperties&pnio.ChanSpecMask != pnio.ChanSpecOtherRemain || it.ChannelProperties&pnio.ChanMaintQualified != 0 || an.Type != pnio.AlarmDiagnosis {
		t.Fatalf("disappears %+v %+v", an, it)
	}
	ds := d.Diagnoses(1, 1)
	if len(ds) != 2 || ds[0].ChannelProperties&pnio.ChanMaintQualified != pnio.ChanMaintDemanded {
		t.Fatalf("diagnoses %+v", ds)
	}
	if err := d.AddDiagnosis(ctx, 1, 1, maint); err != nil {
		t.Fatal(err)
	}
	noAlarm(t, d, got)
	// Without the output fault only the maintenance demand remains: no
	// fault bit.
	if err := d.RemoveDiagnosis(ctx, 1, 1, 1, pnio.ChannelErrLineBreak); err != nil {
		t.Fatal(err)
	}
	// RemoveDiagnosis removes every direction of the channel's error type
	// and reports each: the first while the other remains, the last as
	// the end of the submodule's diagnoses.
	an, it = nextAlarm(t, got)
	if an.Type != pnio.AlarmDiagnosis || spec(an) != 0 || it.ChannelProperties&pnio.ChanSpecMask != pnio.ChanSpecOtherRemain || it.ChannelProperties&pnio.ChanDirInOut != pnio.ChanDirInput {
		t.Fatalf("remove, first %+v %+v", an, it)
	}
	an, it = nextAlarm(t, got)
	if an.Type != pnio.AlarmDiagnosisDisappears || spec(an) != 0 || it.ChannelProperties&pnio.ChanSpecMask != pnio.ChanSpecDisappears || it.ChannelProperties&pnio.ChanDirInOut != pnio.ChanDirOutput {
		t.Fatalf("remove, last %+v %+v", an, it)
	}
	noAlarm(t, d, got)
	if ds := d.Diagnoses(1, 1); len(ds) != 0 {
		t.Fatalf("diagnoses %+v", ds)
	}
}

// The alarm specifier summarises the submodule's active diagnoses: a
// maintenance diagnosis does not advertise a fault (p-net issue 2727).
func TestDiagnosisAlarmSpecifier(t *testing.T) {
	d, got := diagPeer(t)
	ctx := context.Background()
	add := func(it pnio.DiagnosisItem) pnio.AlarmNotification {
		t.Helper()
		if err := d.AddDiagnosis(ctx, 1, 1, it); err != nil {
			t.Fatal(err)
		}
		an, _ := nextAlarm(t, got)
		return an
	}
	remove := func(ch, et uint16) pnio.AlarmNotification {
		t.Helper()
		if err := d.RemoveDiagnosis(ctx, 1, 1, ch, et); err != nil {
			t.Fatal(err)
		}
		an, _ := nextAlarm(t, got)
		return an
	}
	if an := add(pnio.DiagnosisItem{ChannelNumber: 1, ChannelProperties: pnio.ChanMaintRequired, ErrorType: 0x100}); spec(an) != 0 {
		t.Fatalf("maintenance required: %#x", spec(an))
	}
	if an := add(pnio.DiagnosisItem{ChannelNumber: 2, ChannelProperties: pnio.ChanMaintQualified, ErrorType: 0x101, Qualifier: 1 << 20}); spec(an) != 0 {
		t.Fatalf("qualified demanded: %#x", spec(an))
	}
	if an := add(pnio.DiagnosisItem{ChannelNumber: 3, ErrorType: pnio.ChannelErrOverload}); spec(an) != faultSpec {
		t.Fatalf("fault: %#x", spec(an))
	}
	// The fault goes; the maintenance diagnoses remain.
	if an := remove(3, pnio.ChannelErrOverload); spec(an) != 0 || an.Type != pnio.AlarmDiagnosis {
		t.Fatalf("fault disappears: %+v", an)
	}
	// A qualified diagnosis with a fault qualifier is a fault; the alarm
	// carries the qualifier.
	if err := d.AddDiagnosis(ctx, 1, 1, pnio.DiagnosisItem{ChannelNumber: 4, ChannelProperties: pnio.ChanMaintQualified, ErrorType: 0x102, Qualifier: 1 << 28}); err != nil {
		t.Fatal(err)
	}
	an, it := nextAlarm(t, got)
	if spec(an) != faultSpec || an.USI != pnio.USIQualifiedChannel || it.Qualifier != 1<<28 {
		t.Fatalf("qualified fault: %+v %+v", an, it)
	}
	// A maintenance diagnosis that appears while a fault is active keeps
	// the fault bits.
	if an := add(pnio.DiagnosisItem{ChannelNumber: 5, ChannelProperties: pnio.ChanMaintDemanded, ErrorType: 0x103}); spec(an) != faultSpec {
		t.Fatalf("maintenance beside a fault: %#x", spec(an))
	}
	remove(4, 0x102)
	remove(1, 0x100)
	remove(2, 0x101)
	if an := remove(5, 0x103); spec(an) != 0 || an.Type != pnio.AlarmDiagnosisDisappears {
		t.Fatalf("last disappears: %+v", an)
	}
}

func TestDiagSpecifier(t *testing.T) {
	it := func(props uint16, q uint32) pnio.DiagnosisItem {
		return pnio.DiagnosisItem{ChannelProperties: props | pnio.ChanSpecAppears, Qualifier: q}
	}
	for _, c := range []struct {
		name  string
		items []pnio.DiagnosisItem
		want  uint16
	}{
		{"none", nil, 0},
		{"fault", []pnio.DiagnosisItem{it(0, 0)}, faultSpec},
		{"accumulative output fault", []pnio.DiagnosisItem{it(pnio.ChanAccumulative|pnio.ChanDirOutput|0x08, 0)}, faultSpec},
		{"required", []pnio.DiagnosisItem{it(pnio.ChanMaintRequired, 0)}, 0},
		{"demanded", []pnio.DiagnosisItem{it(pnio.ChanMaintDemanded, 0)}, 0},
		{"qualified without severity", []pnio.DiagnosisItem{it(pnio.ChanMaintQualified, 0)}, 0},
		{"qualified advice", []pnio.DiagnosisItem{it(pnio.ChanMaintQualified, 1<<3)}, 0},
		{"qualified required", []pnio.DiagnosisItem{it(pnio.ChanMaintQualified, 1<<7)}, 0},
		{"qualified demanded", []pnio.DiagnosisItem{it(pnio.ChanMaintQualified, 1<<26)}, 0},
		{"qualified fault", []pnio.DiagnosisItem{it(pnio.ChanMaintQualified, 1<<27)}, faultSpec},
		{"qualified fault high bit", []pnio.DiagnosisItem{it(pnio.ChanMaintQualified, 1<<31)}, faultSpec},
		{"maintenance and fault", []pnio.DiagnosisItem{it(pnio.ChanMaintRequired, 0), it(0, 0)}, faultSpec},
	} {
		if got := diagSpecifier(c.items); got != c.want {
			t.Errorf("%s: %#x, want %#x", c.name, got, c.want)
		}
	}
}

func TestDiagUSI(t *testing.T) {
	for _, c := range []struct {
		items []pnio.DiagnosisItem
		want  uint16
	}{
		{nil, pnio.USIChannelDiagnosis},
		{[]pnio.DiagnosisItem{{ErrorType: 1}}, pnio.USIChannelDiagnosis},
		{[]pnio.DiagnosisItem{{ExtErrorType: 1}}, pnio.USIExtChannelDiagnosis},
		{[]pnio.DiagnosisItem{{ExtAddValue: 1}}, pnio.USIExtChannelDiagnosis},
		{[]pnio.DiagnosisItem{{Qualifier: 1 << 27}}, pnio.USIQualifiedChannel},
		{[]pnio.DiagnosisItem{{ChannelProperties: pnio.ChanMaintQualified}}, pnio.USIQualifiedChannel},
		{[]pnio.DiagnosisItem{{ExtErrorType: 1}, {Qualifier: 1 << 27}, {}}, pnio.USIQualifiedChannel},
	} {
		if got := diagUSI(c.items...); got != c.want {
			t.Errorf("%+v: %#x, want %#x", c.items, got, c.want)
		}
	}
}

// Without an AR a diagnosis is recorded and its first add reports that no
// AR received it; a repeated identical add changes nothing and returns
// nil; a changed one is recorded and reported like a new one. Concurrent
// identical adds record one diagnosis.
func TestDiagnosisWithoutAR(t *testing.T) {
	n := newNet(t)
	d, _ := n.device(nil)
	ctx := context.Background()
	item := pnio.DiagnosisItem{ChannelNumber: 7, ErrorType: pnio.ChannelErrOvertemp}
	const workers = 32
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() { errs <- d.AddDiagnosis(ctx, 1, 1, item) })
	}
	wg.Wait()
	close(errs)
	var invalid int
	for err := range errs {
		switch {
		case err == nil:
		case errors.Is(err, profinet.ErrInvalidState):
			invalid++
		default:
			t.Fatal(err)
		}
	}
	if invalid != 1 {
		t.Fatalf("%d adds reported no AR, want the first only", invalid)
	}
	if ds := d.Diagnoses(1, 1); len(ds) != 1 {
		t.Fatalf("diagnoses %+v", ds)
	}
	if err := d.AddDiagnosis(ctx, 1, 1, item); err != nil {
		t.Fatalf("repeated add without an AR: %v", err)
	}
	item.ExtAddValue = 9
	if err := d.AddDiagnosis(ctx, 1, 1, item); !errors.Is(err, profinet.ErrInvalidState) {
		t.Fatalf("changed add without an AR: %v", err)
	}
	if ds := d.Diagnoses(1, 1); len(ds) != 1 || ds[0].ExtAddValue != 9 {
		t.Fatalf("diagnoses %+v", ds)
	}
}

// The diagnosis records carry a qualified diagnosis in the qualified
// format.
func TestDiagnosisRecordQualified(t *testing.T) {
	n := newNet(t)
	d, _ := n.device(nil)
	_ = d.AddDiagnosis(context.Background(), 1, 1, pnio.DiagnosisItem{ChannelNumber: 1, ErrorType: 1})
	_ = d.AddDiagnosis(context.Background(), 1, 1, pnio.DiagnosisItem{ChannelNumber: 2, ChannelProperties: pnio.ChanMaintQualified, ErrorType: 2, Qualifier: 1 << 28})
	dds := decodeDiag(t, d.diagnosis(pnio.ReadReq{Index: pnio.IndexDiagDMQSDevice}))
	if len(dds) != 1 || dds[0].USI != pnio.USIQualifiedChannel || len(dds[0].Items) != 2 || dds[0].Items[1].Qualifier != 1<<28 {
		t.Fatalf("diagnosis data %+v", dds)
	}
}
