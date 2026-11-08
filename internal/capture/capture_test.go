// SPDX-License-Identifier: MIT

package capture_test

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet/alarm"
	"github.com/otfabric/go-profinet/dcp"
	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/internal/capture"
	"github.com/otfabric/go-profinet/lldp"
	"github.com/otfabric/go-profinet/pnio"
	"github.com/otfabric/go-profinet/rpc"
	"github.com/otfabric/go-profinet/rt"
)

// TestManifest: every file below testdata/captures is in SHA256SUMS and in
// manifest.json with its hash and size, every source of a file is
// described, and every committed fixture's class is A..D.
func TestManifest(t *testing.T) {
	m, err := capture.ReadManifest()
	if err != nil {
		t.Fatal(err)
	}
	sums := map[string]string{}
	f, err := os.Open(capture.Path("SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		h, p, ok := strings.Cut(sc.Text(), "  ")
		if !ok {
			t.Fatalf("SHA256SUMS line %q", sc.Text())
		}
		sums[p] = h
	}
	sources := map[string]capture.Source{}
	for _, s := range m.Sources {
		if s.URL == "" || s.Revision == "" || s.License == "" || s.Redistribute == "" || s.Class == "" {
			t.Errorf("source %s is incomplete", s.ID)
		}
		sources[s.ID] = s
	}
	inManifest := map[string]bool{}
	for _, e := range m.Files {
		if _, ok := sources[e.Source]; !ok {
			t.Errorf("%s%s: unknown source %q", e.Path, e.Original, e.Source)
		}
		if !strings.Contains("ABCD", e.Class) || len(e.Class) != 1 {
			t.Errorf("%s%s: class %q", e.Path, e.Original, e.Class)
		}
		if !e.Committed {
			if e.Path != "" || e.Original == "" {
				t.Errorf("referenced file %+v", e)
			}
			continue
		}
		inManifest[e.Path] = true
		b, err := os.ReadFile(capture.Path(e.Path))
		if err != nil {
			t.Error(err)
			continue
		}
		h := sha256.Sum256(b)
		if hex.EncodeToString(h[:]) != e.SHA256 || len(b) != e.Size || sums[e.Path] != e.SHA256 {
			t.Errorf("%s: sha256 %x size %d, manifest %s %d, SHA256SUMS %s", e.Path, h, len(b), e.SHA256, e.Size, sums[e.Path])
		}
	}
	err = filepath.WalkDir(capture.Dir(), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() && d.Name() == ".cache" {
				return filepath.SkipDir
			}
			return err
		}
		rel, _ := filepath.Rel(capture.Dir(), p)
		rel = filepath.ToSlash(rel)
		switch {
		case rel == "README.md" || rel == "SHA256SUMS" || rel == "manifest.json" || rel == "coverage.json":
		case strings.HasSuffix(rel, "/LICENSE-MIT") || strings.HasSuffix(rel, "/LICENSE-APACHE"):
			if sums[rel] == "" {
				t.Errorf("%s not in SHA256SUMS", rel)
			}
		default:
			if !inManifest[rel] || sums[rel] == "" {
				t.Errorf("%s is not in manifest.json and SHA256SUMS", rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestCoverageReport: coverage.json names every committed recording, and
// every test it cites exists.
func TestCoverageReport(t *testing.T) {
	var rep struct {
		Format string `json:"format"`
		Cases  []struct {
			Message string   `json:"message"`
			Files   []string `json:"files"`
			Tests   []string `json:"tests"`
		} `json:"cases"`
	}
	b, err := os.ReadFile(capture.Path("coverage.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &rep); err != nil || rep.Format != "go-profinet capture coverage 1" {
		t.Fatalf("coverage.json: %v %q", err, rep.Format)
	}
	covered := map[string]bool{}
	repo := filepath.Join(capture.Dir(), "..", "..")
	for _, c := range rep.Cases {
		for _, f := range c.Files {
			covered[f] = true
		}
		for _, name := range c.Tests {
			pkg, fn, ok := strings.Cut(name, ".")
			if !ok {
				t.Errorf("%s: test %q", c.Message, name)
				continue
			}
			files, _ := filepath.Glob(filepath.Join(repo, pkg, "*_test.go"))
			found := false
			for _, f := range files {
				src, err := os.ReadFile(f)
				if err == nil && strings.Contains(string(src), "func "+fn+"(") {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: no test %s", c.Message, name)
			}
		}
	}
	m, err := capture.ReadManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range m.Files {
		if e.Committed && !covered[e.Path] {
			t.Errorf("%s is in no case of coverage.json", e.Path)
		}
	}
}

func TestParseHex(t *testing.T) {
	h, err := capture.ParseHex("# one\n0a 0b # two\n\t0c\r\n")
	if err != nil || string(h.Frame) != "\x0a\x0b\x0c" || h.Comment != "one\ntwo" {
		t.Fatalf("%+v %v", h, err)
	}
	for _, s := range []string{"", "# only\n", "0g", "abc"} {
		if _, err := capture.ParseHex(s); !errors.Is(err, capture.ErrFormat) {
			t.Errorf("%q: %v", s, err)
		}
	}
	if _, err := capture.ReadHex("no/such.hex"); err == nil {
		t.Error("missing fixture read")
	}
}

func TestDecodeUDP(t *testing.T) {
	good := capture.MustHex("pnio-rs/cm/prmend_req.hex")
	u, err := capture.DecodeUDP(good)
	if err != nil || u.Src.String() != "172.16.2.100:54766" || u.Dst.String() != "172.16.2.10:34964" || len(u.Payload) != 132 {
		t.Fatalf("%v %v %d", u.Src, err, len(u.Payload))
	}
	mut := func(off int, v byte) []byte {
		b := append([]byte(nil), good...)
		b[off] = v
		return b
	}
	for name, b := range map[string][]byte{
		"not IPv4":     capture.MustHex("pnio-rs/cm/dcp_set_res.hex"),
		"version 6":    mut(14, 0x65),
		"short IHL":    mut(14, 0x44),
		"total length": mut(16, 0xFF),
		"TCP":          mut(23, 6),
		"UDP length":   mut(38, 0xFF),
		"short":        good[:30],
	} {
		if _, err := capture.DecodeUDP(b); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
}

func TestReadPcap(t *testing.T) {
	fr, err := capture.ReadPcap("wireshark/ChangeIPUsingDCP.pcap")
	if err != nil || len(fr) != 6 {
		t.Fatalf("%d frames, %v", len(fr), err)
	}
	if _, err := capture.ReadPcap("pnio-rs/cm/connect_req.hex"); err == nil {
		t.Error("hex read as pcap")
	}
	// A minimal pcapng file: section header, Ethernet interface, one
	// packet; and its cut and malformed variants.
	le := binary.LittleEndian
	block := func(typ uint32, body []byte) []byte {
		n := 12 + len(body)
		b := le.AppendUint32(nil, typ)
		b = le.AppendUint32(b, uint32(n))
		b = append(b, body...)
		return le.AppendUint32(b, uint32(n))
	}
	shb := block(0x0A0D0D0A, append(le.AppendUint32(nil, 0x1A2B3C4D), 1, 0, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF))
	idb := block(1, []byte{1, 0, 0, 0, 0, 0, 0, 0})
	pkt := append(make([]byte, 12), le.AppendUint32(le.AppendUint32(nil, 4), 4)...)
	epb := block(6, append(pkt, 1, 2, 3, 4))
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	file := append(append(append([]byte(nil), shb...), idb...), epb...)
	fr, err = capture.ReadPcap(write("ok.pcapng", file))
	if err != nil || len(fr) != 1 || string(fr[0]) != "\x01\x02\x03\x04" {
		t.Fatalf("%x %v", fr, err)
	}
	bad := map[string][]byte{
		"cut":          file[:len(file)-3],
		"not ethernet": append(append(append([]byte(nil), shb...), block(1, []byte{105, 0, 0, 0})...), epb...),
		"short packet": append(append(append([]byte(nil), shb...), idb...), block(6, make([]byte, 8))...),
		"capture len":  append(append(append([]byte(nil), shb...), idb...), block(6, append(append(make([]byte, 12), le.AppendUint32(nil, 99)...), 0, 0, 0, 0))...),
		"block length": append(append([]byte(nil), shb...), 1, 0, 0, 0, 7, 0, 0, 0, 0, 0, 0, 0),
	}
	for name, b := range bad {
		if _, err := capture.ReadPcap(write(name, b)); !errors.Is(err, capture.ErrFormat) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// recorded returns every recorded frame.
func recorded(t *testing.T) map[string][]byte {
	t.Helper()
	m, err := capture.ReadManifest()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, e := range m.Files {
		switch {
		case !e.Committed:
		case strings.HasSuffix(e.Path, ".hex"):
			out[e.Path] = capture.MustHex(e.Path)
		case strings.HasSuffix(e.Path, ".pcap"):
			fr, err := capture.ReadPcap(e.Path)
			if err != nil {
				t.Fatal(err)
			}
			for i, f := range fr {
				out[e.Path+"#"+string(rune('1'+i))] = f
			}
		}
	}
	return out
}

// decodeAll runs a frame through every decoder that takes it. It returns
// whether the frame decoded completely.
func decodeAll(frame []byte) bool {
	e, err := ethernet.Decode(frame)
	if err != nil {
		return false
	}
	ok := true
	switch e.EtherType {
	case ethernet.EtherTypeLLDP:
		_, err = lldp.Decode(e.Payload)
		return err == nil
	case ethernet.EtherTypeProfinet:
		if len(e.Payload) < 2 {
			return false
		}
		switch id := binary.BigEndian.Uint16(e.Payload); {
		case id >= dcp.FrameIDHello:
			f, err := dcp.Decode(e.Payload)
			if err == nil && f.Type != dcp.TypeRequest && f.Service == dcp.ServiceIdentify {
				_, err = dcp.ParseIdentity(e.Src, f.Blocks)
			}
			return err == nil
		case id == pnio.FrameIDAlarmHigh || id == pnio.FrameIDAlarmLow:
			p, err := alarm.Decode(e.Payload)
			if err != nil {
				return false
			}
			if p.Type == alarm.PDUData {
				if blk, _, err := pnio.ReadBlock(p.VarPart); err == nil {
					switch blk.Header.Type {
					case pnio.BlockAlarmNotificationHigh, pnio.BlockAlarmNotificationLow:
						_, err = pnio.DecodeAlarmNotification(blk)
					default:
						_, err = pnio.DecodeAlarmAck(blk)
					}
					ok = err == nil
				} else {
					ok = false
				}
			}
			return ok
		default:
			_, err := rt.Decode(e.Payload, 40)
			return err == nil
		}
	case ethernet.EtherTypeIPv4:
	case ethernet.EtherTypeARP, ethernet.EtherTypeVLAN:
		return false
	}
	u, err := capture.DecodeUDP(frame)
	if err != nil {
		return false
	}
	h, body, err := rpc.DecodeHeader(u.Payload)
	if err != nil {
		return false
	}
	var blocks []byte
	if h.Type == rpc.TypeRequest {
		_, blocks, err = pnio.DecodeNDRRequest(body, h.Order())
	} else {
		_, blocks, err = pnio.DecodeNDRResponse(body, h.Order())
	}
	if err != nil {
		return false
	}
	if h.OpNum == uint16(pnio.OpConnect) {
		if h.Type == rpc.TypeRequest {
			_, err = pnio.DecodeConnectRequest(blocks)
		} else {
			_, err = pnio.DecodeConnectResponse(blocks)
		}
		return err == nil
	}
	_, err = pnio.SplitBlocks(blocks)
	return err == nil || h.OpNum == uint16(pnio.OpWrite) // a MultipleWrite is not a plain block list
}

// TestRecordedFramesDecode: every recorded frame decodes completely.
func TestRecordedFramesDecode(t *testing.T) {
	for name, f := range recorded(t) {
		if !decodeAll(f) && !strings.Contains(name, "ChangeIPUsingDCP.pcap#5") && !strings.Contains(name, "ChangeIPUsingDCP.pcap#6") { // ARP
			t.Errorf("%s does not decode", name)
		}
	}
}

// TestMalformedVariants derives malformed frames from every recorded one,
// deterministically: every truncation, and at every offset a length-like
// 16-bit field set to 0x0000 and to 0xFFFF, and a 32-bit one to
// 0xFFFFFFFF. No decoder may panic or hang; the original frames decode.
func TestMalformedVariants(t *testing.T) {
	n := 0
	for name, f := range recorded(t) {
		for i := range len(f) {
			decodeAll(f[:i])
			n++
		}
		for i := 0; i+2 <= len(f); i++ {
			for _, v := range []uint16{0, 0xFFFF} {
				b := append([]byte(nil), f...)
				binary.BigEndian.PutUint16(b[i:], v)
				decodeAll(b)
				n++
			}
			if i+4 <= len(f) {
				b := append([]byte(nil), f...)
				binary.BigEndian.PutUint32(b[i:], 0xFFFFFFFF)
				decodeAll(b)
				n++
			}
		}
		_ = name
	}
	if n < 10000 {
		t.Fatalf("only %d variants", n)
	}
}

// TestExtendedCorpus checks the raw files make captures-extended fetched
// into testdata/captures/.cache: their SHA256 is the manifest's, and every
// committed excerpt is, byte for byte, the frame of the raw file it names.
// Without the cache it is skipped unless PROFINET_CAPTURES_EXTENDED=1
// requires it.
func TestExtendedCorpus(t *testing.T) {
	cache := capture.Path(".cache")
	if _, err := os.Stat(cache); err != nil {
		if os.Getenv("PROFINET_CAPTURES_EXTENDED") == "1" {
			t.Fatalf("PROFINET_CAPTURES_EXTENDED=1 but no %s: run make captures-extended", cache)
		}
		t.Skip("no fetched raw captures (make captures-extended)")
	}
	m, err := capture.ReadManifest()
	if err != nil {
		t.Fatal(err)
	}
	raw := map[string][][]byte{}
	for _, e := range m.Files {
		if e.Committed || e.SHA256 == "" {
			continue
		}
		p := filepath.Join(cache, e.Source, filepath.FromSlash(e.Original))
		b, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("%s: %v", e.Original, err)
			continue
		}
		if h := sha256.Sum256(b); hex.EncodeToString(h[:]) != e.SHA256 {
			t.Errorf("%s: sha256 %x, manifest %s", e.Original, h, e.SHA256)
			continue
		}
		fr, err := capture.ReadPcap(p)
		if err != nil {
			t.Fatalf("%s: %v", e.Original, err)
		}
		raw[e.Original] = fr
		for i, f := range fr {
			_ = i
			decodeAll(f)
		}
	}
	for _, e := range m.Files {
		if !e.Committed || e.Source != "ics-pcaps" {
			continue
		}
		fr, ok := raw[e.Original]
		if !ok {
			t.Errorf("%s: raw file %s missing", e.Path, e.Original)
			continue
		}
		var k int
		for _, c := range e.Frames {
			k = k*10 + int(c-'0')
		}
		if k < 1 || k > len(fr) || string(fr[k-1]) != string(capture.MustHex(e.Path)) {
			t.Errorf("%s is not frame %d of %s", e.Path, k, e.Original)
		}
	}
}
