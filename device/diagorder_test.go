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

// ackPolicy decides the AlarmAck of the n-th alarm (from 1): its status
// and how long the controller waits before sending it.
type ackPolicy func(n int, an pnio.AlarmNotification) (pnio.Status, time.Duration)

// arrival is an alarm and when it reached the controller.
type arrival struct {
	an pnio.AlarmNotification
	at time.Time
}

// policyPeer is an IO controller in the Data state that acknowledges the
// alarms as policy says; acked reports when each AlarmAck was sent.
func policyPeer(t *testing.T, policy ackPolicy) (*Device, <-chan arrival, func(n int) time.Time) {
	t.Helper()
	d, _, got, acked := policyPeerOn(t, nil, policy)
	return d, got, acked
}

// policyPeerOn is policyPeer with the device's node, for faults, and a
// handler h (nil for none).
func policyPeerOn(t *testing.T, h Handler, policy ackPolicy) (*Device, *node, <-chan arrival, func(n int) time.Time) {
	t.Helper()
	n := newNet(t)
	d, nd := n.device(h)
	p := n.peer(d, "192.168.0.2", true)
	req := p.ioCAR()
	resp := p.establish(req)
	got := make(chan arrival, 16)
	var mu sync.Mutex
	count := 0
	acked := map[int]time.Time{}
	p.openAlarms(req, resp, func(_ bool, an pnio.AlarmNotification, ep *alarm.Endpoint) {
		mu.Lock()
		count++
		i := count
		mu.Unlock()
		got <- arrival{an, time.Now()}
		st, delay := policy(i, an)
		go func() {
			time.Sleep(delay)
			mu.Lock()
			acked[i] = time.Now()
			mu.Unlock()
			ack := pnio.AckFor(&an, st)
			_ = ep.Send(context.Background(), ack.Append(nil))
		}()
	})
	return d, nd, got, func(i int) time.Time {
		mu.Lock()
		defer mu.Unlock()
		return acked[i]
	}
}

func nextArrival(t *testing.T, got <-chan arrival) (arrival, pnio.DiagnosisItem) {
	t.Helper()
	select {
	case a := <-got:
		var it pnio.DiagnosisItem
		if a.an.HasItem {
			if items, err := pnio.DecodeDiagnosisItems(a.an.USI, a.an.Data); err == nil && len(items) == 1 {
				it = items[0]
			}
		}
		return a, it
	case <-time.After(5 * time.Second):
		t.Fatal("no alarm")
	}
	panic("unreachable")
}

// Concurrent diagnosis operations are reported in the order they changed
// the state. Here an AddDiagnosis changes the state and is delayed before
// its alarm; a RemoveDiagnosis of the same diagnosis comes meanwhile.
// Before, the removal's alarm went out first and the controller was left
// with a diagnosis the device no longer had.
func TestDiagnosisOrder(t *testing.T) {
	d, got := diagPeer(t)
	ctx := context.Background()
	x := pnio.DiagnosisItem{ChannelNumber: 1, ChannelProperties: pnio.ChanDirInput, ErrorType: pnio.ChannelErrLineBreak}
	changed := make(chan struct{})
	var once sync.Once
	d.afterDiagChange = func() {
		once.Do(func() {
			close(changed)
			time.Sleep(300 * time.Millisecond)
		})
	}
	errA := make(chan error, 1)
	go func() { errA <- d.AddDiagnosis(ctx, 1, 1, x) }()
	<-changed
	errB := make(chan error, 1)
	go func() { errB <- d.RemoveDiagnosis(ctx, 1, 1, 1, pnio.ChannelErrLineBreak) }()
	if err := <-errA; err != nil {
		t.Fatal(err)
	}
	if err := <-errB; err != nil {
		t.Fatal(err)
	}
	an1, it1 := nextAlarm(t, got)
	an2, it2 := nextAlarm(t, got)
	if an1.Type != pnio.AlarmDiagnosis || !it1.Appears() || an2.Type != pnio.AlarmDiagnosisDisappears || it2.ChannelProperties&pnio.ChanSpecMask != pnio.ChanSpecDisappears {
		t.Fatalf("order: %v %#x, then %v %#x", an1.Type, it1.ChannelProperties, an2.Type, it2.ChannelProperties)
	}
	if len(d.Diagnoses(1, 1)) != 0 {
		t.Fatalf("diagnoses %+v", d.Diagnoses(1, 1))
	}
}

