// SPDX-License-Identifier: MIT

package dcp

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/ethernet"
)

// stationN returns the hardware address of the n-th station and its
// Identify response for transaction xid.
func stationN(n int, xid uint32) (ethernet.MAC, Frame) {
	mac := ethernet.MAC{2, 0, 1, byte(n >> 16), byte(n >> 8), byte(n)}
	id := Identity{NameOfStation: "station", VendorID: 0xF000, DeviceID: 1, Role: RoleIODevice}
	return mac, Frame{FrameID: FrameIDIdentifyResponse, Service: ServiceIdentify, Type: TypeResponseSuccess, Xid: xid, Blocks: id.Blocks()}
}

// collectFrom runs collect on rs, handed over one at a time, and ends it
// with the timeout once every response was taken, unless it returned
// before. It reports how many responses collect took.
func collectFrom(t *testing.T, c *Client, rs []response, limit, bound int) (ids []Identity, taken int, err error) {
	t.Helper()
	type result struct {
		ids []Identity
		err error
	}
	ch := make(chan response)
	timeout := make(chan time.Time)
	res := make(chan result, 1)
	go func() {
		ids, err := c.collect(context.Background(), ch, timeout, limit, bound)
		res <- result{ids, err}
	}()
	for _, r := range rs {
		select {
		case ch <- r:
			taken++
		case got := <-res:
			return got.ids, taken, got.err
		}
	}
	select {
	case timeout <- time.Time{}:
	case got := <-res:
		return got.ids, taken, got.err
	}
	got := <-res
	return got.ids, taken, got.err
}

func responses(n int) []response {
	rs := make([]response, n)
	for i := range rs {
		mac, f := stationN(i, 1)
		rs[i] = response{src: mac, frame: f}
	}
	return rs
}

// TestIdentifyBound: without a Max, Identify holds at most
// defaultMaxIdentities stations and says so when one more answers; a Max
// replaces the bound.
func TestIdentifyBound(t *testing.T) {
	const bound = defaultMaxIdentities
	for _, tc := range []struct {
		name     string
		stations int
		limit    int
		want     int
		full     bool
	}{
		{"none", 0, 0, 0, false},
		{"one", 1, 0, 1, false},
		{"bound-1", bound - 1, 0, bound - 1, false},
		{"bound", bound, 0, bound, false},
		{"bound+1", bound + 1, 0, bound, true},
		{"bound+100", bound + 100, 0, bound, true},
		{"Max 1", 3, 1, 1, false},
		{"Max below the bound", bound, bound - 1, bound - 1, false},
		{"Max at the bound", bound + 5, bound, bound, false},
		{"Max above the bound", bound + 100, bound + 50, bound + 50, false},
		{"Max above the stations", bound + 100, 2 * bound, bound + 100, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{log: profinet.NopLogger(), done: make(chan struct{})}
			ids, taken, err := collectFrom(t, c, responses(tc.stations), tc.limit, bound)
			if len(ids) != tc.want {
				t.Errorf("%d stations returned, want %d", len(ids), tc.want)
			}
			if tc.full != errors.Is(err, profinet.ErrResourceExhausted) || (!tc.full && err != nil) {
				t.Errorf("err %v, want ErrResourceExhausted: %v", err, tc.full)
			}
			// It returns with the first station it has no room for.
			if tc.full && taken > bound+1 {
				t.Errorf("%d responses taken before it returned, want %d", taken, bound+1)
			}
		})
	}
}

// TestIdentifyBoundCounts: only stations count against the bound: a
// station that answers again and a response that is no identity do not.
func TestIdentifyBoundCounts(t *testing.T) {
	const bound = 8
	rs := responses(bound)
	var mixed []response
	for i, r := range rs {
		mixed = append(mixed, r, r) // every station answers twice
		_, bad := stationN(1000+i, 1)
		bad.Blocks = []Block{{ID: DeviceID, HasInfo: true, Data: []byte{1}}}
		mixed = append(mixed, response{src: ethernet.MAC{2, 0, 2, 0, 0, byte(i)}, frame: bad})
		other := r
		other.frame.Service = ServiceGet // not an Identify response
		other.src = ethernet.MAC{2, 0, 3, 0, 0, byte(i)}
		mixed = append(mixed, other)
	}
	c := &Client{log: profinet.NopLogger(), done: make(chan struct{})}
	ids, taken, err := collectFrom(t, c, mixed, 0, bound)
	if err != nil || len(ids) != bound || taken != len(mixed) {
		t.Fatalf("%d stations, err %v, %d of %d responses taken", len(ids), err, taken, len(mixed))
	}
	if c.Malformed() != bound {
		t.Fatalf("%d malformed responses counted, want %d", c.Malformed(), bound)
	}
	// One more station is one too many.
	extra := responses(bound + 1)[bound:]
	ids, _, err = collectFrom(t, c, append(mixed, extra...), 0, bound)
	if !errors.Is(err, profinet.ErrResourceExhausted) || len(ids) != bound {
		t.Fatalf("%d stations, err %v", len(ids), err)
	}
}

