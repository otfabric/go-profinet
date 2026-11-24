// SPDX-License-Identifier: MIT

package device

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/l2/bridge"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/mrp"
	"github.com/otfabric/go-profinet/pnio"
)

// errBusy is a start failure of the moment, as a port refusing another
// subscription.
var errBusy = errors.New("port busy")

// recoveryRing is a client on ring ports 1 and 2 held in PT (port 2
// blocked) with the controller outside the ring, and a stub for mrp.New
// that answers the role's start with fail (nil: mrp.New). calls counts
// the starts per role.
type recoveryRing struct {
	d     *Device
	x     *pdevPeer
	dom   [16]byte
	slow  mrp.Profile
	calls [3]atomic.Int32 // by mrp.Role: disabled, client, manager
}

func newRecoveryRing(t *testing.T, fail func(r mrp.Role, n int32) error) *recoveryRing {
	t.Helper()
	r := &recoveryRing{dom: [16]byte{0xD0, 2}, slow: mrp.Profile200}
	r.slow.Name, r.slow.LinkInterval, r.slow.LinkRepeat = "slow link-up", 30*time.Second, 4 // PT for 2 minutes
	d, n := mrpRingDeviceOn(t, &MRPConfig{Role: mrp.RoleClient, Domain: r.dom, DomainName: "ring-b", RingPorts: [2]int{1, 2}, Profile: r.slow}, 2)
	r.d = d
	eventually(t, 5*time.Second, "client in PT", func() bool {
		st, ok := d.MRPStatus()
		return ok && st.State == mrp.StatePT && r.states() == [2]bridge.State{bridge.Forwarding, bridge.Blocked}
	})
	r.x = newPDevPeer(t, n, d)
	d.mrpRetry = 20 * time.Millisecond
	d.mrpNew = func(c mrp.Config) (*mrp.Entity, error) {
		k := min(int(c.Role), 2)
		if err := fail(c.Role, r.calls[k].Add(1)); err != nil {
			return nil, err
		}
		return mrp.New(c)
	}
	return r
}

func (r *recoveryRing) states() [2]bridge.State {
	br := r.d.cfg.Bridge
	return [2]bridge.State{br.State(0), br.State(1)}
}

// writeManager writes a manager configuration, which the stub may refuse.
func (r *recoveryRing) writeManager() error {
	a := pnio.PDInterfaceMrpDataAdjust{Domain: r.dom, Role: pnio.MrpRoleManager, DomainName: "ring-b"}
	return r.x.write(model.SubslotInterface, pnio.IndexPDInterfaceMrpDataAdjust, a.Append(nil))
}

func (r *recoveryRing) role() (mrp.Role, bool) {
	st, ok := r.d.MRPStatus()
	return st.Role, ok
}

// After the new entity and the restore of the previous one both failed
// for a reason of the moment, the device starts the previous entity
// again by itself, without a new write; until then the ring ports keep
// their state (port 2 stays blocked). Before, no entity ran until a
// controller rewrote the configuration.
func TestMRPRecoversAfterDoubleFailure(t *testing.T) {
	var release atomic.Bool
	r := newRecoveryRing(t, func(role mrp.Role, n int32) error {
		if role == mrp.RoleManager || !release.Load() {
			return errBusy
		}
		return nil
	})
	wantStatus(t, r.writeManager(), pnio.RWInvalidParameter, 0)
	if _, ok := r.role(); ok {
		t.Fatal("an entity runs after the double failure")
	}
	// The recovery retries and fails; the ports stay as the client left
	// them.
	eventually(t, 5*time.Second, "retries", func() bool { return r.calls[mrp.RoleClient].Load() >= 3 })
	if got := r.states(); got != [2]bridge.State{bridge.Forwarding, bridge.Blocked} {
		t.Fatalf("ring ports %v during the recovery", got)
	}
	release.Store(true)
	eventually(t, 5*time.Second, "client recovered", func() bool {
		role, ok := r.role()
		return ok && role == mrp.RoleClient
	})
	r.d.mu.Lock()
	c := r.d.mrpSt.cfg
	r.d.mu.Unlock()
	if c.Role != mrp.RoleClient || c.Domain != r.dom || c.Profile != r.slow {
		t.Fatalf("recovered configuration %+v", c)
	}
	// It stops once the entity runs.
	n := r.calls[mrp.RoleClient].Load()
	time.Sleep(200 * time.Millisecond)
	if got := r.calls[mrp.RoleClient].Load(); got != n {
		t.Fatalf("%d starts after the recovery", got-n)
	}
}

