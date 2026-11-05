// SPDX-License-Identifier: MIT

package dcp

import (
	"bytes"
	"errors"
	"testing"

	"github.com/otfabric/go-profinet"
)

// FuzzBlockData decodes arbitrary block data as an IP suite and as a
// Control/Response: either ErrMalformed or a value that encodes to the
// octets it was decoded from.
func FuzzBlockData(f *testing.F) {
	f.Add(AppendIPSuite(nil, IPSuite{}))
	f.Add(AppendControlResponse(nil, ControlResponse{Block: DeviceName, Error: BlockErrorNone}))
	f.Add([]byte{})
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11})
	f.Fuzz(func(t *testing.T, b []byte) {
		if s, err := DecodeIPSuite(b); err != nil {
			if !errors.Is(err, profinet.ErrMalformed) {
				t.Fatalf("IP suite error %v", err)
			}
		} else if out := AppendIPSuite(nil, s); !bytes.Equal(out, b[:12]) {
			t.Fatalf("IP suite round trip % x, want % x", out, b[:12])
		}
		if r, err := DecodeControlResponse(b); err != nil {
			if !errors.Is(err, profinet.ErrMalformed) {
				t.Fatalf("control response error %v", err)
			}
		} else if out := AppendControlResponse(nil, r); !bytes.Equal(out, b[:3]) {
			t.Fatalf("control response round trip % x, want % x", out, b[:3])
		}
	})
}
