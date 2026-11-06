// SPDX-License-Identifier: MIT

//go:build interop

package interop

import (
	"os"
	"strings"
	"testing"
	"time"
)

// otfabricImage is the image of the adapter otfabric: go-profinet itself,
// a published release behind the profinet-interop contract
// (ghcr.io/otfabric/profinet-interop-otfabric). It is named only by
// PROFINET_INTEROP_OTFABRIC_IMAGE, never by default: the tests below are
// skipped without it, and the Interop workflow does not set it. A pass is
// not independent evidence (INTEROPERABILITY.md); the Self-interop
// workflow runs them to find a change on the wire that breaks an earlier
// release.
func otfabricImage(t *testing.T) string {
	t.Helper()
	img := os.Getenv("PROFINET_INTEROP_OTFABRIC_IMAGE")
	if img == "" {
		t.Skip("PROFINET_INTEROP_OTFABRIC_IMAGE is not set: no otfabric image")
	}
	return img
}

// TestOtfabricDevice: this commit's IO-Controller and DCP requester against
// the IO-Device of the released go-profinet in the otfabric image.
func TestOtfabricDevice(t *testing.T) {
	img := otfabricImage(t)
	e := newEnv(t)
	e.result.peer("otfabric", img, "device (go-profinet release; not independent)")
	dev := e.start("otfab", img, 30, "device", "--station", "otfab-dut", "--gsdml-out", "/share/otfab-dut.xml")
	start := e.waitEvent(dev, 30*time.Second, "start", func(m map[string]any) bool { return m["event"] == "start" })
	mac := str(start["mac"])

	ids := last(jsonLines(e.otf("ident", 3, time.Minute, "dcp", "-station", "otfab-dut", "identify")), "dcp")
	devs, _ := ids["devices"].([]any)
	e.expect("DCP Identify by name", len(devs) == 1 && strings.EqualFold(str(devs[0].(map[string]any)["mac"]), mac), "%v", devs)
	get := last(jsonLines(e.otf("get", 3, time.Minute, "dcp", "-mac", mac, "get")), "dcp")
	id, _ := get["identity"].(map[string]any)
	e.expect("DCP Get NameOfStation", get["error"] == nil && id["nameOfStation"] == "otfab-dut", "%v", get)

	r := report(t, e.otf("ctrl", 2, 90*time.Second, "controller", "-station", "otfab-dut", "-model", "/share/otfab-dut.xml",
		"-im0", "-read", "2:1:0x0010", "-output", "2:1:0:a1b2c3d4", "-output", "3:1:0:beef", "-duration", "3s"))
	e.expect("Connect, PrmEnd, ApplicationReady from the device's GSDML (legacy startup)", r["running"] == true, "%v", r["error"])
	if r["running"] == true {
		im0, _ := r["im0"].(map[string]any)
		e.expect("Read I&M0", im0 != nil && im0["OrderID"] == "OTF-INTEROP-1", "%v", im0)
		in, _ := r["inputs"].(map[string]any)
		one, _ := in["1:1"].(map[string]any)
		e.expect("Cyclic inputs of slot 1", one != nil && len(str(one["data"])) == 16, "%v", in)
		outputs := map[string]bool{}
		for _, ev := range e.events(dev) {
			if ev["event"] == "output" {
				outputs[str(ev["slot"])+":"+str(ev["subslot"])+"="+str(ev["data"])] = true
			}
		}
		e.expect("Cyclic outputs at the device", outputs["2:1=a1b2c3d4"] && outputs["3:1=beef"], "%v", outputs)
	}
	ra := report(t, e.otf("ctrl-adv", 2, 90*time.Second, "controller", "-station", "otfab-dut", "-model", "/share/otfab-dut.xml",
		"-advanced", "-duration", "2s"))
	e.expect("Connect with advanced startup (RTClass 2)", ra["running"] == true, "%v", ra["error"])

	set := last(jsonLines(e.otf("setname", 3, time.Minute, "dcp", "-mac", mac, "set-name", "otfab-renamed")), "dcp")
	e.expect("DCP Set NameOfStation (temporary)", set["error"] == nil, "%v", set)
	get = last(jsonLines(e.otf("get2", 3, time.Minute, "dcp", "-mac", mac, "get")), "dcp")
	id, _ = get["identity"].(map[string]any)
	e.expect("DCP Get reflects the new name", id["nameOfStation"] == "otfab-renamed", "%v", get)
}

// TestOtfabricController: the IO-Controller of the released go-profinet in
// the otfabric image against this commit's IO-Device, and its DCP requester.
func TestOtfabricController(t *testing.T) {
	img := otfabricImage(t)
	e := newEnv(t)
	e.result.peer("otfabric", img, "controller (go-profinet release; not independent)")
	dev := e.startOTF("dut", 20, "device", "-station", "otf-dut", "-gsdml-out", "/share/otf-dut.xml")
	start := e.waitEvent(dev, 30*time.Second, "start", func(m map[string]any) bool { return m["event"] == "start" })
	mac := str(start["mac"])

	ids := last(jsonLines(e.run("ident", img, 3, time.Minute, "dcp", "--station", "otf-dut", "identify")), "dcp")
	devs, _ := ids["devices"].([]any)
	e.expect("DCP Identify by name, from the image", len(devs) == 1 && strings.EqualFold(str(devs[0].(map[string]any)["mac"]), mac), "%v", devs)

	for _, startup := range []struct {
		name string
		args []string
	}{{"legacy startup", nil}, {"advanced startup (RTClass 2)", []string{"--advanced"}}} {
		args := append([]string{"controller", "--station", "otf-dut", "--model", "/share/otf-dut.xml", "--im0",
			"--output", "2:1:0:01020304", "--duration", "3s"}, startup.args...)
		r := report(t, e.run("ctrl", img, 2, 90*time.Second, args...))
		e.expect("Connect, PrmEnd, ApplicationReady from this commit's GSDML, "+startup.name, r["running"] == true, "%v", r["error"])
		if r["running"] != true {
			continue
		}
		im0, _ := r["im0"].(map[string]any)
		e.expect("Read I&M0, "+startup.name, im0 != nil && im0["OrderID"] == "OTF-INTEROP-1", "%v", im0)
		in, _ := r["inputs"].(map[string]any)
		one, _ := in["1:1"].(map[string]any)
		e.expect("Cyclic inputs at the image's controller, "+startup.name, one != nil && len(str(one["data"])) == 16, "%v", in)
	}
	outputs := map[string]bool{}
	for _, ev := range e.events(dev) {
		if ev["event"] == "output" {
			outputs[str(ev["slot"])+":"+str(ev["subslot"])+"="+str(ev["data"])] = true
		}
	}
	e.expect("Cyclic outputs from the image's controller at this commit's device", outputs["2:1=01020304"], "%v", outputs)
}
