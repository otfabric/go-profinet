// SPDX-License-Identifier: MIT

package mrp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/l2"
	"github.com/otfabric/go-profinet/vnet"
)

// recSwitch is a Switch that records what the entity asks of it.
type recSwitch struct {
	mu    sync.Mutex
	calls []string
	// onFlush, when set, runs in Flush.
	onFlush atomic.Pointer[func()]
}

func (s *recSwitch) SetBlocked(port int, blocked bool) {
	s.mu.Lock()
	s.calls = append(s.calls, fmt.Sprintf("port %d blocked %v", port, blocked))
	s.mu.Unlock()
}

func (s *recSwitch) Flush() {
	s.mu.Lock()
	s.calls = append(s.calls, "flush")
	s.mu.Unlock()
	if f := s.onFlush.Load(); f != nil {
		(*f)()
	}
}

func (s *recSwitch) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// rig is one entity whose two ring ports are each cabled to an endpoint
// of the test: the test reads there what the entity sends and puts
// there what the entity is to receive.
type rig struct {
	t     *testing.T
	ent   *Entity
	sw    *recSwitch
	ports [2]*vnet.Endpoint
	wire  [2]*vnet.Endpoint
	mac   ethernet.MAC
	seq   uint16
	// changes counts OnChange; onChange, when set, runs in it.
	changes  atomic.Int64
	onChange atomic.Pointer[func()]
}

// quiet is a profile whose timers do not run out within a test: what
// happens, happens because the test made it.
var quiet = Profile{Name: "quiet", TopChangeInterval: time.Millisecond, TopChangeRepeat: 1,
	TestInterval: time.Hour, TestMax: 3, LinkInterval: 30 * time.Second, LinkRepeat: 4}

