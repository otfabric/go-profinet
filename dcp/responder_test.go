// SPDX-License-Identifier: MIT

package dcp

import (
	"hash/fnv"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/ethernet"
)

// identifyDelay must keep the spread the hash/fnv FNV-1a hash gave.
func TestIdentifyDelayMatchesFNV1a(t *testing.T) {
	for _, m := range []ethernet.MAC{{}, {0x02, 0, 0, 0, 0, 1}, {0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, {0x00, 0x0E, 0xCF, 0x12, 0x34, 0x56}} {
		h := fnv.New32a()
		if _, err := h.Write(m[:]); err != nil {
			t.Fatal(err)
		}
		want := time.Duration(h.Sum32()%1000) * time.Microsecond * 10
		if got := identifyDelay(m); got != want {
			t.Errorf("identifyDelay(%v) = %v, want %v", m, got, want)
		}
	}
}
