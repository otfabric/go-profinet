// SPDX-License-Identifier: MIT

package device

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/alarm"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/mrp"
	"github.com/otfabric/go-profinet/pnio"
)

// An AlarmAck for no outstanding alarm (late, after the AR's alarm ended,
// or with a sequence number never sent) reaches the handler and changes
// nothing else.
func TestAlarmAckForNoAlarm(t *testing.T) {
	n := newNet(t)
	h := newRecHandler()
	d, _ := n.device(h)
	a := &ar{d: d, ackWait: map[ackKey]chan pnio.AlarmAck{}}
	ch := make(chan pnio.AlarmAck, 1)
	a.ackWait[ackKey{seq: 5}] = ch
	d.alarmData(a, (&pnio.AlarmAck{Type: pnio.AlarmProcess, Specifier: 6}).Append(nil))
	select {
	case got := <-ch:
		t.Fatalf("an AlarmAck for 6 ended alarm 5: %+v", got)
	default:
	}
	if len(a.ackWait) != 1 {
		t.Fatalf("waiting %v", a.ackWait)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.acks) != 1 {
		t.Fatalf("acks %+v", h.acks)
	}
}

// The AR ends while the caller of a transmitted alarm waits for its
// AlarmAck: the caller gets ErrAborted.
func TestAlarmAbortedAwaitingAck(t *testing.T) {
	n := newNet(t)
	d, _ := n.device(nil)
	p := n.peer(d, "192.168.0.2", true)
	req := p.ioCAR()
	resp := p.establish(req)
	got := make(chan struct{}, 4)
	p.openAlarms(req, resp, func(bool, pnio.AlarmNotification, *alarm.Endpoint) { got <- struct{}{} })
	errc := make(chan error, 1)
	go func() { errc <- d.SendAlarm(context.Background(), Alarm{Type: pnio.AlarmProcess, Slot: 1, Subslot: 1}) }()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("no alarm")
	}
	if err := p.release(req); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if !errors.Is(err, profinet.ErrAborted) {
			t.Fatalf("SendAlarm: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SendAlarm still waits after the release")
	}
}

// A port evaluation that cannot take the diagnosis lock before its
// context ends records nothing.
func TestSetPortMismatchLockFails(t *testing.T) {
	n := newNet(t)
	d, _ := n.device(nil)
	d.diagSem <- struct{}{}
	defer func() { <-d.diagSem }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.mu.Lock()
	gen := d.pd.gen
	d.mu.Unlock()
	recorded, err := d.setPortMismatch(ctx, model.SubslotPort1, gen, []uint16{extPeerPortMismatch})
	if recorded || !errors.Is(err, context.Canceled) {
		t.Fatalf("setPortMismatch: %v %v", recorded, err)
	}
	if got := mismatch(d); len(got) != 0 {
		t.Fatalf("mismatch %#x", got)
	}
}

// A written configuration whose entity mrp.New would refuse is refused
// with the running entity untouched: here the device's profile, which a
// write with no timers keeps, is one mrp.New refuses.
func TestMRPWriteRefusedBeforeStop(t *testing.T) {
	d, n := mrpRingDeviceOn(t, &MRPConfig{Role: mrp.RoleClient, RingPorts: [2]int{1, 2}}, 2)
	eventually(t, 5*time.Second, "client", func() bool { _, ok := d.MRPStatus(); return ok })
	x := newPDevPeer(t, n, d)
	d.mu.Lock()
	ent := d.mrpSt.ent
	bad := d.mrpSt.cfg.Profile
	bad.TestInterval = -time.Millisecond
	d.mrpSt.cfg.Profile = bad
	dom := d.mrpSt.cfg.Domain
	d.mu.Unlock()
	a := pnio.PDInterfaceMrpDataAdjust{Domain: dom, Role: pnio.MrpRoleManager, DomainName: "ring-x"}
	wantStatus(t, x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust, a.Append(nil)), pnio.RWInvalidParameter, 0)
	d.mu.Lock()
	ent2, role := d.mrpSt.ent, d.mrpSt.cfg.Role
	d.mu.Unlock()
	if ent2 != ent || role != mrp.RoleClient {
		t.Fatalf("the refused write replaced the entity (%v) or the role (%v)", ent2 != ent, role)
	}
}