// A diagnosis operation waits for the previous one's alarms, but no longer
// than its ctx.
func TestDiagnosisWaitCancelled(t *testing.T) {
	d, _ := diagPeer(t)
	d.diagSem <- struct{}{}
	defer func() { <-d.diagSem }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	x := pnio.DiagnosisItem{ChannelNumber: 1, ErrorType: pnio.ChannelErrLineBreak}
	if err := d.AddDiagnosis(ctx, 1, 1, x); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AddDiagnosis: %v", err)
	}
	if err := d.RemoveDiagnosis(ctx, 1, 1, 1, pnio.ChannelErrLineBreak); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RemoveDiagnosis: %v", err)
	}
	if len(d.Diagnoses(1, 1)) != 0 {
		t.Fatal("recorded without the order")
	}
}

// When the second alarm of a replacement is not delivered (here the
// controller refuses it), it is owed: the next diagnosis operation of the
// submodule, an identical AddDiagnosis included, sends it first, so the
// controller's view catches up. Before, the identical call returned nil
// and the controller kept the old diagnosis.
func TestDiagnosisOwed(t *testing.T) {
	refused := pnio.Status{Code: 0xDF, Decode: 0x80, Code1: 0x22}
	d, got, _ := policyPeer(t, func(n int, _ pnio.AlarmNotification) (pnio.Status, time.Duration) {
		if n == 3 {
			return refused, 0
		}
		return pnio.Status{}, 0
	})
	ctx := context.Background()
	fault := pnio.DiagnosisItem{ChannelNumber: 1, ChannelProperties: pnio.ChanDirInput, ErrorType: pnio.ChannelErrLineBreak}
	if err := d.AddDiagnosis(ctx, 1, 1, fault); err != nil {
		t.Fatal(err)
	}
	nextArrival(t, got)
	req := fault
	req.ChannelProperties |= pnio.ChanMaintRequired
	var se *pnio.StatusError
	if err := d.AddDiagnosis(ctx, 1, 1, req); !errors.As(err, &se) {
		t.Fatalf("replacement with a refused second alarm: %v", err)
	}
	nextArrival(t, got) // the new one appears
	nextArrival(t, got) // the old one disappears: refused
	// The identical call sends what is owed, then nothing more.
	if err := d.AddDiagnosis(ctx, 1, 1, req); err != nil {
		t.Fatal(err)
	}
	a, it := nextArrival(t, got)
	if a.an.Type != pnio.AlarmDiagnosis || it.ChannelProperties&pnio.ChanSpecMask != pnio.ChanSpecOtherRemain || it.ChannelProperties&pnio.ChanMaintRequired != 0 {
		t.Fatalf("owed alarm %+v %#x", a.an, it.ChannelProperties)
	}
	if err := d.AddDiagnosis(ctx, 1, 1, req); err != nil {
		t.Fatal(err)
	}
	if err := d.SendAlarm(ctx, Alarm{Type: pnio.AlarmProcess, Slot: 1, Subslot: 1}); err != nil {
		t.Fatal(err)
	}
	if a, _ := nextArrival(t, got); a.an.Type != pnio.AlarmProcess {
		t.Fatalf("unexpected alarm %+v", a.an)
	}

	// Alarms owed to an AR that no longer owns the submodule are dropped:
	// its successor reads the diagnoses from the records.
	k := key{1, 1}
	d.mu.Lock()
	d.diagOwed[k] = owedDiag{ar: &ar{}, notes: []diagNote{{pnio.AlarmDiagnosis, fault, 0}}}
	d.mu.Unlock()
	if err := d.AddDiagnosis(ctx, 1, 1, req); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	_, left := d.diagOwed[k]
	d.mu.Unlock()
	if left {
		t.Fatal("owed alarms of another AR kept")
	}
	if err := d.SendAlarm(ctx, Alarm{Type: pnio.AlarmProcess, Slot: 1, Subslot: 1}); err != nil {
		t.Fatal(err)
	}
	if a, _ := nextArrival(t, got); a.an.Type != pnio.AlarmProcess {
		t.Fatalf("owed alarm of another AR sent: %+v", a.an)
	}
}

