// SPDX-License-Identifier: MIT

package dcp

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/l2"
	"github.com/otfabric/go-profinet/vnet"
)

// txEnv is a responder for one fake station and two raw requesters.
type txEnv struct {
	r    *Responder
	st   *fakeStation
	a, b *rawDev
}

func newTxEnv(t *testing.T, opts ResponderOptions) *txEnv {
	t.Helper()
	n := vnet.New(5)
	t.Cleanup(func() { _ = n.Close() })
	ep, err := n.NewEndpoint(vnet.EndpointConfig{MAC: devMAC})
	if err != nil {
		t.Fatal(err)
	}
	m := l2.NewMux(ep)
	t.Cleanup(func() { _ = m.Close() })
	st := testStation()
	r, err := NewResponder(m, st, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return &txEnv{r: r, st: st,
		a: newRaw(t, n, ethernet.MAC{2, 0, 0, 0, 0, 0xA1}),
		b: newRaw(t, n, ethernet.MAC{2, 0, 0, 0, 0, 0xB2})}
}

// call sends a Get or Set and returns the response with the same Xid.
func (e *txEnv) call(t *testing.T, d *rawDev, s ServiceID, xid uint32, blocks ...Block) Frame {
	t.Helper()
	d.send(t, devMAC, &Frame{FrameID: FrameIDGetSet, Service: s, Type: TypeRequest, Xid: xid, Blocks: blocks})
	_, resp, ok := d.recv(t, 2*time.Second)
	if !ok {
		t.Fatalf("%s xid %d not answered", s, xid)
	}
	if resp.Xid != xid || resp.Service != s {
		t.Fatalf("response %s xid %d, want %s %d", resp.Service, resp.Xid, s, xid)
	}
	return resp
}

func blockErrors(t *testing.T, f Frame) []BlockError {
	t.Helper()
	var out []BlockError
	for _, b := range f.Blocks {
		cr, err := DecodeControlResponse(b.Data)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, cr.Error)
	}
	return out
}

func nameBlock(name string) Block {
	return Block{ID: DeviceName, HasInfo: true, Info: QualifierTemporary, Data: []byte(name)}
}

var (
	txStart = Block{ID: CtrlStart, HasInfo: true}
	txStop  = Block{ID: CtrlStop, HasInfo: true}
)

// A requester other than the last one can Get and then Set at once: both
// are answered (a tool whose Get and Set come from different addresses,
// or a Set right after another tool's Get).
func TestOtherRequesterGetThenSet(t *testing.T) {
	e := newTxEnv(t, ResponderOptions{})
	if e.r.txFor != transactionHold || transactionHold != 3*time.Second {
		t.Fatalf("hold %v", e.r.txFor)
	}
	e.call(t, e.a, ServiceGet, 1, Block{ID: DeviceName})
	e.call(t, e.a, ServiceSet, 2, nameBlock("plant-a"))
	if got := e.call(t, e.b, ServiceGet, 3, Block{ID: DeviceName}); len(got.Blocks) == 0 {
		t.Fatal("empty Get response")
	}
	resp := e.call(t, e.b, ServiceSet, 4, nameBlock("plant-b"))
	if got := blockErrors(t, resp); !slices.Equal(got, []BlockError{BlockErrorNone}) {
		t.Fatalf("errors %v", got)
	}
	if got := e.st.Identity().NameOfStation; got != "plant-b" {
		t.Fatalf("name %q", got)
	}
	if s := e.r.Stats(); s.Ignored != 0 || s.Refused != 0 || s.Get != 2 || s.Set != 2 {
		t.Fatalf("stats %+v", s)
	}
}

