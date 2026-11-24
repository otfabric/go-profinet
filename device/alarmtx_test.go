// SPDX-License-Identifier: MIT

package device

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/alarm"
	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/pnio"
	"github.com/otfabric/go-profinet/vnet"
)

// rtaPDU decodes the RTA PDU an Ethernet frame carries.
func rtaPDU(frame []byte) (alarm.PDU, bool) {
	if len(frame) < 14 {
		return alarm.PDU{}, false
	}
	off, et := 14, binary.BigEndian.Uint16(frame[12:])
	if et == 0x8100 && len(frame) >= 18 {
		off, et = 18, binary.BigEndian.Uint16(frame[16:])
	}
	if ethernet.EtherType(et) != ethernet.EtherTypeProfinet {
		return alarm.PDU{}, false
	}
	p, err := alarm.Decode(frame[off:])
	return p, err == nil
}

// dropAlarmData makes the device's node drop the alarm DATA PDUs it
// sends while on, and reports each one it sent on sent.
func dropAlarmData(nd *node, sent chan<- struct{}) func(on bool) {
	return func(on bool) {
		if !on {
			nd.ep.SetFaults(vnet.Faults{})
			return
		}
		nd.ep.SetFaults(vnet.Faults{Loss: 1, Match: func(f []byte) bool {
			p, ok := rtaPDU(f)
			if ok && p.Type == alarm.PDUData {
				select {
				case sent <- struct{}{}:
				default:
				}
				return true
			}
			return false
		}})
	}
}

func lineBreak(ch uint16) pnio.DiagnosisItem {
	return pnio.DiagnosisItem{ChannelNumber: ch, ChannelProperties: pnio.ChanDirInput, ErrorType: pnio.ChannelErrLineBreak}
}

func isOwed(d *Device, k key) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.diagOwed[k]
	return ok
}

// noArrival checks that no alarm reaches the controller for a while.
func noArrival(t *testing.T, got <-chan arrival, what string) {
	t.Helper()
	select {
	case a := <-got:
		t.Fatalf("%s: alarm %+v", what, a.an)
	case <-time.After(300 * time.Millisecond):
	}
}

// A diagnosis alarm whose caller's context ends after its transmission
// and before the transport ACK stays with the AR: the endpoint keeps
// retransmitting it, it is not owed, and the controller receives it once.
// The next alarm follows it in sequence, after its AlarmAck. Before, the
// endpoint left the PDU pending and the device also owed the alarm: the
// next operation retransmitted the PDU and then sent the owed alarm
// again, with a new sequence number.
func TestAlarmCancelledBeforeTransportAck(t *testing.T) {
	d, nd, got, acked := policyPeerOn(t, nil, func(int, pnio.AlarmNotification) (pnio.Status, time.Duration) {
		return pnio.Status{}, 0
	})
	sent := make(chan struct{}, 1)
	drop := dropAlarmData(nd, sent)
	drop(true)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- d.AddDiagnosis(ctx, 1, 1, lineBreak(1)) }()
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("the alarm was not transmitted")
	}
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) || !errors.Is(err, errAlarmOutstanding) {
		t.Fatalf("AddDiagnosis: %v", err)
	}
	if isOwed(d, key{1, 1}) {
		t.Fatal("a transmitted alarm is owed")
	}
	drop(false)
	first, it := nextArrival(t, got)
	if it.ChannelNumber != 1 {
		t.Fatalf("first alarm for channel %d", it.ChannelNumber)
	}
	if err := d.AddDiagnosis(context.Background(), 1, 1, lineBreak(2)); err != nil {
		t.Fatal(err)
	}
	if acked(1).IsZero() {
		t.Fatal("the next alarm went before the AlarmAck")
	}
	second, it := nextArrival(t, got)
	if it.ChannelNumber != 2 {
		t.Fatalf("next alarm for channel %d, want 2 (channel 1 sent twice)", it.ChannelNumber)
	}
	if s1, s2 := first.an.Sequence(), second.an.Sequence(); s2 != (s1+1)&pnio.AlarmSpecSeqMask {
		t.Fatalf("sequence numbers %d then %d", s1, s2)
	}
	noArrival(t, got, "after both")
}

// A diagnosis alarm whose first transmission fails never left: it is
// owed, its AlarmSpecifier sequence number is given back, and the next
// operation sends it, then its own, in sequence. Before, the number was
// used up and the controller saw a gap.
func TestAlarmNotTransmittedGivesBackSequence(t *testing.T) {
	d, nd, got, _ := policyPeerOn(t, nil, func(int, pnio.AlarmNotification) (pnio.Status, time.Duration) {
		return pnio.Status{}, 0
	})
	if err := d.AddDiagnosis(context.Background(), 1, 1, lineBreak(1)); err != nil {
		t.Fatal(err)
	}
	first, _ := nextArrival(t, got)
	nd.ep.SetFaults(vnet.Faults{})
	a := arOf(d)
	// The endpoint fails the first transmission: the link is down.
	nd.ep.SetLinkUp(false)
	if err := d.AddDiagnosis(context.Background(), 1, 1, lineBreak(2)); !errors.Is(err, profinet.ErrLinkDown) {
		t.Fatalf("AddDiagnosis on a down link: %v", err)
	}
	if !isOwed(d, key{1, 1}) {
		t.Fatal("an alarm that never left is not owed")
	}
	a.mu.Lock()
	next := a.alarmSeq[0]
	a.mu.Unlock()
	if want := (first.an.Sequence() + 1) & pnio.AlarmSpecSeqMask; next != want {
		t.Fatalf("next sequence number %d, want %d given back", next, want)
	}
	nd.ep.SetLinkUp(true)
	if err := d.AddDiagnosis(context.Background(), 1, 1, lineBreak(3)); err != nil {
		t.Fatal(err)
	}
	prev := first.an.Sequence()
	for _, ch := range []uint16{2, 3} {
		arr, it := nextArrival(t, got)
		if it.ChannelNumber != ch {
			t.Fatalf("alarm for channel %d, want %d", it.ChannelNumber, ch)
		}
		if s := arr.an.Sequence(); s != (prev+1)&pnio.AlarmSpecSeqMask {
			t.Fatalf("channel %d: sequence %d after %d", ch, s, prev)
		}
		prev = arr.an.Sequence()
	}
	noArrival(t, got, "after the owed alarm")
}

