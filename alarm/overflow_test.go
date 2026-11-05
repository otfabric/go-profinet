// SPDX-License-Identifier: MIT

package alarm

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/pnio"
)

// A VarPart longer than the 16-bit VarPartLen used to be encoded with a
// wrapped length. AppendPayload now panics, and Send refuses it before
// using a sequence number.
func TestOversizedVarPart(t *testing.T) {
	p := PDU{FrameID: pnio.FrameIDAlarmHigh, Type: PDUData, VarPart: make([]byte, math.MaxUint16)}
	if got, err := Decode(p.AppendPayload(nil)); err != nil || len(got.VarPart) != math.MaxUint16 {
		t.Fatalf("largest VarPart: %v", err)
	}
	func() {
		defer func() {
			err, ok := recover().(error)
			if !ok || !errors.Is(err, profinet.ErrInvalidConfig) {
				t.Fatalf("recovered %v", err)
			}
		}()
		p.VarPart = make([]byte, math.MaxUint16+1)
		p.AppendPayload(nil)
	}()

	_, b, ea, _ := pair(t, time.Second, 3)
	ctx := context.Background()
	if err := ea.Send(ctx, make([]byte, math.MaxUint16+1)); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("send: err %v", err)
	}
	// The refused payload used no sequence number: the next is delivered.
	if err := ea.Send(ctx, []byte("next")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "delivery", func() bool { return len(b.received()) == 1 })
	if s := ea.Stats(); s.Sent != 1 || s.Acks != 1 {
		t.Fatalf("stats %+v", s)
	}
}
