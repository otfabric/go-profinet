// SPDX-License-Identifier: MIT

package rt

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/internal/leakcheck"
	"github.com/otfabric/go-profinet/l2"
	"github.com/otfabric/go-profinet/vnet"
)

// TestCloseConcurrent: Close of a dispatcher and of a scheduler from
// several goroutines at once (contract row L2); when Close has returned
// no task runs any more.
func TestCloseConcurrent(t *testing.T) {
	n := vnet.New(1)
	defer func() { _ = n.Close() }()
	ep, err := n.NewEndpoint(vnet.EndpointConfig{Name: "b", MAC: macB})
	if err != nil {
		t.Fatal(err)
	}
	mux := l2.NewMux(ep)
	defer func() { _ = mux.Close() }()
	d, err := NewDispatcher(mux)
	if err != nil {
		t.Fatal(err)
	}
	leakcheck.Together(8, func(int) { d.Close() })
	select {
	case <-d.done:
	default:
		t.Fatal("the dispatcher runs after Close")
	}

	s := NewScheduler()
	var runs atomic.Int64
	s.Add(time.Millisecond, func(time.Time) { runs.Add(1) })
	eventually(t, "the task to run", func() bool { return runs.Load() > 0 })
	leakcheck.Together(8, func(int) { s.Close() })
	select {
	case <-s.exited:
	default:
		t.Fatal("the scheduler runs after Close")
	}
	after := runs.Load()
	s.Close()
	if runs.Load() != after {
		t.Fatal("a task ran after Close returned")
	}
}
