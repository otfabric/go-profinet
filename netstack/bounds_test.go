// SPDX-License-Identifier: MIT

package netstack

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/vnet"
)

// addrN returns the n-th address of 10.0.0.0/8 after 10.1.0.0 and a
// hardware address for it.
func addrN(n int) (netip.Addr, ethernet.MAC) {
	return netip.AddrFrom4([4]byte{10, 1 + byte(n>>16), byte(n >> 8), byte(n)}),
		ethernet.MAC{2, 1, 0, byte(n >> 16), byte(n >> 8), byte(n)}
}

func decoded(t testing.TB, b []byte) *ethernet.Frame {
	t.Helper()
	f, err := ethernet.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	return &f
}

func (s *Stack) tableLen() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.arp)
}

func (s *Stack) entry(a netip.Addr) (ethernet.MAC, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.arp[a]
	return m, ok
}

// waiting returns the number of waiters of resolutions and the number of
// addresses the table of waiters has an entry for.
func (s *Stack) waiting() (waiters, addrs int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ws := range s.waiters {
		waiters += len(ws)
	}
	return waiters, len(s.waiters)
}

func (s *Stack) resolving() bool {
	w, _ := s.waiting()
	return w > 0
}

// net1 returns a virtual network that ends with the test.
func net1(t testing.TB) *vnet.Network {
	t.Helper()
	n := vnet.New(1)
	t.Cleanup(func() { _ = n.Close() })
	return n
}

var mask8 = netip.MustParseAddr("255.0.0.0")

// TestARPTableBound: the ARP table holds at most maxARPEntries addresses,
// whatever arrives.
func TestARPTableBound(t *testing.T) {
	ctx := context.Background()
	own, ownMAC := ip4("10.0.0.1"), macOf(1)
	announce := func(s *Stack, n int) {
		a, m := addrN(n)
		s.handleARP(ctx, decoded(t, arpFrame(ethernet.Broadcast, 1, m, a, ethernet.MAC{}, a)))
	}
	for _, senders := range []int{0, 1, maxARPEntries - 1, maxARPEntries, maxARPEntries + 1, 2 * maxARPEntries} {
		s, _, _ := newStackCfg(t, net1(t), 1, Config{Addr: own, Mask: mask8})
		for n := range senders {
			announce(s, n)
		}
		if got, want := s.tableLen(), min(senders, maxARPEntries); got != want {
			t.Errorf("%d senders: the table holds %d addresses, want %d", senders, got, want)
		}
	}

	s, _, _ := newStackCfg(t, net1(t), 1, Config{Addr: own, Mask: mask8})
	for n := range maxARPEntries {
		announce(s, n)
	}
	first, _ := addrN(0)
	second, _ := addrN(1)
	extra, extraMAC := addrN(maxARPEntries + 7)
	has := func(a netip.Addr) bool { _, ok := s.entry(a); return ok }

	// Full: an address nobody asked for is not learned and takes nobody's
	// place, by ARP or by an IPv4 packet for another station.
	s.handleARP(ctx, decoded(t, arpFrame(ethernet.Broadcast, 1, extraMAC, extra, ethernet.MAC{}, extra)))
	s.handleIP(decoded(t, udpFrame(macOf(9), extraMAC, extra, ip4("10.0.0.9"), 1, 2, []byte("x"), ipOpts{})))
	if has(extra) || !has(first) || s.tableLen() != maxARPEntries {
		t.Fatalf("full table: unasked address learned %v, oldest kept %v, %d addresses", has(extra), has(first), s.tableLen())
	}
	// An address in the table is still confirmed, with a new hardware
	// address as well; it is then the one used last.
	changed := ethernet.MAC{2, 9, 9, 9, 9, 9}
	s.handleARP(ctx, decoded(t, arpFrame(ethernet.Broadcast, 1, changed, first, ethernet.MAC{}, first)))
	if m, _ := s.entry(first); m != changed {
		t.Fatalf("confirmed entry %s, want %s", m, changed)
	}
	// A request for the stack's own address is learned in place of the
	// entry seen longest ago, which is now the second.
	s.handleARP(ctx, decoded(t, arpFrame(ethernet.Broadcast, 1, extraMAC, extra, ethernet.MAC{}, own)))
	if !has(extra) || !has(first) || has(second) || s.tableLen() != maxARPEntries {
		t.Fatalf("request for the own address: learned %v, confirmed entry kept %v, oldest kept %v, %d addresses",
			has(extra), has(first), has(second), s.tableLen())
	}
	// So is the sender of a datagram for the stack; an entry that a send
	// used meanwhile stays.
	third, thirdMAC := addrN(2)
	if mac, err := s.resolve(ctx, third); err != nil || mac != thirdMAC {
		t.Fatalf("resolve: %s, %v", mac, err)
	}
	fourth, _ := addrN(3)
	peer, peerMAC := addrN(maxARPEntries + 8)
	s.handleIP(decoded(t, udpFrame(ownMAC, peerMAC, peer, own, 1, 2, []byte("x"), ipOpts{})))
	if !has(peer) || !has(third) || has(fourth) || s.tableLen() != maxARPEntries {
		t.Fatalf("datagram for the stack: learned %v, used entry kept %v, oldest kept %v, %d addresses",
			has(peer), has(third), has(fourth), s.tableLen())
	}
	// The answer to a resolution in progress is learned as well.
	asked, askedMAC := addrN(maxARPEntries + 9)
	res := make(chan error, 1)
	go func() {
		mac, err := s.resolve(ctx, asked)
		if err == nil && mac != askedMAC {
			err = errors.New("wrong hardware address")
		}
		res <- err
	}()
	waitFor(t, s.resolving)
	s.handleARP(ctx, decoded(t, arpFrame(ethernet.Broadcast, 2, askedMAC, asked, ethernet.MAC{}, asked)))
	if err := <-res; err != nil {
		t.Fatal(err)
	}
	if w, a := s.waiting(); !has(asked) || s.tableLen() != maxARPEntries || w != 0 || a != 0 {
		t.Fatalf("answer to a resolution: learned %v, %d addresses, %d waiters for %d addresses", has(asked), s.tableLen(), w, a)
	}
	// SetAddr empties the table; it fills again.
	if err := s.SetAddr(own, mask8, netip.Addr{}); err != nil {
		t.Fatal(err)
	}
	announce(s, 5)
	if s.tableLen() != 1 {
		t.Fatalf("%d addresses after SetAddr and one announcement", s.tableLen())
	}
}

