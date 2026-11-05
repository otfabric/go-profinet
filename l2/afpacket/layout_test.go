// SPDX-License-Identifier: MIT

//go:build linux

package afpacket

import (
	"encoding/binary"
	"errors"
	"math"
	"syscall"
	"testing"
	"unsafe"

	"github.com/otfabric/go-profinet"
)

// The offsets the port decodes and encodes with encoding/binary are those
// of the kernel structures as package syscall declares them.
func TestLayouts(t *testing.T) {
	var sll syscall.RawSockaddrLinklayer
	var h syscall.Cmsghdr
	for name, c := range map[string][2]uintptr{
		"sll_pkttype": {unsafe.Offsetof(sll.Pkttype), sockaddrPkttypeOff},
		"cmsg_level":  {unsafe.Offsetof(h.Level), cmsgLevelOff},
		"cmsg_type":   {unsafe.Offsetof(h.Type), cmsgTypeOff},
		"cmsg_len":    {unsafe.Sizeof(h.Len), cmsgLevelOff},
	} {
		if c[0] != c[1] {
			t.Errorf("%s at %d, decoded at %d", name, c[0], c[1])
		}
	}
}

// auxCmsg is a SOL_PACKET/PACKET_AUXDATA control message.
func auxCmsg(status uint32, tci, tpid uint16) []byte {
	b := make([]byte, syscall.CmsgSpace(sizeofAuxdata))
	h := (*syscall.Cmsghdr)(unsafe.Pointer(&b[0]))
	h.Level, h.Type = syscall.SOL_PACKET, packetAuxdata
	h.SetLen(syscall.CmsgLen(sizeofAuxdata))
	data := b[syscall.CmsgLen(0):]
	binary.NativeEndian.PutUint32(data[auxStatusOff:], status)
	binary.NativeEndian.PutUint16(data[auxVLANTCIOff:], tci)
	binary.NativeEndian.PutUint16(data[auxVLANTPIDOff:], tpid)
	return b
}

func TestVLANFromAux(t *testing.T) {
	if tci, tpid, ok := vlanFromAux(auxCmsg(tpStatusVLANValid, 0xC005, 0)); !ok || tci != 0xC005 || tpid != 0x8100 {
		t.Fatalf("%x %x %v", tci, tpid, ok)
	}
	if tci, tpid, ok := vlanFromAux(auxCmsg(tpStatusVLANValid|tpStatusVLANTPIDValid, 7, 0x88A8)); !ok || tci != 7 || tpid != 0x88A8 {
		t.Fatalf("%x %x %v", tci, tpid, ok)
	}
	if _, _, ok := vlanFromAux(auxCmsg(0, 7, 0)); ok {
		t.Fatal("untagged frame")
	}
	// Another control message first, then the auxiliary data.
	other := make([]byte, syscall.CmsgSpace(4))
	oh := (*syscall.Cmsghdr)(unsafe.Pointer(&other[0]))
	oh.Level, oh.Type = syscall.SOL_SOCKET, syscall.SCM_RIGHTS
	oh.SetLen(syscall.CmsgLen(4))
	if tci, _, ok := vlanFromAux(append(other, auxCmsg(tpStatusVLANValid, 9, 0)...)); !ok || tci != 9 {
		t.Fatalf("%x %v", tci, ok)
	}
	if _, _, ok := vlanFromAux(other); ok {
		t.Fatal("no auxiliary data")
	}
	// A cmsg_len beyond the buffer, or beyond what fits an int on any
	// platform, is not followed.
	for _, l := range []uint64{1 << 40, cmsgSpaceAux + 1, uint64(syscall.CmsgSpace(sizeofAuxdata)) + 1, 1} {
		b := auxCmsg(tpStatusVLANValid, 1, 0)
		(*syscall.Cmsghdr)(unsafe.Pointer(&b[0])).Len = 0
		if cmsgLevelOff == 8 {
			binary.NativeEndian.PutUint64(b, l)
		} else {
			binary.NativeEndian.PutUint32(b, uint32(l))
		}
		if _, _, ok := vlanFromAux(b); ok {
			t.Errorf("cmsg_len %d followed", l)
		}
	}
}

func TestPacketMreq(t *testing.T) {
	m, err := packetMreq(3, syscall.PACKET_MR_MULTICAST, []byte{1, 0x0E, 0xCF, 0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	var want struct {
		Ifindex int32
		Type    uint16
		Alen    uint16
		Address [8]byte
	}
	if unsafe.Sizeof(want) != sizeofMreq {
		t.Fatalf("packet_mreq is %d octets", unsafe.Sizeof(want))
	}
	want.Ifindex, want.Type, want.Alen = 3, syscall.PACKET_MR_MULTICAST, 6
	copy(want.Address[:], []byte{1, 0x0E, 0xCF})
	if raw := (*[sizeofMreq]byte)(unsafe.Pointer(&want)); *raw != m {
		t.Fatalf("% x, want % x", m, raw[:])
	}
	cases := []struct {
		idx  int
		addr []byte
	}{{-1, nil}, {1, make([]byte, 9)}}
	if math.MaxInt > math.MaxInt32 {
		cases = append(cases, struct {
			idx  int
			addr []byte
		}{math.MaxInt, nil})
	}
	for _, c := range cases {
		if _, err := packetMreq(c.idx, 0, c.addr); !errors.Is(err, profinet.ErrInvalidConfig) {
			t.Errorf("%d/%d: %v", c.idx, len(c.addr), err)
		}
	}
}
