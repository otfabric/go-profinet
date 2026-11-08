// SPDX-License-Identifier: MIT

package controller

// The controller's record client against answers recorded from devices
// (testdata/captures): a GE Intelligent Platforms device's Read Implicit
// I&M0FilterData answer and p-net's I&M0 answers to a CPU 1515-2 PN. A
// scripted device replies with the recorded RPC PDU; only its activity
// UUID and sequence number, the dynamic fields of a call, are the
// controller's.

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/internal/capture"
	"github.com/otfabric/go-profinet/pnio"
	"github.com/otfabric/go-profinet/rpc"
)

// recordedDevice answers every call with the recorded response ref.
func recordedDevice(t *testing.T, n *tnet, ip string, refs ...string) {
	t.Helper()
	nd := n.node(ip)
	conn, err := nd.stack.ListenPacket(context.Background(), "udp4", ":34964")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buf := make([]byte, 2048)
		for i := 0; ; i++ {
			k, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			q, _, err := rpc.DecodeHeader(buf[:k])
			if err != nil || q.Type != rpc.TypeRequest {
				continue
			}
			u, err := capture.DecodeUDP(capture.MustLoad(refs[i%len(refs)]))
			if err != nil {
				return
			}
			h, body, err := rpc.DecodeHeader(u.Payload)
			if err != nil {
				return
			}
			h.Activity, h.SeqNum = q.Activity, q.SeqNum
			_, _ = conn.WriteTo(append(h.AppendTo(nil), body...), from)
		}
	}()
}

func TestCaptureRecordClient(t *testing.T) {
	n := newNet(t)
	recordedDevice(t, n, "192.168.0.50", "ics-pcaps/unknown_ge-ip-device_read-implicit.pcap#2")
	recordedDevice(t, n, "192.168.0.51", "pnio-rs/s7-1500_p-net_read-im0-dap-res.hex", "pnio-rs/s7-1500_p-net_read-im0-interface-res.hex")
	cn := n.node("192.168.0.1")
	conn, err := cn.stack.ListenUDP(0)
	if err != nil {
		t.Fatal(err)
	}
	cli := rpc.NewClient(conn, true, rpc.ClientOptions{Retransmit: time.Second})
	t.Cleanup(func() { _ = cli.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Read Implicit I&M0FilterData (the GE device's: little endian).
	ge := NewRecordClient(cli, &net.UDPAddr{IP: net.IPv4(192, 168, 0, 50), Port: rpc.Port}, pnio.ObjectUUID(1, 3, 0x015A))
	b, err := ge.Read(ctx, rpc.UUID{}, Record{Slot: 0, Subslot: 1, Index: pnio.IndexIM0Filter}, 32768)
	if err != nil {
		t.Fatal(err)
	}
	blks, err := pnio.SplitBlocks(b)
	if err != nil || len(blks) != 3 || blks[0].Header.Type != pnio.BlockIM0FilterSubmodule || blks[1].Header.Type != pnio.BlockIM0FilterModule || blks[2].Header.Type != pnio.BlockIM0FilterDevice {
		t.Fatalf("I&M0FilterData %+v %v", blks, err)
	}

	// I&M0 of the DAP and of the interface (p-net's: big endian).
	pn := NewRecordClient(cli, &net.UDPAddr{IP: net.IPv4(192, 168, 0, 51), Port: rpc.Port}, pnio.ObjectUUID(1, 2, 0x0493))
	ar := rpc.NewRandomUUID()
	for _, sub := range []uint16{1, 0x8000} {
		im, err := pn.ReadIM0(ctx, ar, 0, sub)
		if err != nil {
			t.Fatal(err)
		}
		if im.VendorID != 0x0493 || im.OrderID != "12345 Abcdefghijk" || im.SerialNumber != "007" || im.Supported != 0x000E {
			t.Errorf("I&M0 of 0/%#x: %+v", sub, im)
		}
	}
}
