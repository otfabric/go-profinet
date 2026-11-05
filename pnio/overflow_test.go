// SPDX-License-Identifier: MIT

package pnio

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet"
)

// mustPanicTooLarge runs f and fails unless it panics with an error
// matching profinet.ErrInvalidConfig.
func mustPanicTooLarge(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		t.Helper()
		v := recover()
		err, ok := v.(error)
		if !ok || !errors.Is(err, profinet.ErrInvalidConfig) {
			t.Fatalf("%s: recovered %v, want a panic matching ErrInvalidConfig", name, v)
		}
	}()
	f()
}

// A block or a count that does not fit its 16-bit field used to be
// encoded with a wrapped length; it now panics instead.
func TestEncodersRefuseOversizedFields(t *testing.T) {
	// The largest block that fits: a length of 0xFFFF (version included).
	ok := RawBlock{Header: BlockHeader{Type: BlockARVendorReq, VersionHigh: 1}, Body: make([]byte, math.MaxUint16-2)}
	b := ok.Append(nil)
	if blk, rest, err := ReadBlock(b); err != nil || len(rest) != 0 || blk.Header.Length != math.MaxUint16 {
		t.Fatalf("largest block: %+v %v", blk.Header, err)
	}
	mustPanicTooLarge(t, "raw block", func() {
		big := RawBlock{Header: BlockHeader{Type: BlockARVendorReq, VersionHigh: 1}, Body: make([]byte, math.MaxUint16-1)}
		big.Append(nil)
	})
	mustPanicTooLarge(t, "station name", func() {
		a := testARBlockReq()
		a.StationName = strings.Repeat("a", math.MaxUint16+1)
		a.Append(nil)
	})
	mustPanicTooLarge(t, "module count", func() {
		e := ExpectedSubmoduleBlockReq{Modules: make([]ExpectedModule, math.MaxUint16+1)}
		e.Append(nil)
	})
	mustPanicTooLarge(t, "ModuleDiff", func() {
		d := ModuleDiffBlock{APIs: []DiffAPI{{Modules: make([]DiffModule, 7000)}}}
		d.Append(nil)
	})
	mustPanicTooLarge(t, "len32 negative", func() { len32("x", -1) })
	if strconv.IntSize == 64 {
		mustPanicTooLarge(t, "len32", func() { len32("x", math.MaxInt) })
	}
	if len32("x", 7) != 7 || len16("x", 7) != 7 {
		t.Fatal("in range")
	}
}

// A Connect whose ExpectedSubmoduleBlockReqs describe more modules than
// one ModuleDiffBlock of the answer can list is refused as out of
// resources; the device used to answer it with a ModuleDiffBlock whose
// length had wrapped.
func TestDecodeConnectRequestModuleDiffBound(t *testing.T) {
	expected := func(n, api int) ExpectedSubmoduleBlockReq {
		e := ExpectedSubmoduleBlockReq{}
		for i := range n {
			e.Modules = append(e.Modules, ExpectedModule{API: uint32(api + i), Slot: 1})
		}
		return e
	}
	c := ConnectRequest{AR: testARBlockReq(), Expected: []ExpectedSubmoduleBlockReq{expected(2100, 0), expected(2100, 2100)}}
	_, err := DecodeConnectRequest(c.Append(nil))
	var se *StatusError
	if !errors.As(err, &se) || se.Status != ServiceStatus(CodeConnect, Code1CMRPC, CMRPCOutOfMemory) || !errors.Is(err, profinet.ErrResourceExhausted) {
		t.Fatalf("err %v", err)
	}
	// The same modules in one API fit.
	c.Expected = []ExpectedSubmoduleBlockReq{expected(2100, 0), expected(2100, 0)}
	for i := range c.Expected {
		for j := range c.Expected[i].Modules {
			c.Expected[i].Modules[j].API = 0
		}
	}
	if _, err := DecodeConnectRequest(c.Append(nil)); err != nil {
		t.Fatal(err)
	}
	if n := moduleDiffBound(c.Expected); n != 4+6+4200*10 {
		t.Fatalf("bound %d", n)
	}
}

// TestAppendString8Truncates: a string8 field holds at most 255 octets; a
// longer string is cut to 255, with a length octet that matches.
func TestAppendString8Truncates(t *testing.T) {
	b := appendString8(nil, strings.Repeat("x", 300))
	if len(b) != 1+math.MaxUint8 || b[0] != math.MaxUint8 {
		t.Fatalf("%d octets, length octet %d", len(b), b[0])
	}
	if b := appendString8(nil, "port-001"); len(b) != 9 || b[0] != 8 {
		t.Fatalf("short: % x", b)
	}
}