func newRig(t *testing.T, role Role, profile Profile, up [2]bool) *rig {
	t.Helper()
	r := &rig{t: t, sw: &recSwitch{}, mac: ethernet.MAC{0x02, 0x20, 0, 0, 0, 0x10}}
	var muxes [2]*l2.Mux
	for i := range muxes {
		n := vnet.New(uint64(40 + i))
		t.Cleanup(func() { _ = n.Close() })
		var err error
		if r.ports[i], err = n.NewEndpoint(vnet.EndpointConfig{MAC: ethernet.MAC{0x02, 0x20, 0, 0, 0, byte(i + 1)}, Promiscuous: true}); err != nil {
			t.Fatal(err)
		}
		if r.wire[i], err = n.NewEndpoint(vnet.EndpointConfig{MAC: ethernet.MAC{0x02, 0x20, 0, 0, 1, byte(i + 1)}, Promiscuous: true}); err != nil {
			t.Fatal(err)
		}
		r.ports[i].SetLinkUp(up[i])
		muxes[i] = l2.NewMux(r.ports[i])
		m := muxes[i]
		t.Cleanup(func() { _ = m.Close() })
	}
	ent, err := New(Config{Role: role, Ports: muxes, Index: [2]int{0, 1}, Switch: r.sw, MAC: r.mac, Profile: profile, OnChange: func() {
		r.changes.Add(1)
		if f := r.onChange.Load(); f != nil {
			(*f)()
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	r.ent = ent
	t.Cleanup(func() { _ = ent.Close() })
	return r
}

// put makes the entity receive f on ring port i.
func (r *rig) put(i int, dst ethernet.MAC, f Frame) {
	r.t.Helper()
	r.seq++
	f.Common = &Common{SequenceID: r.seq, Domain: DefaultDomain}
	h := ethernet.Frame{Dst: dst, Src: r.wire[i].HardwareAddr(), EtherType: EtherType}
	b, err := f.AppendTo(h.AppendHeader(nil))
	if err != nil {
		r.t.Fatal(err)
	}
	if err := r.wire[i].WriteFrame(context.Background(), ethernet.Pad(b, 0)); err != nil {
		r.t.Fatal(err)
	}
}

// nextTest returns the next MRP_Test the entity sends on ring port i.
func (r *rig) nextTest(i int) Frame {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	buf := make([]byte, 1600)
	for {
		n, err := r.wire[i].ReadFrame(ctx, buf)
		if err != nil {
			r.t.Fatalf("no MRP_Test on ring port %d: %v", i+1, err)
		}
		ef, err := ethernet.Decode(buf[:n])
		if err != nil || ef.EtherType != EtherType {
			continue
		}
		if f, err := Decode(ef.Payload); err == nil && f.Test != nil {
			return f
		}
	}
}

func (r *rig) wait(what string, cond func(Status) bool) {
	r.t.Helper()
	eventually(r.t, 10*time.Second, what, func() bool { return cond(r.ent.Status()) })
}

// TestLinkUpBurstCountsNoInterval: a manager counts a missed test per
// test interval. The rounds it sends at once for the MRP_LinkUp frames of
// clients do not count: any number of them between two intervals leaves
// a closed ring closed.
func TestLinkUpBurstCountsNoInterval(t *testing.T) {
	r := newRig(t, RoleManager, quiet, [2]bool{true, true})
	// The first round: its test comes around, the ring is closed.
	test := r.nextTest(0)
	r.put(1, MACTest, Frame{Test: test.Test})
	r.wait("the ring to close", func(s Status) bool { return !s.RingOpen && s.State == StateChkRC && s.Blocked[1] })
	closed := r.ent.Status()
	calls := len(r.sw.all())

	client := ethernet.MAC{0x02, 0x20, 0, 0, 2, 1}
	for burst := 1; burst <= 2*(quiet.TestMax+1); burst++ {
		before := r.ent.Status().TestsSent
		r.put(0, MACControl, Frame{Link: &LinkChange{Up: true, SA: client, PortRole: PortPrimary, Interval: 20, Blocked: 1}})
		// The round the frame asked for went out; its tests are not
		// brought around.
		r.wait("the test round of a LinkUp", func(s Status) bool { return s.TestsSent >= before+2 })
		s := r.ent.Status()
		if s.RingOpen || s.Transitions != closed.Transitions || !s.Blocked[1] || s.State != StateChkRC {
			t.Fatalf("after %d MRP_LinkUp frames within one test interval: ring open %v, %d transitions, secondary port blocked %v, state %s; want the closed ring unchanged",
				burst, s.RingOpen, s.Transitions, s.Blocked[1], s.State)
		}
	}
	if got := r.sw.all(); len(got) != calls {
		t.Fatalf("the switch was told %v during the bursts", got[calls:])
	}
	if s := r.ent.Status(); s.LinkChanges != uint64(2*(quiet.TestMax+1)) {
		t.Fatalf("%d link changes counted", s.LinkChanges)
	}
}

// TestMissedIntervalsOpenTheRing: the intervals themselves still count:
// more than TestMax of them without a test coming around open the ring.
func TestMissedIntervalsOpenTheRing(t *testing.T) {
	p := quiet
	p.TestInterval = 10 * time.Millisecond
	r := newRig(t, RoleManager, p, [2]bool{true, true})
	test := r.nextTest(0)
	r.put(1, MACTest, Frame{Test: test.Test})
	r.wait("the ring to close", func(s Status) bool { return !s.RingOpen })
	r.wait("the ring to open", func(s Status) bool { return s.RingOpen && !s.Blocked[1] && s.Transitions == 1 })
}

// pt returns a client whose second ring port came up and waits, blocked,
// for the manager (state PT): its link timer is armed.
func pt(t *testing.T) *rig {
	t.Helper()
	r := newRig(t, RoleClient, quiet, [2]bool{true, false})
	r.wait("the first port", func(s Status) bool { return s.State == StateDEIdle })
	r.ports[1].SetLinkUp(true)
	r.wait("the second port", func(s Status) bool { return s.State == StatePT && s.Blocked[1] })
	return r
}

// TestTimersAfterClose: a timer that is due when Close has returned finds
// the entity closed: no port state, no flush, no OnChange (contract row
// L16: no work starts after Close).
func TestTimersAfterClose(t *testing.T) {
	r := pt(t)
	r.ent.flushAfter(time.Hour)
	if err := r.ent.Close(); err != nil {
		t.Fatal(err)
	}
	calls, changes := len(r.sw.all()), r.changes.Load()
	// What the link timer and the flush timer run.
	r.ent.unblockAfterWait(1)
	r.ent.flushPending()
	if got := r.sw.all(); len(got) != calls || r.changes.Load() != changes {
		t.Fatalf("after Close returned: the switch was told %v, OnChange ran %d times", got[calls:], r.changes.Load()-changes)
	}
	if s := r.ent.Status(); s.State != StatePT || !s.Blocked[1] {
		t.Fatalf("state %s, secondary port blocked %v: the ring ports keep their last state", s.State, s.Blocked[1])
	}
	// Close again, and concurrently (rows L1 and L2).
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := r.ent.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}

// TestCloseWaitsForTimerCallback: a timer callback that began before
// Close has ended when Close returns. The test runs in a synctest bubble:
// "Close has not returned" is read when every goroutine is blocked, not
// after a wait.
func TestCloseWaitsForTimerCallback(t *testing.T) {
	for name, fire := range map[string]func(r *rig){
		"link timer":  func(r *rig) { r.ent.unblockAfterWait(1) },
		"flush timer": func(r *rig) { r.ent.flushPending() },
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := pt(t)
				// The entity has reported the link changes and waits for
				// its next tick: the callback held below is the timer's.
				synctest.Wait()
				entered, gate := make(chan struct{}), make(chan struct{})
				// The callback is let go when the test ends, however it
				// ends.
				release := sync.OnceFunc(func() { close(gate) })
				defer release()
				var once sync.Once
				held := func() {
					once.Do(func() {
						close(entered)
						<-gate
					})
				}
				if name == "flush timer" {
					// The flush timer ends in the switch, not in OnChange.
					r.sw.onFlush.Store(&held)
				} else {
					r.onChange.Store(&held)
				}
				go fire(r)
				<-entered
				closed := make(chan error, 1)
				go func() { closed <- r.ent.Close() }()
				synctest.Wait()
				select {
				case err := <-closed:
					t.Fatalf("Close returned (%v) while a timer callback was running", err)
				default:
				}
				release()
				synctest.Wait()
				select {
				case err := <-closed:
					if err != nil {
						t.Fatal(err)
					}
				default:
					t.Fatal("Close did not return after the timer callback ended")
				}
			})
		})
	}
}