// A configuration written during a recovery wins: the recovery of the
// older one stops and does not replace the entity of the newer.
func TestMRPRecoveryYieldsToNewerConfig(t *testing.T) {
	var managerOK atomic.Bool
	r := newRecoveryRing(t, func(role mrp.Role, n int32) error {
		if role == mrp.RoleManager && managerOK.Load() {
			return nil
		}
		return errBusy // the client never starts
	})
	wantStatus(t, r.writeManager(), pnio.RWInvalidParameter, 0)
	eventually(t, 5*time.Second, "retries", func() bool { return r.calls[mrp.RoleClient].Load() >= 3 })
	managerOK.Store(true)
	if err := r.writeManager(); err != nil {
		t.Fatal(err)
	}
	n := r.calls[mrp.RoleClient].Load()
	time.Sleep(300 * time.Millisecond)
	if got := r.calls[mrp.RoleClient].Load(); got != n {
		t.Fatalf("the recovery of the older configuration went on: %d more starts", got-n)
	}
	if role, ok := r.role(); !ok || role != mrp.RoleManager {
		t.Fatalf("entity %v %v, want the newer manager", role, ok)
	}
}

// A start that fails for good during the recovery ends it: retrying a
// configuration that cannot run would change nothing.
func TestMRPRecoveryStopsOnPermanentFailure(t *testing.T) {
	r := newRecoveryRing(t, func(role mrp.Role, n int32) error {
		if role == mrp.RoleClient && n >= 3 {
			return profinet.ErrInvalidConfig
		}
		return errBusy
	})
	wantStatus(t, r.writeManager(), pnio.RWInvalidParameter, 0)
	eventually(t, 5*time.Second, "first retry", func() bool { return r.calls[mrp.RoleClient].Load() >= 3 })
	time.Sleep(300 * time.Millisecond)
	if got := r.calls[mrp.RoleClient].Load(); got != 3 {
		t.Fatalf("%d client starts, want 3: the restore and two retries", got)
	}
	if _, ok := r.role(); ok {
		t.Fatal("an entity runs")
	}
}

// Close ends a recovery waiting for its next attempt, at once.
func TestMRPRecoveryStopsAtClose(t *testing.T) {
	r := newRecoveryRing(t, func(mrp.Role, int32) error { return errBusy })
	r.d.mrpRetry = time.Hour
	wantStatus(t, r.writeManager(), pnio.RWInvalidParameter, 0)
	done := make(chan struct{})
	go func() {
		_ = r.d.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waits for the recovery's backoff")
	}
	if got := r.calls[mrp.RoleClient].Load(); got != 1 {
		t.Fatalf("%d client starts, want the restore only", got)
	}
}

// A configuration mrp.New refuses is refused before the running entity
// stops: the entity, its port states and the configuration stay. Before,
// the entity was stopped first and the device fell back on the restore.
func TestMRPRoleValidatedBeforeStop(t *testing.T) {
	d, _ := mrpRingDeviceOn(t, &MRPConfig{Role: mrp.RoleClient, RingPorts: [2]int{1, 2}}, 2)
	eventually(t, 5*time.Second, "client", func() bool { _, ok := d.MRPStatus(); return ok })
	d.mu.Lock()
	ent, cfg := d.mrpSt.ent, d.mrpSt.cfg
	d.mu.Unlock()
	d.mrpMu.Lock()
	err := d.setMRPRole(mrp.Role(99), cfg.Domain, cfg.DomainName)
	d.mrpMu.Unlock()
	if !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("setMRPRole: %v", err)
	}
	d.mu.Lock()
	ent2, cfg2 := d.mrpSt.ent, d.mrpSt.cfg
	d.mu.Unlock()
	if ent2 != ent || cfg2 != cfg {
		t.Fatal("a refused role changed the entity or the configuration")
	}
	if st, ok := d.MRPStatus(); !ok || st.Role != mrp.RoleClient {
		t.Fatalf("entity %+v %v", st, ok)
	}
}
