// SPDX-License-Identifier: MIT

package rpc

import (
	"errors"
	"testing"

	"github.com/otfabric/go-profinet"
)

// The fragments of one message share a byte order: a fragment in another
// one is refused, in either order and whichever fragment comes first
// (p-net issue 2275 was a device that switched after the first fragment).
func TestReassemblyMixedDRep(t *testing.T) {
	for _, c := range []struct {
		name          string
		first, second uint8
	}{
		{"big then little", DRepBigEndian, DRepLittleEndian},
		{"little then big", DRepLittleEndian, DRepBigEndian},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newReassembly(0)
			if _, done, err := r.add(&Header{Flags1: FlagFrag, DRep: [3]byte{c.first}}, []byte{1}); done || err != nil {
				t.Fatalf("first fragment: done %v err %v", done, err)
			}
			_, done, err := r.add(&Header{Flags1: FlagFrag | FlagLastFrag, FragNum: 1, DRep: [3]byte{c.second}}, []byte{2})
			if done || !errors.Is(err, profinet.ErrMalformed) {
				t.Fatalf("mixed byte order: done %v err %v", done, err)
			}
		})
	}
	// Character and floating-point representation (the other DRep nibble
	// and octets) do not matter; one byte order completes.
	r := newReassembly(0)
	_, _, _ = r.add(&Header{Flags1: FlagFrag, FragNum: 1, Flags2: 0, DRep: [3]byte{DRepLittleEndian | 1, 1}}, []byte{2})
	if _, done, err := r.add(&Header{Flags1: FlagFrag, DRep: [3]byte{DRepLittleEndian}}, []byte{1}); done || err != nil {
		t.Fatalf("done %v err %v", done, err)
	}
	b, done, err := r.add(&Header{Flags1: FlagFrag | FlagLastFrag, FragNum: 2, DRep: [3]byte{DRepLittleEndian}}, []byte{3})
	if !done || err != nil || string(b) != "\x01\x02\x03" {
		t.Fatalf("% x done %v err %v", b, done, err)
	}
}
