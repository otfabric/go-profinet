// SPDX-License-Identifier: MIT

//go:build interop

package interop

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/pnio"
)

// TestPnioRsController: go-profinet as IO-Controller of pnio-rs, with the
// expected configuration read from pnio-rs's own GSDML.
func TestPnioRsController(t *testing.T) {
	e := newEnv(t)
	img := image("pnio-rs")
	e.result.peer("pnio-rs", img, "device")
	dev := e.start("pniors", img, 30, "device", "--station", "pnio-dut")
	e.waitEvent(dev, 30*time.Second, "Idle", func(m map[string]any) bool { return m["event"] == "ar-state" })
	stop := e.capture(dev, "pniors-controller.pcap")
	one := hex.EncodeToString([]byte{0x3f, 0x80, 0, 0})
	im1 := "002100380100" + strings.Repeat("61", 32) + strings.Repeat("62", 22)
	out := e.otf("ctrl", 2, 90*time.Second, "controller", "-station", "pnio-dut", "-model", "/gsdml-testdata/pnio-rs_sample-16real-32bool.xml",
		"-advanced", "-im0", "-write", "0:1:0xAFF1:"+im1, "-read", "0:1:0xAFF1", "-read", "1:1:0x800C", "-read", "0:1:0xAFF4",
		"-output", "3:1:0:"+strings.Repeat(one, 16), "-output", "4:1:0:a5a5a5a5", "-duration", "4s")
	r := report(t, out)
	e.expect("GSDML-derived expected configuration; Connect, PrmEnd, ApplicationReady (advanced, RTClass 2)", r["running"] == true, "%v", r["error"])
	if r["running"] != true {
		return
	}
	im0, _ := r["im0"].(map[string]any)
	e.expect("Read I&M0", im0 != nil && im0["OrderID"] == "PNIO-SAMPLE", "%v", im0)
	recs, _ := r["records"].([]any)
	var gotIM1, diag, im4 map[string]any
	for _, x := range recs {
		m := x.(map[string]any)
		switch fmt.Sprintf("%v %v", m["op"], m["index"]) {
		case "read 45041":
			gotIM1 = m
		case "read 32780":
			diag = m
		case "read 45044":
			im4 = m
		}
	}
	e.expect("Write and read back I&M1", gotIM1 != nil && gotIM1["data"] == im1, "%v", gotIM1)
	e.result.na("Diagnosis records (0x800C)", "pnio-rs answers every diagnosis index with invalid index (documented limitation)")
	e.expect("Diagnosis record answers invalid index as pnio-rs documents", diag != nil && strings.HasPrefix(str(diag["status"]), "DE 80 B0"), "%v", diag)
	e.expect("I&M4 answers invalid index as pnio-rs documents", im4 != nil && strings.HasPrefix(str(im4["status"]), "DE 80 B0"), "%v", im4)
	in, _ := r["inputs"].(map[string]any)
	reals, _ := in["1:1"].(map[string]any)
	bools, _ := in["2:1"].(map[string]any)
	e.expect("Cyclic echo of 16 REAL outputs", reals != nil && reals["data"] == strings.Repeat(one, 16) && reals["iops"] == 128.0, "%v", reals)
	e.expect("Cyclic echo of 32 BOOL outputs", bools != nil && bools["data"] == "a5a5a5a5", "%v", bools)
	evs := e.events(dev)
	e.expect("pnio-rs reaches Data", func() bool {
		for _, ev := range evs {
			if ev["event"] == "ar-state" && ev["state"] == "Data" {
				return true
			}
		}
		return false
	}(), "%v", evs)
	e.expect("Release ends the AR at pnio-rs", e.eventually(dev, 5*time.Second, func(ev map[string]any) bool {
		return ev["event"] == "ar-state" && strings.Contains(str(ev["state"]), "ControllerRelease")
	}), "")
	// Legacy startup with RT_CLASS_1 FrameIDs is outside pnio-rs's range.
	lr := report(t, e.otf("ctrl-legacy", 2, 60*time.Second, "controller", "-station", "pnio-dut", "-model", "/gsdml-testdata/pnio-rs_sample-16real-32bool.xml", "-timeout", "6s"))
	e.expect("pnio-rs rejects RT_CLASS_1 FrameIDs (0xC000..) with a Connect error, which the controller reports", lr["running"] != true && strings.Contains(str(lr["error"]), "connect"), "%v", lr["error"])
	e.oracles(stop())
}

// TestPnioRsDCP: go-profinet's DCP requester against pnio-rs.
func TestPnioRsDCP(t *testing.T) {
	e := newEnv(t)
	img := image("pnio-rs")
	e.result.peer("pnio-rs", img, "device")
	dev := e.start("pniors", img, 30, "device", "--station", "pnio-dut")
	e.waitEvent(dev, 30*time.Second, "Idle", func(m map[string]any) bool { return m["event"] == "ar-state" })
	ids := last(jsonLines(e.otf("ident", 3, time.Minute, "dcp", "-station", "pnio-dut", "identify")), "dcp")
	devs, _ := ids["devices"].([]any)
	e.expect("DCP Identify by name", len(devs) == 1 && devs[0].(map[string]any)["vendorId"] == 65535.0, "%v", devs)
	all := last(jsonLines(e.otf("identall", 3, time.Minute, "dcp", "identify")), "dcp")
	devs, _ = all["devices"].([]any)
	e.expect("DCP Identify all", len(devs) >= 1, "%v", devs)
	e.result.na("DCP Get/Set NameOfStation/Signal/Reset (pnio-rs)", "pnio-rs v0.1.0 does not implement them")
}

