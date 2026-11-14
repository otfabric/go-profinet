// SPDX-License-Identifier: MIT

package mrp

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/l2"
	"github.com/otfabric/go-profinet/l2/bridge"
	"github.com/otfabric/go-profinet/vnet"
)

type countingSwitch struct {
	br      *bridge.Bridge
	flushes atomic.Int64
}

func (c *countingSwitch) SetBlocked(i int, b bool) { c.br.SetBlocked(i, b) }
func (c *countingSwitch) Flush()                   { c.flushes.Add(1); c.br.Flush() }

func topologyChangeFrame(interval, seq uint16) []byte {
	sa := ethernet.MAC{0x02, 0x77, 0, 0, 0, 1}
	f := Frame{Version: 1, TopologyChange: &TopologyChange{Prio: 0x8000, SA: sa, Interval: interval},
		Common: &Common{SequenceID: seq, Domain: DefaultDomain}}
	h := ethernet.Frame{Dst: MACControl, Src: sa, EtherType: EtherType}
	b, err := f.AppendTo(h.AppendHeader(nil))
	if err != nil {
		panic(err)
	}
	return ethernet.Pad(b, 0)
}

func TestTopologyChangeTimers(t *testing.T) {
	seg0, seg1 := vnet.New(11), vnet.New(12)
	defer func() { _ = seg0.Close() }()
	defer func() { _ = seg1.Close() }()
	p0, _ := seg0.NewEndpoint(vnet.EndpointConfig{MAC: ethernet.MAC{0x02, 0x31, 0, 0, 0, 1}, Promiscuous: true})
	p1, _ := seg1.NewEndpoint(vnet.EndpointConfig{MAC: ethernet.MAC{0x02, 0x31, 0, 0, 0, 2}, Promiscuous: true})
	atk, _ := seg0.NewEndpoint(vnet.EndpointConfig{MAC: ethernet.MAC{0x02, 0x77, 0, 0, 0, 1}, Promiscuous: true})
	br, err := bridge.New(bridge.Config{Ports: []l2.Port{p0, p1}, MAC: ethernet.MAC{0x02, 0x31, 0, 0, 0, 9}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = br.Close() }()
	m0, m1 := l2.NewMux(br.Port(0)), l2.NewMux(br.Port(1))
	defer func() { _ = m0.Close() }()
	defer func() { _ = m1.Close() }()
	sw := &countingSwitch{br: br}
	ent, err := New(Config{Role: RoleClient, Ports: [2]*l2.Mux{m0, m1}, Index: [2]int{0, 1}, Switch: sw, MAC: ethernet.MAC{0x02, 0x31, 0, 0, 0, 9}, Profile: Profile200})
	if err != nil {
		t.Fatal(err)
	}
	const n = 500
	start := time.Now()
	for i := range n {
		_ = atk.WriteFrame(context.Background(), topologyChangeFrame(300, uint16(i)))
		if i%32 == 31 {
			for ent.Status().TopologyChanges < uint64(i+1)*8/10 {
				time.Sleep(time.Millisecond)
			}
		}
	}
	dl := time.Now().Add(10 * time.Second)
	for ent.Status().TopologyChanges < uint64(n)*9/10 && time.Now().Before(dl) {
		time.Sleep(5 * time.Millisecond)
	}
	_ = ent.Close()
	atClose := sw.flushes.Load()
	time.Sleep(800 * time.Millisecond)
	after := sw.flushes.Load()
	if after != atClose {
		t.Errorf("%d delayed flushes after Close", after-atClose)
	}
	// They share one timer: at most one flush per interval came before.
	if atClose > 1+int64(time.Since(start)/(300*time.Millisecond)) {
		t.Errorf("%d flushes during the stream", atClose)
	}
}
