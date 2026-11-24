// SPDX-License-Identifier: MIT

package lldp

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/vnet"
)

// An explicit TTL replaces TTLFactor × Interval in the frames sent,
// periodic and SendNow alike, in whole seconds within 1..65535; the
// receiver keeps the neighbor that long.
func TestAgentExplicitTTL(t *testing.T) {
	n := vnet.New(2)
	defer func() { _ = n.Close() }()
	na, nb := newNode(t, n, 0xA), newNode(t, n, 0xB)
	rx := newAgent(t, nb, AgentOptions{Interval: -1})
	tx := newAgent(t, na, AgentOptions{Interval: 50 * time.Millisecond, TTLFactor: 4, TTL: 21 * time.Second, Local: func() LocalInfo {
		return LocalInfo{StationName: "dev", PortName: "port-001"}
	}})
	waitFor(t, "neighbor", func() bool { return len(rx.Neighbors()) == 1 })
	nb0 := rx.Neighbors()[0]
	if nb0.Frame.TTL != 21 || nb0.Expires.Sub(nb0.Updated) != 21*time.Second {
		t.Fatalf("neighbor %+v", nb0)
	}
	before := tx.Sent()
	if err := tx.SendNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "SendNow", func() bool { return tx.Sent() > before })
	if got := rx.Neighbors()[0].Frame.TTL; got != 21 {
		t.Fatalf("SendNow TTL %d", got)
	}
	for _, c := range []struct {
		ttl  time.Duration
		want uint16
	}{
		{21 * time.Second, 21},
		{21900 * time.Millisecond, 21},
		{500 * time.Millisecond, 1},
		{70000 * time.Second, 65535},
		{math.MaxInt64, 65535},
	} {
		a := &Agent{opts: AgentOptions{Interval: 5 * time.Second, TTLFactor: 4, TTL: c.ttl}}
		if got := a.ttl(); got != c.want {
			t.Errorf("TTL %v: announced %d, want %d", c.ttl, got, c.want)
		}
	}
	// Without TTL the product applies: 5 s × 4, the generic default.
	if got := (&Agent{opts: AgentOptions{Interval: 5 * time.Second, TTLFactor: 4}}).ttl(); got != 20 {
		t.Fatalf("default TTL %d", got)
	}
}
