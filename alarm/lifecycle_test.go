// SPDX-License-Identifier: MIT

package alarm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/internal/leakcheck"
)

// TestCloseConcurrent: Close of an endpoint and of its manager from
// several goroutines at once (contract row L2).
func TestCloseConcurrent(t *testing.T) {
	a, _, ea, _ := pair(t, time.Second, 3)
	leakcheck.Together(12, func(i int) {
		if i%3 == 0 {
			a.mgr.Close()
			return
		}
		ea.Close()
	})
	if err := ea.Send(context.Background(), []byte{1}); !errors.Is(err, profinet.ErrClosed) {
		t.Fatalf("Send after Close: %v", err)
	}
	select {
	case <-a.mgr.done:
	default:
		t.Fatal("the manager routes after Close")
	}
}

// TestOnDataMaySend: OnData runs on the endpoint's goroutine and may use
// the endpoint (contract row L9): it answers every PDU with one of its
// own.
func TestOnDataMaySend(t *testing.T) {
	a, b, _, _ := pair(t, time.Second, 3)
	answers := make(chan error, 4)
	var eb2 *Endpoint
	ready := make(chan struct{})
	eb2 = b.open(t, EndpointConfig{LocalRef: 12, RemoteRef: 11, RemoteMAC: a.mac, Timeout: time.Second, Retries: 3, OnData: func(v []byte) {
		<-ready
		stats := eb2.Stats()
		if stats.Received == 0 {
			answers <- errors.New("Stats from OnData shows nothing received")
			return
		}
		answers <- eb2.Send(context.Background(), append([]byte("re:"), v...))
	}})
	close(ready)
	ea2 := a.open(t, EndpointConfig{LocalRef: 11, RemoteRef: 12, RemoteMAC: b.mac, Timeout: time.Second, Retries: 3})
	if err := ea2.Send(context.Background(), []byte("ping")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-answers:
		if err != nil {
			t.Fatalf("Send from OnData: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("OnData did not return from Send")
	}
	waitFor(t, "the answer", func() bool {
		got := a.received()
		return len(got) == 1 && string(got[0]) == "re:ping"
	})
}
