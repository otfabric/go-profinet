// SPDX-License-Identifier: MIT

package device

import (
	"testing"

	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/pnio"
)

// TestResetPDevUndoKeepsLaterWrite: the undo of a refused Connect gives
// a port back what the reset took, unless the port was written since:
// that write stands.
func TestResetPDevUndoKeepsLaterWrite(t *testing.T) {
	n := newNet(t)
	d, _ := n.device(nil)
	port := model.SubslotPort1
	old := &pnio.PDPortDataCheck{Subslot: port}
	d.mu.Lock()
	d.pd.checks[port] = old
	d.mu.Unlock()
	a := &ar{match: map[key]bool{{0, port}: true}}
	undo := d.resetPDev(a, nil)
	d.mu.Lock()
	reset := d.pd.checks[port] == nil
	d.mu.Unlock()
	if !reset {
		t.Fatal("the Connect's reset kept the port check")
	}
	later := &pnio.PDPortDataCheck{Subslot: port}
	d.mu.Lock()
	d.pd.checks[port] = later
	d.mu.Unlock()
	undo()
	d.mu.Lock()
	got := d.pd.checks[port]
	d.mu.Unlock()
	if got != later {
		t.Fatalf("port check %p after the undo, want the later write %p (old %p)", got, later, old)
	}
}
