// SPDX-License-Identifier: MIT

package pnio

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"github.com/otfabric/go-profinet"
)

var (
	sB = ValueStatusBad
	sU = ValueStatusUncertain
	sG = ValueStatusGood
)

// TestEmbeddedStatusGoldenVectors: the vectors of the layouts of IEC
// 61158-5-10:2023 5.3.1.6.8–5.3.1.6.10 and 5.3.2.2, as given with the
// evidence of the work package (and checked by hand against the layout).
func TestEmbeddedStatusGoldenVectors(t *testing.T) {
	for _, tc := range []struct {
		v    uint8
		s    ValueStatus
		want byte
	}{{0, sB, 0x00}, {0, sG, 0x03}, {1, sG, 0x07}, {42, sU, 0xAA}, {63, sG, 0xFF}} {
		got, err := EncodeUnsigned8S(tc.v, tc.s)
		if err != nil || got != tc.want {
			t.Errorf("Unsigned8_S %d/%v = %02x %v, want %02x", tc.v, tc.s, got, err, tc.want)
		}
		if v, s := DecodeUnsigned8S(tc.want); v != tc.v || s != tc.s {
			t.Errorf("decode %02x = %d/%v", tc.want, v, s)
		}
	}
	for _, tc := range []struct {
		v    uint16
		s    ValueStatus
		want [2]byte
	}{{1, sG, [2]byte{0x00, 0x07}}, {1000, sG, [2]byte{0x0F, 0xA3}}, {16383, sG, [2]byte{0xFF, 0xFF}}} {
		got, err := EncodeUnsigned16S(tc.v, tc.s)
		if err != nil || got != tc.want {
			t.Errorf("Unsigned16_S %d/%v = % x %v, want % x", tc.v, tc.s, got, err, tc.want)
		}
		if v, s := DecodeUnsigned16S(tc.want); v != tc.v || s != tc.s {
			t.Errorf("decode % x = %d/%v", tc.want, v, s)
		}
	}
	for _, tc := range []struct {
		v    int16
		s    ValueStatus
		want [2]byte
	}{{-8192, sB, [2]byte{0x80, 0x00}}, {-100, sG, [2]byte{0xFE, 0x73}}, {-1, sB, [2]byte{0xFF, 0xFC}}, {-1, sG, [2]byte{0xFF, 0xFF}}, {8191, sG, [2]byte{0x7F, 0xFF}}} {
		got, err := EncodeInteger16S(tc.v, tc.s)
		if err != nil || got != tc.want {
			t.Errorf("Integer16_S %d/%v = % x %v, want % x", tc.v, tc.s, got, err, tc.want)
		}
		if v, s := DecodeInteger16S(tc.want); v != tc.v || s != tc.s {
			t.Errorf("decode % x = %d/%v", tc.want, v, s)
		}
	}
	all := func(m int, s ValueStatus) []ValueStatus { return slices.Repeat([]ValueStatus{s}, 8*m) }
	only := func(m int, ch ...int) []ValueStatus {
		st := all(m, sB)
		for _, c := range ch {
			st[c-1] = sG
		}
		return st
	}
	for _, tc := range []struct {
		name   string
		values []byte
		status []ValueStatus
		want   []byte
	}{
		{"all off, all good", []byte{0x00}, all(1, sG), []byte{0x00, 0xFF, 0xFF}},
		{"all on, all good", []byte{0xFF}, all(1, sG), []byte{0xFF, 0xFF, 0xFF}},
		{"channel 1 on, all bad", []byte{0x01}, all(1, sB), []byte{0x01, 0x00, 0x00}},
		{"channel 1 on, only it good", []byte{0x01}, only(1, 1), []byte{0x01, 0x03, 0x00}},
		{"channel 8 on, only it good", []byte{0x80}, only(1, 8), []byte{0x80, 0x00, 0xC0}},
		{"16 channels, 1 and 16 good", []byte{0x01, 0x80}, only(2, 1, 16), []byte{0x01, 0x80, 0x03, 0x00, 0x00, 0xC0}},
	} {
		got, err := EncodeOctetStringS(nil, tc.values, tc.status)
		if err != nil || !bytes.Equal(got, tc.want) {
			t.Errorf("%s: % x %v, want % x", tc.name, got, err, tc.want)
		}
		v, st, err := DecodeOctetStringS(tc.want)
		if err != nil || !bytes.Equal(v, tc.values) || !slices.Equal(st, tc.status) {
			t.Errorf("%s: decode %x %v %v", tc.name, v, st, err)
		}
	}
}

