// SPDX-License-Identifier: MIT

package device

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/dcp"
	"github.com/otfabric/go-profinet/store"
)

// gateStore blocks one Save while armed, to schedule the window between
// ResetToFactory's AR abort and the end of the reset.
type gateStore struct {
	*store.Memory
	mu      sync.Mutex
	armed   bool
	entered chan struct{}
	release chan struct{}
}

func (s *gateStore) Save(ctx context.Context, key string, v []byte) error {
	s.mu.Lock()
	armed := s.armed
	s.armed = false
	s.mu.Unlock()
	if armed {
		close(s.entered)
		<-s.release
	}
	return s.Memory.Save(ctx, key, v)
}

func TestConnectDuringResetToFactory(t *testing.T) {
	n := newNet(t)
	gs := &gateStore{Memory: store.NewMemory(), entered: make(chan struct{}), release: make(chan struct{})}
	d, _ := n.device(newRecHandler(), withStoreCfg(gs, "unit"))
	p := n.peer(d, "192.168.0.2", true)
	st := station{d}

	gs.mu.Lock()
	gs.armed = true
	gs.mu.Unlock()
	res := make(chan dcp.BlockError, 1)
	go func() { res <- st.ResetToFactory(dcp.ResetCommunication) }()
	select {
	case <-gs.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("reset never persisted")
	}
	// A controller connects while the reset is persisting (ARs already aborted).
	req := p.ioCAR()
	if _, err := p.connect(req); err == nil {
		t.Fatal("connect accepted during the reset")
	}
	close(gs.release)
	code := <-res
	if code != dcp.BlockErrorNone {
		t.Fatalf("reset: %v", code)
	}
	if len(d.ARs()) != 0 || !d.IP().IsZero() {
		t.Fatalf("after the reset: %d ARs, IP %s", len(d.ARs()), d.IP())
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.resetting {
		t.Fatal("still refusing Connects after the reset")
	}
}
