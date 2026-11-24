// SPDX-License-Identifier: MIT

package alarm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-profinet"
)

func doneWithin(t *testing.T, tr *Transfer, d time.Duration) error {
	t.Helper()
	select {
	case <-tr.Done():
		return tr.Err()
	case <-time.After(d):
		t.Fatal("the transfer did not end")
		return nil
	}
}

// A transfer started and acknowledged delivers its PDU once; the next
// Start follows it in sequence.
func TestStartAcknowledged(t *testing.T) {
	_, b, ea, _ := pair(t, time.Second, 3)
	tr, err := ea.Start(context.Background(), []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	if err := doneWithin(t, tr, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	tr2, err := ea.Start(context.Background(), []byte("two"))
	if err != nil {
		t.Fatal(err)
	}
	if err := doneWithin(t, tr2, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := b.received(); len(got) != 2 || string(got[0]) != "one" || string(got[1]) != "two" {
		t.Fatalf("received %q", got)
	}
	if s := ea.Stats(); s.Sent != 2 || s.Retransmits != 0 {
		t.Fatalf("stats %+v", s)
	}
}

// The context of a Start ends while the peer has not acknowledged the
// PDU: the transfer keeps retransmitting it, the peer receives it once,
// and the next PDU follows in sequence. With Send, the PDU would have
// been left pending and retransmitted only by the next Send.
func TestStartOutlivesContext(t *testing.T) {
	_, b, ea, _ := pair(t, 20*time.Millisecond, 10)
	b.ep.SetLinkUp(false) // the peer sees nothing yet
	ctx, cancel := context.WithCancel(context.Background())
	tr, err := ea.Start(ctx, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	time.Sleep(30 * time.Millisecond)
	if tr.Err() != nil {
		t.Fatalf("Err before Done: %v", tr.Err())
	}
	b.ep.SetLinkUp(true)
	if err := doneWithin(t, tr, 5*time.Second); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	ea.mu.Lock()
	pending := ea.pending
	ea.mu.Unlock()
	if pending != nil {
		t.Fatal("a started PDU was left pending")
	}
	if err := ea.Send(context.Background(), []byte("y")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "x and y", func() bool { return len(b.received()) == 2 })
	if got := b.received(); string(got[0]) != "x" || string(got[1]) != "y" {
		t.Fatalf("received %q", got)
	}
}

// A Start whose context ends before its PDU could go out (the PDU before
// it is still in transfer) sends nothing and uses no sequence number.
func TestStartWaitsForSlot(t *testing.T) {
	_, b, ea, _ := pair(t, 20*time.Millisecond, 10)
	b.ep.SetLinkUp(false)
	tr, err := ea.Start(context.Background(), []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := ea.Start(ctx, []byte("never")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start behind a transfer: %v", err)
	}
	if err := ea.Send(ctx, []byte("never")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Send behind a transfer: %v", err)
	}
	b.ep.SetLinkUp(true)
	if err := doneWithin(t, tr, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	tr2, err := ea.Start(context.Background(), []byte("y"))
	if err != nil {
		t.Fatal(err)
	}
	if err := doneWithin(t, tr2, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := b.received(); len(got) != 2 || string(got[0]) != "x" || string(got[1]) != "y" {
		t.Fatalf("received %q", got)
	}
}

// A first transmission that fails gives the sequence number back: no
// transfer, and the next Start is accepted in sequence.
func TestStartTransmitFailure(t *testing.T) {
	a, b, ea, _ := pair(t, 20*time.Millisecond, 2)
	a.ep.SetLinkUp(false)
	if tr, err := ea.Start(context.Background(), []byte("x")); !errors.Is(err, profinet.ErrLinkDown) || tr != nil {
		t.Fatalf("Start on a down link: %v %v", tr, err)
	}
	a.ep.SetLinkUp(true)
	tr, err := ea.Start(context.Background(), []byte("y"))
	if err != nil {
		t.Fatal(err)
	}
	if err := doneWithin(t, tr, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := b.received(); len(got) != 1 || string(got[0]) != "y" {
		t.Fatalf("received %q", got)
	}
}

// A transfer the peer never acknowledges times out, and is terminal like
// a timed-out Send.
func TestStartTimeout(t *testing.T) {
	_, b, ea, _ := pair(t, 10*time.Millisecond, 2)
	b.ep.SetLinkUp(false)
	tr, err := ea.Start(context.Background(), []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if err := doneWithin(t, tr, 5*time.Second); !errors.Is(err, profinet.ErrTimeout) {
		t.Fatalf("transfer: %v", err)
	}
	if s := ea.Stats(); s.Retransmits != 2 {
		t.Fatalf("stats %+v", s)
	}
	if _, err := ea.Start(context.Background(), []byte("y")); !errors.Is(err, profinet.ErrInvalidState) {
		t.Fatalf("Start after a timeout: %v", err)
	}
}

// Close ends a transfer with ErrClosed and waits for it; a Start after
// Close fails.
func TestStartClose(t *testing.T) {
	_, b, ea, _ := pair(t, time.Second, 3)
	b.ep.SetLinkUp(false)
	tr, err := ea.Start(context.Background(), []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	ea.Close()
	select {
	case <-tr.Done():
	default:
		t.Fatal("Close returned before the transfer ended")
	}
	if err := tr.Err(); !errors.Is(err, profinet.ErrClosed) {
		t.Fatalf("transfer: %v", err)
	}
	if _, err := ea.Start(context.Background(), []byte("y")); !errors.Is(err, profinet.ErrClosed) {
		t.Fatalf("Start after Close: %v", err)
	}
	if err := ea.Send(context.Background(), []byte("y")); !errors.Is(err, profinet.ErrClosed) {
		t.Fatalf("Send after Close: %v", err)
	}
}

// Start retransmits a PDU an earlier Send left pending before its own,
// and gives up when its context ends first: its own PDU never leaves.
func TestStartAfterPendingSend(t *testing.T) {
	_, b, ea, _ := pair(t, 20*time.Millisecond, 50)
	b.ep.SetLinkUp(false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := ea.Send(ctx, []byte("x")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled send: %v", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel2()
	if _, err := ea.Start(ctx2, []byte("never")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start behind a pending PDU: %v", err)
	}
	b.ep.SetLinkUp(true)
	tr, err := ea.Start(context.Background(), []byte("y"))
	if err != nil {
		t.Fatal(err)
	}
	if err := doneWithin(t, tr, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := b.received(); len(got) != 2 || string(got[0]) != "x" || string(got[1]) != "y" {
		t.Fatalf("received %q", got)
	}
}

// Start refuses a VarPart that does not fit, before taking a number.
func TestStartTooLong(t *testing.T) {
	_, _, ea, _ := pair(t, time.Second, 3)
	if _, err := ea.Start(context.Background(), make([]byte, 70000)); !errors.Is(err, profinet.ErrInvalidConfig) || !strings.Contains(err.Error(), "70000") {
		t.Fatalf("Start: %v", err)
	}
}
