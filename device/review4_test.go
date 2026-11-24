// SPDX-License-Identifier: MIT

package device

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/mrp"
	"github.com/otfabric/go-profinet/pnio"
)

func arOf(d *Device) *ar {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, a := range d.ars {
		return a
	}
	return nil
}

// TestOwedWhileNotInData: alarms owed to an AR that leaves the Data state
// for a while (a PrmBegin) stay owed: the repeated RemoveDiagnosis sends
// them once the AR is back. Before, they were dropped and the repeat
// returned ErrNotFound with the alarms never delivered.
func TestOwedWhileNotInData(t *testing.T) {
	refused := pnio.Status{Code: 0xDF, Decode: 0x80, Code1: 0x22}
	d, got, _ := policyPeer(t, func(n int, _ pnio.AlarmNotification) (pnio.Status, time.Duration) {
		if n == 1 {
			return refused, 0
		}
		return pnio.Status{}, 0
	})
	ctx := context.Background()
	fault := pnio.DiagnosisItem{ChannelNumber: 1, ChannelProperties: pnio.ChanDirInput, ErrorType: pnio.ChannelErrLineBreak}
	var se *pnio.StatusError
	if err := d.AddDiagnosis(ctx, 1, 1, fault); !errors.As(err, &se) {
		t.Fatalf("AddDiagnosis: %v, want the refusal", err)
	}
	nextArrival(t, got)
	a := arOf(d)
	setState := func(s ARState) {
		a.mu.Lock()
		a.state = s
		a.mu.Unlock()
	}
	setState(ARStateParameterizing)
	if err := d.RemoveDiagnosis(ctx, 1, 1, fault.ChannelNumber, fault.ErrorType); !errors.Is(err, profinet.ErrInvalidState) {
		t.Fatalf("RemoveDiagnosis out of Data: %v, want ErrInvalidState", err)
	}
	setState(ARStateData)
	if err := d.RemoveDiagnosis(ctx, 1, 1, fault.ChannelNumber, fault.ErrorType); err != nil {
		t.Fatalf("repeated RemoveDiagnosis: %v, want the owed alarms sent", err)
	}
	if _, it := nextArrival(t, got); it.ChannelNumber != 1 {
		t.Fatalf("first owed alarm %+v", it)
	}
	if a2, _ := nextArrival(t, got); a2.an.Type != pnio.AlarmDiagnosisDisappears {
		t.Fatalf("second owed alarm %v, want the disappearance", a2.an.Type)
	}
}

// TestOutstandingDiagnosisNotOwed: a diagnosis alarm the controller
// received, whose AlarmAck the caller did not wait for, is not owed: the
// next operation does not send it again. Before, it was owed and the
// controller received it twice.
func TestOutstandingDiagnosisNotOwed(t *testing.T) {
	d, got, acked := policyPeer(t, func(n int, _ pnio.AlarmNotification) (pnio.Status, time.Duration) {
		if n == 1 {
			return pnio.Status{}, 300 * time.Millisecond
		}
		return pnio.Status{}, 0
	})
	one := pnio.DiagnosisItem{ChannelNumber: 1, ChannelProperties: pnio.ChanDirInput, ErrorType: pnio.ChannelErrLineBreak}
	two := pnio.DiagnosisItem{ChannelNumber: 2, ChannelProperties: pnio.ChanDirInput, ErrorType: pnio.ChannelErrLineBreak}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := d.AddDiagnosis(ctx, 1, 1, one); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AddDiagnosis: %v", err)
	}
	nextArrival(t, got)
	d.mu.Lock()
	_, owed := d.diagOwed[key{1, 1}]
	d.mu.Unlock()
	if owed {
		t.Fatal("the delivered alarm is owed")
	}
	if err := d.AddDiagnosis(context.Background(), 1, 1, two); err != nil {
		t.Fatal(err)
	}
	if acked(1).IsZero() {
		t.Fatal("the next alarm went before the outstanding one's AlarmAck")
	}
	if _, it := nextArrival(t, got); it.ChannelNumber != 2 {
		t.Fatalf("next alarm for channel %d, want 2 (channel 1 sent twice)", it.ChannelNumber)
	}
}

// TestMRPPortAdjustDuringAdjustRollback: a PDPortMrpDataAdjust written
// by another AR while a PDInterfaceMrpDataAdjust to a new domain fails
// and is rolled back is checked against the restored domain. Before, it
// was checked against the refused one, accepted, and left the port with
// a domain the interface does not have.
func TestMRPPortAdjustDuringAdjustRollback(t *testing.T) {
	dom, other := [16]byte{0xD0, 8}, [16]byte{0xD0, 9}
	d, n := mrpRingDevice(t, &MRPConfig{Role: mrp.RoleClient, Domain: dom, DomainName: "ring-p"})
	x := newPDevPeer(t, n, d)
	p2 := n.peer(d, "192.168.0.5", true)
	sar2 := p2.ioSAR()
	if _, err := p2.connect(sar2); err != nil {
		t.Fatal(err)
	}
	x2 := &pdevPeer{t: t, p: p2, ar: sar2.AR.ARUUID}
	port := model.SubslotPort1 + 1
	inEntity := make(chan struct{})
	portDone := make(chan error, 1)
	var calls atomic.Int32
	d.mrpNew = func(c mrp.Config) (*mrp.Entity, error) {
		if calls.Add(1) == 1 {
			close(inEntity)
			select {
			case err := <-portDone:
				portDone <- err
			case <-time.After(300 * time.Millisecond):
			}
			return nil, profinet.ErrInvalidConfig
		}
		return mrp.New(c)
	}
	a := pnio.PDInterfaceMrpDataAdjust{Domain: other, Role: pnio.MrpRoleClient, DomainName: "ring-p"}
	adjDone := make(chan error, 1)
	go func() { adjDone <- x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust, a.Append(nil)) }()
	<-inEntity
	pa := pnio.PDPortMrpData{Domain: other}
	go func() { portDone <- x2.write(port, pnio.IndexPDPortMrpDataAdjust, pa.Append(nil)) }()
	wantStatus(t, <-adjDone, pnio.RWInvalidParameter, 0)
	wantStatus(t, <-portDone, pnio.RWInvalidParameter, 0)
	d.mu.Lock()
	stored := d.mrpSt.portAdj[port]
	d.mu.Unlock()
	if stored != nil {
		t.Fatalf("port adjustment %+v stored for the refused domain", stored)
	}
}
