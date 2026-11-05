// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/mrp"
	"github.com/otfabric/go-profinet/pnio"
	"github.com/otfabric/go-profinet/rpc"
)

// An RPCPort outside 0..65535 used to be accepted (by a ListenPacket
// that does not parse the port itself) and truncated into the
// ARRPCBlockReq, so the device answered on another port.
func TestNewRejectsRPCPortOutOfRange(t *testing.T) {
	n := newNet(t)
	nd := n.node(ctrlIP)
	lp := func(ctx context.Context, network, _ string) (net.PacketConn, error) {
		return nd.stack.ListenPacket(ctx, network, ":0")
	}
	for _, port := range []int{-1, math.MaxUint16 + 1, math.MaxUint16 + 1 + 34964} {
		c, err := New(Config{Mux: nd.mux, ListenPacket: lp, RPCPort: port})
		if !errors.Is(err, profinet.ErrInvalidConfig) {
			if c != nil {
				_ = c.Close()
			}
			t.Fatalf("RPCPort %d: err %v", port, err)
		}
	}
}

// A ring port number beyond the subslot range used to wrap onto a port
// of the model (65537 became port 1).
func TestMRPRingPortOutOfRange(t *testing.T) {
	m := &model.Device{DAP: model.StandardDAP("DAP", 0x100, 2)}
	ok := &MRPSetting{Role: mrp.RoleClient, RingPorts: [2]int{1, 2}}
	if _, err := mrpParameters(m, ok); err != nil {
		t.Fatalf("valid ports: %v", err)
	}
	for _, ports := range [][2]int{{1 << 16, 2}, {1<<16 + 1, 2}, {1, 0x10000 - 0x8001 + 2}, {-1, 2}, {1, 3}} {
		s := &MRPSetting{Role: mrp.RoleClient, RingPorts: ports}
		if _, err := mrpParameters(m, s); !errors.Is(err, profinet.ErrInvalidConfig) {
			t.Fatalf("ports %v: err %v", ports, err)
		}
	}
}

func TestRecordLength(t *testing.T) {
	if n, err := recordLength(4096); err != nil || n != 4096 {
		t.Fatalf("4096: %d %v", n, err)
	}
	if _, err := recordLength(-1); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("-1: %v", err)
	}
	if strconv.IntSize == 64 {
		big := int(^uint(0) >> 1) // math.MaxInt, beyond 32 bits here
		if _, err := recordLength(big); !errors.Is(err, profinet.ErrInvalidConfig) {
			t.Fatalf("MaxInt: %v", err)
		}
	}
}

func TestNext16Wraps(t *testing.T) {
	var c atomic.Uint32
	c.Store(math.MaxUint16 - 1)
	if v := next16(&c); v != math.MaxUint16 {
		t.Fatalf("%d", v)
	}
	if v := next16(&c); v != 0 {
		t.Fatalf("wrap: %d", v)
	}
	c.Store(math.MaxUint32)
	if v := next16(&c); v != 0 {
		t.Fatalf("32-bit wrap: %d", v)
	}
}

func TestJitterRange(t *testing.T) {
	seen := map[float64]bool{}
	for range 1000 {
		j := jitter()
		if j < 0 || j >= 1 {
			t.Fatalf("jitter %v", j)
		}
		seen[j] = true
	}
	if len(seen) < 990 {
		t.Fatalf("%d distinct values in 1000", len(seen))
	}
}

// bigModel returns a device with one slot of n submodules: far beyond
// what any 16-bit block length can carry.
func bigModel(n int) *model.Device {
	m := testModel()
	mod := model.Module{Name: "BIG", IdentNumber: 0x99}
	for i := range n {
		mod.Submodules = append(mod.Submodules, model.Submodule{Name: "S", IdentNumber: 0x98, Subslot: uint16(i%0x7FFF) + 1})
	}
	m.Slots = append(m.Slots, model.Slot{Number: 9, Module: mod})
	return m
}

func TestOversizeConfigurationRejected(t *testing.T) {
	n := newNet(t)
	c, nd := n.controller()
	if _, err := c.AddDevice(DeviceConfig{StationName: "big", Model: bigModel(11000)}); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("11000 submodules: %v", err)
	}
	long := strings.Repeat("a", maxNameLen+1)
	if _, err := New(Config{Mux: nd.mux, ListenPacket: nd.stack.ListenPacket, StationName: long, RPCPort: 40000}); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("station name: %v", err)
	}
	rc := NewRecordClient(nil, nil, rpc.NilUUID)
	if _, err := rc.OpenSupervisor(context.Background(), SupervisorConfig{InitiatorObject: pnio.ObjectUUID(1, 1, 1), StationName: long}); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("supervisor station name: %v", err)
	}
	m := &model.Device{DAP: model.StandardDAP("DAP", 0x100, 2)}
	if _, err := mrpParameters(m, &MRPSetting{Role: mrp.RoleClient, DomainName: long}); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("MRP domain name: %v", err)
	}
}

// A request the encoder cannot represent is an error, not a panic (the
// RPC client is never reached).
func TestConnectEncodeError(t *testing.T) {
	rc := NewRecordClient(nil, nil, rpc.NilUUID)
	req := &pnio.ConnectRequest{AR: pnio.ARBlockReq{StationName: strings.Repeat("a", 1<<16)}}
	if _, err := rc.Connect(context.Background(), req); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("err %v", err)
	}
}

func TestEncodeSafely(t *testing.T) {
	if b, err := encodeSafely(func() ([]byte, error) { return []byte{1}, nil }); err != nil || len(b) != 1 {
		t.Fatalf("%x %v", b, err)
	}
	if _, err := encodeSafely(func() ([]byte, error) { panic(fmt.Errorf("x: %w", profinet.ErrInvalidConfig)) }); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("err %v", err)
	}
	for _, v := range []any{"bug", errors.New("other")} {
		func() {
			defer func() {
				if r := recover(); r != v {
					t.Errorf("recovered %v, want %v", r, v)
				}
			}()
			_, _ = encodeSafely(func() ([]byte, error) { panic(v) })
		}()
	}
}

// TestWriteRecordLengthLimit: a write whose data, a WriteMultiple item or
// the WriteMultiple as a whole exceeds the 32-bit RecordDataLength is
// refused before anything is sent (maxRecordData lowered to stand in for
// 4 GiB).
func TestWriteRecordLengthLimit(t *testing.T) {
	old := maxRecordData
	maxRecordData = 60
	t.Cleanup(func() { maxRecordData = old })
	n := newNet(t)
	c, _ := n.controller()
	rc := NewRecordClient(c.RPC(), devAddr(), pnio.ObjectUUID(1, 2, 3))
	ar := rpc.NewRandomUUID()
	ctx := context.Background()
	rec := Record{Subslot: 1, Index: 0x10}
	if err := rc.Write(ctx, ar, rec, make([]byte, 61)); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("Write of 61 octets: %v", err)
	}
	if _, err := rc.WriteMultiple(ctx, ar, []WriteItem{{Record: rec, Data: make([]byte, 61)}}); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("WriteMultiple item of 61 octets: %v", err)
	}
	// Each item fits, the request with its WriteReq headers does not.
	if _, err := rc.WriteMultiple(ctx, ar, []WriteItem{{Record: rec, Data: make([]byte, 8)}}); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("WriteMultiple over the total: %v", err)
	}
}
