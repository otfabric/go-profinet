// SPDX-License-Identifier: MIT

package alarm_test

// RTA PDUs recorded between a CPU 1515-2 PN and p-net (testdata/captures,
// pnio-rs alarm/): every PDU decodes to what tshark's pn_io dissector shows
// and re-encodes to the recorded bytes, and an AlarmAck built for p-net's
// notification is the one the CPU sent.

import (
	"bytes"
	"testing"

	"github.com/otfabric/go-profinet/alarm"
	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/internal/capture"
	"github.com/otfabric/go-profinet/pnio"
)

type rtaCase struct {
	file      string
	frameID   uint16
	typ       alarm.PDUType
	send, ack uint16
	varLen    int
	block     pnio.BlockType // DATA: the block in the VarPart
	status    uint32         // ERR: the PNIOStatus
}

var rtaCases = []rtaCase{
	{"alarm_process_notif.hex", pnio.FrameIDAlarmHigh, alarm.PDUData, 0xFFFF, 0xFFFE, 29, pnio.BlockAlarmNotificationHigh, 0},
	{"alarm_ack_rta_high_cpu.hex", pnio.FrameIDAlarmHigh, alarm.PDUAck, 0xFFFE, 0xFFFF, 0, 0, 0},
	{"alarm_ack_high_cpu.hex", pnio.FrameIDAlarmHigh, alarm.PDUData, 0xFFFF, 0xFFFF, 22, pnio.BlockAlarmAckHigh, 0},
	{"alarm_ack_rta_high_dev.hex", pnio.FrameIDAlarmHigh, alarm.PDUAck, 0xFFFF, 0xFFFF, 0, 0, 0},
	{"alarm_diag_notif.hex", pnio.FrameIDAlarmLow, alarm.PDUData, 0xFFFF, 0xFFFE, 40, pnio.BlockAlarmNotificationLow, 0},
	{"alarm_ack_rta_low_cpu.hex", pnio.FrameIDAlarmLow, alarm.PDUAck, 0xFFFE, 0xFFFF, 0, 0, 0},
	{"alarm_diag_ack_cpu.hex", pnio.FrameIDAlarmLow, alarm.PDUData, 0xFFFF, 0xFFFF, 22, pnio.BlockAlarmAckLow, 0},
	{"alarm_ack_rta_low_dev.hex", pnio.FrameIDAlarmLow, alarm.PDUAck, 0xFFFF, 0xFFFF, 0, 0, 0},
	{"alarm_diag_update_appears.hex", pnio.FrameIDAlarmLow, alarm.PDUData, 0x0000, 0xFFFF, 40, pnio.BlockAlarmNotificationLow, 0},
	{"alarm_diag_update_others_remain.hex", pnio.FrameIDAlarmLow, alarm.PDUData, 0x0001, 0x0000, 40, pnio.BlockAlarmNotificationLow, 0},
	{"alarm_diag_usi_disappears.hex", pnio.FrameIDAlarmLow, alarm.PDUData, 0x0004, 0x0003, 28, pnio.BlockAlarmNotificationLow, 0},
	{"alarm_diag_std_remove.hex", pnio.FrameIDAlarmLow, alarm.PDUData, 0x0005, 0x0004, 40, pnio.BlockAlarmNotificationLow, 0},
	// ERR-RTA always on the low channel. The CPU's frames carry 18 more
	// octets after the 4 of VarPartLen (vendor data in the padding).
	{"alarm_err_rta_cpu_removed.hex", pnio.FrameIDAlarmLow, alarm.PDUErr, 0xFFFE, 0xFFFE, 4, 0, 0xCF81FD11},
	{"alarm_err_rta_dev_removed_reply.hex", pnio.FrameIDAlarmLow, alarm.PDUErr, 0xFFFF, 0xFFFE, 4, 0, 0xCF81FD11},
	{"alarm_err_rta_dev.hex", pnio.FrameIDAlarmLow, alarm.PDUErr, 0x0006, 0x0005, 4, 0, 0xCF81FD00},
	{"alarm_err_rta_cpu.hex", pnio.FrameIDAlarmLow, alarm.PDUErr, 0x0005, 0x0005, 4, 0, 0xCF81FD0B},
}

