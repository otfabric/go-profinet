// SPDX-License-Identifier: MIT

package rpc

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

// A newer call on an activity whose call still executes is refused, so one
// peer cannot queue handler goroutines beyond MaxActivities.
func TestNewerCallOnExecutingActivityRefused(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	s, sc, peer := memServer(t, ServerOptions{}, func(ctx context.Context, r *Request) ([]byte, error) {
		<-release
		return nil, nil
	})
	defer once.Do(func() { close(release) })
	base := runtime.NumGoroutine()
	act := MustParseUUID("11111111-2222-3333-4444-555555555555")
	const n = 200
	body := make([]byte, 64)
	for i := 1; i <= n; i++ {
		h := reqHeader(act, uint32(i))
		h.BodyLen = uint16(len(body))
		if _, err := peer.WriteTo(append(h.AppendTo(nil), body...), sc.LocalAddr()); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && s.Stats().Requests+s.Stats().Busy < n {
		time.Sleep(10 * time.Millisecond)
	}
	st := s.Stats()
	if st.Requests != 1 || st.Busy != n-1 {
		t.Fatalf("requests=%d busy=%d, want 1 and %d", st.Requests, st.Busy, n-1)
	}
	if extra := runtime.NumGoroutine() - base; extra > s.opts.MaxActivities {
		t.Fatalf("%d extra goroutines, want at most MaxActivities (%d)", extra, s.opts.MaxActivities)
	}
}

// A packet for a call in progress from another host than the call's is
// dropped: only the called host answers it.
func TestClientDropsOtherHost(t *testing.T) {
	c, _, peer := clientEnv(t, ClientOptions{Retransmit: time.Minute})
	other := peer.net.listen("other")
	defer func() { _ = other.Close() }()
	ch := doAsync(c, context.Background(), peer.LocalAddr(), Call{Iface: testIface, IfaceVersion: 1})
	req, _, from := expect(t, peer, TypeRequest)
	fault := respond(req, TypeFault)
	other.sendPkt(from, fault, []byte{0, 0, 0, 0})
	time.Sleep(50 * time.Millisecond)
	select {
	case r := <-ch:
		t.Fatalf("call ended by another host: %+v %v", r.r, r.err)
	default:
	}
	if c.Foreign() != 1 {
		t.Fatalf("foreign %d", c.Foreign())
	}
	peer.sendPkt(from, respond(req, TypeResponse), []byte("ok"))
	if r := wait(t, ch); r.err != nil || string(r.r.Body) != "ok" {
		t.Fatalf("result %+v %v", r.r, r.err)
	}
}
