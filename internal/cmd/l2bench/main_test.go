// SPDX-License-Identifier: MIT

//go:build linux

package main

import (
	"syscall"
	"testing"
	"unsafe"
)

func TestTimespecNano(t *testing.T) {
	ts := syscall.NsecToTimespec(1_700_000_000_123_456_789)
	b := unsafe.Slice((*byte)(unsafe.Pointer(&ts)), unsafe.Sizeof(ts))
	if ns, ok := timespecNano(b); !ok || ns != 1_700_000_000_123_456_789 {
		t.Fatalf("%d %v", ns, ok)
	}
	if _, ok := timespecNano(b[:len(b)-1]); ok {
		t.Fatal("short timespec")
	}
	bad := syscall.Timespec{Sec: 1, Nsec: 1_000_000_000}
	if _, ok := timespecNano(unsafe.Slice((*byte)(unsafe.Pointer(&bad)), unsafe.Sizeof(bad))); ok {
		t.Fatal("tv_nsec out of range")
	}
}

func TestCstr(t *testing.T) {
	if got := cstr([]int8{'6', '.', '1', -61, 0, 'x'}); got != "6.1?" {
		t.Fatalf("%q", got)
	}
}
