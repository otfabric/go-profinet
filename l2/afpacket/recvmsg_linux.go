// SPDX-License-Identifier: MIT

//go:build linux && !386

package afpacket

import (
	"math"
	"syscall"
	"unsafe"
)

// recvmsg receives one frame into the port's message (p.msg, prepared by
// recv), without allocating. The kernel returns at most len(buf) octets
// and at most len(p.oob) octets of ancillary data; larger values are
// reported as EMSGSIZE rather than trusted.
func recvmsg(fd uintptr, p *Port, buf []byte) (n, oobn int, pkttype uint8, err error) {
	// unsafe: the raw recvmsg(2) passes the address of the port's msghdr,
	// which syscall.Recvmsg would allocate per call.
	r, _, e := syscall.Syscall(syscall.SYS_RECVMSG, fd, uintptr(unsafe.Pointer(&p.msg)), 0)
	if e != 0 {
		return 0, 0, 0, e
	}
	cl := p.msg.Controllen
	if r > math.MaxInt32 || cl > cmsgSpaceAux || int(r) > len(buf) {
		return 0, 0, 0, syscall.EMSGSIZE
	}
	return int(r), int(cl), p.from[sockaddrPkttypeOff], nil
}