// TestProfinetPyController: profinet-py, an independent IO-Controller,
// against go-profinet's IO-Device, configured from the GSDML the device
// generates.
func TestProfinetPyController(t *testing.T) {
	e := newEnv(t)
	img := image("profinet-py")
	e.result.peer("profinet-py", img, "controller")
	dev := e.startOTF("dut", 20, "device", "-station", "otf-dut", "-gsdml-out", "/share/otf-dut.xml", "-alarm-after", "1s")
	e.waitEvent(dev, 30*time.Second, "start", func(m map[string]any) bool { return m["event"] == "start" })
	stop := e.capture(dev, "profinet-py-controller.pcap")
	out := e.run("pypn", img, 3, 3*time.Minute, "controller", "--station", "otf-dut", "--gsdml", "/share/otf-dut.xml",
		"--output", "2:1:a1b2c3d4", "--output", "3:1:beef", "--read-input", "1:1", "--read-input", "3:1", "--duration", "3")
	var rep struct {
		Steps []struct {
			Step  string `json:"step"`
			OK    bool   `json:"ok"`
			Value any    `json:"value"`
			Error string `json:"error"`
		} `json:"steps"`
		Alarms []any `json:"alarms"`
	}
	if err := json.Unmarshal(lastLine(out), &rep); err != nil {
		t.Fatalf("profinet-py output: %v\n%s", err, out)
	}
	steps := map[string]int{}
	for i, s := range rep.Steps {
		steps[s.Step] = i
	}
	ok := func(name string) bool { i, found := steps[name]; return found && rep.Steps[i].OK }
	val := func(name string) string {
		if i, found := steps[name]; found {
			return str(rep.Steps[i].Value)
		}
		return ""
	}
	e.expect("DCP discovery by profinet-py", ok("discover"), "%v", rep.Steps)
	e.expect("Read I&M0 (IOSAR device access)", ok("read-im0") && strings.Contains(val("read-im0"), "OTF-INTEROP-1"), "%s", val("read-im0"))
	e.expect("Read I&M2..I&M4", ok("read-im2") && ok("read-im3") && ok("read-im4"), "")
	e.expect("Write and read a user record", ok("write-record") && val("read-record") == "0005", "%s", val("read-record"))
	e.expect("ReadImplicit I&M0", ok("read-implicit-im0") && strings.HasPrefix(val("read-implicit-im0"), "00200038"), "%s", val("read-implicit-im0"))
	e.expect("Invalid index answered with an error", !ok("read-invalid-index"), "")
	e.expect("ModuleDiff read", ok("module-diff"), "%s", val("module-diff"))
	e.expect("Slot discovery (RealIdentificationData)", ok("discover-slots") && strings.Contains(val("discover-slots"), "slot=3"), "%s", val("discover-slots"))
	e.result.deviation("I&M1 write by profinet-py", "profinet-py writes BlockLength 58 with 2 padding octets; p-net, pnio-rs and go-profinet use 56; go-profinet answers DF 80 B8 00")
	e.expect("GSDML generated by go-profinet parsed by profinet-py; cyclic AR (IOCARSingle) established", ok("start-cyclic"), "%v", rep.Steps)
	e.expect("Cyclic inputs at profinet-py", ok("input 1:1") && len(val("input 1:1")) == 16 && val("input 1:1") != "0000000000000000", "%s", val("input 1:1"))
	stats := func() map[string]any {
		if i, found := steps["cyclic-stats"]; found {
			m, _ := rep.Steps[i].Value.(map[string]any)
			return m
		}
		return nil
	}()
	e.expect("Cyclic statistics without missed frames", ok("cyclic-stats") && stats["frames_missed"] == 0.0 && stats["frames_invalid"] == 0.0 && stats["frames_received"].(float64) > 50, "%v", stats)
	evs := e.events(dev)
	outputs := map[string]bool{}
	for _, ev := range evs {
		if ev["event"] == "output" {
			outputs[fmt.Sprintf("%v:%v=%v", ev["slot"], ev["subslot"], ev["data"])] = true
		}
	}
	e.expect("Cyclic outputs from profinet-py at the device", outputs["2:1=a1b2c3d4"] && outputs["3:1=beef"], "%v", outputs)
	var arTypes []string
	for _, ev := range evs {
		if ev["event"] == "ar" && ev["to"] == "Data" {
			arTypes = append(arTypes, str(ev["type"]))
		}
	}
	e.expect("IOSAR and IOCARSingle ARs from profinet-py", strings.Contains(strings.Join(arTypes, ","), "IOSAR") && strings.Contains(strings.Join(arTypes, ","), "IOCARSingle"), "%v", arTypes)
	e.expect("Diagnosis alarm sent to profinet-py and acknowledged", hasEvent(evs, "diagnosis-sent") && last(evs, "diagnosis-sent")["error"] == nil, "%v", last(evs, "diagnosis-sent"))
	e.oracles(stop(), peerDeviation{Protocol: "PNIO-CM", Message: "Trailing stray characters",
		Why: "profinet-py writes I&M1 with BlockLength 58 and two padding octets; the standard block is 56 octets (tshark flags the extra octets of its Write request)"})
}

