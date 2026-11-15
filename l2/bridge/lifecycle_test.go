// SPDX-License-Identifier: MIT

package bridge

import (
	"context"
	"errors"
	"testing"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/internal/leakcheck"
)

// TestCloseConcurrent: Close of a bridge from several goroutines at once
// (contract row L2): all return when its goroutines have ended, and its
// CPU and local ports report closed.
func TestCloseConcurrent(t *testing.T) {
	check := leakcheck.Check(t)
	e := newEnv(t, 2, Config{})
	leakcheck.Together(8, func(int) {
		if err := e.b.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := e.b.CPU().ReadFrame(context.Background(), make([]byte, 64)); !errors.Is(err, profinet.ErrClosed) {
		t.Fatalf("ReadFrame on the CPU port after Close: %v", err)
	}
	if err := e.b.Port(0).WriteFrame(context.Background(), frame(hostA, cpuMAC, 1)); !errors.Is(err, profinet.ErrClosed) {
		t.Fatalf("WriteFrame on a local port after Close: %v", err)
	}
	// The bridge's goroutines read its physical ports; they end with
	// Close, not with the ports.
	check()
}