// TestManagerSwapsPorts: a manager whose primary port is down makes the
// other one the primary while its receivers start. Run with the race
// detector, it shows that the receivers, the link poll and Close read the
// swapped fields under the lock.
func TestManagerSwapsPorts(t *testing.T) {
	for range 20 {
		r := newRig(t, RoleManager, quiet, [2]bool{false, true})
		r.wait("the swap", func(s Status) bool { return s.State == StatePrmUp })
		r.ent.mu.Lock()
		idx, sub0 := r.ent.cfg.Index, r.ent.subs[0]
		r.ent.mu.Unlock()
		if idx != [2]int{1, 0} {
			t.Fatalf("port numbers %v after the swap", idx)
		}
		// A frame on the port that is now the primary is handled as one
		// of ring port 1: a client's LinkUp asks for a test round on it.
		before := r.ent.Status().TestsSent
		r.put(1, MACControl, Frame{Link: &LinkChange{Up: true, SA: ethernet.MAC{2, 0x20, 0, 0, 2, 1}, PortRole: PortPrimary, Interval: 20, Blocked: 1}})
		r.wait("a test round", func(s Status) bool { return s.TestsSent > before && s.LinkChanges == 1 })
		if err := r.ent.Close(); err != nil {
			t.Fatal(err)
		}
		// Both subscriptions are closed, whichever index they have now.
		for _, s := range []*l2.Subscription{sub0, r.ent.subs[1]} {
			select {
			case <-s.Done():
			default:
				t.Fatal("a subscription is open after Close")
			}
		}
	}
}

// TestTestIntervalValidated: a test interval below zero is refused; zero
// selects the default profile.
func TestTestIntervalValidated(t *testing.T) {
	n := vnet.New(1)
	defer func() { _ = n.Close() }()
	ep, _ := n.NewEndpoint(vnet.EndpointConfig{MAC: ethernet.MAC{0x02, 0, 0, 0, 0, 1}})
	m := l2.NewMux(ep)
	defer func() { _ = m.Close() }()
	for _, tc := range []struct {
		interval time.Duration
		ok       bool
	}{
		{-time.Hour, false},
		{-1, false},
		{0, true},
		{1, true},
		{time.Millisecond, true},
	} {
		p := quiet
		p.TestInterval = tc.interval
		e, err := New(Config{Role: RoleManager, Ports: [2]*l2.Mux{m, m}, Index: [2]int{0, 1}, Switch: &recSwitch{}, MAC: ethernet.MAC{2, 0, 0, 0, 0, 3}, Profile: p})
		if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, profinet.ErrInvalidConfig)) {
			t.Errorf("TestInterval %v: err %v, want accepted %v", tc.interval, err, tc.ok)
		}
		if e != nil {
			if tc.interval == 0 && e.cfg.Profile != Profile200 {
				t.Errorf("TestInterval 0: profile %+v, want the default", e.cfg.Profile)
			}
			_ = e.Close()
		}
	}
}