// refBits is an independent model of the layouts: it writes the value and
// the status bit by bit, most significant first, into a field of width
// bits, without the shift-and-or of the codecs.
func refBits(value int64, status ValueStatus, width int) []byte {
	bits := make([]byte, 0, width)
	for i := width - 3; i >= 0; i-- {
		bits = append(bits, byte(uint64(value)>>uint(i)&1))
	}
	bits = append(bits, byte(status>>1&1), byte(status&1))
	out := make([]byte, width/8)
	for i, b := range bits {
		if b == 1 {
			out[i/8] |= 0x80 >> (i % 8)
		}
	}
	return out
}

// TestEmbeddedStatusExhaustive: every value and status of the three
// numeric types encodes as the independent bit model says and decodes back;
// every octet pattern decodes to a pair that encodes back to it.
func TestEmbeddedStatusExhaustive(t *testing.T) {
	for s := ValueStatusBad; s <= ValueStatusGood; s++ {
		for v := 0; v <= MaxUnsigned8S; v++ {
			got, err := EncodeUnsigned8S(uint8(v), s)
			if err != nil || got != refBits(int64(v), s, 8)[0] {
				t.Fatalf("Unsigned8_S %d/%v: %02x %v", v, s, got, err)
			}
		}
		for v := 0; v <= MaxUnsigned16S; v++ {
			got, err := EncodeUnsigned16S(uint16(v), s)
			if err != nil || !bytes.Equal(got[:], refBits(int64(v), s, 16)) {
				t.Fatalf("Unsigned16_S %d/%v: % x %v", v, s, got, err)
			}
		}
		for v := MinInteger16S; v <= MaxInteger16S; v++ {
			got, err := EncodeInteger16S(int16(v), s)
			if err != nil || !bytes.Equal(got[:], refBits(int64(v), s, 16)) {
				t.Fatalf("Integer16_S %d/%v: % x %v", v, s, got, err)
			}
		}
	}
	for b := range 256 {
		v, s := DecodeUnsigned8S(byte(b))
		if e, err := EncodeUnsigned8S(v, s); err != nil || e != byte(b) {
			t.Fatalf("Unsigned8_S %02x round trip: %02x %v", b, e, err)
		}
	}
	for raw := range 1 << 16 {
		b := [2]byte{byte(raw >> 8), byte(raw)}
		uv, us := DecodeUnsigned16S(b)
		iv, is := DecodeInteger16S(b)
		e1, err1 := EncodeUnsigned16S(uv, us)
		e2, err2 := EncodeInteger16S(iv, is)
		if err1 != nil || err2 != nil || e1 != b || e2 != b {
			t.Fatalf("% x round trip: % x %v, % x %v", b, e1, err1, e2, err2)
		}
	}
}

