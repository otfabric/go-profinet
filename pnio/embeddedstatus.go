// SPDX-License-Identifier: MIT

package pnio

import (
	"fmt"
	"strconv"

	"github.com/otfabric/go-profinet"
)

// The binary layouts of the data types with an embedded status, from
// IEC 61158-5-10:2023 (Edition 5.0), Application layer service definition,
// Type 10 elements: Unsigned16_S 5.3.1.6.8 (Tables 20 and 21), Integer16_S
// 5.3.1.6.9 (Tables 22 and 23), Unsigned8_S 5.3.1.6.10 (Tables 24 and 25),
// OctetString_S 5.3.2.2 (Tables 26 and 27). The same layouts are in "RIO
// for Process Automation", PNO Order No. 3.132, V1.0 (2003), 4.2.1, Tables
// 8 and 9, and the status codes in OPC 30144 v1.00, 6.5, Table 17. The IEC
// text was read in its indexed form; its PDF was not available
// (LIMITATIONS.md).
//
// These are wire codecs only. How a GSDML DefaultValue writes such a value
// and its status is not established, so the gsdml package does not use
// them for Ref defaults (docs/GSDML.md). Integer32_S and Unsigned32_S have
// no layout here: no source this package has defines them.

// ValueStatus is the two-bit status of a value with an embedded status.
// IEC 61158-5-10 defines the four codes for input channels and marks the
// corresponding output-channel values reserved; the codecs here do not
// judge which one a context allows.
type ValueStatus uint8

// The status codes (IEC 61158-5-10:2023 Tables 21, 23, 25, 27; OPC 30144
// Table 17).
const (
	ValueStatusBad        ValueStatus = 0 // B
	ValueStatusSimulation ValueStatus = 1 // S
	ValueStatusUncertain  ValueStatus = 2 // U
	ValueStatusGood       ValueStatus = 3 // G
)

// String returns the code's letter: B, S, U or G.
func (s ValueStatus) String() string {
	if s <= ValueStatusGood {
		return string("BSUG"[s])
	}
	return "ValueStatus(" + strconv.Itoa(int(s)) + ")"
}

// The value ranges: the value takes the bits above the two status bits.
const (
	MaxUnsigned8S  = 63
	MaxUnsigned16S = 16383
	MinInteger16S  = -8192
	MaxInteger16S  = 8191
)

// MaxOctetStringSValues is the most value octets EncodeOctetStringS and
// DecodeOctetStringS take: 21845, so that the 3·m octets fit the 65535 of
// a record. A larger one is refused before anything is allocated.
const MaxOctetStringSValues = 21845

func checkStatus(s ValueStatus) error {
	if s > ValueStatusGood {
		return fmt.Errorf("pnio: %w: value status %d (0 to 3)", profinet.ErrInvalidConfig, s)
	}
	return nil
}

// EncodeUnsigned8S returns the octet of an Unsigned8_S: the value in bits
// 7 to 2, the status in bits 1 and 0. A value above 63 or a status above 3
// is refused with profinet.ErrInvalidConfig, not masked.
func EncodeUnsigned8S(v uint8, s ValueStatus) (byte, error) {
	if v > MaxUnsigned8S {
		return 0, fmt.Errorf("pnio: %w: Unsigned8_S value %d (0 to %d)", profinet.ErrInvalidConfig, v, MaxUnsigned8S)
	}
	if err := checkStatus(s); err != nil {
		return 0, err
	}
	return v<<2 | byte(s), nil
}

// DecodeUnsigned8S returns the value and the status of an Unsigned8_S.
// Every octet is one.
func DecodeUnsigned8S(b byte) (uint8, ValueStatus) {
	return b >> 2, ValueStatus(b & 3)
}

// EncodeUnsigned16S returns the two octets, big endian, of an
// Unsigned16_S: the value in bits 15 to 2, the status in bits 1 and 0. A
// value above 16383 or a status above 3 is refused with
// profinet.ErrInvalidConfig.
func EncodeUnsigned16S(v uint16, s ValueStatus) ([2]byte, error) {
	if v > MaxUnsigned16S {
		return [2]byte{}, fmt.Errorf("pnio: %w: Unsigned16_S value %d (0 to %d)", profinet.ErrInvalidConfig, v, MaxUnsigned16S)
	}
	if err := checkStatus(s); err != nil {
		return [2]byte{}, err
	}
	raw := v<<2 | uint16(s)
	return [2]byte{byte(raw >> 8), byte(raw)}, nil
}

