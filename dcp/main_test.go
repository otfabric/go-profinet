// SPDX-License-Identifier: MIT

package dcp

import (
	"testing"

	"github.com/otfabric/go-profinet/internal/leakcheck"
)

// TestMain fails the package's tests when they pass and leave a goroutine
// behind; the report names it.
func TestMain(m *testing.M) { leakcheck.Main(m) }