// TestEmbeddedStatusBounds: values outside the range and statuses above 3
// are refused, not masked into a valid-looking octet.
func TestEmbeddedStatusBounds(t *testing.T) {
	if _, err := EncodeUnsigned8S(64, sG); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Errorf("Unsigned8_S 64: %v", err)
	}
	if _, err := EncodeUnsigned8S(0, 4); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Errorf("Unsigned8_S status 4: %v", err)
	}
	if _, err := EncodeUnsigned16S(16384, sG); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Errorf("Unsigned16_S 16384: %v", err)
	}
	if _, err := EncodeUnsigned16S(0, 255); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Errorf("Unsigned16_S status 255: %v", err)
	}
	for _, v := range []int16{-8193, 8192, -32768, 32767} {
		if _, err := EncodeInteger16S(v, sG); !errors.Is(err, profinet.ErrInvalidConfig) {
			t.Errorf("Integer16_S %d: %v", v, err)
		}
	}
	if _, err := EncodeInteger16S(0, 4); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Errorf("Integer16_S status 4: %v", err)
	}
	if ValueStatusGood.String() != "G" || ValueStatusSimulation.String() != "S" || ValueStatus(7).String() != "ValueStatus(7)" {
		t.Errorf("String: %v %v %v", ValueStatusGood, ValueStatusSimulation, ValueStatus(7))
	}
}

// TestOctetStringSInvalid: wrong lengths and statuses are refused, and an
// encoder error leaves dst as it was.
func TestOctetStringSInvalid(t *testing.T) {
	dst := []byte{0xAB}
	for _, tc := range []struct {
		values []byte
		status []ValueStatus
	}{
		{nil, nil},
		{[]byte{0}, make([]ValueStatus, 7)},
		{[]byte{0}, make([]ValueStatus, 9)},
		{[]byte{0}, append(make([]ValueStatus, 7), 4)},
		{make([]byte, MaxOctetStringSValues+1), make([]ValueStatus, 8*(MaxOctetStringSValues+1))},
	} {
		got, err := EncodeOctetStringS(dst, tc.values, tc.status)
		if !errors.Is(err, profinet.ErrInvalidConfig) || !bytes.Equal(got, []byte{0xAB}) {
			t.Errorf("%d values, %d statuses: % x %v", len(tc.values), len(tc.status), got, err)
		}
	}
	for _, n := range []int{0, 1, 2, 4, 5, 7} {
		if _, _, err := DecodeOctetStringS(make([]byte, n)); !errors.Is(err, profinet.ErrMalformed) {
			t.Errorf("%d octets: %v", n, err)
		}
	}
	if _, _, err := DecodeOctetStringS(make([]byte, 3*MaxOctetStringSValues+3)); !errors.Is(err, profinet.ErrResourceExhausted) {
		t.Errorf("too long: %v", err)
	}
	// The largest one is accepted, and the value octets are a copy.
	b := make([]byte, 3*MaxOctetStringSValues)
	v, st, err := DecodeOctetStringS(b)
	if err != nil || len(v) != MaxOctetStringSValues || len(st) != 8*MaxOctetStringSValues {
		t.Fatalf("largest: %d %d %v", len(v), len(st), err)
	}
	v[0] = 1
	if b[0] != 0 {
		t.Fatal("the value octets alias the input")
	}
	// It appends.
	if got, err := EncodeOctetStringS([]byte{0xAB}, []byte{0x01}, make([]ValueStatus, 8)); err != nil || !bytes.Equal(got, []byte{0xAB, 0x01, 0x00, 0x00}) {
		t.Fatalf("append: % x %v", got, err)
	}
}

// FuzzOctetStringS: every input either fails, with a sentinel, or decodes
// to values and statuses that encode back to the same octets.
func FuzzOctetStringS(f *testing.F) {
	for _, s := range [][]byte{{0x00, 0xFF, 0xFF}, {0x01, 0x80, 0x03, 0x00, 0x00, 0xC0}, {}, {1, 2}, make([]byte, 30)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		v, st, err := DecodeOctetStringS(b)
		if err != nil {
			if !errors.Is(err, profinet.ErrMalformed) && !errors.Is(err, profinet.ErrResourceExhausted) {
				t.Fatalf("error %v matches no sentinel", err)
			}
			return
		}
		back, err := EncodeOctetStringS(nil, v, st)
		if err != nil || !bytes.Equal(back, b) {
			t.Fatalf("round trip of % x: % x %v", b, back, err)
		}
	})
}
