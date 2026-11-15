// SPDX-License-Identifier: MIT

//go:build darwin || freebsd || netbsd || dragonfly

// Package bpf is the macOS, FreeBSD, NetBSD and DragonFly backend of l2.Port: a Berkeley Packet
// Filter device (/dev/bpf*) attached to one network interface.
//
// Opening a port needs read and write access to a /dev/bpf device: root,
// or on macOS membership of the access_bpf group that Wireshark's ChmodBPF
// helper creates. The device is set to immediate mode, writes complete
// headers (the source address is the frame's), does not return the frames
// the host sends, and runs in promiscuous mode only when asked.
package bpf

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/ethernet"
)

// Options configure a port.
type Options struct {
	// Promiscuous receives unicast frames for other addresses too.
	Promiscuous bool
}

// Port is a BPF device bound to one interface. It implements l2.Port and
// l2.LinkStater.
type Port struct {
	iface *net.Interface
	mac   ethernet.MAC
	fd    int

	readMu  sync.Mutex
	pending []byte // records read but not returned yet
	buf     []byte

	writeMu sync.Mutex
	closed  chan struct{}
	once    sync.Once
}

// readTimeout is how long a read waits before it checks for cancellation.
const readTimeout = 100 * time.Millisecond

func ioctl(fd int, req uint, arg unsafe.Pointer) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(req), uintptr(arg))
	if e != 0 {
		return e
	}
	return nil
}

// Open opens a BPF device on the named interface.
func Open(name string, opts Options) (*Port, error) {
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return nil, fmt.Errorf("bpf: %w: interface %q: %v", profinet.ErrNotFound, name, err)
	}
	if len(ifi.HardwareAddr) != 6 {
		return nil, fmt.Errorf("bpf: %w: interface %q is not Ethernet", profinet.ErrUnsupported, name)
	}
	fd := -1
	var lastErr error
	for i := range 256 {
		fd, err = syscall.Open(fmt.Sprintf("/dev/bpf%d", i), syscall.O_RDWR|syscall.O_CLOEXEC, 0)
		if err == nil {
			break
		}
		lastErr = err
		if errors.Is(err, syscall.EBUSY) {
			continue
		}
		if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
			return nil, fmt.Errorf("bpf: open /dev/bpf%d: %w (needs root or the access_bpf group)", i, err)
		}
		if errors.Is(err, syscall.ENOENT) {
			break
		}
	}
	if fd < 0 {
		return nil, fmt.Errorf("bpf: %w: no free /dev/bpf device: %v", profinet.ErrResourceExhausted, lastErr)
	}
	ok := false
	defer func() {
		if !ok {
			_ = syscall.Close(fd)
		}
	}()
	// The buffer length must be set before the interface.
	blen := uint32(1 << 16)
	_ = ioctl(fd, syscall.BIOCSBLEN, unsafe.Pointer(&blen))
	var ifr [32]byte // struct ifreq
	copy(ifr[:16], name)
	if err := ioctl(fd, syscall.BIOCSETIF, unsafe.Pointer(&ifr[0])); err != nil {
		return nil, fmt.Errorf("bpf: attach to %s: %w", name, err)
	}
	one := uint32(1)
	zero := uint32(0)
	for _, s := range []struct {
		req  uint
		arg  *uint32
		what string
	}{
		{syscall.BIOCIMMEDIATE, &one, "immediate mode"},
		{syscall.BIOCSHDRCMPLT, &one, "complete headers"},
		{syscall.BIOCSSEESENT, &zero, "see-sent"},
	} {
		if err := ioctl(fd, s.req, unsafe.Pointer(s.arg)); err != nil {
			return nil, fmt.Errorf("bpf: %s: %w", s.what, err)
		}
	}
	if opts.Promiscuous {
		if err := ioctl(fd, syscall.BIOCPROMISC, nil); err != nil {
			return nil, fmt.Errorf("bpf: promiscuous: %w", err)
		}
	}
	tv := syscall.NsecToTimeval(int64(readTimeout))
	if err := ioctl(fd, syscall.BIOCSRTIMEOUT, unsafe.Pointer(&tv)); err != nil {
		return nil, fmt.Errorf("bpf: read timeout: %w", err)
	}
	if err := ioctl(fd, syscall.BIOCGBLEN, unsafe.Pointer(&blen)); err != nil {
		return nil, fmt.Errorf("bpf: buffer length: %w", err)
	}
	ok = true
	return &Port{iface: ifi, mac: ethernet.MAC(ifi.HardwareAddr), fd: fd, buf: make([]byte, blen), closed: make(chan struct{})}, nil
}

// HardwareAddr implements l2.Port.
func (p *Port) HardwareAddr() ethernet.MAC { return p.mac }

// MTU implements l2.Port.
func (p *Port) MTU() int {
	if p.iface.MTU <= 0 {
		return ethernet.DefaultMTU
	}
	return p.iface.MTU
}

// Name returns the interface name.
func (p *Port) Name() string { return p.iface.Name }