// TestARPTableAllocation: what a stack keeps of the ARP packets it
// receives does not grow with their number.
func TestARPTableAllocation(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newStackCfg(t, net1(t), 1, Config{Addr: ip4("10.0.0.1"), Mask: mask8})
	const packets = 64 * maxARPEntries
	frames := make([]*ethernet.Frame, packets)
	for n := range frames {
		a, m := addrN(n)
		frames[n] = decoded(t, arpFrame(ethernet.Broadcast, 1, m, a, ethernet.MAC{}, a))
	}
	// The table is filled first: growing a map to its bound is not what
	// is measured.
	for _, f := range frames[:maxARPEntries] {
		s.handleARP(ctx, f)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for _, f := range frames {
		s.handleARP(ctx, f)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	if s.tableLen() != maxARPEntries {
		t.Fatalf("%d addresses", s.tableLen())
	}
	// A full table takes no memory for a packet it does not learn from.
	if grown := int64(after.HeapAlloc) - int64(before.HeapAlloc); grown > 256<<10 {
		t.Fatalf("the heap grew by %d octets over %d packets", grown, packets)
	}
}

// TestResolveLeavesNoWaiter: a resolution removes its waiter however it
// ends (contract row L6: the slot of a cancelled request is free).
func TestResolveLeavesNoWaiter(t *testing.T) {
	dst := &net.UDPAddr{IP: net.IPv4(10, 0, 0, 2), Port: 1}
	t.Run("no answer", func(t *testing.T) {
		s, _, _ := newStackCfg(t, net1(t), 1, Config{Addr: ip4("10.0.0.1"), Mask: mask24, ARPTimeout: 15 * time.Millisecond})
		c, _ := s.ListenUDP(0)
		for range 5 {
			if _, err := c.WriteTo([]byte{1}, dst); !errors.Is(err, profinet.ErrTimeout) {
				t.Fatalf("err %v", err)
			}
		}
		if w, a := s.waiting(); w != 0 || a != 0 {
			t.Fatalf("%d waiters for %d addresses kept after 5 sends without an answer", w, a)
		}
	})
	t.Run("answer", func(t *testing.T) {
		s, _, _ := newStackCfg(t, net1(t), 1, Config{Addr: ip4("10.0.0.1"), Mask: mask24, ARPTimeout: time.Minute})
		c, _ := s.ListenUDP(0)
		res := make(chan error, 2)
		for range 2 {
			go func() {
				_, err := c.WriteTo([]byte{1}, dst)
				res <- err
			}()
		}
		waitFor(t, func() bool { w, _ := s.waiting(); return w == 2 })
		s.handleARP(context.Background(), decoded(t, arpFrame(macOf(1), 2, macOf(2), ip4("10.0.0.2"), macOf(1), ip4("10.0.0.1"))))
		for range 2 {
			if err := <-res; err != nil {
				t.Fatalf("send: %v", err)
			}
		}
		if w, a := s.waiting(); w != 0 || a != 0 {
			t.Fatalf("%d waiters for %d addresses kept after the answer", w, a)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		s, _, _ := newStackCfg(t, net1(t), 1, Config{Addr: ip4("10.0.0.1"), Mask: mask24, ARPTimeout: time.Minute})
		c, _ := s.ListenUDP(0)
		_ = c.SetWriteDeadline(time.Now().Add(15 * time.Millisecond))
		if _, err := c.WriteTo([]byte{1}, dst); !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("err %v", err)
		}
		if w, a := s.waiting(); w != 0 || a != 0 {
			t.Fatalf("%d waiters for %d addresses kept after a deadline", w, a)
		}
	})
	t.Run("stack closed", func(t *testing.T) {
		s, _, _ := newStackCfg(t, net1(t), 1, Config{Addr: ip4("10.0.0.1"), Mask: mask24, ARPTimeout: time.Minute})
		c, _ := s.ListenUDP(0)
		res := make(chan error, 1)
		go func() {
			_, err := c.WriteTo([]byte{1}, dst)
			res <- err
		}()
		waitFor(t, s.resolving)
		_ = s.Close()
		if err := <-res; !errors.Is(err, profinet.ErrClosed) {
			t.Fatalf("err %v", err)
		}
		if w, a := s.waiting(); w != 0 || a != 0 {
			t.Fatalf("%d waiters for %d addresses kept after Close", w, a)
		}
	})
	t.Run("send fails", func(t *testing.T) {
		s, mux, _ := newStackCfg(t, net1(t), 1, Config{Addr: ip4("10.0.0.1"), Mask: mask24, ARPTimeout: time.Minute})
		_ = mux.Close()
		<-s.done
		if _, err := s.resolve(context.Background(), ip4("10.0.0.2")); !errors.Is(err, profinet.ErrClosed) {
			t.Fatalf("err %v", err)
		}
		if w, a := s.waiting(); w != 0 || a != 0 {
			t.Fatalf("%d waiters for %d addresses kept after a failed send", w, a)
		}
	})
}

// TestMuxStopClosesSockets: when the Mux stops, the stack closes its
// sockets, as Close does: a blocked ReadFrom returns (contract row L3).
func TestMuxStopClosesSockets(t *testing.T) {
	s, mux, _ := newStackCfg(t, net1(t), 1, Config{Addr: ip4("10.0.0.1"), Mask: mask24})
	c, err := s.ListenUDP(7)
	if err != nil {
		t.Fatal(err)
	}
	res := make(chan error, 1)
	go func() {
		_, _, err := c.ReadFrom(make([]byte, 16))
		res <- err
	}()
	_ = mux.Close()
	select {
	case err := <-res:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("ReadFrom: %v, want net.ErrClosed", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ReadFrom is still blocked after the Mux stopped")
	}
	<-s.swept
	s.mu.RLock()
	open := len(s.socks)
	s.mu.RUnlock()
	if open != 0 {
		t.Fatalf("%d sockets open on a stopped stack", open)
	}
	if _, err := s.ListenUDP(8); !errors.Is(err, profinet.ErrClosed) {
		t.Fatalf("ListenUDP on a stopped stack: %v", err)
	}
	// A closed socket says so in both vocabularies.
	if _, err := c.WriteTo([]byte{1}, &net.UDPAddr{IP: net.IPv4bcast, Port: 1}); !errors.Is(err, net.ErrClosed) || !errors.Is(err, profinet.ErrClosed) {
		t.Fatalf("WriteTo: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestStackCloseConcurrent: Close from several goroutines; each returns
// with the sockets closed (contract row L2).
func TestStackCloseConcurrent(t *testing.T) {
	s, _, _ := newStackCfg(t, net1(t), 1, Config{Addr: ip4("10.0.0.1"), Mask: mask24})
	c, _ := s.ListenUDP(7)
	const callers = 8
	res := make(chan error, callers)
	start := make(chan struct{})
	for range callers {
		go func() {
			<-start
			err := s.Close()
			if err == nil {
				// What Close promises holds when it returns.
				select {
				case <-c.done:
				default:
					err = errors.New("Close returned with a socket open")
				}
			}
			res <- err
		}()
	}
	close(start)
	for range callers {
		if err := <-res; err != nil {
			t.Fatal(err)
		}
	}
	// A socket's own Close as well.
	s2, _, _ := newStackCfg(t, net1(t), 2, Config{Addr: ip4("10.0.0.2"), Mask: mask24})
	c2, _ := s2.ListenUDP(7)
	for range callers {
		go func() { res <- c2.Close() }()
	}
	for range callers {
		if err := <-res; err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := c2.ReadFrom(make([]byte, 8)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("ReadFrom after Close: %v", err)
	}
	if again, err := s2.ListenUDP(7); err != nil {
		t.Fatalf("the port of a closed socket: %v", err)
	} else {
		_ = again.Close()
	}
}

// FuzzReceive feeds a frame to the receive path of a stack as an ARP and
// as an IPv4 frame. Whatever it holds: the table and the socket queue stay
// within their bounds, and a datagram that is delivered is a part of the
// frame.
func FuzzReceive(f *testing.F) {
	own, peer := ip4("10.0.0.1"), ip4("10.0.0.2")
	seeds := [][]byte{
		arpFrame(ethernet.Broadcast, 1, macOf(2), peer, ethernet.MAC{}, own),
		arpFrame(macOf(1), 2, macOf(2), peer, macOf(1), own),
		udpFrame(macOf(1), macOf(2), peer, own, 1000, 7, []byte("data"), ipOpts{}),
		udpFrame(macOf(1), macOf(2), peer, own, 1000, 7, nil, ipOpts{zeroUDPSum: true}),
		udpFrame(macOf(1), macOf(2), peer, own, 1000, 7, []byte("frag"), ipOpts{flags: 0x2000}),
		udpFrame(macOf(1), macOf(2), peer, ip4("10.0.0.255"), 1000, 7, []byte("bc"), ipOpts{}),
	}
	for _, s := range seeds {
		f.Add(s[ethernet.HeaderLen:])
	}
	f.Add([]byte{})
	f.Add([]byte{0x4F, 0, 0, 60})
	f.Fuzz(func(t *testing.T, payload []byte) {
		s, _, _ := newStackCfg(t, net1(t), 1, Config{Addr: own, Mask: mask24, QueueLen: 4})
		c, err := s.ListenUDP(7)
		if err != nil {
			t.Fatal(err)
		}
		for _, et := range []ethernet.EtherType{ethernet.EtherTypeARP, ethernet.EtherTypeIPv4} {
			fr := &ethernet.Frame{Dst: macOf(1), Src: macOf(2), EtherType: et, Payload: payload}
			for range 6 {
				if et == ethernet.EtherTypeARP {
					s.handleARP(context.Background(), fr)
				} else {
					s.handleIP(fr)
				}
			}
		}
		if n := s.tableLen(); n > 2 {
			t.Fatalf("%d addresses learned from one sender address per kind of frame", n)
		}
		if len(c.rx) > 4 {
			t.Fatalf("%d datagrams queued, queue of 4", len(c.rx))
		}
		for len(c.rx) > 0 {
			d := <-c.rx
			if len(d.data) > len(payload)-ipHeaderLen-udpHeaderLen {
				t.Fatalf("a datagram of %d octets from a packet of %d", len(d.data), len(payload))
			}
			if d.from == nil || len(d.from.IP) != 4 {
				t.Fatalf("source %v", d.from)
			}
		}
		st := s.Stats()
		if st.ReceivedIP > 6 || st.Overflow > 6 || st.ARPRequests+st.ARPReplys > 6 {
			t.Fatalf("counters %+v after 6 frames of each kind", st)
		}
	})
}
