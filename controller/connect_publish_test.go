// SPDX-License-Identifier: MIT

package controller

import (
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/rt"
)

// stallConnect makes the next accepted Connect of the controller's devices
// wait before its alarm endpoints and cyclic machinery start. reached is
// closed when a Connect waits there; release lets it go on (the end of the
// test does so as well).
func stallConnect(t *testing.T, c *Controller) (reached chan struct{}, release func()) {
	reached = make(chan struct{})
	gate := make(chan struct{})
	var once, open sync.Once
	c.afterConnect = func() {
		once.Do(func() {
			close(reached)
			<-gate
		})
	}
	release = func() { open.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return reached, release
}

func waitReached(t *testing.T, reached chan struct{}) {
	t.Helper()
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("no Connect was answered")
	}
}

// TestStatsWhileConnecting: an AR becomes the device's current one only
// when its provider, consumer and tasks exist. Until then Stats, AR and
// ARUUID report no AR; afterwards they report all of it.
func TestStatsWhileConnecting(t *testing.T) {
	n := newNet(t)
	c, _ := n.controller()
	n.device(devIP)
	reached, release := stallConnect(t, c)
	cd, err := c.AddDevice(DeviceConfig{StationName: "dut", Model: testModel(), ReconnectMin: 50 * time.Millisecond, ReconnectMax: 100 * time.Millisecond, DataHoldFactor: 100})
	if err != nil {
		t.Fatal(err)
	}
	waitReached(t, reached)
	// The Connect response is in; nothing of the AR runs yet.
	st := cd.Stats()
	if st.Connects != 1 || st.Provider != (rt.ProviderStats{}) || st.Consumer != (rt.ConsumerStats{}) || st.Cycle != (rt.TaskStats{}) {
		t.Errorf("Stats while the AR is set up: %+v, want one connect and no cyclic counters", st)
	}
	if info, ok := cd.AR(); ok {
		t.Errorf("AR while the AR is set up: %+v", info)
	}
	if id := cd.ARUUID(); !id.IsNil() {
		t.Errorf("ARUUID while the AR is set up: %s", id)
	}
	if id := cd.inputFrameID(); id != 0 {
		t.Errorf("input FrameID while the AR is set up: 0x%04X", id)
	}
	release()
	waitState(t, cd, StateRunning, 10*time.Second)
	cd.mu.Lock()
	a := cd.ar
	complete := a != nil && a.provider != nil && a.consumer != nil && a.provTask != nil && a.wdTask != nil && a.alarmHigh != nil && a.alarmLow != nil
	cd.mu.Unlock()
	if !complete {
		t.Fatalf("the current AR is incomplete: %+v", a)
	}
	if _, ok := cd.AR(); !ok || cd.ARUUID().IsNil() {
		t.Fatal("no AR after the Connect")
	}
	eventually(t, 10*time.Second, "cyclic counters", func() bool {
		st := cd.Stats()
		return st.Provider.Sent > 0 && st.Cycle.Cycles > 0
	})
}

// TestSetPrimaryWhileConnecting: a request made while the AR is set up is
// the one its provider sends.
func TestSetPrimaryWhileConnecting(t *testing.T) {
	n := newNet(t)
	c, _ := n.controller()
	n.fake("fake")
	reached, release := stallConnect(t, c)
	cd, err := c.AddDevice(DeviceConfig{StationName: "fake", Model: testModel(), ReconnectMin: time.Hour, ReductionRatio: 512, DataHoldFactor: 0x1E00,
		SystemRedundancy: &SRSetting{ARNumber: 1}})
	if err != nil {
		t.Fatal(err)
	}
	waitReached(t, reached)
	if err := cd.SetPrimary(true); err != nil {
		t.Fatal(err)
	}
	release()
	var a *arState
	eventually(t, 10*time.Second, "the AR", func() bool {
		cd.mu.Lock()
		defer cd.mu.Unlock()
		a = cd.ar
		return a != nil
	})
	if got, want := a.provider.DataStatus(), srDataStatus(true); got != want {
		t.Fatalf("DataStatus %s, want %s", got, want)
	}
}
