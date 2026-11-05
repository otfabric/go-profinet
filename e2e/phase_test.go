// SPDX-License-Identifier: MIT

package e2e

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/alarm"
	"github.com/otfabric/go-profinet/controller"
	"github.com/otfabric/go-profinet/device"
	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/pnio"
	"github.com/otfabric/go-profinet/vnet"
)

// frameClass names what an Ethernet frame on the virtual network carries:
// "rpc" (UDP, the DCE/RPC of the CM), "dcp", "rta-data", "rta-ack",
// "rta-nack", "rta-err", "rt" (cyclic data) or "other".
func frameClass(b []byte) string {
	f, err := ethernet.Decode(b)
	if err != nil {
		return "other"
	}
	switch f.EtherType {
	case 0x0800:
		// IPv4: protocol at offset 9; UDP only.
		if len(f.Payload) >= 20 && f.Payload[9] == 17 {
			return "rpc"
		}
		return "other"
	case 0x8892:
	default:
		return "other"
	}
	if len(f.Payload) < 2 {
		return "other"
	}
	id := binary.BigEndian.Uint16(f.Payload)
	switch {
	case id == pnio.FrameIDAlarmHigh || id == pnio.FrameIDAlarmLow:
		p, err := alarm.Decode(f.Payload)
		if err != nil {
			return "other"
		}
		switch p.Type {
		case alarm.PDUData:
			return "rta-data"
		case alarm.PDUAck:
			return "rta-ack"
		case alarm.PDUNack:
			return "rta-nack"
		case alarm.PDUErr:
			return "rta-err"
		}
		return "other"
	case id >= 0xFEFC && id <= 0xFEFF:
		return "dcp"
	default:
		return "rt"
	}
}

// only returns a Match function for the frames of the given classes.
func only(classes ...string) func([]byte) bool {
	return func(b []byte) bool {
		c := frameClass(b)
		for _, x := range classes {
			if c == x {
				return true
			}
		}
		return false
	}
}

// everyNth matches every nth frame of the given classes (the first, the
// n+1st, ...), deterministically whatever the goroutine schedule. With
// n = 2 a request and response exchange can lose every response, so
// startup tests use 3.
func everyNth(n int64, classes ...string) func([]byte) bool {
	var c atomic.Int64
	m := only(classes...)
	return func(b []byte) bool { return m(b) && c.Add(1)%n == 1 }
}

// TestChaosStartupFrames impairs only the frames of the AR's startup and
// its acyclic services (the RPC of Connect, Write, PrmEnd,
// ApplicationReady, Read) or only DCP: the AR must reach Running with
// the data flowing, records must read, and nothing may leak.
func TestChaosStartupFrames(t *testing.T) {
	cases := []struct {
		name   string
		faults vnet.Faults
	}{
		{"rpc duplicated and reordered", vnet.Faults{Duplicate: 0.5, Reorder: 0.5, ReorderHold: 3 * time.Millisecond, Match: only("rpc")}},
		{"rpc every third lost", vnet.Faults{Loss: 1, Match: everyNth(3, "rpc")}},
		{"rpc delayed with jitter", vnet.Faults{Delay: 5 * time.Millisecond, Jitter: 20 * time.Millisecond, Match: only("rpc")}},
		{"rpc corrupted", vnet.Faults{Corrupt: 0.2, Match: only("rpc")}},
		{"dcp duplicated and reordered", vnet.Faults{Duplicate: 1, Reorder: 0.5, ReorderHold: 3 * time.Millisecond, Match: only("dcp")}},
	}
	for _, c := range cases {
		for _, seed := range seeds(t) {
			t.Run(fmt.Sprintf("%s/seed=%d", c.name, seed), func(t *testing.T) {
				base := runtime.NumGoroutine()
				func() {
					e := newEnv(t, seed)
					dev, dn := e.device("dut", "192.168.0.10", testModel(), nil)
					ctrl, cn := e.controller("192.168.0.1")
					dn.ep.SetFaults(c.faults)
					cn.ep.SetFaults(c.faults)
					// The faults hit the startup, not the cyclic data: the hold
					// rides out runner stalls (stallHold).
					cd, err := ctrl.AddDevice(controller.DeviceConfig{StationName: "dut", Model: testModel(), DataHoldFactor: stallHold, WatchdogFactor: stallHold,
						RPCTimeout: 2 * time.Second, ReconnectMax: 200 * time.Millisecond})
					if err != nil {
						t.Fatal(err)
					}
					waitState(t, cd, controller.StateRunning, 60*time.Second)
					_ = dev.SetInput(1, 1, []byte{1, 2, 3, 4, 5, 6, 7, 8}, pnio.IOxSGood)
					eventually(t, 30*time.Second, "inputs", func() bool {
						in, _, _ := cd.Input(1, 1)
						return bytes.Equal(in, []byte{1, 2, 3, 4, 5, 6, 7, 8})
					})
					ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					defer cancel()
					for range 5 {
						if _, err := cd.ReadIM0(ctx); err != nil {
							t.Fatalf("read: %v", err)
						}
					}
					st, ct := dn.ep.Stats(), cn.ep.Stats()
					t.Logf("device %+v controller %+v; aborts %d", st, ct, cd.Stats().Aborts)
					if c.faults.Delay == 0 && st.Dropped+st.Duplicated+st.Reordered+st.Corrupted+ct.Dropped+ct.Duplicated+ct.Reordered+ct.Corrupted == 0 {
						t.Fatal("no faults injected")
					}
					_ = e.net.Close()
					_ = ctrl.Close()
					_ = dev.Close()
				}()
				checkLeaks(t, base)
			})
		}
	}
}

