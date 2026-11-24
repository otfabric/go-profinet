// SPDX-License-Identifier: MIT

package mrp

import (
	"slices"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/ethernet"
)

func linkFrame(up bool) Frame {
	return Frame{Link: &LinkChange{Up: up, SA: ethernet.MAC{0x02, 0x20, 0, 0, 2, 1}, PortRole: PortPrimary, Interval: 20, Blocked: 1}}
}

// TestAdditionalTestEvents: while ADD_TEST is set no LinkChange asks for
// a test, whichever its kind and order: the second test comes after
// MRP_TSTshortT, the third at MRP_TSTdefaultT. Before, a LinkUp asked for
// a test round of its own at once, past ADD_TEST.
func TestAdditionalTestEvents(t *testing.T) {
	for _, c := range []struct {
		name     string
		ev1, ev2 bool // Up
	}{{"down-down", false, false}, {"up-up", true, true}, {"down-up", false, true}, {"up-down", true, false}} {
		t.Run(c.name, func(t *testing.T) {
			p := quiet
			p.TestInterval = 600 * time.Millisecond
			p.TestMax = 1000
			p.TestShortInterval = 80 * time.Millisecond
			r := newRig(t, RoleManager, p, [2]bool{true, true})
			r.nextTest(0)
			time.Sleep(100 * time.Millisecond)
			sent := time.Now()
			r.put(0, MACControl, linkFrame(c.ev1))
			r.nextTest(0)
			t1 := time.Now()
			r.put(0, MACControl, linkFrame(c.ev2))
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
		})
	}
}

// TestLinkUpTestWithoutShortInterval: without MRP_TSTshortT a client's
// LinkUp still makes the manager test at once.
func TestLinkUpTestWithoutShortInterval(t *testing.T) {
	p := quiet
	p.TestInterval = 600 * time.Millisecond
	p.TestMax = 1000
	r := newRig(t, RoleManager, p, [2]bool{true, true})
	r.nextTest(0)
	time.Sleep(50 * time.Millisecond)
	sent := time.Now()
	r.put(0, MACControl, linkFrame(true))
	r.nextTest(0)
	if d := time.Since(sent); d > 100*time.Millisecond {
		t.Errorf("test %v after a LinkUp, want at once", d)
	}
}

// TestProfileTestTiming: the test intervals of Profile200 and Profile500
// are kept to within a few milliseconds in the median of several, not
// rounded up to the link polling tick. Before, Profile200 tested every
// 30 ms and Profile500 every 60 ms, and their MRP_TSTshortT came after
// about 20 and 40 ms. A general-purpose OS bounds no single interval;
// the median is what is checked.
func TestProfileTestTiming(t *testing.T) {
	median := func(ds []time.Duration) time.Duration {
		ds = slices.Clone(ds)
		slices.Sort(ds)
		return ds[len(ds)/2]
	}
	const slack = 8 * time.Millisecond
	for _, base := range []Profile{Profile200, Profile500} {
		t.Run(base.Name, func(t *testing.T) {
			p := base
			p.TestMax = 1000 // the rig returns no test: the ring stays closed
			r := newRig(t, RoleManager, p, [2]bool{true, true})
			r.nextTest(0)
			last := time.Now()
			var gaps []time.Duration
			for range 9 {
				r.nextTest(0)
				gaps = append(gaps, time.Since(last))
				last = time.Now()
			}
			if m := median(gaps); m < p.TestInterval-time.Millisecond || m > p.TestInterval+slack {
				t.Errorf("MRP_TSTdefaultT: median %v of %v, want %v", m, gaps, p.TestInterval)
			}
			var shorts []time.Duration
			for i := range 7 {
				// After a regular test: the previous additional test ended
				// (at its expiry), and no test is queued on the wire.
				r.nextTest(0)
				r.put(0, MACControl, linkFrame(i%2 == 0))
				r.nextTest(0)
				a := time.Now()
				r.nextTest(0)
				shorts = append(shorts, time.Since(a))
			}
			if m := median(shorts); m < p.TestShortInterval-time.Millisecond || m > p.TestShortInterval+slack {
				t.Errorf("MRP_TSTshortT: median %v of %v, want %v", m, shorts, p.TestShortInterval)
			}
		})
	}
}
