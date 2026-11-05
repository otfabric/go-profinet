// SPDX-License-Identifier: MIT

package gsdml

import (
	"errors"
	"slices"
	"testing"

	"github.com/otfabric/go-profinet"
)

// FuzzParseValueList: a ValueList from a GSDML file (untrusted input)
// never expands beyond the limit, a failure is ErrMalformed or
// ErrResourceExhausted, and a parsed list survives FormatValueList.
func FuzzParseValueList(f *testing.F) {
	for _, s := range []string{"1..4 6", "0..4294967295", "4294967295", "1 2 3", "5..1", "..", "1..", "-1", " ", "1..4 1..4"} {
		f.Add(s, 16)
	}
	f.Add("0..4294967295", 1<<20)
	f.Fuzz(func(t *testing.T, s string, limit int) {
		limit %= 1 << 16
		vals, err := ParseValueList(s, limit)
		if err != nil {
			if !errors.Is(err, profinet.ErrMalformed) && !errors.Is(err, profinet.ErrResourceExhausted) {
				t.Fatalf("error %v", err)
			}
			return
		}
		if len(vals) > max(limit, 0) {
			t.Fatalf("%d values, limit %d", len(vals), limit)
		}
		// FormatValueList joins consecutive values; parsing it gives the
		// same values when they are ascending without repeats.
		if slices.IsSorted(vals) && len(slices.Compact(slices.Clone(vals))) == len(vals) {
			again, err := ParseValueList(FormatValueList(vals), max(limit, 1))
			if err != nil || !slices.Equal(again, vals) {
				t.Fatalf("%q: format round trip %v, %v", s, again, err)
			}
		}
	})
}
