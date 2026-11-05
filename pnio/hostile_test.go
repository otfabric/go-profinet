// SPDX-License-Identifier: MIT

package pnio

import "testing"

// TestSplitWriteMultipleHugeLength: an item that claims 4 GiB of record
// data is refused, on every platform (on 32-bit ones int(0xFFFFFFFF) is
// -1, which passed the length check and then panicked slicing).
func TestSplitWriteMultipleHugeLength(t *testing.T) {
	h := WriteReq{Index: 0x10, Length: 0xFFFFFFFF}
	b := append(h.Append(nil), 1, 2, 3, 4)
	if _, err := SplitWriteMultiple(b); err == nil {
		t.Fatal("4 GiB item accepted")
	}
}