// An AlarmAck later than the caller's wait (AlarmAckTimeout) does not
// let the next alarm of the priority go first: it waits for the AlarmAck,
// and the alarm, which the controller received, is not owed. Before, the
// device's AlarmAck timer freed the priority and owed the alarm: the next
// alarm overtook the late AlarmAck and the first was sent twice.
func TestAlarmAckLaterThanTimer(t *testing.T) {
	late := AlarmAckTimeout + 500*time.Millisecond
	d, _, got, acked := policyPeerOn(t, nil, func(n int, _ pnio.AlarmNotification) (pnio.Status, time.Duration) {
		if n == 1 {
			return pnio.Status{}, late
		}
		return pnio.Status{}, 0
	})
	err := d.AddDiagnosis(context.Background(), 1, 1, lineBreak(1))
	if !errors.Is(err, profinet.ErrTimeout) || !errors.Is(err, errAlarmOutstanding) {
		t.Fatalf("AddDiagnosis: %v", err)
	}
	nextArrival(t, got)
	if isOwed(d, key{1, 1}) {
		t.Fatal("the received alarm is owed")
	}
	if arOf(d) == nil || arOf(d).currentState() != ARStateData {
		t.Fatal("the AR was aborted for a late AlarmAck")
	}
	if err := d.AddDiagnosis(context.Background(), 1, 1, lineBreak(2)); err != nil {
		t.Fatal(err)
	}
	if acked(1).IsZero() {
		t.Fatal("the next alarm went before the late AlarmAck")
	}
	if _, it := nextArrival(t, got); it.ChannelNumber != 2 {
		t.Fatalf("next alarm for channel %d, want 2", it.ChannelNumber)
	}
	noArrival(t, got, "after both")
}

// A late negative AlarmAck ends the outstanding alarm: the priority is
// free again, and the refused alarm is not sent again (p-net does not
// resend a refused alarm).
func TestAlarmLateNegativeAck(t *testing.T) {
	refused := pnio.ServiceStatus(pnio.CodePNIO, pnio.Code1AlarmAck, 1)
	d, _, got, acked := policyPeerOn(t, nil, func(n int, _ pnio.AlarmNotification) (pnio.Status, time.Duration) {
		if n == 1 {
			return refused, 300 * time.Millisecond
		}
		return pnio.Status{}, 0
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := d.AddDiagnosis(ctx, 1, 1, lineBreak(1)); !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, errAlarmOutstanding) {
		t.Fatalf("AddDiagnosis: %v", err)
	}
	nextArrival(t, got)
	if err := d.AddDiagnosis(context.Background(), 1, 1, lineBreak(2)); err != nil {
		t.Fatal(err)
	}
	if acked(1).IsZero() {
		t.Fatal("the next alarm went before the refusal")
	}
	if _, it := nextArrival(t, got); it.ChannelNumber != 2 {
		t.Fatalf("next alarm for channel %d, want 2 (the refused one resent)", it.ChannelNumber)
	}
	noArrival(t, got, "after both")
	if isOwed(d, key{1, 1}) {
		t.Fatal("the refused alarm is owed")
	}
}

// An alarm whose caller stopped waiting and whose transport then gives up
// aborts the AR, as a timeout the caller waits for does; it is not owed,
// and the priority's wait ends with the AR.
func TestAlarmOutstandingTransportTimeout(t *testing.T) {
	h := newRecHandler()
	d, nd, _, _ := policyPeerOn(t, h, func(int, pnio.AlarmNotification) (pnio.Status, time.Duration) {
		return pnio.Status{}, 0
	})
	a := arOf(d)
	sent := make(chan struct{}, 1)
	dropAlarmData(nd, sent)(true)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- d.AddDiagnosis(ctx, 1, 1, lineBreak(1)) }()
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("the alarm was not transmitted")
	}
	cancel()
	if err := <-errc; !errors.Is(err, errAlarmOutstanding) {
		t.Fatalf("AddDiagnosis: %v", err)
	}
	// The next alarm waits for the outstanding one, which ends with the AR.
	next := make(chan error, 1)
	go func() { next <- d.AddDiagnosis(context.Background(), 1, 1, lineBreak(2)) }()
	h.waitReason(t, a.uuid, pnio.AbortAlarmSend)
	select {
	case err := <-next:
		if err == nil {
			t.Fatal("an alarm was delivered to an aborted AR")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the next alarm still waits after the abort")
	}
}
