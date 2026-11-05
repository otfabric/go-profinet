// SPDX-License-Identifier: MIT

package alarm

import (
	"context"
	"encoding/binary"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/l2"
	"github.com/otfabric/go-profinet/pnio"
	"github.com/otfabric/go-profinet/vnet"
)

// FuzzEndpoint feeds an endpoint a hostile peer's PDUs (ACKs, NACKs,
// DATA and ERR with arbitrary sequence numbers) while it sends an alarm.
// Invariants: no panic; the Send ends within its retry budget; no more
// DATA is delivered than arrived; Close returns.
func FuzzEndpoint(f *testing.F) {
	pdu := func(p PDU) []byte {
		p.FrameID, p.Dst, p.Src, p.Version = pnio.FrameIDAlarmHigh, 1, 2, Version
		return p.AppendPayload(nil)
	}
	join := func(ps ...[]byte) []byte {
		var b []byte
		for _, p := range ps {
			b = binary.BigEndian.AppendUint16(b, uint16(len(p)))
			b = append(b, p...)
		}
		return b
	}
	f.Add(join(pdu(PDU{Type: PDUAck, AddFlags: WindowSize, SendSeq: InitialSendSeq, AckSeq: InitialSendSeq})))
	f.Add(join(pdu(PDU{Type: PDUNack, AddFlags: WindowSize}), pdu(PDU{Type: PDUNack, AddFlags: WindowSize}), pdu(PDU{Type: PDUNack})))
	f.Add(join(pdu(PDU{Type: PDUData, AddFlags: WindowSize | FlagTACK, SendSeq: InitialSendSeq, AckSeq: InitialAckSeq, VarPart: []byte("x")}),
		pdu(PDU{Type: PDUData, AddFlags: WindowSize | FlagTACK, SendSeq: InitialSendSeq, AckSeq: InitialAckSeq, VarPart: []byte("x")}),
		pdu(PDU{Type: PDUData, AddFlags: WindowSize | FlagTACK, SendSeq: 0x7FFF, AckSeq: 0x1234})))
	f.Add(join(pdu(PDU{Type: PDUErr, VarPart: []byte{0xDB, 0x81, 0x40, 0x04}}), pdu(PDU{Type: PDUErr})))
	f.Add(join(pdu(PDU{Type: PDUType(9), AckSeq: 0xFFFF})))

	n := vnet.New(1)
	ep, err := n.NewEndpoint(vnet.EndpointConfig{MAC: ethernet.MAC{2, 0, 0, 0, 0, 1}})
	if err != nil {
		f.Fatal(err)
	}
	mux := l2.NewMux(ep)
	mgr, err := NewManager(mux)
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() {
		mgr.Close()
		_ = mux.Close()
		_ = n.Close()
	})
	f.Fuzz(func(t *testing.T, b []byte) {
		var delivered atomic.Int64
		e, err := mgr.Open(EndpointConfig{High: true, LocalRef: 1, RemoteRef: 2, RemoteMAC: ethernet.MAC{2, 0, 0, 0, 0, 2},
			Timeout: 5 * time.Millisecond, Retries: 2,
			OnData: func([]byte) { delivered.Add(1) }, OnErr: func(pnio.Status) {}})
		if err != nil {
			t.Fatal(err)
		}
		res := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			res <- e.Send(ctx, []byte("alarm"))
		}()
		var data int64
		for len(b) >= 2 {
			l := min(int(binary.BigEndian.Uint16(b)), len(b)-2)
			p, err := Decode(b[2 : 2+l])
			b = b[2+l:]
			if err != nil {
				continue
			}
			if p.Type == PDUData {
				data++
			}
			e.handle(&p)
		}
		select {
		case err := <-res:
			// A forged ACK may complete it; otherwise it times out.
			if err != nil && !errors.Is(err, profinet.ErrTimeout) {
				t.Fatalf("Send: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Send did not end within its retries")
		}
		done := make(chan struct{})
		go func() {
			e.Close()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("Close hangs")
		}
		if got := e.Stats().Received; int64(got) > data {
			t.Fatalf("%d DATA received of %d", got, data)
		}
		if delivered.Load() > data {
			t.Fatalf("%d delivered of %d", delivered.Load(), data)
		}
	})
}