// TestOnChangeMayReadStatus: OnChange runs without the entity's lock and
// may call Status (contract row L9).
func TestOnChangeMayReadStatus(t *testing.T) {
	r := newRig(t, RoleClient, quiet, [2]bool{true, false})
	var seen atomic.Pointer[Status]
	read := func() {
		s := r.ent.Status()
		seen.Store(&s)
	}
	r.wait("the first port", func(s Status) bool { return s.State == StateDEIdle })
	r.onChange.Store(&read)
	r.ports[1].SetLinkUp(true)
	eventually(t, 10*time.Second, "OnChange to see the second port", func() bool {
		s := seen.Load()
		return s != nil && s.State == StatePT && s.Blocked[1]
	})
}

// TestAdditionalTest: a client's MRP_LinkChange makes the manager test
// at once and again after MRP_TSTshortT, then at MRP_TSTdefaultT
// (IEC 62439-2:2016, Table 45: LinkChangeInd with ADD_TEST false sets it
// and calls TestRingReq(MRP_TSTshortT); the next TestTimer expiry clears
// it and calls TestRingReq(MRP_TSTdefaultT)). A second LinkChange while
// the additional test runs asks for none. Before, the manager waited for
// the next default interval.
func TestAdditionalTest(t *testing.T) {
	p := quiet
	p.TestInterval = 600 * time.Millisecond
	p.TestMax = 1000
	p.TestShortInterval = 80 * time.Millisecond
	r := newRig(t, RoleManager, p, [2]bool{true, true})
	r.nextTest(0) // the first round
	time.Sleep(100 * time.Millisecond)
	client := ethernet.MAC{0x02, 0x20, 0, 0, 2, 1}
	down := Frame{Link: &LinkChange{SA: client, PortRole: PortPrimary, Interval: 20, Blocked: 1}}
	sent := time.Now()
	r.put(0, MACControl, down)
	r.nextTest(0)
	t1 := time.Now()
	// While ADD_TEST is set, a LinkChange asks for no further test.
	r.put(0, MACControl, down)
	r.nextTest(0)
	t2 := time.Now()
	r.nextTest(0)
	t3 := time.Now()
	if d := t1.Sub(sent); d > 50*time.Millisecond {
		t.Errorf("first test %v after the LinkChange, want at once", d)
	}
	if d := t2.Sub(t1); d < 50*time.Millisecond || d > 300*time.Millisecond {
		t.Errorf("second test %v after the first, want MRP_TSTshortT (%v)", d, p.TestShortInterval)
	}
	if d := t3.Sub(t2); d < 450*time.Millisecond {
		t.Errorf("third test %v after the second, want MRP_TSTdefaultT (%v)", d, p.TestInterval)
	}
	// The next LinkChange, after the timer expired, starts another one.
	sent = time.Now()
	r.put(0, MACControl, down)
	r.nextTest(0)
	if d := time.Since(sent); d > 50*time.Millisecond {
		t.Errorf("test %v after a later LinkChange, want at once", d)
	}
	if s := r.ent.Status(); s.LinkChanges != 3 {
		t.Fatalf("%d link changes", s.LinkChanges)
	}
}

// TestNoAdditionalTestWithoutShortInterval: a profile without
// TestShortInterval keeps the default rhythm after a LinkDown.
func TestNoAdditionalTestWithoutShortInterval(t *testing.T) {
	p := quiet
	p.TestInterval = 300 * time.Millisecond
	p.TestMax = 1000
	r := newRig(t, RoleManager, p, [2]bool{true, true})
	r.nextTest(0)
	t0 := time.Now()
	client := ethernet.MAC{0x02, 0x20, 0, 0, 2, 1}
	r.put(0, MACControl, Frame{Link: &LinkChange{SA: client, PortRole: PortPrimary, Interval: 20, Blocked: 1}})
	r.nextTest(0)
	if d := time.Since(t0); d < 200*time.Millisecond {
		t.Errorf("test %v after the previous one, want the default interval", d)
	}
}
