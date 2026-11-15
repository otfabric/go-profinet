// SPDX-License-Identifier: MIT

package dcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/internal/leakcheck"
)

// TestCloseConcurrent: Close of a requester and of a responder from
// several goroutines at once (contract row L2).
func TestCloseConcurrent(t *testing.T) {
	_, c, _ := newClientEnv(t, ClientOptions{})
	leakcheck.Together(8, func(int) {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	select {
	case <-c.done:
	default:
		t.Fatal("the requester runs after Close")
	}
	if _, err := c.Identify(context.Background(), nil, IdentifyOptions{Wait: time.Minute}); !errors.Is(err, profinet.ErrClosed) {
		t.Fatalf("Identify after Close: %v", err)
	}

	r, _, _ := responderEnv(t, testStation(), ResponderOptions{NoResponseDelay: true}, devMAC, 0)
	leakcheck.Together(8, func(int) {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	select {
	case <-r.sub.Done():
	default:
		t.Fatal("the responder's subscription is open after Close")
	}
}