// Alarms of one priority are sent one at a time, each after the
// controller's AlarmAck of the previous, as p-net's alarm machine does.
// Before, a second alarm went out while the first awaited its AlarmAck.
func TestAlarmsSerializedToAck(t *testing.T) {
	d, got, acked := policyPeer(t, func(n int, _ pnio.AlarmNotification) (pnio.Status, time.Duration) {
		if n == 1 {
			return pnio.Status{}, 300 * time.Millisecond
		}
		return pnio.Status{}, 0
	})
	ctx := context.Background()
	errs := make(chan error, 2)
	go func() { errs <- d.SendAlarm(ctx, Alarm{Type: pnio.AlarmProcess, Slot: 1, Subslot: 1}) }()
	first, _ := nextArrival(t, got)
	go func() { errs <- d.SendAlarm(ctx, Alarm{Type: pnio.AlarmProcess, Slot: 3, Subslot: 1}) }()
	second, _ := nextArrival(t, got)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if ack := acked(1); ack.IsZero() || second.at.Before(ack) {
		t.Fatalf("second alarm at %v, first AlarmAck at %v (first alarm at %v)", second.at, ack, first.at)
	}
}

// ARDiagnosisState summarises the alarm's submodule only (as p-net does;
// whether it should cover the AR's other submodules is not established):
// when the fault of one submodule disappears while another submodule of
// the AR keeps one, the alarm carries no fault bits.
func TestARDiagnosisStateScope(t *testing.T) {
	d, got := diagPeer(t)
	ctx := context.Background()
	fault := pnio.DiagnosisItem{ChannelNumber: 1, ChannelProperties: pnio.ChanDirInput, ErrorType: pnio.ChannelErrLineBreak}
	for _, slot := range []uint16{1, 3} {
		if err := d.AddDiagnosis(ctx, slot, 1, fault); err != nil {
			t.Fatal(err)
		}
		nextAlarm(t, got)
	}
	if err := d.RemoveDiagnosis(ctx, 1, 1, 1, pnio.ChannelErrLineBreak); err != nil {
		t.Fatal(err)
	}
	an, _ := nextAlarm(t, got)
	if an.Slot != 1 || spec(an) != 0 {
		t.Fatalf("slot %d specifier %#x", an.Slot, spec(an))
	}
}