// DecodeUnsigned16S returns the value and the status of an Unsigned16_S.
func DecodeUnsigned16S(b [2]byte) (uint16, ValueStatus) {
	raw := uint16(b[0])<<8 | uint16(b[1])
	return raw >> 2, ValueStatus(raw & 3)
}

// EncodeInteger16S returns the two octets, big endian, of an Integer16_S:
// the value as a 14-bit two's complement number in bits 15 to 2, the
// status in bits 1 and 0. A value outside −8192 to 8191 or a status above 3
// is refused with profinet.ErrInvalidConfig.
func EncodeInteger16S(v int16, s ValueStatus) ([2]byte, error) {
	if v < MinInteger16S || v > MaxInteger16S {
		return [2]byte{}, fmt.Errorf("pnio: %w: Integer16_S value %d (%d to %d)", profinet.ErrInvalidConfig, v, MinInteger16S, MaxInteger16S)
	}
	if err := checkStatus(s); err != nil {
		return [2]byte{}, err
	}
	raw := uint16(v)<<2 | uint16(s)
	return [2]byte{byte(raw >> 8), byte(raw)}, nil
}

// DecodeInteger16S returns the value and the status of an Integer16_S; the
// arithmetic shift extends the sign of the 14-bit value.
func DecodeInteger16S(b [2]byte) (int16, ValueStatus) {
	raw := int16(uint16(b[0])<<8 | uint16(b[1]))
	return raw >> 2, ValueStatus(raw & 3)
}

// EncodeOctetStringS appends an OctetString_S of m value octets to dst:
// the m octets of the 8·m binary channels (channel 1 is bit 0 of the first
// octet), then their 8·m two-bit statuses in 2·m octets (the status of
// channel 1 in bits 1 and 0 of the first status octet, each next channel's
// in the next two bits), 3·m octets in all; values and statuses are not
// interleaved. status holds one entry per channel, 8·len(values); values
// must not be empty or longer than MaxOctetStringSValues, and every status
// must be 0 to 3, or the result is profinet.ErrInvalidConfig and dst is
// returned as it was.
func EncodeOctetStringS(dst, values []byte, status []ValueStatus) ([]byte, error) {
	m := len(values)
	if m == 0 || m > MaxOctetStringSValues {
		return dst, fmt.Errorf("pnio: %w: OctetString_S of %d value octets (1 to %d)", profinet.ErrInvalidConfig, m, MaxOctetStringSValues)
	}
	if len(status) != 8*m {
		return dst, fmt.Errorf("pnio: %w: OctetString_S of %d value octets needs %d statuses, not %d", profinet.ErrInvalidConfig, m, 8*m, len(status))
	}
	for i, s := range status {
		if s > ValueStatusGood {
			return dst, fmt.Errorf("pnio: %w: OctetString_S channel %d: value status %d (0 to 3)", profinet.ErrInvalidConfig, i+1, s)
		}
	}
	dst = append(dst, values...)
	start := len(dst)
	dst = append(dst, make([]byte, 2*m)...)
	for i, s := range status {
		dst[start+i/4] |= byte(s) << (2 * (i % 4))
	}
	return dst, nil
}

// DecodeOctetStringS returns the value octets and the channel statuses
// (8 per value octet) of an OctetString_S. b must be 3·m octets for some m
// from 1 to MaxOctetStringSValues: an empty b or one whose length is no
// multiple of three is profinet.ErrMalformed, a longer one
// profinet.ErrResourceExhausted, before anything is allocated. The value
// octets are a copy.
func DecodeOctetStringS(b []byte) ([]byte, []ValueStatus, error) {
	if len(b) > 3*MaxOctetStringSValues {
		return nil, nil, fmt.Errorf("pnio: %w: OctetString_S of %d octets (at most %d)", profinet.ErrResourceExhausted, len(b), 3*MaxOctetStringSValues)
	}
	if len(b) == 0 || len(b)%3 != 0 {
		return nil, nil, fmt.Errorf("pnio: %w: OctetString_S of %d octets (a multiple of 3)", profinet.ErrMalformed, len(b))
	}
	m := len(b) / 3
	values := append([]byte(nil), b[:m]...)
	status := make([]ValueStatus, 8*m)
	for i := range status {
		status[i] = ValueStatus(b[m+i/4] >> (2 * (i % 4)) & 3)
	}
	return values, status, nil
}
