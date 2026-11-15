// SPDX-License-Identifier: MIT

package rpc

import (
	"context"
	"errors"
	"testing"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/internal/leakcheck"
)

// TestCloseConcurrent: Close of a client and of a server from several
// goroutines at once, a second time as well (contract rows L1 and L2).
func TestCloseConcurrent(t *testing.T) {
	c, _, peer := clientEnv(t, ClientOptions{})
	leakcheck.Together(8, func(int) {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := c.Close(); err != nil {
		t.Fatalf("Close again: %v", err)
	}
	if _, err := c.Do(context.Background(), peer.LocalAddr(), Call{Iface: testIface, IfaceVersion: 1}); !errors.Is(err, profinet.ErrClosed) {
		t.Fatalf("Do after Close: %v", err)
	}

	s, _, _ := memServer(t, ServerOptions{}, nil)
	leakcheck.Together(8, func(int) {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := s.Close(); err != nil {
		t.Fatalf("Close again: %v", err)
	}
}