// An alarm stays outstanding until the controller's AlarmAck, also when
// its caller stops waiting: alarm A is delivered (the transport
// acknowledges it), its AlarmAck is late, the caller's ctx ends, and
// alarm B of the same priority goes out only after A's AlarmAck. Before,
// B went out at once, with A unacknowledged.
func TestAlarmOutstandingAfterCancel(t *testing.T) {
	d, got, acked := policyPeer(t, func(n int, _ pnio.AlarmNotification) (pnio.Status, time.Duration) {
		if n == 1 {
			return pnio.Status{}, 400 * time.Millisecond
		}
		return pnio.Status{}, 0
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := d.SendAlarm(ctx, Alarm{Type: pnio.AlarmProcess, Slot: 1, Subslot: 1}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("A: %v", err)
	}
	a, _ := nextArrival(t, got)
	if err := d.SendAlarm(context.Background(), Alarm{Type: pnio.AlarmProcess, Slot: 3, Subslot: 1}); err != nil {
		t.Fatalf("B: %v", err)
	}
	b, _ := nextArrival(t, got)
	if ack := acked(1); ack.IsZero() || b.at.Before(ack) {
		t.Fatalf("B at %v, A's AlarmAck at %v (A at %v)", b.at, ack, a.at)
	}
	// B waited within its ctx: one that ends first gives up.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	d.mu.Lock()
	var x *ar
	for _, v := range d.ars {
		x = v
	}
	d.mu.Unlock()
	x.alarmSem[0] <- struct{}{} // a priority kept busy
	err := d.SendAlarm(ctx2, Alarm{Type: pnio.AlarmProcess, Slot: 1, Subslot: 1})
	<-x.alarmSem[0]
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("C: %v", err)
	}
}

// Repeating a RemoveDiagnosis whose alarm was not delivered retries the
// alarm: the repeat sends what is owed and returns nil; once nothing is
// owed, a further repeat returns ErrNotFound. Before, the repeat returned
// ErrNotFound and the controller kept the diagnosis.
func TestRemoveDiagnosisRetry(t *testing.T) {
	refused := pnio.Status{Code: 0xDF, Decode: 0x80, Code1: 0x22}
	d, got, _ := policyPeer(t, func(n int, _ pnio.AlarmNotification) (pnio.Status, time.Duration) {
		if n == 2 {
			return refused, 0
		}
		return pnio.Status{}, 0
	})
	ctx := context.Background()
	fault := pnio.DiagnosisItem{ChannelNumber: 1, ChannelProperties: pnio.ChanDirInput, ErrorType: pnio.ChannelErrLineBreak}
	if err := d.AddDiagnosis(ctx, 1, 1, fault); err != nil {
		t.Fatal(err)
	}
	nextArrival(t, got)
	var se *pnio.StatusError
	if err := d.RemoveDiagnosis(ctx, 1, 1, 1, pnio.ChannelErrLineBreak); !errors.As(err, &se) {
		t.Fatalf("removal with a refused alarm: %v", err)
	}
	nextArrival(t, got)
	if err := d.RemoveDiagnosis(ctx, 1, 1, 1, pnio.ChannelErrLineBreak); err != nil {
		t.Fatalf("repeated removal: %v", err)
	}
	a, it := nextArrival(t, got)
	if a.an.Type != pnio.AlarmDiagnosisDisappears || it.ChannelProperties&pnio.ChanSpecMask != pnio.ChanSpecDisappears {
		t.Fatalf("retried alarm %v %#x", a.an.Type, it.ChannelProperties)
	}
	if err := d.RemoveDiagnosis(ctx, 1, 1, 1, pnio.ChannelErrLineBreak); !errors.Is(err, profinet.ErrNotFound) {
		t.Fatalf("removal with nothing owed: %v", err)
	}
}

// The alarms of a diagnosis operation belong to the AR they were sent to:
// when the submodule moves to another AR while an alarm fails, what is
// owed stays with the first AR and is dropped, so the new AR receives no
// historical alarm (it reads the diagnoses in its records). Before, the
// owner was looked up after the failure and the new AR received the old
// AR's alarm.
func TestOwedDiagnosisStaysWithItsAR(t *testing.T) {
	n := newNet(t)
	d, _ := n.device(nil)
	refused := pnio.Status{Code: 0xDF, Decode: 0x80, Code1: 0x22}
	p1 := n.peer(d, "192.168.0.2", true)
	req1 := p1.ioCAR()
	resp1 := p1.establish(req1)
	p1.openAlarms(req1, resp1, func(_ bool, an pnio.AlarmNotification, ep *alarm.Endpoint) {
		ack := pnio.AckFor(&an, refused)
		go func() { _ = ep.Send(context.Background(), ack.Append(nil)) }()
	})
	p2 := n.peer(d, "192.168.0.3", true)
	req2 := p2.ioCAR()
	resp2 := p2.establish(req2)
	got2 := make(chan pnio.AlarmNotification, 4)
	p2.openAlarms(req2, resp2, func(_ bool, an pnio.AlarmNotification, ep *alarm.Endpoint) {
		got2 <- an
		ack := pnio.AckFor(&an, pnio.Status{})
		go func() { _ = ep.Send(context.Background(), ack.Append(nil)) }()
	})
	d.mu.Lock()
	a2 := d.ars[req2.AR.ARUUID]
	d.mu.Unlock()
	if a2 == nil || a2.currentState() != ARStateData {
		t.Fatal("second AR not in Data")
	}
	k := key{1, 1}
	// The submodule moves to the second AR while the first one's alarm
	// fails.
	d.diagFailed = func() {
		d.imgMu.Lock()
		d.owner[k] = a2
		d.imgMu.Unlock()
	}
	ctx := context.Background()
	fault := pnio.DiagnosisItem{ChannelNumber: 1, ChannelProperties: pnio.ChanDirInput, ErrorType: pnio.ChannelErrLineBreak}
	var se *pnio.StatusError
	if err := d.AddDiagnosis(ctx, 1, 1, fault); !errors.As(err, &se) {
		t.Fatalf("refused alarm: %v", err)
	}
	d.diagFailed = nil
	// An identical AddDiagnosis sends only what is owed to the new owner:
	// nothing.
	if err := d.AddDiagnosis(ctx, 1, 1, fault); err != nil {
		t.Fatal(err)
	}
	if err := d.SendAlarm(ctx, Alarm{Type: pnio.AlarmProcess, Slot: 1, Subslot: 1}); err != nil {
		t.Fatal(err)
	}
	select {
	case an := <-got2:
		if an.Type != pnio.AlarmProcess {
			t.Fatalf("the new AR received the old AR's %v", an.Type)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no alarm at the new AR")
	}
}