func TestCaptureRTA(t *testing.T) {
	for _, c := range rtaCases {
		t.Run(c.file, func(t *testing.T) {
			e, err := ethernet.Decode(capture.MustHex("pnio-rs/alarm/" + c.file))
			if err != nil {
				t.Fatal(err)
			}
			if !e.Tagged || e.VLAN.Priority != alarm.Priority(c.frameID) {
				t.Errorf("VLAN %v %+v, want priority %d", e.Tagged, e.VLAN, alarm.Priority(c.frameID))
			}
			p, err := alarm.Decode(e.Payload)
			if err != nil {
				t.Fatal(err)
			}
			if p.FrameID != c.frameID || p.Type != c.typ || p.SendSeq != c.send || p.AckSeq != c.ack || len(p.VarPart) != c.varLen || p.Dst != 0 || p.Src != 0 {
				t.Errorf("%+v", p)
			}
			// TACK is set on DATA only, WindowSize 1 always.
			if p.TACK() != (c.typ == alarm.PDUData) || p.AddFlags&0x0F != 1 {
				t.Errorf("AddFlags %#02x", p.AddFlags)
			}
			if got, want := p.AppendPayload(nil), e.Payload[:14+c.varLen]; !bytes.Equal(got, want) {
				t.Errorf("re-encoded\n got % x\nwant % x", got, want)
			}
			switch c.typ {
			case alarm.PDUData:
				blk, _, err := pnio.ReadBlock(p.VarPart)
				if err != nil || blk.Header.Type != c.block {
					t.Fatalf("block %v: %v", blk.Header.Type, err)
				}
			case alarm.PDUErr:
				if st := pnio.StatusFromUint32(uint32(p.VarPart[0])<<24 | uint32(p.VarPart[1])<<16 | uint32(p.VarPart[2])<<8 | uint32(p.VarPart[3])); st != pnio.StatusFromUint32(c.status) {
					t.Errorf("status %v", st)
				}
			case alarm.PDUAck, alarm.PDUNack:
				// No VarPart (checked above).
			}
		})
	}
	// The ErrorCode2 values the recording holds.
	if pnio.AbortStatus(pnio.AbortAlarmInd) != pnio.StatusFromUint32(0xCF81FD0B) {
		t.Error("AbortStatus(AbortAlarmInd) is not the CPU's CF 81 FD 0B")
	}
}

// TestCaptureAckFor: the AlarmAcks AckFor builds for p-net's process and
// diagnosis notifications are, byte for byte, the ones the CPU sent: the
// whole AlarmSpecifier echoed (0xA800 for the diagnosis: channel,
// submodule and AR diagnosis, sequence 0) and PNIOStatus OK.
func TestCaptureAckFor(t *testing.T) {
	notif := func(file string) pnio.AlarmNotification {
		e, _ := ethernet.Decode(capture.MustHex("pnio-rs/alarm/" + file))
		p, err := alarm.Decode(e.Payload)
		if err != nil {
			t.Fatal(err)
		}
		blk, _, err := pnio.ReadBlock(p.VarPart)
		if err != nil {
			t.Fatal(err)
		}
		n, err := pnio.DecodeAlarmNotification(blk)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	cpuAck := func(file string) []byte {
		e, _ := ethernet.Decode(capture.MustHex("pnio-rs/alarm/" + file))
		p, err := alarm.Decode(e.Payload)
		if err != nil {
			t.Fatal(err)
		}
		return p.VarPart
	}
	n := notif("alarm_process_notif.hex")
	if n.Type != pnio.AlarmProcess || !n.High || n.Slot != 1 || n.Subslot != 1 || n.ModuleIdentNumber != 0x30 || n.SubmoduleIdentNumber != 0x130 || n.USI != 0x0010 || !bytes.Equal(n.Data, []byte{1}) {
		t.Errorf("process notification %+v", n)
	}
	a := pnio.AckFor(&n, pnio.Status{})
	if got, want := a.Append(nil), cpuAck("alarm_ack_high_cpu.hex"); !bytes.Equal(got, want) {
		t.Errorf("process AlarmAck\n got % x\nwant % x", got, want)
	}

	d := notif("alarm_diag_notif.hex")
	if d.Specifier != 0xA800 || d.USI != pnio.USIExtChannelDiagnosis {
		t.Errorf("diagnosis notification %+v", d)
	}
	items, err := pnio.DecodeDiagnosisItems(d.USI, d.Data)
	if err != nil || len(items) != 1 || items[0].ChannelNumber != 4 || items[0].ChannelProperties != 0x2801 || items[0].ErrorType != 1 {
		t.Errorf("diagnosis items %+v %v", items, err)
	}
	ours := pnio.AckFor(&d, pnio.Status{})
	if got, want := ours.Append(nil), cpuAck("alarm_diag_ack_cpu.hex"); !bytes.Equal(got, want) {
		t.Errorf("diagnosis AlarmAck\n got % x\nwant % x", got, want)
	}
}
