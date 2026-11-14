// SPDX-License-Identifier: MIT

package dcp

import (
	"testing"
	"time"

	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/l2"
	"github.com/otfabric/go-profinet/vnet"
)

// After a Set, the Get and Set requests of another requester are ignored
// for the lock time; the first requester and, after the lock, the other
// one are answered.
func TestRequesterLock(t *testing.T) {
	n := vnet.New(5)
	t.Cleanup(func() { _ = n.Close() })
	ep, err := n.NewEndpoint(vnet.EndpointConfig{MAC: devMAC})
	if err != nil {
		t.Fatal(err)
	}
	m := l2.NewMux(ep)
	t.Cleanup(func() { _ = m.Close() })
	st := testStation()
	r, err := NewResponder(m, st, ResponderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if r.lockFor != requesterLock || requesterLock != 3*time.Second {
		t.Fatalf("lock %v", r.lockFor)
	}
	r.lockFor = 300 * time.Millisecond
	a := newRaw(t, n, ethernet.MAC{2, 0, 0, 0, 0, 0xA1})
	b := newRaw(t, n, ethernet.MAC{2, 0, 0, 0, 0, 0xB2})
	set := func(x uint32, name string) *Frame {
		return &Frame{FrameID: FrameIDGetSet, Service: ServiceSet, Type: TypeRequest, Xid: x,
			Blocks: []Block{{ID: DeviceName, HasInfo: true, Info: 1, Data: []byte(name)}}}
	}
	a.send(t, devMAC, set(1, "plant-a"))
	if _, _, ok := a.recv(t, 2*time.Second); !ok {
		t.Fatal("first requester not answered")
	}
	b.send(t, devMAC, set(2, "plant-b"))
	if _, _, ok := b.recv(t, 100*time.Millisecond); ok {
		t.Fatal("second requester answered within the lock")
	}
	if got := st.Identity().NameOfStation; got != "plant-a" {
		t.Fatalf("name %q", got)
	}
	if r.Stats().Ignored == 0 {
		t.Fatal("ignored request not counted")
	}
	a.send(t, devMAC, set(3, "plant-a"))
	if _, _, ok := a.recv(t, 2*time.Second); !ok {
		t.Fatal("lock holder not answered")
	}
	time.Sleep(400 * time.Millisecond)
	b.send(t, devMAC, set(4, "plant-b"))
	if _, _, ok := b.recv(t, 2*time.Second); !ok {
		t.Fatal("second requester not answered after the lock")
	}
	if got := st.Identity().NameOfStation; got != "plant-b" {
		t.Fatalf("name %q after the lock", got)
	}
}
