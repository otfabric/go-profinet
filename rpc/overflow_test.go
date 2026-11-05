// SPDX-License-Identifier: MIT

package rpc

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/otfabric/go-profinet"
)

// A body that needs more fragments than the 16-bit fragment number counts
// is refused; it used to be sent with fragment numbers that wrapped to 0.
func TestPacketsTooManyFragments(t *testing.T) {
	_, err := packets(Header{}, make([]byte, math.MaxUint16+2), 1)
	if !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("err %v", err)
	}
	ps, err := packets(Header{}, make([]byte, math.MaxUint16+1), 1)
	if err != nil {
		t.Fatal(err)
	}
	h, _, err := DecodeHeader(ps[len(ps)-1])
	if err != nil || h.FragNum != math.MaxUint16 || h.Flags1&FlagLastFrag == 0 {
		t.Fatalf("last fragment %+v %v", h, err)
	}
}

// A fragment size above the 16-bit fragment length is reduced to the
// largest UDP datagram; it used to be sent with a wrapped BodyLen.
func TestPacketsFragmentBodyLimit(t *testing.T) {
	ps, err := packets(Header{}, make([]byte, 70000), 70000)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("%d packets", len(ps))
	}
	for i, p := range ps {
		h, body, err := DecodeHeader(p)
		if err != nil || int(h.BodyLen) != len(p)-HeaderLen || len(body) > maxFragmentBodyLimit {
			t.Fatalf("packet %d: %+v %v", i, h, err)
		}
	}
	if fragmentBody(0) != DefaultMaxFragmentBody || fragmentBody(1<<20) != maxFragmentBodyLimit || fragmentBody(100) != 100 {
		t.Fatal("fragmentBody")
	}
	c := NewClient(listen(t), true, ClientOptions{MaxFragmentBody: 1 << 20})
	defer func() { _ = c.Close() }()
	if c.opts.MaxFragmentBody != maxFragmentBodyLimit {
		t.Fatalf("client MaxFragmentBody %d", c.opts.MaxFragmentBody)
	}
}

func TestBodyLen(t *testing.T) {
	if bodyLen(5) != 5 || bodyLen(-1) != math.MaxUint16 || bodyLen(math.MaxUint16+1) != math.MaxUint16 {
		t.Fatal("bodyLen")
	}
}

func TestBootTime(t *testing.T) {
	if got := bootTime(time.Unix(1<<32+5, 0)); got != 5 {
		t.Fatalf("boot %d", got)
	}
	if got := bootTime(time.Unix(1700000000, 0)); got != 1700000000 {
		t.Fatalf("boot %d", got)
	}
}

// A call whose body cannot be fragmented fails before anything is sent;
// a handler response that cannot be fragmented is answered with a FAULT.
func TestOversizedBodies(t *testing.T) {
	srv := NewServer(listen(t), true, ServerOptions{MaxFragmentBody: 1})
	srv.Register(testIface, 1, HandlerFunc(func(context.Context, *Request) ([]byte, error) {
		return make([]byte, math.MaxUint16+2), nil
	}))
	t.Cleanup(func() { _ = srv.Close() })
	c := NewClient(listen(t), true, ClientOptions{Timeout: 2 * time.Second, MaxFragmentBody: 1})
	defer func() { _ = c.Close() }()
	ctx := context.Background()
	_, err := c.Do(ctx, srv.LocalAddr(), Call{Iface: testIface, IfaceVersion: 1, Body: make([]byte, math.MaxUint16+2)})
	if !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("call: err %v", err)
	}
	_, err = c.Do(ctx, srv.LocalAddr(), Call{Iface: testIface, IfaceVersion: 1})
	var f *Fault
	if !errors.As(err, &f) || f.Status != StatusFaultUnspec || f.Reject {
		t.Fatalf("response: err %v", err)
	}
}