// TestChaosAlarmAckLoss loses every other RTA ACK and every other RTA
// DATA PDU in both directions (the alarms of the device and the
// AlarmAcks of the controller) while the device sends alarms. Every
// SendAlarm must succeed by retransmission, the controller must see each
// alarm exactly once, and the AR must survive.
func TestChaosAlarmAckLoss(t *testing.T) {
	for _, classes := range [][]string{{"rta-ack"}, {"rta-data"}, {"rta-ack", "rta-data"}} {
		t.Run(fmt.Sprint(classes), func(t *testing.T) {
			base := runtime.NumGoroutine()
			func() {
				e := newEnv(t, 7)
				var mu sync.Mutex
				seen := map[string]int{}
				dev, dn := e.device("dut", "192.168.0.10", testModel(), nil)
				ctrl, cn := e.controller("192.168.0.1")
				cd, err := ctrl.AddDevice(controller.DeviceConfig{StationName: "dut", Model: testModel(),
					DataHoldFactor: stallHold, WatchdogFactor: stallHold,
					RTATimeoutFactor: 1, RTARetries: 5,
					OnAlarm: func(a controller.AlarmEvent) {
						mu.Lock()
						seen[string(a.Notification.Data)]++
						mu.Unlock()
					}})
				if err != nil {
					t.Fatal(err)
				}
				waitState(t, cd, controller.StateRunning, 10*time.Second)
				eventually(t, 2*time.Second, "device AR in Data", func() bool {
					ars := dev.ARs()
					return len(ars) == 1 && ars[0].State == device.ARStateData
				})
				ar := cd.ARUUID()
				// One counter for both directions: every other frame of the
				// classes is lost, whoever sends it.
				faults := vnet.Faults{Loss: 1, Match: everyNth(2, classes...)}
				eps := []*vnet.Endpoint{dn.ep, cn.ep}
				for _, ep := range eps {
					ep.SetFaults(faults)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				const n = 6
				for i := range n {
					err := dev.SendAlarm(ctx, device.Alarm{High: i%2 == 0, Type: pnio.AlarmProcess, Slot: 3, Subslot: 1, HasItem: true, USI: 0x0042, Data: fmt.Appendf(nil, "a%d", i)})
					if err != nil {
						t.Fatalf("alarm %d: %v", i, err)
					}
				}
				eventually(t, 10*time.Second, "all alarms", func() bool {
					mu.Lock()
					defer mu.Unlock()
					return len(seen) == n
				})
				// Late retransmissions must not deliver an alarm twice.
				time.Sleep(300 * time.Millisecond)
				mu.Lock()
				for k, v := range seen {
					if v != 1 {
						t.Errorf("alarm %s delivered %d times", k, v)
					}
				}
				mu.Unlock()
				if st, _ := cd.State(); st != controller.StateRunning || cd.ARUUID() != ar {
					t.Fatalf("AR lost: %s", st)
				}
				var dropped uint64
				for _, ep := range eps {
					dropped += ep.Stats().Dropped
				}
				if dropped == 0 {
					t.Fatal("no frame dropped")
				}
				_ = e.net.Close()
				_ = ctrl.Close()
				_ = dev.Close()
			}()
			checkLeaks(t, base)
		})
	}
}

// TestCloseDuringPhase closes the controller's device, the whole
// controller or the IO device as soon as the AR enters each phase of its
// startup (RPC and DCP frames are delayed so that each phase lasts a
// while). Afterwards the IO device must hold no AR for long, a new
// controller must establish an AR again, and no goroutine may be left.
func TestCloseDuringPhase(t *testing.T) {
	phases := []controller.State{controller.StateResolving, controller.StateConnecting, controller.StateParameterizing, controller.StateWaitAppReady, controller.StateRunning}
	for _, closer := range []string{"device handle", "controller", "io device"} {
		for _, phase := range phases {
			t.Run(fmt.Sprintf("%s/%s", closer, phase), func(t *testing.T) {
				base := runtime.NumGoroutine()
				func() {
					e := newEnv(t, 11)
					dev, dn := e.device("dut", "192.168.0.10", testModel(), nil)
					ctrl, cn := e.controller("192.168.0.1")
					slow := vnet.Faults{Delay: 30 * time.Millisecond, Match: only("rpc", "dcp")}
					dn.ep.SetFaults(slow)
					cn.ep.SetFaults(slow)
					reached := make(chan struct{}, 1)
					cd, err := ctrl.AddDevice(controller.DeviceConfig{StationName: "dut", Model: testModel(), DataHoldFactor: stallHold, WatchdogFactor: stallHold,
						ActivityTimeout: time.Second, ReconnectMin: time.Hour,
						OnState: func(_, to controller.State, _ error) {
							if to == phase {
								select {
								case reached <- struct{}{}:
								default:
								}
							}
						}})
					if err != nil {
						t.Fatal(err)
					}
					select {
					case <-reached:
					case <-time.After(20 * time.Second):
						st, last := cd.State()
						t.Fatalf("never reached %s: in %s (%v)", phase, st, last)
					}
					done := make(chan error, 1)
					go func() {
						switch closer {
						case "device handle":
							done <- cd.Close()
						case "controller":
							done <- ctrl.Close()
						default:
							done <- dev.Close()
						}
					}()
					select {
					case err := <-done:
						if err != nil {
							t.Fatalf("close: %v", err)
						}
					case <-time.After(10 * time.Second):
						t.Fatalf("Close in %s hangs", phase)
					}
					dn.ep.SetFaults(vnet.Faults{})
					cn.ep.SetFaults(vnet.Faults{})
					if closer == "io device" {
						// The controller's device notices the loss (or never
						// had an AR) and closes cleanly.
						if err := cd.Close(); err != nil {
							t.Fatal(err)
						}
						_ = ctrl.Close()
						_ = e.net.Close()
						return
					}
					// The IO device drops the AR it may hold: by the release,
					// a failed ApplicationReady or the CM activity timeout.
					eventually(t, 15*time.Second, "IO device without AR", func() bool { return len(dev.ARs()) == 0 })
					// A new controller establishes an AR.
					ctrl2, _ := e.controller("192.168.0.2")
					cd2, err := ctrl2.AddDevice(controller.DeviceConfig{StationName: "dut", Model: testModel(), DataHoldFactor: stallHold, WatchdogFactor: stallHold})
					if err != nil {
						t.Fatal(err)
					}
					waitState(t, cd2, controller.StateRunning, 20*time.Second)
					_ = ctrl2.Close()
					_ = ctrl.Close()
					_ = dev.Close()
					_ = e.net.Close()
				}()
				checkLeaks(t, base)
			})
		}
	}
}
