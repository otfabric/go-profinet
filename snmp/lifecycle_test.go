// SPDX-License-Identifier: MIT

package snmp

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/internal/leakcheck"
)

// TestCloseConcurrent: Close of a serving agent from several goroutines
// at once (contract row L2): all return and Serve ends.
func TestCloseConcurrent(t *testing.T) {
	a := newTestAgent(t, Config{})
	served := make(chan error, 1)
	go func() { served <- a.Serve(context.Background()) }()
	for serving := false; !serving; runtime.Gosched() {
		a.mu.Lock()
		serving = a.serving
		a.mu.Unlock()
	}
	leakcheck.Together(8, func(int) {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := <-served; err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.Serve(context.Background()); !errors.Is(err, profinet.ErrClosed) {
		t.Fatalf("Serve after Close: %v", err)
	}
}
