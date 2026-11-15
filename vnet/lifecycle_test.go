// SPDX-License-Identifier: MIT

package vnet

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/internal/leakcheck"
)

// TestCloseConcurrent: Close of a network and of its endpoints from
// several goroutines at once (contract row L2): all return, a blocked
// read ends and nothing is left running.
func TestCloseConcurrent(t *testing.T) {
	check := leakcheck.Check(t)
	n := New(1)
	a := newEP(t, n, EndpointConfig{MAC: ethernet.MAC{2, 0, 0, 0, 0, 1}})
	b := newEP(t, n, EndpointConfig{MAC: ethernet.MAC{2, 0, 0, 0, 0, 2}})
	// A delayed frame starts the goroutine of a's line.
	a.SetFaults(Faults{Delay: time.Millisecond})
	if err := a.WriteFrame(context.Background(), (&ethernet.Frame{Dst: b.HardwareAddr(), Src: a.HardwareAddr(), EtherType: 0x88B5, Payload: []byte{1}}).Encode()); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ReadFrame(context.Background(), make([]byte, 64)); err != nil {
		t.Fatal(err)
	}
	read := make(chan error, 1)
	go func() {
		_, err := b.ReadFrame(context.Background(), make([]byte, 64))
		read <- err
	}()
	leakcheck.Together(12, func(i int) {
		var err error
		switch i % 3 {
		case 0:
			err = n.Close()
		case 1:
			err = a.Close()
		default:
			err = b.Close()
		}
		if err != nil {
			t.Error(err)
		}
	})
	if err := <-read; !errors.Is(err, profinet.ErrClosed) {
		t.Fatalf("ReadFrame: %v", err)
	}
	f := (&ethernet.Frame{Dst: ethernet.Broadcast, Src: a.HardwareAddr(), EtherType: 0x88B5, Payload: []byte{1}}).Encode()
	if err := a.WriteFrame(context.Background(), f); !errors.Is(err, profinet.ErrClosed) {
		t.Fatalf("WriteFrame after Close: %v", err)
	}
	if _, err := n.NewEndpoint(EndpointConfig{MAC: ethernet.MAC{2, 0, 0, 0, 0, 3}}); !errors.Is(err, profinet.ErrClosed) {
		t.Fatalf("NewEndpoint after Close: %v", err)
	}
	check()
}
