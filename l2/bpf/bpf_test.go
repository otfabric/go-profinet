// SPDX-License-Identifier: MIT

//go:build darwin || freebsd || netbsd || dragonfly

package bpf

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/ethernet"
)

// pair opens the ends of a link named by PROFINET_L2_IFACE_A and _B (a
// feth pair on macOS: ifconfig feth0 create; ifconfig feth1 create;
// ifconfig feth0 peer feth1). Without them the tests are skipped.
func pair(t *testing.T) (*Port, *Port) {
	t.Helper()
	a, b := os.Getenv("PROFINET_L2_IFACE_A"), os.Getenv("PROFINET_L2_IFACE_B")
	if a == "" || b == "" {
		t.Skip("real Layer 2 test: set PROFINET_L2_IFACE_A and PROFINET_L2_IFACE_B")
	}
	pa, err := Open(a, Options{})
	if err != nil {
		t.Fatal(err)
	}
	pb, err := Open(b, Options{})
	if err != nil {
		_ = pa.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = pa.Close()
		_ = pb.Close()
	})
	return pa, pb
}

func TestExchange(t *testing.T) {
	a, b := pair(t)
	f := ethernet.Frame{Dst: b.HardwareAddr(), Src: a.HardwareAddr(), EtherType: ethernet.EtherTypeProfinet, Payload: []byte{0xFE, 0xFD, 7}}
	if err := a.WriteFrame(context.Background(), f.Encode()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	buf := make([]byte, 2000)
	for {
		n, err := b.ReadFrame(ctx, buf)
		if err != nil {
			t.Fatal(err)
		}
		g, err := ethernet.Decode(buf[:n])
		if err == nil && g.EtherType == ethernet.EtherTypeProfinet && bytes.HasPrefix(g.Payload, f.Payload) {
			break
		}
	}
	if !a.LinkUp() || a.MTU() < 1500 || a.Name() == "" {
		t.Fatal("link")
	}
	_ = a.Close()
	if _, err := a.ReadFrame(context.Background(), buf); !errors.Is(err, profinet.ErrClosed) {
		t.Fatalf("err %v", err)
	}
	if err := a.WriteFrame(context.Background(), f.Encode()); !errors.Is(err, profinet.ErrClosed) {
		t.Fatalf("err %v", err)
	}
}

func TestOpenErrors(t *testing.T) {
	if _, err := Open("no-such-if0", Options{}); !errors.Is(err, profinet.ErrNotFound) {
		t.Fatalf("err %v", err)
	}
}

// The two layouts of struct bpf_hdr among the systems of the build tag:
// a 32-bit time stamp with records aligned to 4 octets (macOS on every
// architecture and the 32-bit BSDs), and a 64-bit time stamp with records
// aligned to 8 (the 64-bit BSDs). The walk is tested with both on every
// host.
var (
	layout32 = layout{size: 20, caplen: 8, datalen: 12, hdrlen: 16, align: 4}
	layout64 = layout{size: 32, caplen: 16, datalen: 20, hdrlen: 24, align: 8}
)

// ethHdrLen is bh_hdrlen as the kernel sets it on an Ethernet interface:
// the header rounded up so that what follows the Ethernet header is
// aligned.
func (l layout) ethHdrLen() int {
	return l.wordAlign(l.floor()+ethernet.HeaderLen) - ethernet.HeaderLen
}

// record encodes a BPF record with a header of hdr octets as the kernel
// lays it out for l: the first caplen octets of frame, padded to the
// alignment.
func record(l layout, hdr int, frame []byte, caplen int) []byte {
	b := make([]byte, l.wordAlign(hdr+caplen))
	binary.NativeEndian.PutUint32(b[l.caplen:], uint32(caplen))
	binary.NativeEndian.PutUint32(b[l.datalen:], uint32(len(frame)))
	binary.NativeEndian.PutUint16(b[l.hdrlen:], uint16(hdr))
	copy(b[hdr:], frame[:caplen])
	return b
}

// TestNativeLayout: the layout the package walks with is that of the
// syscall package for this system.
func TestNativeLayout(t *testing.T) {
	if native.align != 4 && native.align != 8 {
		t.Fatalf("alignment %d", native.align)
	}
	if native.size != int(unsafe.Sizeof(syscall.BpfHdr{})) || native.caplen >= native.datalen || native.datalen >= native.hdrlen ||
		native.floor() > native.size || native.size-native.floor() >= 8 {
		t.Fatalf("layout %+v", native)
	}
	if native.ethHdrLen() < native.floor() || (native.ethHdrLen()+ethernet.HeaderLen)%native.align != 0 {
		t.Fatalf("header length %d for layout %+v", native.ethHdrLen(), native)
	}
	if runtime.GOOS == "darwin" && (native != layout32 || native.floor() != 18 || native.ethHdrLen() != 18) {
		t.Fatalf("macOS layout %+v, header %d, want %+v and 18", native, native.ethHdrLen(), layout32)
	}
}

func TestNextRecord(t *testing.T) {
	hdr := native.ethHdrLen()
	f1 := bytes.Repeat([]byte{1}, 61)
	f2 := bytes.Repeat([]byte{2}, 64)
	buf := append(record(native, hdr, f1, len(f1)), record(native, hdr, f2, 10)...) // f2 truncated by the snapshot
	buf = append(buf, record(native, hdr, f2, len(f2))...)
	rec, rest, ok := nextRecord(buf)
	if !ok || !bytes.Equal(rec, f1) {
		t.Fatalf("first %v %x", ok, rec)
	}
	rec, rest, ok = nextRecord(rest)
	if !ok || rec != nil {
		t.Fatalf("truncated record not skipped: %v %x", ok, rec)
	}
	rec, rest, ok = nextRecord(rest)
	if !ok || !bytes.Equal(rec, f2) || len(rest) != 0 {
		t.Fatalf("third %v %x %d", ok, rec, len(rest))
	}
	if _, _, ok := nextRecord(rest); ok {
		t.Fatal("empty buffer")
	}
	bad := record(native, hdr, f1, len(f1))
	binary.NativeEndian.PutUint32(bad[native.caplen:], 5000)
	if _, _, ok := nextRecord(bad); ok {
		t.Fatal("record beyond the buffer")
	}
}

// TestNextRecordLayouts walks buffers laid out for each layout: every
// frame comes out once and whole, whatever the header length and the
// alignment, and a record that does not fit ends the buffer.
func TestNextRecordLayouts(t *testing.T) {
	for name, l := range map[string]layout{"32-bit time stamp, alignment 4": layout32, "64-bit time stamp, alignment 8": layout64} {
		t.Run(name, func(t *testing.T) {
			eth := l.ethHdrLen()
			// Frames whose ends fall on every offset within the alignment.
			var frames [][]byte
			var buf []byte
			for i := range 2 * l.align {
				f := bytes.Repeat([]byte{byte(i + 1)}, ethernet.MinFrameLen+i)
				frames = append(frames, f)
				buf = append(buf, record(l, eth, f, len(f))...)
				// One the snapshot cut short after each: it is skipped.
				buf = append(buf, record(l, eth, f, ethernet.HeaderLen)...)
			}
			// The last record of a read ends with its frame, unpadded.
			last := bytes.Repeat([]byte{0xEE}, 61)
			buf = append(buf, record(l, eth, last, len(last))[:eth+len(last)]...)
			frames = append(frames, last)
			var got [][]byte
			rest := buf
			for {
				rec, r, ok := l.next(rest)
				if !ok {
					break
				}
				rest = r
				if rec != nil {
					got = append(got, rec)
				}
			}
			if len(rest) != 0 {
				t.Fatalf("%d octets of the buffer left after %d frames", len(rest), len(got))
			}
			if len(got) != len(frames) {
				t.Fatalf("%d frames from the buffer, want %d", len(got), len(frames))
			}
			for i := range frames {
				if !bytes.Equal(got[i], frames[i]) {
					t.Fatalf("frame %d: %x, want %x", i, got[i], frames[i])
				}
			}

			f := bytes.Repeat([]byte{7}, 60)
			one := func(hdr int) []byte { return record(l, hdr, f, len(f)) }
			put32 := func(b []byte, off int, v uint32) []byte {
				binary.NativeEndian.PutUint32(b[off:], v)
				return b
			}
			for _, c := range []struct {
				name string
				buf  []byte
				ok   bool
				rec  []byte
				rest int
			}{
				{"empty buffer", nil, false, nil, 0},
				{"one octet", []byte{0}, false, nil, 0},
				{"one octet less than the struct", make([]byte, l.size-1), false, nil, 0},
				{"the struct with a zero header length", make([]byte, l.size), false, nil, 0},
				{"header one octet below the floor", one(l.floor() - 1), false, nil, 0},
				{"header at the floor", one(l.floor()), true, f, 0},
				{"header as the kernel sets it", one(eth), true, f, 0},
				{"header one alignment longer", one(eth + l.align), true, f, 0},
				{"a record without a frame", record(l, l.floor(), nil, 0), true, []byte{}, 0},
				{"header ends with the buffer", record(l, l.size, nil, 0), true, []byte{}, 0},
				{"header beyond the buffer", func() []byte {
					b := one(eth)
					binary.NativeEndian.PutUint16(b[l.hdrlen:], uint16(len(b)+1))
					return put32(b, l.caplen, 0)
				}(), false, nil, 0},
				{"frame ends with the buffer", one(eth)[:eth+len(f)], true, f, 0},
				{"frame one octet beyond the buffer", one(eth)[:eth+len(f)-1], false, nil, 0},
				{"captured length one more than present", put32(one(eth), l.caplen, uint32(len(one(eth))-eth+1)), false, nil, 0},
				{"captured length 2^31-1", put32(one(eth), l.caplen, 1<<31-1), false, nil, 0},
				{"captured length 2^31", put32(one(eth), l.caplen, 1<<31), false, nil, 0},
				{"captured length 2^32-1", put32(one(eth), l.caplen, 1<<32-1), false, nil, 0},
				{"two records", append(one(eth), one(eth)...), true, f, len(one(eth))},
			} {
				rec, rest, ok := l.next(c.buf)
				if ok != c.ok || !bytes.Equal(rec, c.rec) || (rec == nil) != (c.rec == nil) || len(rest) != c.rest {
					t.Errorf("%s: ok %v, frame %x, %d octets left; want ok %v, frame %x, %d left", c.name, ok, rec, len(rest), c.ok, c.rec, c.rest)
				}
			}
		})
	}
}

// FuzzNextRecord walks any buffer with the layouts of the systems of the
// build tag. Whatever the buffer holds: the walk ends, a frame is a part
// of the buffer, and the frames together are no longer than the buffer.
func FuzzNextRecord(f *testing.F) {
	for _, l := range []layout{layout32, layout64} {
		var buf []byte
		for _, n := range []int{14, 60, 61, 1514} {
			buf = append(buf, record(l, l.ethHdrLen(), bytes.Repeat([]byte{byte(n)}, n), n)...)
		}
		f.Add(buf)
		f.Add(buf[:len(buf)-7])
		f.Add(buf[:l.size])
	}
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		for _, l := range []layout{native, layout32, layout64} {
			rest, total := b, 0
			for steps := 0; ; steps++ {
				if steps > len(b) {
					t.Fatalf("layout %+v: the walk of %d octets takes more than %d steps", l, len(b), steps)
				}
				rec, next, ok := l.next(rest)
				if !ok {
					break
				}
				if len(next) >= len(rest) {
					t.Fatalf("layout %+v: a record left %d of %d octets", l, len(next), len(rest))
				}
				if total += len(rec); total > len(b) {
					t.Fatalf("layout %+v: %d octets of frames from a buffer of %d", l, total, len(b))
				}
				if len(rec) > 0 && (len(rec) > len(rest)-l.floor() || !bytes.Contains(rest, rec)) {
					t.Fatalf("layout %+v: a frame of %d octets is no part of the %d octets left", l, len(rec), len(rest))
				}
				rest = next
			}
		}
	})
}
