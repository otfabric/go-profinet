// SPDX-License-Identifier: MIT

package controller

import (
	"bytes"
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/alarm"
	"github.com/otfabric/go-profinet/pnio"
)

func handovers() int {
	buf := make([]byte, 64<<20)
	n := runtime.Stack(buf, true)
	return bytes.Count(buf[:n], []byte("controller.(*Device).takeReleased("))
}

// TestReleasedAlarmHandoverBounded: Released alarms each start an untracked
// takeReleased goroutine; they outlive the AR that received them.
func TestReleasedAlarmHandoverBounded(t *testing.T) {
	n := newNet(t)
	c, cn := n.controller()
	f := n.fake("fake")
	hang := make(chan struct{})
	defer close(hang)
	f.set(func(f *fakeDev) {
		f.control = func(cb pnio.ControlBlock) pnio.Status {
			if cb.Type == pnio.BlockPrmEndPlugReq {
				<-hang
			}
			return pnio.Status{}
		}
	})
	cd, err := c.AddDevice(DeviceConfig{StationName: "fake", Model: testModel(), ReconnectMin: time.Hour, ReductionRatio: 512, DataHoldFactor: 0x1E00, RTATimeoutFactor: 10})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, cd, StateWaitAppReady, 5*time.Second)
	req := <-f.requests
	cb := pnio.ControlBlock{Type: pnio.BlockAppReadyReq, ARUUID: req.AR.ARUUID, SessionKey: req.AR.SessionKey, Command: pnio.CmdApplicationReady}
	if st, _, err := f.appReady(c, 34964, cb); err != nil || !st.OK() {
		t.Fatalf("appready %v %v", st, err)
	}
	waitState(t, cd, StateRunning, 5*time.Second)
	ep, err := f.am.Open(alarm.EndpointConfig{LocalRef: 0x77, RemoteRef: req.AlarmCR.LocalAlarmReference, RemoteMAC: cn.mux.HardwareAddr(), Timeout: time.Second, Retries: 3})
	if err != nil {
		t.Fatal(err)
	}
	const N = 50
	for i := range N {
		note := pnio.AlarmNotification{Type: pnio.AlarmReleased, Slot: 1, Subslot: 1, ModuleIdentNumber: 0x10, SubmoduleIdentNumber: 0x11, Specifier: uint16(i) & pnio.AlarmSpecSeqMask}
		if err := ep.Send(context.Background(), note.Append(nil)); err != nil {
			t.Fatalf("alarm %d: %v", i, err)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if got := handovers(); got > 1 {
		t.Fatalf("%d handovers of one submodule at once", got)
	}
	// The device aborts the AR with an ERR PDU.
	_ = ep.SendErr(context.Background(), pnio.Status{Code: 0xCF, Decode: 0x81, Code1: 0xFD, Code2: 0x01})
	eventually(t, 3*time.Second, "AR to leave Running", func() bool { s, _ := cd.State(); return s != StateRunning })
	eventually(t, 2*time.Second, "handover to end with the AR", func() bool { return handovers() == 0 })
}
