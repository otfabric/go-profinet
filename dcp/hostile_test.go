// SPDX-License-Identifier: MIT

package dcp

import (
	"testing"
	"time"

	"github.com/otfabric/go-profinet/ethernet"
)

// TestResponderHostileRequests: requests a hostile tool can fit in one
// frame whose answer would not fit one (a Get of hundreds of
// AllSelectors), Sets of hundreds of blocks, and Identify filters of
// hundreds of blocks. The responder answers what fits, sends nothing
// larger than a frame, and keeps answering afterwards.
func TestResponderHostileRequests(t *testing.T) {
	st := testStation()
	r, raw, _ := responderEnv(t, st, ResponderOptions{NoResponseDelay: true}, devMAC, 0)
	many := func(n int, b Block) []Block {
		out := make([]Block, n)
		for i := range out {
			out[i] = b
		}
		return out
	}
	// 1490 octets of DCP data: hundreds of 2-octet selectors.
	raw.send(t, devMAC, &Frame{FrameID: FrameIDGetSet, Service: ServiceGet, Type: TypeRequest, Xid: 1, Blocks: many(MaxDataLen/2-8, Block{ID: AllSelector})})
	if _, f, ok := raw.recv(t, 300*time.Millisecond); ok {
		if b, err := f.AppendTo(nil); err != nil || len(b) > MaxDataLen+10 {
			t.Fatalf("answer of %d octets: %v", len(b), err)
		}
	}
	// Sets of the same harmless block over and over, and of a name.
	raw.send(t, devMAC, &Frame{FrameID: FrameIDGetSet, Service: ServiceSet, Type: TypeRequest, Xid: 2, Blocks: many(240, Block{ID: CtrlStart, HasInfo: true})})
	raw.recv(t, 300*time.Millisecond)
	raw.send(t, devMAC, &Frame{FrameID: FrameIDGetSet, Service: ServiceSet, Type: TypeRequest, Xid: 3, Blocks: many(100, Block{ID: DeviceName, HasInfo: true, Data: []byte("x1")})})
	raw.recv(t, 300*time.Millisecond)
	// An Identify whose filter is hundreds of blocks.
	raw.send(t, ethernet.DCPIdentifyMulticast, &Frame{FrameID: FrameIDIdentifyRequest, Service: ServiceIdentify, Type: TypeRequest, Xid: 4, ResponseDelay: 1, Blocks: many(240, Block{ID: DeviceName, Data: []byte("x1")})})
	raw.recv(t, 300*time.Millisecond)
	// The responder still answers a normal Identify.
	raw.send(t, ethernet.DCPIdentifyMulticast, &Frame{FrameID: FrameIDIdentifyRequest, Service: ServiceIdentify, Type: TypeRequest, Xid: 5, ResponseDelay: 1, Blocks: FilterAll()})
	for {
		_, f, ok := raw.recv(t, 2*time.Second)
		if !ok {
			t.Fatalf("no answer after the hostile requests; stats %+v", r.Stats())
		}
		if f.Xid == 5 && f.FrameID == FrameIDIdentifyResponse {
			break
		}
	}
}
