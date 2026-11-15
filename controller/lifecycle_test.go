// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/device"
	"github.com/otfabric/go-profinet/internal/leakcheck"
	"github.com/otfabric/go-profinet/pnio"
)

// sockets is a ListenPacket that fails its failAt-th call (0: never) and
// knows which of the sockets it opened are closed.
type sockets struct {
	listen func(ctx context.Context, network, address string) (net.PacketConn, error)
	failAt int32
	calls  atomic.Int32
	mu     sync.Mutex
	open   map[*trackedConn]bool
}

type trackedConn struct {
	net.PacketConn
	s *sockets
}

func (c *trackedConn) Close() error {
	c.s.mu.Lock()
	delete(c.s.open, c)
	c.s.mu.Unlock()
	return c.PacketConn.Close()
}

func (s *sockets) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	if n := s.calls.Add(1); n == s.failAt {
		return nil, errors.New("no socket")
	}
	c, err := s.listen(ctx, network, address)
	if err != nil {
		return nil, err
	}
	tc := &trackedConn{PacketConn: c, s: s}
	s.mu.Lock()
	if s.open == nil {
		s.open = map[*trackedConn]bool{}
	}
	s.open[tc] = true
	s.mu.Unlock()
	return tc, nil
}

func (s *sockets) left() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.open)
}

// TestNewFailureReleases: a controller that cannot open one of its
// sockets fails and keeps nothing: the sockets it had opened are closed
// and no goroutine is left (contract row L14).
func TestNewFailureReleases(t *testing.T) {
	n := newNet(t)
	nd := n.node(ctrlIP)
	// How many sockets a controller opens.
	all := &sockets{listen: nd.stack.ListenPacket}
	c, err := New(Config{Mux: nd.mux, ListenPacket: all.ListenPacket})
	if err != nil {
		t.Fatal(err)
	}
	total := all.calls.Load()
	if err := c.Close(); err != nil || all.left() != 0 {
		t.Fatalf("Close: %v, %d sockets open", err, all.left())
	}
	if total < 2 {
		t.Fatalf("%d sockets opened", total)
	}
	for failAt := int32(1); failAt <= total; failAt++ {
		check := leakcheck.Check(t)
		s := &sockets{listen: nd.stack.ListenPacket, failAt: failAt}
		if c, err := New(Config{Mux: nd.mux, ListenPacket: s.ListenPacket}); err == nil {
			_ = c.Close()
			t.Fatalf("socket %d of %d failed and New succeeded", failAt, total)
		}
		if s.left() != 0 {
			t.Errorf("socket %d of %d failed: %d sockets left open", failAt, total, s.left())
		}
		check()
	}
	// The ports are free again: a controller starts.
	again, err := New(Config{Mux: nd.mux, ListenPacket: nd.stack.ListenPacket})
	if err != nil {
		t.Fatalf("New after the failures: %v", err)
	}
	_ = again.Close()
}

// TestCloseConcurrent: Close of a controller and of its device from
// several goroutines at once (contract row L2): all return, the device is
// stopped and nothing of either is left.
func TestCloseConcurrent(t *testing.T) {
	n := newNet(t)
	s := &sockets{}
	c, _ := n.controller(func(cfg *Config) {
		s.listen = cfg.ListenPacket
		cfg.ListenPacket = s.ListenPacket
	})
	n.device(devIP)
	cd, err := c.AddDevice(DeviceConfig{StationName: "dut", Model: testModel(), ReconnectMin: 50 * time.Millisecond, DataHoldFactor: 100})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, cd, StateRunning, 10*time.Second)
	leakcheck.Together(8, func(i int) {
		var err error
		if i%2 == 0 {
			err = cd.Close()
		} else {
			err = c.Close()
		}
		if err != nil {
			t.Error(err)
		}
	})
	// Every caller has returned, the one that did the work as well.
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if st, _ := cd.State(); st != StateStopped {
		t.Fatalf("device state %s after Close", st)
	}
	if _, ok := c.Device("dut"); ok {
		t.Fatal("the device is still the controller's")
	}
	if _, err := c.AddDevice(DeviceConfig{StationName: "late", Model: testModel()}); !errors.Is(err, profinet.ErrClosed) {
		t.Fatalf("AddDevice after Close: %v", err)
	}
	if s.left() != 0 {
		t.Fatalf("%d sockets open after Close", s.left())
	}
}

// TestCallbacksMayUseTheDevice: OnState and OnAlarm may call the methods
// of the Device and the Controller that do not wait (contract row L9).
func TestCallbacksMayUseTheDevice(t *testing.T) {
	n := newNet(t)
	c, _ := n.controller()
	dev, _ := n.device(devIP)
	var inState, inAlarm atomic.Int64
	use := func(count *atomic.Int64) {
		d, ok := c.Device("dut")
		if !ok {
			t.Error("the device is not the controller's inside a callback")
			return
		}
		st, _ := d.State()
		_ = d.Stats()
		_, running := d.AR()
		if running && st < StateParameterizing {
			t.Errorf("an AR in state %s", st)
		}
		_, _, _ = d.Input(1, 1)
		_ = d.SetOutput(2, 1, []byte{1, 2, 3, 4}, pnio.IOxSGood)
		_ = d.Name()
		count.Add(1)
	}
	cd, err := c.AddDevice(DeviceConfig{StationName: "dut", Model: testModel(), ReconnectMin: 50 * time.Millisecond, DataHoldFactor: 100,
		OnState: func(_, _ State, _ error) { use(&inState) },
		OnAlarm: func(AlarmEvent) { use(&inAlarm) },
	})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, cd, StateRunning, 10*time.Second)
	// Offline, Resolving, Connecting, Parameterizing, WaitApplicationReady, Running.
	if inState.Load() < 5 {
		t.Fatalf("OnState returned %d times before Running", inState.Load())
	}
	eventually(t, 5*time.Second, "device AR in Data", func() bool {
		ars := dev.ARs()
		return len(ars) == 1 && ars[0].State == device.ARStateData
	})
	if err := dev.SendAlarm(context.Background(), device.Alarm{Type: pnio.AlarmProcess, Slot: 3, Subslot: 1}); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "OnAlarm to return", func() bool { return inAlarm.Load() == 1 })
	if cd.Stats().Alarms != 1 {
		t.Fatalf("stats %+v", cd.Stats())
	}
}
