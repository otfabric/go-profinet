// SPDX-License-Identifier: MIT

package lldp

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/internal/leakcheck"
	"github.com/otfabric/go-profinet/vnet"
)

// TestCloseConcurrent: Close of an agent from several goroutines at once
// (contract row L2): its periodic transmission has ended when any of
// them returns.
func TestCloseConcurrent(t *testing.T) {
	n := vnet.New(1)
	defer func() { _ = n.Close() }()
	a := newAgent(t, newNode(t, n, 1), AgentOptions{Interval: time.Millisecond, Local: func() LocalInfo {
		return LocalInfo{StationName: "a", PortName: "port-001"}
	}})
	waitFor(t, "a frame", func() bool { return a.Sent() > 0 })
	var sent [8]uint64
	leakcheck.Together(8, func(i int) {
		a.Close()
		sent[i] = a.Sent()
	})
	select {
	case <-a.sub.Done():
	default:
		t.Fatal("the agent's subscription is open after Close")
	}
	final := a.Sent()
	for i, s := range sent {
		if s != final {
			t.Errorf("caller %d returned from Close at %d frames, %d were sent in the end", i, s, final)
		}
	}
}

// TestOnChangeMayUseTheAgent: OnChange runs without the agent's lock and
// may call its other methods (contract row L9).
func TestOnChangeMayUseTheAgent(t *testing.T) {
	n := vnet.New(1)
	defer func() { _ = n.Close() }()
	peer := newNode(t, n, 2)
	var a *Agent
	var seen atomic.Int64
	ready := make(chan struct{})
	a = newAgent(t, newNode(t, n, 1), AgentOptions{Interval: -1, OnChange: func() {
		<-ready
		ns := a.Neighbors()
		if _, ok := a.Latest(); ok != (len(ns) > 0) {
			t.Error("Latest and Neighbors disagree inside OnChange")
		}
		_ = a.Sent() + a.Malformed()
		seen.Store(int64(len(ns)))
	}})
	close(ready)
	peer.sendLLDP(t, Frame{ChassisSubtype: 7, ChassisID: []byte("peer"), PortSubtype: 7, PortID: []byte("port-001"), TTL: 20})
	waitFor(t, "the callback to see the neighbor", func() bool { return seen.Load() == 1 })
}
