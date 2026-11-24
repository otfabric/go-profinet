// SPDX-License-Identifier: MIT

package device

import (
	"math"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/lldp"
)

// The device announces an LLDP TTL of 21 s with its default 5 s interval
// (p-net v2.0.0 PF_LLDP_TTL), not the generic 20 s of interval × 4;
// another interval keeps the generic TTL.
func TestDeviceLLDPTTL(t *testing.T) {
	for _, c := range []struct {
		name     string
		interval time.Duration
		want     uint16
	}{
		{"default interval", 0, 21},
		{"explicit 5 s", 5 * time.Second, 21},
		// Another interval keeps the generic TTL: 4 × 100 ms, at least 1 s.
		{"short interval", 100 * time.Millisecond, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			n := newNet(t)
			n.device(nil, func(cfg *Config) { cfg.LLDPInterval = c.interval })
			rx := n.node("")
			agent, err := lldp.NewAgent(rx.mux, lldp.AgentOptions{Interval: -1})
			if err != nil {
				t.Fatal(err)
			}
			defer agent.Close()
			var nb lldp.Neighbor
			eventually(t, 10*time.Second, "device LLDP frame", func() bool {
				var ok bool
				nb, ok = agent.Latest()
				return ok
			})
			if nb.Frame.TTL != c.want {
				t.Fatalf("TTL %d, want %d", nb.Frame.TTL, c.want)
			}
		})
	}
}

func TestLLDPTTL(t *testing.T) {
	for _, c := range []struct {
		interval, want time.Duration
	}{
		{0, 21 * time.Second},
		{5 * time.Second, 21 * time.Second},
		{-time.Second, 0},
		{time.Second, 0},
		{6 * time.Second, 0},
		{math.MaxInt64, 0},
	} {
		if got := lldpTTL(c.interval); got != c.want {
			t.Errorf("lldpTTL(%v) = %v, want %v", c.interval, got, c.want)
		}
	}
}
