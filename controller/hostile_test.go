// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"encoding/binary"
	"errors"
	"math/rand/v2"
	"net/netip"
	"os"
	"strconv"
	"testing"
	"time"

	profinet "github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/dcp"
	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/l2"
	"github.com/otfabric/go-profinet/netstack"
	"github.com/otfabric/go-profinet/pnio"
	"github.com/otfabric/go-profinet/rpc"
	"github.com/otfabric/go-profinet/vnet"
)

// registeredARs is the number of ARs the controller routes calls to.
func registeredARs(c *Controller) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.byAR)
}

// settle waits until d either failed or got past Connecting, and returns
// its state and error.
func settle(t *testing.T, d *Device) (State, error) {
	t.Helper()
	var st State
	var err error
	eventually(t, 10*time.Second, "connect outcome", func() bool {
		st, err = d.State()
		return err != nil || st >= StateParameterizing
	})
	return st, err
}

// TestHostileConnectResponse: Connect responses a malicious device can
// send. Each is either refused, with no AR registered, or (if it is
// valid after all) gets the AR past Connecting; closing the device
// always ends with nothing registered.
func TestHostileConnectResponse(t *testing.T) {
	n := newNet(t)
	c, _ := n.controller()
	f := n.fake("fake")
	cases := []struct {
		name   string
		mod    func(req *pnio.ConnectRequest, r *pnio.ConnectResponse) pnio.Status
		refuse bool
	}{
		{"hundreds of IOCRs", func(_ *pnio.ConnectRequest, r *pnio.ConnectResponse) pnio.Status {
			for len(r.IOCRs) < 300 {
				r.IOCRs = append(r.IOCRs, r.IOCRs[0])
			}
			return pnio.Status{}
		}, false},
		{"no IOCR", func(_ *pnio.ConnectRequest, r *pnio.ConnectResponse) pnio.Status {
			r.IOCRs = nil
			return pnio.Status{}
		}, true},
		{"IOCR types swapped", func(_ *pnio.ConnectRequest, r *pnio.ConnectResponse) pnio.Status {
			r.IOCRs[0].Type, r.IOCRs[1].Type = r.IOCRs[1].Type, r.IOCRs[0].Type
			return pnio.Status{}
		}, true},
		{"output FrameID out of range", func(_ *pnio.ConnectRequest, r *pnio.ConnectResponse) pnio.Status {
			r.IOCRs[1].FrameID = 0xFFFF
			return pnio.Status{}
		}, true},
		{"alarm data length 0", func(_ *pnio.ConnectRequest, r *pnio.ConnectResponse) pnio.Status {
			r.AlarmCR.MaxAlarmDataLength = 0
			return pnio.Status{}
		}, false},
		{"alarm data length max", func(_ *pnio.ConnectRequest, r *pnio.ConnectResponse) pnio.Status {
			r.AlarmCR.MaxAlarmDataLength = 0xFFFF
			return pnio.Status{}
		}, false},
		{"huge ModuleDiff", func(req *pnio.ConnectRequest, r *pnio.ConnectResponse) pnio.Status {
			md := &pnio.ModuleDiffBlock{}
			api := pnio.DiffAPI{}
			for i := range 1500 {
				api.Modules = append(api.Modules, pnio.DiffModule{Slot: uint16(i), IdentNumber: 1, State: 2,
					Submodules: []pnio.DiffSubmodule{{Subslot: 1, IdentNumber: 1, State: 7}}})
			}
			md.APIs = []pnio.DiffAPI{api, {API: 1, Modules: api.Modules[:1500]}}
			r.ModuleDiff = md
			return pnio.Status{}
		}, false},
		{"other session key", func(_ *pnio.ConnectRequest, r *pnio.ConnectResponse) pnio.Status {
			r.AR.SessionKey++
			return pnio.Status{}
		}, false},
		{"zero responder MAC", func(_ *pnio.ConnectRequest, r *pnio.ConnectResponse) pnio.Status {
			r.AR.ResponderMAC = ethernet.MAC{}
			return pnio.Status{}
		}, false},
		{"broadcast responder MAC", func(_ *pnio.ConnectRequest, r *pnio.ConnectResponse) pnio.Status {
			r.AR.ResponderMAC = ethernet.Broadcast
			return pnio.Status{}
		}, false},
	}
	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			f.set(func(f *fakeDev) { f.connect = cs.mod })
			cd, err := c.AddDevice(DeviceConfig{StationName: "fake", Model: testModel(), ReconnectMin: time.Hour, ReductionRatio: 512, DataHoldFactor: 0x1E00})
			if err != nil {
				t.Fatal(err)
			}
			st, err := settle(t, cd)
			t.Logf("state %s, err %v", st, err)
			if err != nil && registeredARs(c) != 0 {
				// The AR may still be torn down; it must go.
				eventually(t, 5*time.Second, "AR unregistered", func() bool { return registeredARs(c) == 0 })
			}
			if cs.refuse && err == nil {
				t.Fatalf("accepted, state %s", st)
			}
			if err := cd.Close(); err != nil {
				t.Fatal(err)
			}
			eventually(t, 5*time.Second, "AR unregistered after Close", func() bool { return registeredARs(c) == 0 })
		})
	}
	f.set(func(f *fakeDev) { f.connect = nil })
}

