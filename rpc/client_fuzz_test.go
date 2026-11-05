// SPDX-License-Identifier: MIT

package rpc

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// splitPackets cuts b into packets, each preceded by a 16-bit big-endian
// length; at most 32.
func splitPackets(b []byte) [][]byte {
	var out [][]byte
	for len(b) >= 2 && len(out) < 32 {
		n := int(binary.BigEndian.Uint16(b))
		b = b[2:]
		n = min(n, len(b))
		out = append(out, b[:n])
		b = b[n:]
	}
	return out
}

func joinPackets(pkts ...[]byte) []byte {
	var b []byte
	for _, p := range pkts {
		b = binary.BigEndian.AppendUint16(b, uint16(len(p)))
		b = append(b, p...)
	}
	return b
}

// FuzzClient answers a call of the client with arbitrary packets from a
// hostile server: each one that decodes as a header gets the call's
// activity and sequence number, so that it reaches the call. Invariants:
// no panic; the call ends by the deadline; a response it returns is
// within MaxMessage; the client serves the next call normally.
func FuzzClient(f *testing.F) {
	le := [3]byte{DRepLittleEndian}
	pkt := func(h Header, body []byte) []byte {
		h.DRep = le
		h.BodyLen = uint16(len(body))
		return append(h.AppendTo(nil), body...)
	}
	f.Add(joinPackets(pkt(Header{Type: TypeResponse}, []byte("ok"))))
	f.Add(joinPackets(pkt(Header{Type: TypeResponse, Flags1: FlagFrag}, []byte("a")), pkt(Header{Type: TypeResponse, Flags1: FlagFrag | FlagLastFrag, FragNum: 1}, []byte("b"))))
	f.Add(joinPackets(pkt(Header{Type: TypeResponse, Flags1: FlagFrag | FlagLastFrag, FragNum: 255}, []byte("z")), pkt(Header{Type: TypeResponse, Flags1: FlagFrag, FragNum: 300}, nil)))
	f.Add(joinPackets(pkt(Header{Type: TypeWorking}, nil), pkt(Header{Type: TypeNoCall}, nil), pkt(Header{Type: TypeFault}, []byte{1, 0, 0, 0})))
	f.Add(joinPackets(pkt(Header{Type: TypeReject}, nil), pkt(Header{Type: TypeFack}, nil)))
	f.Add(joinPackets(pkt(Header{Type: TypeResponse, BodyLen: 0xFFFF}, nil)))
	f.Add([]byte{0, 3, 4, 0, 0})

	const maxMessage = 4096
	n := newMemNet()
	cc := n.listen("client")
	srv := n.listen("server")
	c := NewClient(cc, true, ClientOptions{Retransmit: 50 * time.Millisecond, MaxMessage: maxMessage, Activities: 1})
	f.Cleanup(func() {
		_ = c.Close()
		_ = srv.Close()
	})
	call := Call{Iface: testIface, IfaceVersion: 1, Body: []byte("req")}
	f.Fuzz(func(t *testing.T, b []byte) {
		type result struct {
			r   *Response
			err error
		}
		res := make(chan result, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			r, err := c.Do(ctx, srv.LocalAddr(), call)
			res <- result{r, err}
		}()
		var h Header
		var from net.Addr
		for {
			var ok bool
			if h, _, from, ok = srv.recv(2 * time.Second); !ok {
				t.Fatal("no request")
			}
			// Skip the ACK of the previous input's last call.
			if h.Type == TypeRequest {
				break
			}
		}
		for _, p := range splitPackets(b) {
			if rh, _, err := DecodeHeader(p); err == nil {
				rh.Activity, rh.SeqNum = h.Activity, h.SeqNum
				p = append(rh.AppendTo(nil), p[HeaderLen:]...)
			}
			_, _ = srv.WriteTo(p, from)
		}
		// End a call the packets left open.
		srv.sendPkt(from, Header{Type: TypeReject, Activity: h.Activity, SeqNum: h.SeqNum}, []byte{1, 0, 0, 0})
		var r result
		select {
		case r = <-res:
		case <-time.After(5 * time.Second):
			t.Fatal("call did not end")
		}
		if r.err == nil && len(r.r.Body) > maxMessage {
			t.Fatalf("response of %d octets, MaxMessage %d", len(r.r.Body), maxMessage)
		}
		// Drain what the client sent meanwhile (retransmissions, ACKs),
		// then check that a well-behaved server is served.
		for {
			if _, _, _, ok := srv.recv(time.Millisecond); !ok {
				break
			}
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			r, err := c.Do(ctx, srv.LocalAddr(), call)
			res <- result{r, err}
		}()
		for {
			h2, _, from2, ok := srv.recv(2 * time.Second)
			if !ok {
				t.Fatal("no second request")
			}
			if h2.Type != TypeRequest || h2.SeqNum == h.SeqNum {
				continue
			}
			srv.sendPkt(from2, Header{Type: TypeResponse, Activity: h2.Activity, SeqNum: h2.SeqNum}, []byte("ok"))
			break
		}
		if r := <-res; r.err != nil || string(r.r.Body) != "ok" {
			t.Fatalf("second call: %v", r.err)
		}
	})
}
