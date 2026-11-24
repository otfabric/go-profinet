// SPDX-License-Identifier: MIT

package device

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/pnio"
)

// pauseEval makes the next port evaluation that has a result to record
// stop before recording it: paused receives once it stopped, and the
// evaluation goes on when release is closed. Later evaluations run
// through. results counts the evaluations that had a result to record.
func pauseEval(d *Device) (paused <-chan struct{}, release chan<- struct{}, results *atomic.Int32) {
	p, r := make(chan struct{}), make(chan struct{})
	var once sync.Once
	results = new(atomic.Int32)
	d.evalPaused = func(uint16) {
		results.Add(1)
		once.Do(func() {
			close(p)
			<-r
		})
	}
	return p, r, results
}

// noMismatchState checks that the port has no remote mismatch: no
// diagnosis, nothing recorded as active, no alarm owed for it.
func noMismatchState(t *testing.T, d *Device, when string) {
	t.Helper()
	if got := mismatch(d); len(got) != 0 {
		t.Fatalf("%s: stale mismatch diagnosis %#x", when, got)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if a := d.pd.active[model.SubslotPort1]; len(a) != 0 {
		t.Fatalf("%s: stale active %#x", when, a)
	}
	if o, ok := d.diagOwed[key{0, model.SubslotPort1}]; ok {
		t.Fatalf("%s: alarms owed for a stale mismatch: %+v", when, o.notes)
	}
}

// An evaluation of the previous port check that finishes after an IOCAR
// Connect reset the port records nothing: the new AR starts without the
// mismatch, in its ModuleDiffBlock and afterwards. Before, the paused
// evaluation added the diagnosis of the erased check after the reset,
// and it stayed (owed to the new AR) until a later evaluation.
func TestPDevStaleEvalAfterConnectReset(t *testing.T) {
	n := newNet(t)
	d, _ := n.device(nil)
	paused, release, _ := pauseEval(d)
	x := newPDevPeer(t, n, d)
	const port = model.SubslotPort1
	_ = lldpPeer(t, n, "sw", "port-003")
	eventually(t, 5*time.Second, "neighbor", func() bool { _, ok := d.ports[0].lldpA.Latest(); return ok })
	if err := x.write(port, pnio.IndexPDPortDataCheck, check(port, pnio.CheckPeer{PortID: "port-003", ChassisID: "plc"})); err != nil {
		t.Fatal(err)
	}
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("the evaluation of the check did not run")
	}
	// The evaluation found the mismatch and waits; the Connect resets the
	// port in the meantime.
	p := n.peer(d, "192.168.0.3", true)
	connected := make(chan error, 1)
	go func() {
		_, err := p.connect(p.ioCAR())
		connected <- err
	}()
	// The Connect does not wait for the paused evaluator.
	select {
	case err := <-connected:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the Connect waits for the port evaluation")
	}
	noMismatchState(t, d, "after the Connect")
	close(release)
	// The resumed evaluation and the one the reset asked for record
	// nothing.
	eventually(t, 5*time.Second, "evaluator idle", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return len(d.pdPoke) == 0
	})
	time.Sleep(100 * time.Millisecond)
	noMismatchState(t, d, "after the evaluation")
}

// An evaluation of a port check rewritten while it ran records nothing:
// the mismatch of the old check never appears, and the evaluation of the
// new one, which matches the neighbor, finds nothing to report. Before,
// the paused evaluation added the old mismatch and the next one removed
// it, both alarmed to the controller.
func TestPDevStaleEvalAfterCheckRewrite(t *testing.T) {
	n := newNet(t)
	d, _ := n.device(nil)
	paused, release, results := pauseEval(d)
	x := newPDevPeer(t, n, d)
	const port = model.SubslotPort1
	_ = lldpPeer(t, n, "sw", "port-003")
	eventually(t, 5*time.Second, "neighbor", func() bool { _, ok := d.ports[0].lldpA.Latest(); return ok })
	if err := x.write(port, pnio.IndexPDPortDataCheck, check(port, pnio.CheckPeer{PortID: "port-003", ChassisID: "plc"})); err != nil {
		t.Fatal(err)
	}
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("the evaluation of the check did not run")
	}
	if err := x.write(port, pnio.IndexPDPortDataCheck, check(port, pnio.CheckPeer{PortID: "port-003", ChassisID: "sw"})); err != nil {
		t.Fatal(err)
	}
	close(release)
	time.Sleep(200 * time.Millisecond)
	noMismatchState(t, d, "after the rewrite")
	// The new check had nothing to record: no stale mismatch appeared for
	// it to remove.
	if r := results.Load(); r != 1 {
		t.Fatalf("%d evaluations had a result to record, want the paused one", r)
	}
	// A check that does not match is still reported.
	if err := x.write(port, pnio.IndexPDPortDataCheck, check(port, pnio.CheckPeer{PortID: "port-009", ChassisID: "sw"})); err != nil {
		t.Fatal(err)
	}
	waitMismatch(t, d, extPeerPortMismatch)
}
