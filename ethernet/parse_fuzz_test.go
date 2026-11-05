// SPDX-License-Identifier: MIT

package ethernet

import (
	"errors"
	"testing"
)

// FuzzParseMAC: ParseMAC either fails with ErrInvalidMAC or returns an
// address whose String parses back to it.
func FuzzParseMAC(f *testing.F) {
	for _, s := range []string{"00:0e:cf:01:02:03", "00-0E-CF-01-02-03", "000ecf010203", "", ":::::", "0:0:0:0:0:0", "+0:00:00:00:00:00", "00:00:00:00:00:00:00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		m, err := ParseMAC(s)
		if err != nil {
			if !errors.Is(err, ErrInvalidMAC) {
				t.Fatalf("error %v is not ErrInvalidMAC", err)
			}
			return
		}
		again, err := ParseMAC(m.String())
		if err != nil || again != m {
			t.Fatalf("%q: %s does not parse back: %v", s, m, err)
		}
	})
}
