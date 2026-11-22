// SPDX-License-Identifier: MIT

package gsdml_test

import (
	"testing"

	"github.com/otfabric/go-profinet/controller"
	"github.com/otfabric/go-profinet/gsdml"
)

// A RecordWrite converts to a controller.Parameter: the two keep the same
// fields, so a plan feeds DeviceConfig.Parameters without gsdml importing
// the controller.
func TestRecordWriteIsControllerParameter(t *testing.T) {
	t.Parallel()
	w := gsdml.RecordWrite{Slot: 1, Subslot: 2, Index: 3, Data: []byte{4}}
	p := controller.Parameter(w)
	if p.Slot != 1 || p.Subslot != 2 || p.Index != 3 || len(p.Data) != 1 || p.Data[0] != 4 {
		t.Fatalf("%+v", p)
	}
}