// TestPnioRsReparameterize: pnio-rs answers PrmBegin in Data with
// DD 81 3D 03 (CMDEV state conflict): not applicable; its AR runs on.
// TestProfinetPyAPI: profinet-py as IO-Controller of go-profinet's device
// with the IO module of slot 3 in API 0x4E01 (testdata/otf-device-api.json;
// the GSDML file go-profinet writes for it names the API, which
// profinet-py's GSDML loader reads). profinet-py's read and write take an
// API; its IOCRs and expected configuration list each slot under its
// submodule's API.
// profileWrite is what TestProfinetPyAPI writes to record 0xB600 of the
// device's submodule in API 0x4E01: 16 octets other than its default.
var profileWrite = []byte{0x80, 0x01, 0x02, 0x20, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x00, 0x07}

func TestProfinetPyAPI(t *testing.T) {
	e := newEnv(t)
	img := image("profinet-py")
	e.result.peer("profinet-py", img, "controller")
	dev := e.startOTF("dut", 20, "device", "-station", "otf-dut", "-model", "/otfnode-testdata/otf-device-api.json", "-gsdml-out", "/share/otf-dut-api.xml")
	e.waitEvent(dev, 30*time.Second, "start", func(m map[string]any) bool { return m["event"] == "start" })
	stop := e.capture(dev, "profinet-py-api.pcap")
	out := e.run("pypn", img, 3, 3*time.Minute, "controller", "--station", "otf-dut", "--gsdml", "/share/otf-dut-api.xml",
		"--read", "0x4e01@3:1:0x11", "--write", "0x4e01@3:1:0x11:0a0b", "--read", "0x4e01@3:1:0x11", "--read", "0@3:1:0x11", "--read", "0x4e02@3:1:0x11",
		"--read", "0x4e01@3:1:0xF000", "--read", "0:1:0xF821",
		"--read", "0x4e01@3:1:0xB600", "--write", "0x4e01@3:1:0xB600:"+hex.EncodeToString(profileWrite), "--read", "0x4e01@3:1:0xB600",
		"--read", "0@3:1:0xB600", "--read", "0x4e02@3:1:0xB600",
		"--write", "0x4e01@3:1:0xB900:00000000", "--write", "0x4e01@3:1:0xB600:0102",
		"--read", "0x4e01@3:1:0xB900", "--read", "0x4e01@3:1:0xB600",
		"--read", "0x4e01@3:1:0xC001", "--read", "0x4e01@3:1:0x8001", "--read", "0@3:1:0xC001", "--read", "0@3:1:0x8001",
		"--output", "2:1:a1b2c3d4", "--output", "3:1:beef", "--read-input", "1:1", "--read-input", "3:1", "--duration", "3")
	var rep struct {
		Steps []struct {
			Step  string `json:"step"`
			OK    bool   `json:"ok"`
			Value any    `json:"value"`
			Error string `json:"error"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(lastLine(out), &rep); err != nil {
		t.Fatalf("profinet-py output: %v\n%s", err, out)
	}
	steps := map[string]int{}
	for i, s := range rep.Steps {
		steps[s.Step] = i
	}
	ok := func(name string) bool { i, found := steps[name]; return found && rep.Steps[i].OK }
	val := func(name string) string {
		if i, found := steps[name]; found {
			return str(rep.Steps[i].Value)
		}
		return ""
	}
	errOf := func(name string) string {
		if i, found := steps[name]; found {
			return rep.Steps[i].Error
		}
		return ""
	}
	// The two reads of slot 3's record repeat a step name; the report
	// keeps both in order.
	var recReads []string
	for _, s := range rep.Steps {
		if s.Step == "read 0x4e01@3:1:0x11" && s.OK {
			recReads = append(recReads, str(s.Value))
		}
	}
	e.expect("Read a record of a submodule in API 0x4E01: its default", len(recReads) == 2 && recReads[0] == "0002", "%v", recReads)
	e.expect("Write a record of a submodule in API 0x4E01, read back", ok("write 0x4e01@3:1:0x11:0a0b") && len(recReads) == 2 && recReads[1] == "0a0b", "%v %s", recReads, errOf("write 0x4e01@3:1:0x11:0a0b"))
	// go-profinet's device routes records by (API, slot, subslot): the
	// slot and subslot of the submodule in another API, or in an API the
	// device does not have, is "invalid area/API" (ErrorDecode 0x80
	// PNIORW, ErrorCode1 0xB4, as profinet-py decodes it).
	refused := func(name string) bool {
		return !ok(name) && strings.Contains(errOf(name), "Decode=0xB4") && strings.Contains(errOf(name), "ErrorCode1=0x80")
	}
	e.expect("Read of that record in API 0 refused: invalid area/API (0xB4)", refused("read 0@3:1:0x11"), "%s", errOf("read 0@3:1:0x11"))
	e.expect("Read in an API the device does not have (0x4E02) refused: invalid area/API (0xB4)", refused("read 0x4e02@3:1:0x11"), "%s", errOf("read 0x4e02@3:1:0x11"))
	decode := func(name string) (pnio.RawBlock, error) {
		b, err := hex.DecodeString(val(name))
		if err != nil {
			return pnio.RawBlock{}, err
		}
		blk, _, err := pnio.ReadBlock(b)
		return blk, err
	}
	var apis []uint32
	blk, err := decode("read 0:1:0xF821")
	if err == nil {
		var d pnio.APIData
		d, err = pnio.DecodeAPIData(blk)
		apis = d.APIs
	}
	e.expect("APIData (0xF821) read by profinet-py: APIs 0 and 0x4E01", ok("read 0:1:0xF821") && err == nil && slices.Equal(apis, []uint32{0, 0x4E01}), "%v %v %s", apis, err, val("read 0:1:0xF821"))
	var ident string
	blk, err = decode("read 0x4e01@3:1:0xF000")
	if err == nil {
		var d pnio.IdentificationData
		d, err = pnio.DecodeIdentificationData(blk)
		for _, a := range d.APIs {
			for _, sl := range a.Slots {
				for _, sm := range sl.Submodules {
					ident += fmt.Sprintf("%#x %d/%d:%#x/%#x ", a.API, sl.Slot, sm.Subslot, sl.IdentNumber, sm.IdentNumber)
				}
			}
		}
	}
	e.expect("RealIdentificationData for API 0x4E01 (0xF000) read by profinet-py: slot 3 alone", err == nil && ident == "0x4e01 3/1:0x30/0x31 ", "%q %v", ident, err)
	// profinet-py's slot discovery reads 0xF000 in API 0, which Wireshark's
	// index table names "RealIdentificationData for one API": API 0's
	// slots, without slot 3.
	// Identification of slot 3 at slot level (0xC001) and subslot level
	// (0x8001) in its API: the same one submodule. In API 0, the slot-level
	// read is answered with API 0 and no slot (API 0 has none at slot 3),
	// and the subslot-level one is refused: go-profinet's rule
	// (LIMITATIONS.md), observed by an independent controller.
	identOf := func(name string) (string, error) {
		blk, err := decode(name)
		if err != nil {
			return "", err
		}
		d, err := pnio.DecodeIdentificationData(blk)
		out := ""
		for _, a := range d.APIs {
			out += fmt.Sprintf("api %#x:", a.API)
			for _, sl := range a.Slots {
				for _, sm := range sl.Submodules {
					out += fmt.Sprintf(" %d/%d:%#x/%#x", sl.Slot, sm.Subslot, sl.IdentNumber, sm.IdentNumber)
				}
			}
			out += ";"
		}
		return out, err
	}
	const slot3Ident = "api 0x4e01: 3/1:0x30/0x31;"
	id, err := identOf("read 0x4e01@3:1:0xC001")
	e.expect("RealIdentificationData for slot 3 in API 0x4E01 (0xC001) read by profinet-py: that slot's one submodule", ok("read 0x4e01@3:1:0xC001") && err == nil && id == slot3Ident, "%q %v %s", id, err, errOf("read 0x4e01@3:1:0xC001"))
	id, err = identOf("read 0x4e01@3:1:0x8001")
	e.expect("RealIdentificationData for subslot 3/1 in API 0x4E01 (0x8001) read by profinet-py: that submodule", ok("read 0x4e01@3:1:0x8001") && err == nil && id == slot3Ident, "%q %v %s", id, err, errOf("read 0x4e01@3:1:0x8001"))
	id, err = identOf("read 0@3:1:0xC001")
	e.expect("RealIdentificationData for slot 3 in API 0 (0xC001): API 0 without a slot", ok("read 0@3:1:0xC001") && err == nil && id == "api 0x0:;", "%q %v %s", id, err, errOf("read 0@3:1:0xC001"))
	e.expect("RealIdentificationData for subslot 3/1 in API 0 (0x8001) refused: invalid area/API (0xB4)", refused("read 0@3:1:0x8001"), "%s", errOf("read 0@3:1:0x8001"))

	// Profile records (0xB000-0xBFFF) of the submodule in API 0x4E01, as
	// generic records: their transport and the model's access rules, not
	// a profile's meaning. The step names repeat; the report keeps every
	// step in order.
	all := func(name string) (vals, errs []string) {
		for _, st := range rep.Steps {
			if st.Step == name {
				if st.OK {
					vals = append(vals, str(st.Value))
				} else {
					errs = append(errs, st.Error)
				}
			}
		}
		return vals, errs
	}
	written := hex.EncodeToString(profileWrite)
	b600, b600Errs := all("read 0x4e01@3:1:0xB600")
	e.expect("Read profile record 0xB600 in API 0x4E01: its default", len(b600) == 3 && b600[0] == "80010110fffff0f0f0f0f0f0f0ff0005", "%v %v", b600, b600Errs)
	e.expect("Write profile record 0xB600 in API 0x4E01, read back", ok("write 0x4e01@3:1:0xB600:"+written) && len(b600) == 3 && b600[1] == written, "%v %s", b600, errOf("write 0x4e01@3:1:0xB600:"+written))
	e.expect("Read of 0xB600 in API 0 refused: invalid area/API (0xB4)", refused("read 0@3:1:0xB600"), "%s", errOf("read 0@3:1:0xB600"))
	e.expect("Read of 0xB600 in API 0x4E02 refused: invalid area/API (0xB4)", refused("read 0x4e02@3:1:0xB600"), "%s", errOf("read 0x4e02@3:1:0xB600"))
	// profinet-py prints the PNIORW codes as Decode=ErrorCode1 and
	// ErrorCode1=ErrorDecode; 0xDF is a write's ErrorCode.
	writeRefused := func(name string, code string) bool {
		er := errOf(name)
		return !ok(name) && strings.Contains(er, "Decode="+code) && strings.Contains(er, "ErrorCode1=0x80") && strings.Contains(er, "ErrorCode2=0xDF")
	}
	e.expect("Write of read-only profile record 0xB900 refused: access denied (0xB6)", writeRefused("write 0x4e01@3:1:0xB900:00000000", "0xB6"), "%s", errOf("write 0x4e01@3:1:0xB900:00000000"))
	e.expect("Write of 2 octets to 0xB600 (length 16) refused: write length (0xB1)", writeRefused("write 0x4e01@3:1:0xB600:0102", "0xB1"), "%s", errOf("write 0x4e01@3:1:0xB600:0102"))
	e.expect("After the refused requests: 0xB900 keeps its default, 0xB600 the value written", val("read 0x4e01@3:1:0xB900") == "80010011" && len(b600) == 3 && b600[2] == written, "%s %v", val("read 0x4e01@3:1:0xB900"), b600)
	e.expect("Slot discovery by profinet-py (0xF000 in API 0): API 0's slots, not slot 3", ok("discover-slots") && strings.Contains(val("discover-slots"), "slot=2") && !strings.Contains(val("discover-slots"), "slot=3"), "%s", val("discover-slots"))
	e.expect("GSDML with a submodule in API 0x4E01, written by go-profinet, parsed by profinet-py; cyclic AR (IOCARSingle) established", ok("start-cyclic") && strings.Contains(val("start-cyclic"), "slot:3"), "%v %s", val("start-cyclic"), errOf("start-cyclic"))
	e.expect("Cyclic inputs of the submodule in API 0x4E01 at profinet-py", ok("input 3:1") && len(val("input 3:1")) == 4, "%s", val("input 3:1"))
	evs := e.events(dev)
	outputs := map[string]bool{}
	for _, ev := range evs {
		if ev["event"] == "output" {
			outputs[fmt.Sprintf("%v:%v=%v", ev["slot"], ev["subslot"], ev["data"])] = true
		}
	}
	e.expect("Cyclic outputs from profinet-py at the device, in API 0x4E01 (slot 3) and API 0 (slot 2)", outputs["3:1=beef"] && outputs["2:1=a1b2c3d4"], "%v", outputs)
	var writes []string
	for _, ev := range evs {
		if ev["event"] == "write" {
			writes = append(writes, fmt.Sprintf("%v:%v:%v=%v", ev["slot"], ev["subslot"], ev["index"], ev["data"]))
		}
	}
	e.expect("The device received the write in API 0x4E01", slices.Contains(writes, "3:1:17=0a0b"), "%v", writes)
	e.expect("The device received the write of 0xB600 and neither refused write", slices.Contains(writes, "3:1:46592="+written) && !slices.ContainsFunc(writes, func(w string) bool {
		return strings.HasPrefix(w, "3:1:47360=") || w == "3:1:46592=0102"
	}), "%v", writes)
	pcap := stop()
	e.oracles(pcap, peerDeviation{Protocol: "PNIO-CM", Message: "Trailing stray characters",
		Why: "profinet-py writes I&M1 with BlockLength 58 and two padding octets; the standard block is 56 octets (tshark flags the extra octets of its Write request)"})
	// What tshark decodes: profinet-py's Connect names both APIs, and the
	// record frames of index 0x0011 name the API of each request.
	fr := e.tsharkFields(pcap, "dcerpc.pkt_type == 0 && dcerpc.opnum == 0 && pn_io.iocr_type", "pn_io.api")
	e.expect("tshark fields: profinet-py's Connect request names APIs 0x00000000 and 0x00004e01", len(fr) == 1 &&
		slices.Contains(fr[0].Fields["pn_io.api"], "0x00004e01") && slices.Contains(fr[0].Fields["pn_io.api"], "0x00000000"), "%+v", fr)
	fr = e.tsharkFields(pcap, "pn_io.index == 0x0011", "pn_io.api", "pn_io.slot_nr", "pn_io.subslot_nr")
	n := map[string]int{}
	slot3 := true
	for _, f := range fr {
		for _, a := range f.Fields["pn_io.api"] {
			n[a]++
		}
		slot3 = slot3 && allEqual(f.Fields["pn_io.slot_nr"], "0x0003") && allEqual(f.Fields["pn_io.subslot_nr"], "0x0001")
	}
	e.expect("tshark fields: record 0x0011 read and written in API 0x00004e01, slot 3, subslot 1; the refused reads in APIs 0 and 0x00004e02", slot3 && n["0x00004e01"] >= 6 && n["0x00000000"] >= 1 && n["0x00004e02"] >= 1, "%v %+v", n, fr)
	// The Write of 0xB600: the header's API, slot, subslot, index and
	// RecordDataLength, and the data as profile user data (pn.user_data, as
	// in TestCodecDecoders). The filter matches the frame only if its last
	// 16 octets are the data written.
	fr = e.tsharkFields(pcap, "pn_io.index == 0xb600 && dcerpc.pkt_type == 0 && frame[-16:] == "+colonHex(profileWrite),
		"pn_io.api", "pn_io.slot_nr", "pn_io.subslot_nr", "pn_io.index", "pn_io.record_data_length", "pn.user_data")
	e.expect("tshark fields: profinet-py's Write of 0xB600 in API 0x00004e01, slot 3, subslot 1, RecordDataLength 16, its 16 octets as profile user data",
		len(fr) == 1 && slices.Equal(fr[0].Fields["pn_io.api"], []string{"0x00004e01"}) &&
			slices.Equal(fr[0].Fields["pn_io.slot_nr"], []string{"0x0003"}) && slices.Equal(fr[0].Fields["pn_io.subslot_nr"], []string{"0x0001"}) &&
			slices.Equal(fr[0].Fields["pn_io.index"], []string{"0xb600"}) && slices.Equal(fr[0].Fields["pn_io.record_data_length"], []string{"16"}) &&
			slices.Equal(fr[0].Fields["pn.user_data"], []string{"data"}), "%+v", fr)
	// The identification reads: tshark decodes the header's API of each.
	fr = e.tsharkFields(pcap, "dcerpc.pkt_type == 0 && (pn_io.index == 0xc001 || pn_io.index == 0x8001)", "pn_io.api", "pn_io.index")
	reqs := map[string]bool{}
	for _, f := range fr {
		reqs[strings.Join(f.Fields["pn_io.api"], ",")+" "+strings.Join(f.Fields["pn_io.index"], ",")] = true
	}
	e.expect("tshark fields: the 0xC001 and 0x8001 requests in APIs 0x00004e01 and 0", len(fr) == 4 && reqs["0x00004e01 0xc001"] && reqs["0x00004e01 0x8001"] && reqs["0x00000000 0xc001"] && reqs["0x00000000 0x8001"], "%v %+v", reqs, fr)
}

func TestPnioRsReparameterize(t *testing.T) {
	e := newEnv(t)
	img := image("pnio-rs")
	e.result.peer("pnio-rs", img, "device")
	dev := e.start("pniors", img, 30, "device", "--station", "pnio-dut")
	e.waitEvent(dev, 30*time.Second, "start", named("start"))
	im1 := hex.EncodeToString((&pnio.IM1{TagFunction: "reparameterized"}).Append(nil))
	r := report(t, e.otf("ctrl", 2, 90*time.Second, "controller", "-station", "pnio-dut", "-model", "/gsdml-testdata/pnio-rs_sample-16real-32bool.xml",
		"-advanced", "-reparam", "0:1:0xAFF1:"+im1, "-duration", "2s"))
	if r["running"] != true {
		t.Fatalf("not running: %v", r["error"])
	}
	var re map[string]any
	for _, x := range r["records"].([]any) {
		if m := x.(map[string]any); m["op"] == "reparameterize" {
			re = m
		}
	}
	if re != nil && re["error"] == nil {
		e.expect("Re-parameterization in Data (pnio-rs)", re["state"] == "Running", "%v", re)
		return
	}
	e.result.na("Re-parameterization in Data (pnio-rs)", "pnio-rs refuses PrmBegin in Data: "+str(re["error"]))
	e.expect("A refused PrmBegin leaves the AR running (pnio-rs)", re != nil && re["state"] == "Running" && r["state"] == "Running", "%v %v", re, r["state"])
}

// TestProfinetPyReparameterize: profinet-py, an independent IO-Controller,
// re-parameterizes go-profinet's IO-Device in Data: PrmBegin, a record
// write, PrmEnd; the device answers with ApplicationReady and its AR goes
// back to Data while cyclic data flows.
func TestProfinetPyReparameterize(t *testing.T) {
	e := newEnv(t)
	img := image("profinet-py")
	e.result.peer("profinet-py", img, "controller")
	dev := e.startOTF("dut", 20, "device", "-station", "otf-dut", "-gsdml-out", "/share/otf-dut.xml")
	e.waitEvent(dev, 30*time.Second, "start", named("start"))
	stop := e.capture(dev, "profinet-py-reparameterize.pcap")
	out := e.run("pypn", img, 3, 3*time.Minute, "controller", "--station", "otf-dut", "--gsdml", "/share/otf-dut.xml",
		"--read-input", "1:1", "--duration", "2", "--reparam", "2:1:0x10:0009")
	var rep struct {
		Steps []struct {
			Step  string `json:"step"`
			OK    bool   `json:"ok"`
			Value any    `json:"value"`
			Error string `json:"error"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(lastLine(out), &rep); err != nil {
		t.Fatalf("profinet-py output: %v\n%s", err, out)
	}
	res := map[string]string{}
	for _, st := range rep.Steps {
		if st.OK {
			res[st.Step] = "ok"
		} else {
			res[st.Step] = st.Error
		}
	}
	e.expect("Cyclic AR (IOCARSingle) by profinet-py", res["start-cyclic"] == "ok", "%v", res)
	e.expect("PrmBegin in Data accepted (profinet-py)", res["prm-begin"] == "ok", "%s", res["prm-begin"])
	e.expect("Record write between PrmBegin and PrmEnd (profinet-py)", res["reparam-write 2:1:0x10"] == "ok", "%s", res["reparam-write 2:1:0x10"])
	e.expect("PrmEnd after re-parameterization (profinet-py)", res["prm-end"] == "ok", "%s", res["prm-end"])
	e.expect("ApplicationReady after re-parameterization, answered by profinet-py", res["reparam-application-ready"] == "ok", "%s", res["reparam-application-ready"])
	var path []string
	for _, ev := range e.events(dev) {
		if ev["event"] == "ar" && ev["type"] == "IOCARSingle" {
			path = append(path, str(ev["to"]))
		}
	}
	got := strings.Join(path, ",")
	e.expect("Device AR: Data, Parameterizing, ApplicationReadyPending, Data", strings.Contains(got, "Data,Parameterizing,ApplicationReadyPending,Data"), "%s", got)
	e.expect("Cyclic inputs after re-parameterization", res["input 1:1"] == "ok", "%s", res["input 1:1"])
	e.oracles(stop(), peerDeviation{Protocol: "PNIO-CM", Message: "Trailing stray characters",
		Why: "profinet-py writes I&M1 with BlockLength 58 and two padding octets (its scenario's write-im1 step)"})
}

// TestProfinetPyFixtures: profinet-py, an independent GSDML parser and
// IO-Controller, reads go-profinet's synthetic GSDML fixtures (one-port
// device access points) and runs a cyclic AR with a go-profinet device
// configured from the same file and access point.
func TestProfinetPyFixtures(t *testing.T) {
	for _, c := range []struct{ file, dap string }{
		{"GSDML-V2.43-OTFabric-ModularFixture-20261027.xml", "DAP_1P"},
		{"GSDML-V2.35-OTFabric-CompactFixture-20261027.xml", "DAP_C"},
	} {
		t.Run(c.dap, func(t *testing.T) {
			e := newEnv(t)
			img := image("profinet-py")
			e.result.peer("profinet-py", img, "controller")
			path := "/gsdml-testdata/fixtures/" + c.file
			dev := e.startOTF("dut", 20, "device", "-station", "otf-fixture", "-model", path, "-dap", c.dap)
			e.waitEvent(dev, 30*time.Second, "start", named("start"))
			out := e.run("pypn", img, 3, 3*time.Minute, "controller", "--station", "otf-fixture", "--gsdml", path, "--read-input", "1:1", "--duration", "2")
			var rep struct {
				Steps []struct {
					Step  string `json:"step"`
					OK    bool   `json:"ok"`
					Value any    `json:"value"`
					Error string `json:"error"`
				} `json:"steps"`
			}
			if err := json.Unmarshal(lastLine(out), &rep); err != nil {
				t.Fatalf("profinet-py output: %v\n%s", err, out)
			}
			res := map[string]string{}
			vals := map[string]any{}
			for _, st := range rep.Steps {
				res[st.Step], vals[st.Step] = st.Error, st.Value
				if st.OK {
					res[st.Step] = "ok"
				}
			}
			e.expect("GSDML fixture "+c.dap+" parsed by profinet-py; cyclic AR with a device of the same file", res["start-cyclic"] == "ok", "%v", res["start-cyclic"])
			slots := fmt.Sprint(vals["start-cyclic"])
			hasSlot1 := false
			list, _ := vals["start-cyclic"].([]any)
			for _, x := range list {
				if m, _ := x.(map[string]any); m != nil && m["slot"] == 1.0 {
					hasSlot1 = true
				}
			}
			if !hasSlot1 {
				// profinet-py plugs only the modules a DAP fixes in their
				// slots (FixedInSlots); the modular fixture proposes its
				// modules with UsedInSlots, so the AR has the DAP alone.
				e.result.na("Cyclic inputs from the fixture device "+c.dap,
					"profinet-py builds its configuration from FixedInSlots only and ignores UsedInSlots: it connected with the DAP alone ("+slots+")")
				return
			}
			e.expect("Cyclic inputs from the fixture device "+c.dap, res["input 1:1"] == "ok" && str(vals["input 1:1"]) != "", "%v %v", res["input 1:1"], vals["input 1:1"])
		})
	}
}

// TestProfinetPyDCP: profinet-py's DCP tool against go-profinet's
// responder.
func TestProfinetPyDCP(t *testing.T) {
	e := newEnv(t)
	img := image("profinet-py")
	e.result.peer("profinet-py", img, "dcp-tool")
	dev := e.startOTF("dut", 20, "device", "-station", "otf-dut")
	start := e.waitEvent(dev, 30*time.Second, "start", func(m map[string]any) bool { return m["event"] == "start" })
	mac := str(start["mac"])
	py := func(name string, args ...string) map[string]any {
		var d map[string]any
		out := e.run(name, img, 3, 2*time.Minute, append([]string{"dcp"}, args...)...)
		if err := json.Unmarshal(lastLine(out), &d); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		return d
	}
	stepOK := func(d map[string]any) (bool, string) {
		steps, _ := d["steps"].([]any)
		if len(steps) == 0 {
			return false, "no steps"
		}
		s := steps[0].(map[string]any)
		return s["ok"] == true, str(s["value"]) + str(s["error"])
	}
	ok, v := stepOK(py("ident", "identify"))
	e.expect("DCP Identify all", ok && strings.Contains(v, "otf-dut"), "%s", v)
	ok, v = stepOK(py("get", "get", "--mac", mac))
	e.expect("DCP Get name", ok, "%s", v)
	ok, v = stepOK(py("setname", "set-name", "--mac", mac, "py-renamed"))
	e.expect("DCP Set NameOfStation", ok, "%s", v)
	time.Sleep(500 * time.Millisecond)
	e.expect("Device applies the name", func() bool {
		for _, ev := range e.events(dev) {
			if ev["event"] == "name" && ev["name"] == "py-renamed" {
				return true
			}
		}
		return false
	}(), "")
	ok, v = stepOK(py("setip", "set-ip", "--mac", mac, e.ip(21), "255.255.255.0", "0.0.0.0"))
	e.expect("DCP Set IP", ok, "%s", v)
	ok, v = stepOK(py("signal", "signal", "--mac", mac))
	e.expect("DCP Signal", ok, "%s", v)
	ok, v = stepOK(py("reset", "reset", "--mac", mac))
	e.expect("DCP ResetToFactory", ok, "%s", v)
	time.Sleep(500 * time.Millisecond)
	evs := e.events(dev)
	e.expect("Device reports Signal and Reset", hasEvent(evs, "signal") && hasEvent(evs, "reset"), "")
}

// TestPnioDCP: pnio-dcp (Codewerk) against go-profinet's responder.
func TestPnioDCP(t *testing.T) {
	e := newEnv(t)
	img := image("pnio-dcp")
	e.result.peer("pnio-dcp", img, "dcp-tool")
	dev := e.startOTF("dut", 20, "device", "-station", "otf-dut")
	start := e.waitEvent(dev, 30*time.Second, "start", func(m map[string]any) bool { return m["event"] == "start" })
	mac := str(start["mac"])
	stop := e.capture(dev, "pnio-dcp.pcap")
	pd := func(name string, args ...string) map[string]any {
		var d map[string]any
		out := e.run(name, img, 4, 2*time.Minute, append([]string{"dcp"}, args...)...)
		if err := json.Unmarshal(lastLine(out), &d); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		return d
	}
	d := pd("identall", "identify-all", "--timeout", "2")
	e.expect("DCP Identify all", d["ok"] == true && strings.Contains(str(d["devices"]), "otf-dut"), "%v", d)
	d = pd("getname", "get-name", "--mac", mac)
	e.expect("DCP Get NameOfStation", d["value"] == "otf-dut", "%v", d)
	d = pd("setname", "set-name", "--mac", mac, "dcp-renamed", "--temporary")
	e.expect("DCP Set NameOfStation (temporary)", d["code"] == 0.0, "%v", d)
	d = pd("getname2", "get-name", "--mac", mac)
	e.expect("DCP Get reflects the new name", d["value"] == "dcp-renamed", "%v", d)
	d = pd("setip", "set-ip", "--mac", mac, e.ip(22), "255.255.255.0", "0.0.0.0", "--temporary")
	e.expect("DCP Set IP (temporary)", d["code"] == 0.0, "%v", d)
	d = pd("getip", "get-ip", "--mac", mac)
	e.expect("DCP Get IP", d["value"] == e.ip(22), "%v", d)
	d = pd("blink", "blink", "--mac", mac)
	e.expect("DCP Signal (blink)", d["code"] == 0.0, "%v", d)
	d = pd("reset", "reset", "--mac", mac)
	e.expect("DCP ResetToFactory", d["code"] == 0.0, "%v", d)
	e.oracles(stop())
}
