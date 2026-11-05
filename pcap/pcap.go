// SPDX-License-Identifier: MIT

// Package pcap writes and reads capture files in the classic libpcap
// format with the Ethernet link type, so that traffic of the virtual
// network (and of the real backends) can be inspected with Wireshark or
// tshark.
package pcap

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sync"
	"time"

	"github.com/otfabric/go-profinet"
)

const (
	magicMicro   = 0xA1B2C3D4
	linkEthernet = 1
	// SnapLen is the largest frame stored.
	SnapLen = 65535
)

// Writer writes frames to a pcap file. It is safe for concurrent use.
type Writer struct {
	mu sync.Mutex
	w  io.Writer
}

// NewWriter writes the file header to w.
func NewWriter(w io.Writer) (*Writer, error) {
	h := make([]byte, 24)
	binary.LittleEndian.PutUint32(h[0:], magicMicro)
	binary.LittleEndian.PutUint16(h[4:], 2)
	binary.LittleEndian.PutUint16(h[6:], 4)
	binary.LittleEndian.PutUint32(h[16:], SnapLen)
	binary.LittleEndian.PutUint32(h[20:], linkEthernet)
	if _, err := w.Write(h); err != nil {
		return nil, err
	}
	return &Writer{w: w}, nil
}

// WriteFrame writes one Ethernet frame captured at t. The classic format
// stores the seconds as an unsigned 32-bit count since 1970, so a t before
// 1970 or after 2106-02-07 06:28:15 UTC, and a frame of 2^32 octets or
// more, are refused with [profinet.ErrInvalidConfig] instead of being
// written with a wrapped value.
func (w *Writer) WriteFrame(t time.Time, frame []byte) error {
	sec := t.Unix()
	if sec < 0 || sec > math.MaxUint32 {
		return fmt.Errorf("pcap: %w: timestamp %v outside the classic format's range", profinet.ErrInvalidConfig, t)
	}
	orig := len(frame)
	if int64(orig) > math.MaxUint32 {
		return fmt.Errorf("pcap: %w: frame of %d octets", profinet.ErrInvalidConfig, orig)
	}
	n := min(orig, SnapLen)
	h := make([]byte, 16, 16+n)
	binary.LittleEndian.PutUint32(h[0:], uint32(sec))
	// Nanosecond is below 10^9, so the microseconds fit 20 bits.
	binary.LittleEndian.PutUint32(h[4:], uint32((t.Nanosecond()/1000)&0xFFFFF))
	binary.LittleEndian.PutUint32(h[8:], uint32(n&0xFFFF))
	binary.LittleEndian.PutUint32(h[12:], uint32(orig))
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := w.w.Write(append(h, frame[:n]...))
	return err
}

// Reader reads frames from a pcap file with the Ethernet link type.
type Reader struct {
	r     io.Reader
	order binary.ByteOrder
	nano  bool
}

// NewReader reads and checks the file header.
func NewReader(r io.Reader) (*Reader, error) {
	h := make([]byte, 24)
	if _, err := io.ReadFull(r, h); err != nil {
		return nil, fmt.Errorf("pcap: %w: header: %v", profinet.ErrMalformed, err)
	}
	rd := &Reader{r: r}
	switch {
	case binary.LittleEndian.Uint32(h) == magicMicro:
		rd.order = binary.LittleEndian
	case binary.BigEndian.Uint32(h) == magicMicro:
		rd.order = binary.BigEndian
	case binary.LittleEndian.Uint32(h) == 0xA1B23C4D:
		rd.order, rd.nano = binary.LittleEndian, true
	case binary.BigEndian.Uint32(h) == 0xA1B23C4D:
		rd.order, rd.nano = binary.BigEndian, true
	default:
		return nil, fmt.Errorf("pcap: %w: not a pcap file", profinet.ErrMalformed)
	}
	if lt := rd.order.Uint32(h[20:]); lt != linkEthernet {
		return nil, fmt.Errorf("pcap: %w: link type %d", profinet.ErrUnsupported, lt)
	}
	return rd, nil
}

// Next returns the next frame and its timestamp; io.EOF at the end.
func (r *Reader) Next() (time.Time, []byte, error) {
	h := make([]byte, 16)
	if _, err := io.ReadFull(r.r, h); err != nil {
		if err == io.ErrUnexpectedEOF {
			return time.Time{}, nil, fmt.Errorf("pcap: %w: truncated record header", profinet.ErrMalformed)
		}
		return time.Time{}, nil, err
	}
	n := r.order.Uint32(h[8:])
	if n > SnapLen {
		return time.Time{}, nil, fmt.Errorf("pcap: %w: record of %d octets", profinet.ErrMalformed, n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r.r, b); err != nil {
		return time.Time{}, nil, fmt.Errorf("pcap: %w: truncated record", profinet.ErrMalformed)
	}
	sub := int64(r.order.Uint32(h[4:]))
	if !r.nano {
		sub *= 1000
	}
	return time.Unix(int64(r.order.Uint32(h[0:])), sub), b, nil
}