// TestIdentifyBoundAllocation: what an Identify without a Max allocates
// follows the bound, not the number of responses.
func TestIdentifyBoundAllocation(t *testing.T) {
	const bound = defaultMaxIdentities
	rs := responses(8 * bound)
	c := &Client{log: profinet.NopLogger(), done: make(chan struct{})}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	ids, _, err := collectFrom(t, c, rs, 0, bound)
	runtime.ReadMemStats(&after)
	if !errors.Is(err, profinet.ErrResourceExhausted) || len(ids) != bound {
		t.Fatalf("%d stations, err %v", len(ids), err)
	}
	// An identity of this test takes about 300 octets with its share of
	// the result slice and of the table of sources.
	if got, budget := after.TotalAlloc-before.TotalAlloc, uint64(bound)*1024; got > budget {
		t.Fatalf("%d octets allocated for %d responses, budget %d", got, len(rs), budget)
	}
}

// TestIdentifyBoundOnTheWire: the same through a client on a network:
// stations keep answering until Identify returns.
func TestIdentifyBoundOnTheWire(t *testing.T) {
	_, c, raw := newClientEnv(t, ClientOptions{})
	type result struct {
		ids []Identity
		err error
	}
	res := make(chan result, 1)
	go func() {
		ids, err := c.Identify(context.Background(), nil, IdentifyOptions{Wait: time.Minute})
		res <- result{ids, err}
	}()
	_, req, ok := raw.recv(t, 10*time.Second)
	if !ok || req.Service != ServiceIdentify {
		t.Fatalf("no Identify request: %+v", req)
	}
	var got result
	deadline := time.Now().Add(15 * time.Second)
	// Every response has a new source address, so one that a full queue
	// drops only makes the next one count.
send:
	for n := 0; ; n++ {
		mac, f := stationN(n, req.Xid)
		p, err := f.AppendTo(nil)
		if err != nil {
			t.Fatal(err)
		}
		hdr := ethernet.Frame{Dst: clientMAC, Src: mac, EtherType: ethernet.EtherTypeProfinet}
		if err := raw.mux.Send(context.Background(), ethernet.Pad(append(hdr.AppendHeader(nil), p...), 0)); err != nil {
			t.Fatal(err)
		}
		select {
		case got = <-res:
			break send
		default:
		}
		if n%64 == 63 {
			runtime.Gosched()
			if time.Now().After(deadline) {
				t.Fatalf("Identify still collects after %d responses from as many stations", n+1)
			}
		}
	}
	if !errors.Is(got.err, profinet.ErrResourceExhausted) || len(got.ids) != defaultMaxIdentities {
		t.Fatalf("%d stations, err %v; want %d and ErrResourceExhausted", len(got.ids), got.err, defaultMaxIdentities)
	}
	// A Max above the bound takes more.
	go func() {
		ids, err := c.Identify(context.Background(), nil, IdentifyOptions{Wait: time.Minute, Max: defaultMaxIdentities + 3})
		res <- result{ids, err}
	}()
	_, req, ok = raw.recv(t, 10*time.Second)
	if !ok || req.Service != ServiceIdentify {
		t.Fatalf("no Identify request: %+v", req)
	}
	deadline = time.Now().Add(15 * time.Second)
more:
	for n := 0; ; n++ {
		mac, f := stationN(n, req.Xid)
		p, _ := f.AppendTo(nil)
		hdr := ethernet.Frame{Dst: clientMAC, Src: mac, EtherType: ethernet.EtherTypeProfinet}
		if err := raw.mux.Send(context.Background(), ethernet.Pad(append(hdr.AppendHeader(nil), p...), 0)); err != nil {
			t.Fatal(err)
		}
		select {
		case got = <-res:
			break more
		default:
		}
		if n%64 == 63 {
			runtime.Gosched()
			if time.Now().After(deadline) {
				t.Fatalf("Identify still collects after %d responses", n+1)
			}
		}
	}
	if got.err != nil || len(got.ids) != defaultMaxIdentities+3 {
		t.Fatalf("with a Max: %d stations, err %v", len(got.ids), got.err)
	}
}
