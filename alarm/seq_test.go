// SPDX-License-Identifier: MIT

package alarm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/otfabric/go-profinet"
)

// A DATA PDU whose first transmission failed never reached the peer: its
// sequence number is used again, and the next Send is accepted. Before,
// the number was skipped, the peer refused every later PDU as out of
// sequence and the next Send timed out (the owner then aborted the AR).
func TestSendAfterTransmitFailure(t *testing.T) {
	a, b, ea, _ := pair(t, 20*time.Millisecond, 2)
	a.ep.SetLinkUp(false)
	if err := ea.Send(context.Background(), []byte("x")); !errors.Is(err, profinet.ErrLinkDown) {
		t.Fatalf("send on a down link: %v", err)
	}
	a.ep.SetLinkUp(true)
	if err := ea.Send(context.Background(), []byte("y")); err != nil {
		t.Fatalf("after link up: %v", err)
	}
	if err := ea.Send(context.Background(), []byte("z")); err != nil {
		t.Fatalf("next: %v", err)
	}
	waitFor(t, "y and z", func() bool { return len(b.received()) == 2 })
	if got := b.received(); string(got[0]) != "y" || string(got[1]) != "z" {
		t.Fatalf("received %q", got)
	}
}

// A Send cancelled while its PDU awaits the acknowledgement leaves the PDU
// pending: the next Send retransmits it first, so the peer, which may have
// accepted it, stays in sequence; here the cancelled PDU was lost, and the
// peer receives both in order.
func TestSendAfterCancelledSend(t *testing.T) {
	_, b, ea, _ := pair(t, 20*time.Millisecond, 2)
	b.ep.SetLinkUp(false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := ea.Send(ctx, []byte("x")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled send: %v", err)
	}
	b.ep.SetLinkUp(true)
	if err := ea.Send(context.Background(), []byte("y")); err != nil {
		t.Fatalf("after the cancel: %v", err)
	}
	waitFor(t, "x and y", func() bool { return len(b.received()) == 2 })
	if got := b.received(); string(got[0]) != "x" || string(got[1]) != "y" {
		t.Fatalf("received %q", got)
	}
	if s := ea.Stats(); s.Retransmits == 0 {
		t.Fatalf("the pending PDU was not retransmitted: %+v", s)
	}
}

// A pending PDU the peer never acknowledges fails the next Send with a
// timeout, and is dropped: the owner aborts the AR.
func TestSendPendingTimeout(t *testing.T) {
	_, b, ea, _ := pair(t, 10*time.Millisecond, 1)
	b.ep.SetLinkUp(false)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Millisecond)
	defer cancel()
	_ = ea.Send(ctx, []byte("x"))
	if err := ea.Send(context.Background(), []byte("y")); !errors.Is(err, profinet.ErrTimeout) {
		t.Fatalf("pending PDU without ACK: %v", err)
	}
	ea.mu.Lock()
	pending := ea.pending
	ea.mu.Unlock()
	if pending != nil {
		t.Fatal("PDU still pending after the timeout")
	}
}

// A pending PDU owns its payload: the caller may reuse its buffer once
// Send returned, and the retransmission still carries what was sent.
// Before, the pending PDU aliased the caller's buffer.
func TestSendPendingOwnsPayload(t *testing.T) {
	_, b, ea, _ := pair(t, 20*time.Millisecond, 2)
	b.ep.SetLinkUp(false)
	buf := []byte("abc")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := ea.Send(ctx, buf); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled send: %v", err)
	}
	copy(buf, "ZZZ")
	b.ep.SetLinkUp(true)
	if err := ea.Send(context.Background(), []byte("y")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "both", func() bool { return len(b.received()) == 2 })
	if got := b.received(); string(got[0]) != "abc" || string(got[1]) != "y" {
		t.Fatalf("received %q", got)
	}
}

// After a DATA PDU was never acknowledged, whether as a new PDU or as a
// pending one retransmitted, the endpoint sends no DATA PDU again: every
// later Send fails at once with ErrInvalidState, without a frame. Before,
// it sent with sequence numbers the peer might not share.
func TestSendAfterTimeoutIsTerminal(t *testing.T) {
	for _, pendingFirst := range []bool{false, true} {
		_, b, ea, _ := pair(t, 10*time.Millisecond, 1)
		b.ep.SetLinkUp(false)
		if pendingFirst {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Millisecond)
			_ = ea.Send(ctx, []byte("x"))
			cancel()
		}
		if err := ea.Send(context.Background(), []byte("y")); !errors.Is(err, profinet.ErrTimeout) {
			t.Fatalf("pending %v: %v", pendingFirst, err)
		}
		b.ep.SetLinkUp(true)
		sent := ea.Stats().Sent
		for range 2 {
			if err := ea.Send(context.Background(), []byte("z")); !errors.Is(err, profinet.ErrInvalidState) {
				t.Fatalf("pending %v: send after the timeout: %v", pendingFirst, err)
			}
		}
		if ea.Stats().Sent != sent || len(b.received()) != 0 {
			t.Fatalf("pending %v: sent %d → %d, received %q", pendingFirst, sent, ea.Stats().Sent, b.received())
		}
	}
}
