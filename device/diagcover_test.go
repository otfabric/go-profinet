// SPDX-License-Identifier: MIT

package device

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/pnio"
	"github.com/otfabric/go-profinet/rt"
)

// TestDiagnosisClosedWhileWaiting: a diagnosis operation waiting for
// its turn (diagSem) ends with ErrClosed when the device closes.
func TestDiagnosisClosedWhileWaiting(t *testing.T) {
	n := newNet(t)
	d, _ := n.device(nil)
	d.diagSem <- struct{}{} // an operation in progress
	errc := make(chan error, 1)
	go func() {
		errc <- d.AddDiagnosis(context.Background(), 1, 1, pnio.DiagnosisItem{ChannelNumber: 1, ChannelProperties: pnio.ChanDirInput, ErrorType: pnio.ChannelErrLineBreak})
	}()
	time.Sleep(50 * time.Millisecond)
	_ = d.Close()
	select {
	case err := <-errc:
		if !errors.Is(err, profinet.ErrClosed) {
			t.Fatalf("AddDiagnosis: %v, want ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AddDiagnosis still waiting after Close")
	}
	<-d.diagSem
}

// TestOwedDiagnosisCapped: the alarms owed to a submodule are capped at
// maxOwedDiag; the oldest are dropped.
func TestOwedDiagnosisCapped(t *testing.T) {
	refused := pnio.Status{Code: 0xDF, Decode: 0x80, Code1: 0x22}
	d, got, _ := policyPeer(t, func(int, pnio.AlarmNotification) (pnio.Status, time.Duration) { return refused, 0 })
	done := make(chan struct{})
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			select {
			case <-got:
			case <-done:
				return
			}
		}
	}()
	t.Cleanup(func() { close(done); <-drained })
	ctx := context.Background()
	const ops = maxOwedDiag + 2
	for i := range ops {
		it := pnio.DiagnosisItem{ChannelNumber: uint16(i + 1), ChannelProperties: pnio.ChanDirInput, ErrorType: pnio.ChannelErrLineBreak}
		var se *pnio.StatusError
		if err := d.AddDiagnosis(ctx, 1, 1, it); !errors.As(err, &se) {
			t.Fatalf("AddDiagnosis %d: %v, want the refusal", i+1, err)
		}
	}
	d.mu.Lock()
	o := d.diagOwed[key{1, 1}]
	d.mu.Unlock()
	if len(o.notes) != maxOwedDiag {
		t.Fatalf("%d alarms owed, want %d", len(o.notes), maxOwedDiag)
	}
	// The two oldest (channels 1 and 2) were dropped.
	if first, last := o.notes[0].item.ChannelNumber, o.notes[len(o.notes)-1].item.ChannelNumber; first != 3 || last != ops {
		t.Fatalf("owed channels %d..%d, want 3..%d", first, last, ops)
	}
}

// TestAlarmOwnerSRPrimary: the alarms of a submodule of an AR set go to
// the set's primary AR, none while there is none.
func TestAlarmOwnerSRPrimary(t *testing.T) {
	n := newNet(t)
	d, _ := n.device(nil, func(c *Config) { c.SystemRedundancy = &SRConfig{} })
	p1 := n.peer(d, "192.168.0.2", true)
	p2 := n.peer(d, "192.168.0.3", true)
	r1, r2 := p1.srConnect(1, 5000), p2.srConnect(2, 5000)
	r2.AR.StationName = "plc-b"
	p1.establish(r1)
	p2.establish(r2)
	d.mu.Lock()
	a1, a2 := d.ars[r1.AR.ARUUID], d.ars[r2.AR.ARUUID]
	d.mu.Unlock()
	if !sameSet(a1, a2) {
		t.Fatal("the two IOCARSRs are not one AR set")
	}
	k := key{1, 1}
	if o := d.alarmOwner(k); o != nil {
		t.Fatalf("owner %s without a primary", o.uuid)
	}
	d.srOutput(a1, rt.PrimaryRequest)
	if o := d.alarmOwner(k); o != a1 {
		t.Fatalf("owner %v, want AR 1, the primary", o)
	}
	d.srOutput(a1, rt.BackupRequest)
	d.srOutput(a2, rt.PrimaryRequest)
	if o := d.alarmOwner(k); o != a2 {
		t.Fatalf("owner %v, want AR 2, the primary", o)
	}
}

// TestOutstandingAlarmEndsAtClose: an alarm left outstanding by its
// caller frees its priority when the device closes (Close aborts the AR,
// which ends the wait for its AlarmAck), not at the AlarmAck timer.
func TestOutstandingAlarmEndsAtClose(t *testing.T) {
	// The AlarmAck comes after the device closed; the test waits for its
	// sender to end.
	const late = 500 * time.Millisecond
	d, got, _ := policyPeer(t, func(int, pnio.AlarmNotification) (pnio.Status, time.Duration) { return pnio.Status{}, late })
	start := time.Now()
	t.Cleanup(func() { time.Sleep(late - time.Since(start) + 50*time.Millisecond) })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := d.SendAlarm(ctx, Alarm{Type: pnio.AlarmProcess, Slot: 1, Subslot: 1}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SendAlarm: %v", err)
	}
	nextArrival(t, got)
	d.mu.Lock()
	var x *ar
	for _, v := range d.ars {
		x = v
	}
	d.mu.Unlock()
	if len(x.alarmSem[0]) != 1 {
		t.Fatal("the priority is not held by the outstanding alarm")
	}
	_ = d.Close()
	eventually(t, 5*time.Second, "priority freed", func() bool { return len(x.alarmSem[0]) == 0 })
	if time.Since(start) >= late {
		t.Fatal("freed no earlier than the AlarmAck: the test shows nothing")
	}
}
