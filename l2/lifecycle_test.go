// SPDX-License-Identifier: MIT

package l2

import (
	"context"
	"errors"
	"testing"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/internal/leakcheck"
)

// TestCloseConcurrent: Close of a Mux and of its subscriptions from
// several goroutines at once (contract row L2): all return, and
// everything is closed once.
func TestCloseConcurrent(t *testing.T) {
	s := newSetup(t)
	subs := make([]*Subscription, 4)
	for i := range subs {
		var err error
		if subs[i], err = s.mux.Subscribe(MatchEtherType(ethernet.EtherTypeProfinet), 4); err != nil {
			t.Fatal(err)
		}
	}
	leakcheck.Together(12, func(i int) {
		if i < 8 {
			subs[i%2].Close()
			return
		}
		if err := s.mux.Close(); err != nil {
			t.Error(err)
		}
	})
	for i, sub := range subs {
		select {
		case <-sub.Done():
		default:
			t.Errorf("subscription %d is open after Close", i)
		}
		if _, err := sub.Recv(context.Background()); !errors.Is(err, profinet.ErrClosed) {
			t.Errorf("Recv on subscription %d: %v", i, err)
		}
	}
	select {
	case <-s.mux.Done():
	default:
		t.Fatal("the Mux is not done after Close")
	}
	if _, err := s.mux.Subscribe(MatchEtherType(ethernet.EtherTypeProfinet), 4); !errors.Is(err, profinet.ErrClosed) {
		t.Fatalf("Subscribe after Close: %v", err)
	}
}
