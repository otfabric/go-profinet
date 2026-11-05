// SPDX-License-Identifier: MIT

package rpc

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/otfabric/go-profinet"
)

// FuzzParseUUID: ParseUUID either fails with ErrInvalidConfig or returns
// a UUID whose String parses back to it, and that survives encoding in
// both byte orders.
func FuzzParseUUID(f *testing.F) {
	for _, s := range []string{"dea00001-6c97-11d1-8271-00a02442df7d", "DEA00001-6C97-11D1-8271-00A02442DF7D", "00000000-0000-0000-0000-000000000000",
		"dea00001-6c97-11d1-8271-00a02442df7", "dea00001-6c97-11d1-8271--0a02442df7d", "dea00001x6c97x11d1x8271x00a02442df7d", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		u, err := ParseUUID(s)
		if err != nil {
			if !errors.Is(err, profinet.ErrInvalidConfig) {
				t.Fatalf("error %v is not ErrInvalidConfig", err)
			}
			return
		}
		if again, err := ParseUUID(u.String()); err != nil || again != u {
			t.Fatalf("%q: %s does not parse back: %v", s, u, err)
		}
		var v UUID
		if err := v.UnmarshalText([]byte(s)); err != nil || v != u {
			t.Fatalf("UnmarshalText %q: %v", s, err)
		}
		for _, o := range []ByteOrder{binary.LittleEndian, binary.BigEndian} {
			b := u.Append(nil, o)
			if len(b) != 16 || DecodeUUID(b, o) != u {
				t.Fatalf("%s: encoding round trip", u)
			}
		}
	})
}
