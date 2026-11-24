// SPDX-License-Identifier: MIT

//go:build interop

package interop

import (
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// pnetProfilePeer is the peer name of profinet-interop's
// profinet-interop-pnet-profile image in the results: p-net with a local
// extension, not the reference p-net.
const pnetProfilePeer = "p-net, profile-records variant"

// profileB is the second value TestPnetProfile writes to 0xB600.
var profileB = []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10}

// TestPnetProfile: go-profinet as IO-Controller of profinet-interop's
// profile-records variant of p-net (make image-pnet-profile, v0.15.0 and
// later), with the IO module in API 0x4E01 (pnet-model-api.json). That
// image is p-net extended by OT Fabric (adapters/pnet/profile-records.sh):
// its stack gives the subslot profile indexes 0xB000-0xBFFF to the
// application, as it does 0x0000-0x7FFF, in an AR only, and its sample
// keeps a 16-octet record 0xB600 on the IO module's submodule. Unmodified
// p-net refuses a Write of 0xB600 (DF 80 B4 02). The test shows that
// go-profinet's controller writes a profile record at Connect and in Data
// and reads it back from a separately implemented stack; the extension is
// OT Fabric's, so this does not validate p-net's handling of profile
// records.
func TestPnetProfile(t *testing.T) {
	e := newEnv(t)
	dev := pnetImage(e, "pnet-profile", "pnet-profile", pnetProfilePeer)
	stop := e.capture(dev, "pnet-profile.pcap")
	at := func(rec string) string { return fmt.Sprintf("%#x@%s", apiIOLink, rec) }
	a, b := hex.EncodeToString(profileWrite), hex.EncodeToString(profileB)
	records := func(r map[string]any) map[string]map[string]any {
		out := map[string]map[string]any{}
		for _, list := range []string{"records", "implicit"} {
			recs, _ := r[list].([]any)
			for _, x := range recs {
				m := x.(map[string]any)
				api, _ := m["api"].(float64)
				op := str(m["op"])
				if list == "implicit" {
					op = "implicit"
				}
				out[fmt.Sprintf("%s %#x/%v/%v/%#x", op, uint32(api), m["slot"], m["subslot"], uint16(m["index"].(float64)))] = m
			}
		}
		return out
	}

	// AR 1: 0xB600 written before PrmEnd (a parameter of the submodule in
	// API 0x4E01), read back; the indexes around the profile range.
	out := e.otf("ctrl", 2, 90*time.Second, "controller", "-station", "pnet-profile", "-model", "/otfnode-testdata/pnet-model-api.json",
		"-param", "1:1:0xB600:"+a, "-read", at("1:1:0xB600"),
		"-read", at("1:1:0xB000"), "-read", at("1:1:0xBFFF"), "-read", at("1:1:0x7FFF"), "-read", at("1:1:0xAFFF"),
		"-implicit", at("1:1:0xB600"), "-duration", "2s")
	r := report(t, out)
	e.expect("Connect with profile record 0xB600 written before PrmEnd in API 0x4E01, PrmEnd, ApplicationReady", r["running"] == true, "running=%v error=%v", r["running"], r["error"])
	if r["running"] != true {
		return
	}
	k := records(r)
	t.Logf("AR 1 records %v", k)
	e.expect("Profile record 0xB600 written at Connect, read back in API 0x4E01: the 16 octets written", k["read 0x4e01/1/1/0xb600"]["data"] == a, "%v", k["read 0x4e01/1/1/0xb600"])
	for _, idx := range []uint16{0xB000, 0xBFFF} {
		key := fmt.Sprintf("read 0x4e01/1/1/%#x", idx)
		e.expect(fmt.Sprintf("Profile index 0x%04X the submodule does not have: application read error (DE 80 A0 00)", idx),
			strings.HasPrefix(str(k[key]["status"]), "DE 80 A0 00"), "%v", k[key])
	}
	e.expect("User index 0x7FFF the submodule does not have: application read error (DE 80 A0 00), as unmodified p-net answers",
		strings.HasPrefix(str(k["read 0x4e01/1/1/0x7fff"]["status"]), "DE 80 A0 00"), "%v", k["read 0x4e01/1/1/0x7fff"])
	e.expect("Index 0xAFFF (I&M15): invalid index (DE 80 B0 0A), as unmodified p-net answers",
		strings.HasPrefix(str(k["read 0x4e01/1/1/0xafff"]["status"]), "DE 80 B0 0A"), "%v", k["read 0x4e01/1/1/0xafff"])
	e.expect("ReadImplicit of 0xB600 (no AR): invalid index (DE 80 B0 0A), not given to the application",
		strings.Contains(str(k["implicit 0x4e01/1/1/0xb600"]["error"]), "DE 80 B0 0A"), "%v", k["implicit 0x4e01/1/1/0xb600"])
	e.expect("Release ends the first AR at p-net", e.eventually(dev, 5*time.Second, named("release", "closed")), "")

	// AR 2: 0xB600 written in Data and read back; writes refused by the
	// application: the wrong length, an index the submodule does not have.
	out = e.otf("ctrl-2", 2, 90*time.Second, "controller", "-station", "pnet-profile", "-model", "/otfnode-testdata/pnet-model-api.json",
		"-param", "1:1:0xB600:"+a, "-write", at("1:1:0xB600:"+b), "-read", at("1:1:0xB600"),
		"-write", at("1:1:0xB600:0102"), "-write", at("1:1:0xB000:00000000"), "-duration", "2s")
	r = report(t, out)
	e.expect("Second Connect with 0xB600 before PrmEnd", r["running"] == true, "running=%v error=%v", r["running"], r["error"])
	if r["running"] != true {
		return
	}
	writes, _ := r["records"].([]any)
	var w []map[string]any
	for _, x := range writes {
		if m := x.(map[string]any); m["op"] == "write" {
			w = append(w, m)
		}
	}
	k = records(r)
	t.Logf("AR 2 records %v", k)
	e.expect("Profile record 0xB600 written in Data in API 0x4E01, read back: the 16 octets written",
		len(w) == 3 && w[0]["error"] == nil && k["read 0x4e01/1/1/0xb600"]["data"] == b, "%v %v", w, k["read 0x4e01/1/1/0xb600"])
	e.expect("Profile record 0xB600 of the wrong length (2 octets): application write error (DF 80 A1 00)",
		len(w) == 3 && strings.HasPrefix(str(w[1]["status"]), "DF 80 A1 00"), "%v", w)
	e.expect("Profile index 0xB000 the submodule does not have: application write error (DF 80 A1 00)",
		len(w) == 3 && strings.HasPrefix(str(w[2]["status"]), "DF 80 A1 00"), "%v", w)
	e.expect("Release ends the second AR at p-net", e.eventually(dev, 5*time.Second, named("release", "closed")), "")

	pcap := stop()
	e.oracles(pcap)
	// Every 0xB600 request and answer names API 0x4E01, slot 1, subslot 1.
	fr := e.tsharkFields(pcap, "pn_io.index == 0xb600", "pn_io.api", "pn_io.slot_nr", "pn_io.subslot_nr")
	e.expect("tshark fields: every 0xB600 request and answer in API 0x00004e01, slot 1, subslot 1", len(fr) >= 10 && !slices.ContainsFunc(fr, func(f fieldFrame) bool {
		return !allEqual(f.Fields["pn_io.api"], "0x00004e01") || !allEqual(f.Fields["pn_io.slot_nr"], "0x0001") || !allEqual(f.Fields["pn_io.subslot_nr"], "0x0001")
	}), "%+v", fr)
	// The payloads: the record is the last 16 octets of a Write request
	// and of a Read response (opnum 3 and 2, pkt_type 0 request, 2 answer).
	count := func(filter string) int { return len(e.tsharkFields(pcap, filter, "pn_io.record_data_length")) }
	const wr, rd = "pn_io.index == 0xb600 && dcerpc.opnum == 3 && dcerpc.pkt_type == 0 && pn_io.record_data_length == 16", "pn_io.index == 0xb600 && dcerpc.opnum == 2 && dcerpc.pkt_type == 2 && pn_io.record_data_length == 16"
	e.expect("tshark: 0xB600 Write requests of 16 octets: the first value at both Connects, the second in Data",
		count(wr) == 3 && count(wr+" && frame[-16:] == "+colonHex(profileWrite)) == 2 && count(wr+" && frame[-16:] == "+colonHex(profileB)) == 1, "")
	e.expect("tshark: 0xB600 Read responses of 16 octets: the first value, then the second",
		count(rd) == 2 && count(rd+" && frame[-16:] == "+colonHex(profileWrite)) == 1 && count(rd+" && frame[-16:] == "+colonHex(profileB)) == 1, "")
}