// TestHostileConnectBytes: a device answers Connect with a valid response
// in which seeded random octets are changed, truncated or extended. The
// controller must neither panic nor keep an AR it refused.
func TestHostileConnectBytes(t *testing.T) {
	n := newNet(t)
	c, _ := n.controller()
	f := n.fake("fake")
	seed := uint64(1)
	if v, err := strconv.ParseUint(os.Getenv("PROFINET_CHAOS_SEED"), 10, 64); err == nil {
		seed = v
	}
	t.Logf("seed %d (PROFINET_CHAOS_SEED)", seed)
	rnd := rand.New(rand.NewPCG(seed, 1))
	for i := range 25 {
		mode := i % 3
		f.set(func(f *fakeDev) {
			f.raw = func(op pnio.OpNum, blocks []byte) []byte {
				if op != pnio.OpConnect {
					return nil
				}
				creq, err := pnio.DecodeConnectRequest(blocks)
				if err != nil {
					return nil
				}
				resp := &pnio.ConnectResponse{AR: pnio.ARBlockRes{Type: creq.AR.Type, ARUUID: creq.AR.ARUUID, SessionKey: creq.AR.SessionKey, ResponderMAC: f.mux.HardwareAddr(), UDPRTPort: pnio.LT}}
				for _, c := range creq.IOCRs {
					id := c.FrameID
					if c.Type == pnio.IOCROutput {
						id = 0xC100
					}
					resp.IOCRs = append(resp.IOCRs, pnio.IOCRBlockRes{Type: c.Type, Reference: c.Reference, FrameID: id})
				}
				resp.AlarmCR = &pnio.AlarmCRBlockRes{Type: 1, LocalAlarmReference: 0x77, MaxAlarmDataLength: 200}
				b := resp.Append(nil)
				switch mode {
				case 0:
					for range 1 + rnd.IntN(4) {
						b[rnd.IntN(len(b))] ^= byte(1 + rnd.IntN(255))
					}
				case 1:
					b = b[:rnd.IntN(len(b))]
				case 2:
					// A block length that claims more than is there.
					binary.BigEndian.PutUint16(b[2:], 0xFFFF)
				}
				return pnio.AppendNDRResponse(nil, order, pnio.Status{}, pnio.DefaultArgsMaximum, b)
			}
		})
		cd, err := c.AddDevice(DeviceConfig{StationName: "fake", Model: testModel(), ReconnectMin: time.Hour, ReductionRatio: 512, DataHoldFactor: 0x1E00})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := settle(t, cd); err != nil {
			eventually(t, 5*time.Second, "refused AR unregistered", func() bool { return registeredARs(c) == 0 })
		}
		if err := cd.Close(); err != nil {
			t.Fatal(err)
		}
		eventually(t, 5*time.Second, "AR unregistered after Close", func() bool { return registeredARs(c) == 0 })
	}
	f.set(func(f *fakeDev) { f.raw = nil })
}