// LinkUp implements l2.LinkStater.
func (p *Port) LinkUp() bool {
	ifi, err := net.InterfaceByIndex(p.iface.Index)
	return err == nil && ifi.Flags&net.FlagUp != 0 && ifi.Flags&net.FlagRunning != 0
}

// ReadFrame implements l2.Port.
func (p *Port) ReadFrame(ctx context.Context, b []byte) (int, error) {
	p.readMu.Lock()
	defer p.readMu.Unlock()
	for {
		// A closed port reads nothing, not even the frames left over from
		// the last read of the device.
		select {
		case <-p.closed:
			return 0, fmt.Errorf("bpf: %w", profinet.ErrClosed)
		default:
		}
		if rec, rest, ok := nextRecord(p.pending); ok {
			p.pending = rest
			if rec == nil {
				continue
			}
			return copy(b, rec), nil
		}
		p.pending = nil
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, err := syscall.Read(p.fd, p.buf)
		if err != nil {
			select {
			case <-p.closed:
				return 0, fmt.Errorf("bpf: %w", profinet.ErrClosed)
			default:
			}
			if errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) {
				continue
			}
			return 0, fmt.Errorf("bpf: read %s: %w", p.iface.Name, err)
		}
		p.pending = p.buf[:n]
	}
}

// layout is what the walk of a read buffer takes from the OS: the size of
// struct bpf_hdr, where it holds bh_caplen, bh_datalen (32 bits each) and
// bh_hdrlen (16 bits), and the alignment of the records (BPF_ALIGNMENT).
// They differ between the systems of the build tag: macOS and the 32-bit
// BSDs have a 32-bit time stamp before the fields and align to 4 octets,
// the 64-bit BSDs a 64-bit one and align to 8.
type layout struct {
	size                    int
	caplen, datalen, hdrlen int
	align                   int
}

// native is the layout of the OS and architecture the package is built
// for, from the syscall package's struct and constant.
var native = layout{
	size:    int(unsafe.Sizeof(syscall.BpfHdr{})),
	caplen:  int(unsafe.Offsetof(syscall.BpfHdr{}.Caplen)),
	datalen: int(unsafe.Offsetof(syscall.BpfHdr{}.Datalen)),
	hdrlen:  int(unsafe.Offsetof(syscall.BpfHdr{}.Hdrlen)),
	align:   syscall.BPF_ALIGNMENT,
}

// floor is the header up to and including bh_hdrlen, without the padding
// of the struct: no record has a shorter header, and the kernel rounds
// bh_hdrlen up from it so that what follows the frame's Ethernet header
// is aligned.
func (l layout) floor() int { return l.hdrlen + 2 }

// wordAlign rounds up to the alignment of the records.
func (l layout) wordAlign(n int) int { return (n + l.align - 1) &^ (l.align - 1) }

// nextRecord returns the frame of the first record of b and the rest. A
// truncated record ends the buffer (ok false); a frame the snapshot cut
// short is skipped (rec nil, ok true).
func nextRecord(b []byte) (rec, rest []byte, ok bool) { return native.next(b) }

// next is nextRecord for a layout. The lengths are compared as they are
// read, unsigned, before any becomes an index.
func (l layout) next(b []byte) (rec, rest []byte, ok bool) {
	if len(b) < l.size {
		return nil, nil, false
	}
	caplen := binary.NativeEndian.Uint32(b[l.caplen:])
	datalen := binary.NativeEndian.Uint32(b[l.datalen:])
	start := int(binary.NativeEndian.Uint16(b[l.hdrlen:]))
	if start < l.floor() || start > len(b) || uint64(caplen) > uint64(len(b)-start) {
		return nil, nil, false
	}
	end := start + int(caplen)
	next := min(l.wordAlign(end), len(b))
	if caplen < datalen {
		return nil, b[next:], true
	}
	return b[start:end], b[next:], true
}

// WriteFrame implements l2.Port.
func (p *Port) WriteFrame(ctx context.Context, b []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	frame := b
	if len(frame) < ethernet.MinFrameLen {
		frame = ethernet.Pad(append(make([]byte, 0, ethernet.MinFrameLen), b...), 0)
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	select {
	case <-p.closed:
		return fmt.Errorf("bpf: %w", profinet.ErrClosed)
	default:
	}
	if _, err := syscall.Write(p.fd, frame); err != nil {
		if errors.Is(err, syscall.ENETDOWN) || errors.Is(err, syscall.ENXIO) {
			return fmt.Errorf("bpf: write %s: %w", p.iface.Name, profinet.ErrLinkDown)
		}
		return fmt.Errorf("bpf: write %s: %w", p.iface.Name, err)
	}
	return nil
}

// Close implements l2.Port. A read in progress returns within the read
// timeout. It is safe to call more than once and from multiple
// goroutines.
func (p *Port) Close() error {
	var err error
	p.once.Do(func() {
		close(p.closed)
		p.writeMu.Lock()
		// Wait for the reader so that the descriptor is not reused under it.
		p.readMu.Lock()
		err = syscall.Close(p.fd)
		p.readMu.Unlock()
		p.writeMu.Unlock()
	})
	return err
}
