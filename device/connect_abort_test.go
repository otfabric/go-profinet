// SPDX-License-Identifier: MIT

package device

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/alarm"
	"github.com/otfabric/go-profinet/pnio"
)

// hookOnce makes the first Connect of d run fn at stage; the AR it was
// called with is returned through the channel.
func hookOnce(d *Device, stage connectStage, fn func(a *ar)) <-chan *ar {
	got := make(chan *ar, 1)
	var once sync.Once
	d.connectHook = func(a *ar, s connectStage) {
		if s != stage {
			return
		}
		once.Do(func() {
			fn(a)
			got <- a
		})
	}
	return got
}

// wantNothingLeft checks that the AR of a refused Connect holds nothing
// of the device and that the device still takes its full number of ARs.
func wantNothingLeft(t *testing.T, n *tnet, d *Device, a *ar) {
	t.Helper()
	if s := a.currentState(); s != ARStateClosed {
		t.Errorf("the AR of the refused Connect is %s", s)
	}
	d.mu.Lock()
	var states []string
	for _, x := range d.ars {
		states = append(states, x.currentState().String())
	}
	nARs, frameIDs, reserved, connecting, replacing := len(d.ars), len(d.frameIDs), d.reserved, len(d.connecting), len(d.replacing)
	d.mu.Unlock()
	if nARs != 0 || frameIDs != 0 || reserved != 0 || connecting != 0 || replacing != 0 {
		t.Errorf("after the refused Connect: %d ARs registered %v, %d FrameIDs, %d reservations, %d connecting, %d replacing; want none",
			nARs, states, frameIDs, reserved, connecting, replacing)
	}
	d.imgMu.RLock()
	owned, claims := 0, len(d.claimers)
	for _, o := range d.owner {
		if o == a {
			owned++
		}
	}
	d.imgMu.RUnlock()
	if owned != 0 || claims != 0 {
		t.Errorf("after the refused Connect: the AR owns %d submodules, %d claimers", owned, claims)
	}
	if a.typ != pnio.ARTypeIOSAR {
		// Its endpoints are closed: nothing is sent on them.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		for _, ep := range []*alarm.Endpoint{a.alarmHigh, a.alarmLow} {
			if err := ep.Send(ctx, []byte{0}); err == nil {
				t.Error("an alarm endpoint of the refused Connect is open")
			}
		}
	}
	// Every AR the model allows can still be established.
	for i := range d.model.Capabilities.MaxARs {
		p := n.peer(d, fmt.Sprintf("192.168.0.%d", 100+i), true)
		p.establish(p.ioCAR())
	}
	if got := len(d.ARs()); got != d.model.Capabilities.MaxARs {
		t.Errorf("%d ARs established afterwards, want %d", got, d.model.Capabilities.MaxARs)
	}
}

// TestAbortDuringConnect: an abort that reaches an AR while its Connect
// runs ends the AR with the Connect: the Connect is refused and the AR
// keeps no entry, FrameID, endpoint or submodule.
func TestAbortDuringConnect(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stage connectStage
		iosar bool
	}{
		{"while the AR is built", connectBuilt, false},
		{"before the AR is entered", connectAbortable, false},
		{"device access, while the AR is built", connectBuilt, true},
		{"device access, before the AR is entered", connectAbortable, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newNet(t)
			h := newRecHandler()
			d, _ := n.device(h)
			p := n.peer(d, "192.168.0.2", true)
			got := hookOnce(d, tc.stage, func(a *ar) {
				d.abort(a, pnio.AbortAlarmInd, false)
				// A second one changes nothing.
				d.abort(a, pnio.AbortCMITimeout, false)
			})
			req := p.ioCAR()
			if tc.iosar {
				req = p.ioSAR()
			}
			resp, err := p.connect(req)
			a := <-got
			if err == nil {
				t.Errorf("the Connect of an aborted AR was accepted: %+v", resp.AR)
			} else {
				wantStatus(t, err, pnio.Code1CMRPC, pnio.CMRPCStateConflict)
			}
			a.mu.Lock()
			reason, building, early := a.reason, a.building, a.early
			a.mu.Unlock()
			if reason != pnio.AbortAlarmInd || building || early != nil {
				t.Errorf("reason %s, building %v, kept abort %v; want the first abort carried out", reason, building, early)
			}
			if r, ok := h.reason(req.AR.ARUUID); ok && r != pnio.AbortAlarmInd {
				t.Errorf("reported reason %s", r)
			}
			wantNothingLeft(t, n, d, a)
		})
	}
}

// TestPeerAbortDuringConnect: the same through the alarm endpoint: the
// controller's ERR PDU for the alarm reference of the AR arrives while
// the Connect builds the AR.
func TestPeerAbortDuringConnect(t *testing.T) {
	n := newNet(t)
	d, dn := n.device(newRecHandler())
	p := n.peer(d, "192.168.0.2", true)
	req := p.ioCAR()
	got := hookOnce(d, connectBuilt, func(a *ar) {
		ep, err := p.alarms.Open(alarm.EndpointConfig{
			LocalRef: req.AlarmCR.LocalAlarmReference, RemoteRef: a.alarmRef, RemoteMAC: dn.mux.HardwareAddr(),
			Timeout: 200 * time.Millisecond, Retries: 3,
		})
		if err != nil {
			t.Error(err)
			return
		}
		defer ep.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := ep.SendErr(ctx, pnio.AbortStatus(pnio.AbortUser)); err != nil {
			t.Error(err)
			return
		}
		eventually(t, 5*time.Second, "the abort to reach the AR", func() bool {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.early != nil
		})
	})
	if resp, err := p.connect(req); err == nil {
		t.Errorf("the Connect of an aborted AR was accepted: %+v", resp.AR)
	} else {
		wantStatus(t, err, pnio.Code1CMRPC, pnio.CMRPCStateConflict)
	}
	wantNothingLeft(t, n, d, <-got)
}