// TestHostileRecordAnswers: record answers that lie about their length.
func TestHostileRecordAnswers(t *testing.T) {
	n := newNet(t)
	c, _ := n.controller()
	f := n.fake("fake")
	rc := NewRecordClient(c.RPC(), devAddr(), pnio.ObjectUUID(1, 2, 3))
	ctx := context.Background()
	ar := rpc.NewRandomUUID()
	answer := func(blocks []byte) {
		f.set(func(f *fakeDev) {
			f.raw = func(pnio.OpNum, []byte) []byte {
				return pnio.AppendNDRResponse(nil, order, pnio.Status{}, pnio.DefaultArgsMaximum, blocks)
			}
		})
	}
	rec := Record{Subslot: 1, Index: pnio.IndexIM0}
	// RecordDataLength 4 GiB with a few octets present.
	h := pnio.ReadRes{ARUUID: ar, Subslot: 1, Index: pnio.IndexIM0, Length: 0xFFFFFFFF}
	answer(append(h.Append(nil), 1, 2, 3))
	if _, err := rc.Read(ctx, ar, rec, 0); err == nil {
		t.Fatal("4 GiB record accepted")
	}
	// A WriteMultiple answer of hundreds of headers for one item.
	var res []byte
	for range 300 {
		w := pnio.WriteRes{ARUUID: ar, Index: 0x10}
		res = w.Append(res)
	}
	answer(res)
	out, err := rc.WriteMultiple(ctx, ar, []WriteItem{{Record: Record{Index: 0x10}, Data: []byte{1}}})
	if !errors.Is(err, profinet.ErrMalformed) {
		t.Fatalf("300 results for 1 item: %d results, %v", len(out), err)
	}
	// An answer as large as an RPC message.
	big := make([]byte, rpc.DefaultMaxMessage-pnio.NDRHeaderLen-64)
	h.Length = uint32(len(big))
	answer(append(h.Append(nil), big...))
	if data, err := rc.Read(ctx, ar, rec, uint32(len(big))); err != nil || len(data) != len(big) {
		t.Fatalf("large read: %d octets, %v", len(data), err)
	}
	// The same answer to a Read that asked for less: maxLen bounds it.
	if data, err := rc.Read(ctx, ar, rec, 0); !errors.Is(err, profinet.ErrMalformed) {
		t.Fatalf("read beyond maxLen: %d octets, %v", len(data), err)
	}
	f.set(func(f *fakeDev) { f.raw = nil })
}

// fuzzController starts a controller on its own virtual network.
func fuzzController(tb testing.TB) *Controller {
	tb.Helper()
	nw := vnet.New(1)
	ep, err := nw.NewEndpoint(vnet.EndpointConfig{Name: ctrlIP, MAC: ethernet.MAC{0x02, 0, 0, 0, 0, 1}})
	if err != nil {
		tb.Fatal(err)
	}
	mux := l2.NewMux(ep)
	st, err := netstack.New(mux, netstack.Config{Addr: netip.MustParseAddr(ctrlIP), Mask: mask})
	if err != nil {
		tb.Fatal(err)
	}
	c, err := New(Config{Mux: mux, ListenPacket: st.ListenPacket, StationName: "plc", DCP: dcp.ClientOptions{Timeout: 300 * time.Millisecond}})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		_ = c.Close()
		_ = st.Close()
		_ = mux.Close()
		_ = ep.Close()
		_ = nw.Close()
	})
	return c
}

// FuzzControllerServeRPC feeds the controller's PNIO interface (where a
// device calls ApplicationReady) with arbitrary bodies. Invariants: no
// panic; every answer that is not a reject is an NDR response.
func FuzzControllerServeRPC(f *testing.F) {
	c := fuzzController(f)
	ndr := func(blocks []byte) []byte { return pnio.AppendNDRRequest(nil, order, pnio.DefaultArgsMaximum, blocks) }
	cb := pnio.ControlBlock{Type: pnio.BlockAppReadyReq, ARUUID: rpc.NewRandomUUID(), SessionKey: 1, Command: pnio.CmdApplicationReady}
	f.Add(uint16(pnio.OpControl), ndr(cb.Append(nil)))
	cb.Type = pnio.BlockAppReadyPlugReq
	f.Add(uint16(pnio.OpControl), ndr(cb.Append(nil)))
	f.Add(uint16(pnio.OpRead), ndr(nil))
	f.Add(uint16(pnio.OpControl), []byte{1})
	f.Fuzz(func(t *testing.T, op uint16, body []byte) {
		for _, drep := range []byte{rpc.DRepLittleEndian, 0} {
			h := rpc.Header{OpNum: op, DRep: [3]byte{drep}}
			req := &rpc.Request{Header: h, Body: body}
			out, err := c.serveRPC(context.Background(), req)
			if err != nil {
				continue
			}
			if _, _, err := pnio.DecodeNDRResponse(out, req.Order()); err != nil {
				t.Fatalf("answer is not an NDR response: %v", err)
			}
		}
	})
}
