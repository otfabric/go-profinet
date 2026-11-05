// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"testing"

	"github.com/otfabric/go-profinet/pnio"
	"github.com/otfabric/go-profinet/rpc"
)

// TestReadMaxLenBoundsData: Read documents "maxLen bounds the data"; a
// device that answers with more than maxLen octets must not get them
// through to the caller.
func TestReadMaxLenBoundsData(t *testing.T) {
	n := newNet(t)
	c, _ := n.controller()
	f := n.fake("fake")
	rc := NewRecordClient(c.RPC(), devAddr(), pnio.ObjectUUID(1, 2, 3))
	ar := rpc.NewRandomUUID()
	data := make([]byte, 1000)
	h := pnio.ReadRes{ARUUID: ar, Subslot: 1, Index: pnio.IndexIM0, Length: uint32(len(data))}
	f.set(func(f *fakeDev) {
		f.raw = func(pnio.OpNum, []byte) []byte {
			return pnio.AppendNDRResponse(nil, order, pnio.Status{}, pnio.DefaultArgsMaximum, append(h.Append(nil), data...))
		}
	})
	got, err := rc.Read(context.Background(), ar, Record{Subslot: 1, Index: pnio.IndexIM0}, 16)
	if err == nil && len(got) > 16 {
		t.Fatalf("Read with maxLen 16 returned %d octets", len(got))
	}
}
