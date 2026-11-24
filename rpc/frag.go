// SPDX-License-Identifier: MIT

package rpc

import (
	"fmt"
	"math"

	"github.com/otfabric/go-profinet"
)

// DefaultMaxFragmentBody is the largest body of one packet this package
// sends: a UDP datagram of 1464 octets (header included), which fits an
// Ethernet frame without IP fragmentation and matches what PROFINET
// devices accept.
const DefaultMaxFragmentBody = 1464 - HeaderLen

// DefaultMaxMessage bounds a reassembled request or response body.
const DefaultMaxMessage = 64 * 1024

// MaxFragments bounds the fragments of one message.
const MaxFragments = 256

// maxFragmentBodyLimit is the largest fragment body: a UDP datagram of
// 65507 octets, the largest IPv4 carries, less the packet header. A
// MaxFragmentBody above it is reduced to it, since the 16-bit fragment
// length of the header cannot describe more.
const maxFragmentBodyLimit = 65507 - HeaderLen

// fragmentBody returns the fragment body size for the option value n.
func fragmentBody(n int) int {
	if n <= 0 {
		return DefaultMaxFragmentBody
	}
	return min(n, maxFragmentBodyLimit)
}

// bodyLen returns the length n of a packet body, which its callers bound
// to at most maxFragmentBodyLimit octets, as the 16-bit header field.
func bodyLen(n int) uint16 {
	if n < 0 || n > math.MaxUint16 {
		return math.MaxUint16 // not reached: callers bound n
	}
	return uint16(n)
}

// single encodes the one packet of a body that is never fragmented: an
// acknowledgement, a FACK, a fault or a reject.
func single(h Header, body []byte) []byte {
	h.BodyLen = bodyLen(len(body))
	b := h.AppendTo(make([]byte, 0, HeaderLen+len(body)))
	return append(b, body...)
}

// packets splits a body into the packets of one request or response,
// derived from the template header h. A body that needs more fragments
// than the 16-bit fragment number counts is refused.
func packets(h Header, body []byte, maxFrag int) ([][]byte, error) {
	maxFrag = fragmentBody(maxFrag)
	n := max((len(body)+maxFrag-1)/maxFrag, 1)
	if n-1 > math.MaxUint16 {
		return nil, fmt.Errorf("rpc: %w: body of %d octets needs %d fragments of %d octets, more than %d", profinet.ErrInvalidConfig, len(body), n, maxFrag, math.MaxUint16+1)
	}
	out := make([][]byte, 0, n)
	var frag uint16 // i as the fragment number: n-1 fits, checked above
	for i := 0; i < n; i++ {
		lo := i * maxFrag
		hi := min(lo+maxFrag, len(body))
		p := h
		p.FragNum = frag
		frag++
		if n > 1 {
			p.Flags1 |= FlagFrag | FlagNoFack
			if i == n-1 {
				p.Flags1 |= FlagLastFrag
			}
		}
		out = append(out, single(p, body[lo:hi]))
	}
	return out, nil
}

// reassembly collects the fragments of one message.
type reassembly struct {
	frags [][]byte
	have  int
	last  int // index of the last fragment, -1 until seen
	size  int
	max   int
	// drep is the integer representation (DRep[0]&0xF0) of the first
	// fragment; hasDRep is set once a fragment arrived.
	drep    uint8
	hasDRep bool
}

func newReassembly(maxMessage int) *reassembly {
	if maxMessage <= 0 {
		maxMessage = DefaultMaxMessage
	}
	return &reassembly{last: -1, max: maxMessage}
}

// add adds a fragment. It returns the complete body when every fragment
// has arrived.
func (r *reassembly) add(h *Header, body []byte) ([]byte, bool, error) {
	if h.Flags1&FlagFrag == 0 {
		return append([]byte(nil), body...), true, nil
	}
	// The fragments carry one body marshalled in one representation: a
	// fragment in another byte order cannot belong to it. p-net refuses
	// such a call too (pf_cmrpc.c, rtlabs-com/p-net v2.0.0), and its issue
	// 2275 was a device that changed the byte order after the first
	// fragment of a response.
	if order := h.DRep[0] & 0xF0; !r.hasDRep {
		r.drep, r.hasDRep = order, true
	} else if order != r.drep {
		return nil, false, fmt.Errorf("rpc: %w: fragment %d in another byte order (DRep %#02x, first %#02x)", profinet.ErrMalformed, h.FragNum, order, r.drep)
	}
	n := int(h.FragNum)
	if n >= MaxFragments {
		return nil, false, fmt.Errorf("rpc: %w: fragment number %d", profinet.ErrResourceExhausted, n)
	}
	if r.last >= 0 && n > r.last {
		return nil, false, fmt.Errorf("rpc: %w: fragment %d after the last fragment %d", profinet.ErrMalformed, n, r.last)
	}
	if h.Flags1&FlagLastFrag != 0 {
		if r.last >= 0 && r.last != n {
			return nil, false, fmt.Errorf("rpc: %w: two last fragments", profinet.ErrMalformed)
		}
		if len(r.frags) > n+1 {
			return nil, false, fmt.Errorf("rpc: %w: last fragment %d below received fragments", profinet.ErrMalformed, n)
		}
		r.last = n
	}
	for len(r.frags) <= n {
		r.frags = append(r.frags, nil)
	}
	if r.frags[n] != nil {
		return nil, false, nil // duplicate
	}
	if r.size+len(body) > r.max {
		return nil, false, fmt.Errorf("rpc: %w: message exceeds %d octets", profinet.ErrResourceExhausted, r.max)
	}
	r.frags[n] = append(make([]byte, 0, len(body)+1), body...)
	if len(body) == 0 {
		r.frags[n] = []byte{}
	}
	r.have++
	r.size += len(body)
	if r.last < 0 || r.have != r.last+1 {
		return nil, false, nil
	}
	out := make([]byte, 0, r.size)
	for _, f := range r.frags {
		out = append(out, f...)
	}
	return out, true, nil
}