// Within the Start ... End Transaction of one requester, the Sets of
// another are answered with resource error for every block and change
// nothing; its Gets are answered. The holder's Sets are applied, and
// after its End Transaction the other requester's Set is applied.
func TestTransactionRefusesOtherSets(t *testing.T) {
	var mu sync.Mutex
	var events []AuditEvent
	e := newTxEnv(t, ResponderOptions{Policy: Policy{Audit: func(ev AuditEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}}})
	if got := blockErrors(t, e.call(t, e.a, ServiceSet, 1, txStart)); !slices.Equal(got, []BlockError{BlockErrorNone}) {
		t.Fatalf("start %v", got)
	}
	resp := e.call(t, e.b, ServiceSet, 2, txStart, nameBlock("plant-b"), txStop)
	if got := blockErrors(t, resp); !slices.Equal(got, []BlockError{BlockErrorResource, BlockErrorResource, BlockErrorResource}) {
		t.Fatalf("other requester %v", got)
	}
	if got := e.st.Identity().NameOfStation; got == "plant-b" {
		t.Fatal("refused Set applied")
	}
	e.call(t, e.b, ServiceGet, 3, Block{ID: DeviceName})
	if got := blockErrors(t, e.call(t, e.a, ServiceSet, 4, nameBlock("plant-a"))); !slices.Equal(got, []BlockError{BlockErrorNone}) {
		t.Fatalf("holder %v", got)
	}
	if got := blockErrors(t, e.call(t, e.a, ServiceSet, 5, txStop)); !slices.Equal(got, []BlockError{BlockErrorNone}) {
		t.Fatalf("stop %v", got)
	}
	if got := blockErrors(t, e.call(t, e.b, ServiceSet, 6, nameBlock("plant-b"))); !slices.Equal(got, []BlockError{BlockErrorNone}) {
		t.Fatalf("after the transaction %v", got)
	}
	if got := e.st.Identity().NameOfStation; got != "plant-b" {
		t.Fatalf("name %q", got)
	}
	if s := e.r.Stats(); s.Refused != 3 || s.Ignored != 0 {
		t.Fatalf("stats %+v", s)
	}
	mu.Lock()
	defer mu.Unlock()
	refused := 0
	for _, ev := range events {
		if ev.Outcome == AuditRefused && ev.Error == BlockErrorResource {
			if ev.Source != (ethernet.MAC{2, 0, 0, 0, 0, 0xB2}) {
				t.Fatalf("refused event of %s", ev.Source)
			}
			refused++
		}
	}
	if refused != 3 {
		t.Fatalf("%d refused events: %v", refused, events)
	}
}

// A transaction without End Transaction ends txFor after the holder's
// last Set; each Set of the holder extends it.
func TestTransactionTimeout(t *testing.T) {
	e := newTxEnv(t, ResponderOptions{})
	e.r.txFor = 300 * time.Millisecond
	e.call(t, e.a, ServiceSet, 1, txStart, nameBlock("plant-a"))
	time.Sleep(200 * time.Millisecond)
	e.call(t, e.a, ServiceSet, 2, nameBlock("plant-a2"))
	time.Sleep(200 * time.Millisecond)
	if got := blockErrors(t, e.call(t, e.b, ServiceSet, 3, nameBlock("plant-b"))); !slices.Equal(got, []BlockError{BlockErrorResource}) {
		t.Fatalf("within the extended hold %v", got)
	}
	time.Sleep(400 * time.Millisecond)
	if got := blockErrors(t, e.call(t, e.b, ServiceSet, 4, nameBlock("plant-b"))); !slices.Equal(got, []BlockError{BlockErrorNone}) {
		t.Fatalf("after the hold %v", got)
	}
	if got := e.st.Identity().NameOfStation; got != "plant-b" {
		t.Fatalf("name %q", got)
	}
}

// A Set that brackets its blocks with Start and End Transaction in one
// frame leaves no transaction open.
func TestTransactionInOneFrame(t *testing.T) {
	e := newTxEnv(t, ResponderOptions{})
	e.call(t, e.a, ServiceSet, 1, txStart, nameBlock("plant-a"), txStop)
	if got := blockErrors(t, e.call(t, e.b, ServiceSet, 2, nameBlock("plant-b"))); !slices.Equal(got, []BlockError{BlockErrorNone}) {
		t.Fatalf("errors %v", got)
	}
}

// Refused Sets stay under the policy's Set rate limit.
func TestTransactionRefusalRateLimited(t *testing.T) {
	e := newTxEnv(t, ResponderOptions{Policy: Policy{SetLimit: RateLimit{Rate: 0.001, Burst: 1}}})
	e.call(t, e.a, ServiceSet, 1, txStart)
	if got := blockErrors(t, e.call(t, e.b, ServiceSet, 2, nameBlock("plant-b"))); !slices.Equal(got, []BlockError{BlockErrorResource}) {
		t.Fatalf("errors %v", got)
	}
	e.b.send(t, devMAC, &Frame{FrameID: FrameIDGetSet, Service: ServiceSet, Type: TypeRequest, Xid: 3, Blocks: []Block{nameBlock("plant-b")}})
	if _, _, ok := e.b.recv(t, 200*time.Millisecond); ok {
		t.Fatal("rate-limited Set answered")
	}
	if s := e.r.Stats(); s.RateLimited != 1 {
		t.Fatalf("stats %+v", s)
	}
}
