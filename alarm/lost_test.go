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

// TestSendRetransmissionsLost: a PDU that went out once and whose
// retransmissions then fail to send times out like an unacknowledged
// one, with the last send error in its message.
func TestSendRetransmissionsLost(t *testing.T) {
	a, b, ea, _ := pair(t, 30*time.Millisecond, 3)
	b.ep.SetLinkUp(false) // the peer never sees nor acknowledges it
	errc := make(chan error, 1)
	go func() { errc <- ea.Send(context.Background(), []byte("x")) }()
	waitFor(t, "first transmission", func() bool { return ea.sent.Load() == 1 })
	a.ep.SetLinkUp(false) // the retransmissions fail to send
	select {
	case err := <-errc:
		if !errors.Is(err, profinet.ErrTimeout) || !strings.Contains(err.Error(), "last send") {
			t.Fatalf("Send: %v, want a timeout naming the last send error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send did not end")
	}
}
