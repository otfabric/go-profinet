// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-profinet/gsdml"
)

// TestMain makes the test binary the child process: `-one FILE` analyses a
// file as the command does. Two environment variables, set by the tests
// only, make the child crash or hang.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "-one" {
		switch os.Getenv("GSDMLCORPUS_TEST_CHILD") {
		case "panic":
			panic("test panic")
		case "hang":
			time.Sleep(time.Hour)
		}
		os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

const fixtures = "../../../gsdml/testdata/fixtures/"

func fixture(t *testing.T, name string, edits ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(fixtures + name)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(edits); i += 2 {
		if !bytes.Contains(b, []byte(edits[i])) {
			t.Fatalf("no %s", edits[i])
		}
		b = bytes.Replace(b, []byte(edits[i]), []byte(edits[i+1]), 1)
	}
	return b
}

const modular = "GSDML-V2.43-OTFabric-ModularFixture-20261027.xml"

func TestAnalyze(t *testing.T) {
	t.Parallel()
	r := analyze(fixture(t, modular))
	if r.Stage != "" || r.SchemaVersion != "2.43" || len(r.DAPs) != 2 || r.Modules != 4 || r.Records == 0 || r.Check == nil || r.Check.Errors != 0 {
		t.Fatalf("%+v", r)
	}
	if d := r.DAPs[0]; d.Outcome != gsdml.OutcomeConverted || d.References != 4 || d.Plugged != 3 || d.RecordOctets == 0 || len(d.Failures) != 0 {
		t.Fatalf("%+v", d)
	}
	// A module that does not convert: stage model, a module failure.
	r = analyze(fixture(t, modular, `<ModuleItem ID="MOD_DO16" ModuleIdentNumber="0x00000020">`, `<ModuleItem ID="MOD_DO16" ModuleIdentNumber="x">`))
	if r.Stage != StageModel || !r.DAPs[0].Blocked || len(r.DAPs[0].Failures) != 1 || r.DAPs[0].Failures[0].Kind != "module" || r.Check.Errors == 0 {
		t.Fatalf("%+v", r)
	}
	// A reference to no module: a reference failure.
	r = analyze(fixture(t, modular, `<ModuleItemRef ModuleItemTarget="MOD_DIO8" AllowedInSlots="1..8"/>`, `<ModuleItemRef ModuleItemTarget="MOD_NONE" AllowedInSlots="1..8"/>`))
	if f := r.DAPs[0].Failures; len(f) != 1 || f[0].Kind != "reference" || f[0].Class != gsdml.OutcomeMalformed {
		t.Fatalf("%+v", r.DAPs[0])
	}
	// Not XML: stage parse.
	if r = analyze([]byte("<x")); r.Stage != StageParse || r.ParseOutcome != gsdml.OutcomeMalformed || r.Check != nil {
		t.Fatalf("%+v", r)
	}
	// XML without device access points: one row with the error.
	r = analyze([]byte(`<ISO15745Profile xmlns="http://www.profibus.com/GSDML/2003/11/DeviceProfile"/>`))
	if r.Stage != StageModel || len(r.DAPs) != 1 || r.DAPs[0].Outcome != gsdml.OutcomeMalformed {
		t.Fatalf("%+v", r)
	}
}

// Submodules of an API other than 0 and records above 0x7FFF: counted, and
// a DAP that converts with an API keeps it in its model (both DAPs of the
// fixture use MOD_DI16).
func TestFidelity(t *testing.T) {
	t.Parallel()
	b := fixture(t, modular, `<VirtualSubmoduleItem ID="SM_DI16"`, `<VirtualSubmoduleItem API="19969" ID="SM_DI16"`)
	r := analyze(b)
	if r.APIs["0x4E01"] != 1 || r.RecordIndexes[userRange] == 0 || r.DAPs[0].Outcome != "converted" || !slicesEqual(r.DAPs[0].APIs, []string{"0x4E01"}) || !slicesEqual(r.DAPs[0].PluggedAPIs, []string{"0x4E01"}) || !slicesEqual(r.DAPs[0].ModelAPIs, []string{"0x4E01"}) {
		t.Fatalf("%+v", r)
	}
	// MOD_DIO8 is only in DAP_1P's catalog, and plugged in slot 4 of
	// DAP_2P: its API is in the plugged configuration of DAP_2P only.
	cat := analyze(fixture(t, modular, `<VirtualSubmoduleItem ID="SM_DIO8"`, `<VirtualSubmoduleItem API="0x3A00" ID="SM_DIO8"`))
	if d := cat.DAPs[0]; !slicesEqual(d.APIs, []string{"0x3A00"}) || len(d.PluggedAPIs) != 0 {
		t.Fatalf("DAP_1P %+v", d)
	}
	if d := cat.DAPs[1]; !slicesEqual(d.PluggedAPIs, []string{"0x3A00"}) || !slicesEqual(d.ModelAPIs, []string{"0x3A00"}) {
		t.Fatalf("DAP_2P %+v", d)
	}
	if d := cat.DAPs[0]; len(d.ModelAPIs) != 0 {
		t.Fatalf("DAP_1P model %+v", d)
	}
	if f := Summarize([]FileResult{cat}).Fidelity; f.DAPsAPI != 2 || f.ConvertedAPI != 2 || f.DAPsPluggedAPI != 1 || f.ConvertedPluggedAPI != 1 || f.APIMismatch != 0 {
		t.Fatalf("plugged %+v", f)
	}
	// A converted model without its plugged API is a mismatch.
	lost := FileResult{DAPs: []DAPRow{{Outcome: gsdml.OutcomeConverted, PluggedAPIs: []string{"0x3A00"}}, {Outcome: gsdml.OutcomeMalformed, PluggedAPIs: []string{"0x3A00"}}}}
	if f := Summarize([]FileResult{lost}).Fidelity; f.APIMismatch != 1 {
		t.Fatalf("lost %+v", f)
	}
	if md := Markdown(Summarize([]FileResult{lost}), ""); !strings.Contains(md, "keep the APIs of their plugged configuration: 1.") {
		t.Fatal(md)
	}
	// A record of the profile range (0xB600): counted above 0x7FFF, and
	// ToModel keeps it; one at 0x8100 (PROFINET's own range) it refuses.
	h := analyze(fixture(t, modular, `<ParameterRecordDataItem Index="100" Length="2">`, `<ParameterRecordDataItem Index="46592" Length="2">`))
	if h.RecordIndexes["0xB000-0xBFFF"] != 1 || h.DAPs[0].HighRecords != 1 || h.DAPs[0].Outcome != "converted" {
		t.Fatalf("%+v", h)
	}
	if f := Summarize([]FileResult{h}).Fidelity; f.DAPsHighRecords != 2 || f.ConvertedHighRecords != 2 {
		t.Fatalf("profile %+v", f)
	}
	p := analyze(fixture(t, modular, `<ParameterRecordDataItem Index="100" Length="2">`, `<ParameterRecordDataItem Index="33024" Length="2">`))
	if p.RecordIndexes["0x8000-0x8FFF"] != 1 || p.DAPs[0].HighRecords != 1 || p.DAPs[0].Outcome == "converted" {
		t.Fatalf("0x8100 %+v", p)
	}
	// An index that is no number and one above 0x7FFF in one document:
	// counted apart.
	both := analyze(fixture(t, modular, `<ParameterRecordDataItem Index="100" Length="2">`, `<ParameterRecordDataItem Index="46592" Length="2">`,
		`<ParameterRecordDataItem Index="101" Length="1">`, `<ParameterRecordDataItem Index="xyz" Length="1">`))
	if d := both.DAPs[0]; d.HighRecords != 1 || d.InvalidRecords != 1 || both.RecordIndexes[invalidIndex] != 1 || both.RecordIndexes["0xB000-0xBFFF"] != 1 {
		t.Fatalf("both %+v", both)
	}
	inval := analyze(fixture(t, modular, `<ParameterRecordDataItem Index="101" Length="1">`, `<ParameterRecordDataItem Index="xyz" Length="1">`))
	if f := Summarize([]FileResult{inval}).Fidelity; inval.DAPs[0].HighRecords != 0 || f.FilesHighRecords != 0 || f.DAPsHighRecords != 0 {
		t.Fatalf("invalid only %+v %+v", inval.DAPs[0], f)
	}
	if indexRange("x") != "invalid" || indexRange("0x7FFF") != userRange || indexRange("65535") != "0xF000-0xFFFF" {
		t.Fatal("indexRange")
	}
	inv := analyze(fixture(t, modular, `<VirtualSubmoduleItem ID="SM_DI16"`, `<VirtualSubmoduleItem API="x" ID="SM_DI16"`))
	if inv.APIs["invalid"] != 1 || inv.DAPs[0].Outcome != gsdml.OutcomeMalformed {
		t.Fatalf("%+v", inv.APIs)
	}
	s := Summarize([]FileResult{r, h, r, p})
	if f := s.Fidelity; f.FilesAPI != 2 || f.DAPsAPI != 4 || f.ConvertedAPI != 4 || f.APIs["0x4E01"] != 2 || f.FilesHighRecords != 2 || f.DAPsHighRecords != 4 || f.ConvertedHighRecords != 2 {
		t.Fatalf("%+v", s.Fidelity)
	}
	if md := Markdown(s, ""); !strings.Contains(md, "## Fidelity") || !strings.Contains(md, "| 0x4E01 | 2 |") || !strings.Contains(md, "ToModel conversion") {
		t.Fatal(md)
	}
}

func slicesEqual(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

// The child checks the digest before the analysis: a file that is not
// the indexed one is not analysed at all.
func TestRunOneDigest(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "a.xml")
	b := fixture(t, modular)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	want := strings.Repeat("0", 64)
	if code := runOne(p, want, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var r FileResult
	if err := json.Unmarshal(out.Bytes(), &r); err != nil || r.Stage != StageIntegrity || r.SHA256 != sha256Hex(b) || r.Check != nil || len(r.DAPs) != 0 || !strings.Contains(r.Integrity, want) {
		t.Fatalf("%v %+v", err, r)
	}
	out.Reset()
	if code := runOne(p, sha256Hex(b), &out, &errb); code != 0 || !strings.Contains(out.String(), `"outcome":"converted"`) {
		t.Fatalf("exit %d: %s", code, out.String())
	}
}

func TestSignature(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		`gsdml: malformed: module "MOD_X" ModuleIdentNumber 0x20`: `gsdml: malformed: module "…" ModuleIdentNumber N`,
		`ModuleItem[MOD_1]/Ref[3]: value 'abc' of 12 octets`:      `ModuleItem[…]/Ref[…]: value '…' of N octets`,
		`V2.43 has 1440 submodules`:                               `V2.N has N submodules`,
	} {
		if got := Signature(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestSummarize(t *testing.T) {
	t.Parallel()
	bad := func(id string) FileResult {
		return analyze(fixture(t, modular, `<ModuleItem ID="MOD_DO16" ModuleIdentNumber="0x00000020">`, `<ModuleItem ID="MOD_DO16" ModuleIdentNumber="`+id+`">`))
	}
	results := []FileResult{analyze(fixture(t, modular)), bad("x"), bad("y"), analyze([]byte("<x")), {Path: "c", Crash: "timeout after 2m0s"}}
	for i := range results {
		results[i].Path = string(rune('a' + i))
	}
	s := Summarize(results)
	if s.Files != 5 || s.FileStages["ok"] != 1 || s.FileStages["model"] != 2 || s.FileStages["parse"] != 1 || s.FileStages["crash"] != 1 || s.Valid != 1 {
		t.Fatalf("%+v", s)
	}
	// The two module failures differ only in the value: one signature,
	// two files, four DAPs (both DAPs of each file).
	if top := s.Signatures[0]; top.Kind != "module" || top.Files != 2 || top.DAPs != 4 || top.Outcome != gsdml.OutcomeMalformed {
		t.Fatalf("%+v", s.Signatures)
	}
	if s.DAPOutcomes["blocked"] != 4 || s.ByVersion["2.43"] != [2]int{3, 2} || s.ByVersion["none"] != [2]int{2, 2} || s.ErrorRules["schema"] != 2 {
		t.Fatalf("%+v", s)
	}
	md := Markdown(s, "test run")
	for _, want := range []string{"# GSDML corpus run", "test run", "| model | 2 |", "## Failures, ranked", "| 2 | 4 | module | malformed |"} {
		if !strings.Contains(md, want) {
			t.Fatalf("no %q in\n%s", want, md)
		}
	}
	if strings.Contains(md, "/a") || strings.Contains(md, ".xml") {
		t.Fatal("a file name in the matrix")
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("gsdml/gh-1/GSDML-a.xml", fixture(t, modular))
	write("gsdml/gh-2/GSDML-b.XML", []byte("<x"))
	write("gsdml/gh-2/readme.txt", []byte("no"))
	out, md := filepath.Join(dir, "r.json"), filepath.Join(dir, "m.md")
	var so, se bytes.Buffer
	if code := run([]string{"-dir", filepath.Join(dir, "gsdml"), "-out", out, "-md", md, "-jobs", "2"}, &so, &se); code != 0 {
		t.Fatalf("exit %d: %s", code, se.String())
	}
	var res Result
	b, _ := os.ReadFile(out)
	if err := json.Unmarshal(b, &res); err != nil || res.Schema != Schema || len(res.Files) != 2 || res.Files[0].SHA256 == "" || res.Files[1].Stage != StageParse || res.GoProfinet == "" {
		t.Fatalf("%v %s", err, b)
	}
	if m, _ := os.ReadFile(md); !strings.Contains(string(m), "| ok | 1 |") {
		t.Fatalf("%s", m)
	}
	// The index of a corpus-fetch cache; a path outside it is refused.
	good := sha256Hex(fixture(t, modular))
	write("index.jsonl", []byte(`{"id":"gh-1","path":"gsdml/gh-1/GSDML-a.xml","sha256":"`+good+`","member":""}`+"\n"))
	so.Reset()
	if code := run([]string{"-cache", dir}, &so, &se); code != 0 || !strings.Contains(so.String(), "| ok | 1 |") || !strings.Contains(so.String(), "index.jsonl sha256 ") {
		t.Fatalf("exit %d: %s %s", code, so.String(), se.String())
	}
	// The cached file changed after the fetch, the index not: the file is
	// not analysed as the indexed one.
	write("gsdml/gh-1/GSDML-a.xml", fixture(t, modular, `MinDeviceInterval="32"`, `MinDeviceInterval="64"`))
	if code := run([]string{"-cache", dir, "-out", out}, &so, &se); code != 0 {
		t.Fatalf("exit %d: %s", code, se.String())
	}
	b, _ = os.ReadFile(out)
	res = Result{}
	if err := json.Unmarshal(b, &res); err != nil || res.IndexSHA256 == "" {
		t.Fatal(err)
	}
	if f := res.Files[0]; f.Stage != StageIntegrity || f.SHA256 == good || !strings.Contains(f.Integrity, good) || len(f.DAPs) != 0 || res.Summary.FileStages[StageIntegrity] != 1 {
		t.Fatalf("tampered %+v", f)
	}
	write("index.jsonl", []byte(`{"id":"x","path":"../etc/passwd"}`+"\n"))
	if code := run([]string{"-cache", dir}, &so, &se); code != 2 {
		t.Fatalf("outside: exit %d", code)
	}
	for name, args := range map[string][]string{
		"none":      {},
		"both":      {"-dir", dir, "-cache", dir},
		"jobs":      {"-dir", dir, "-jobs", "0"},
		"flag":      {"-x"},
		"no index":  {"-cache", t.TempDir()},
		"empty dir": {"-dir", t.TempDir()},
		"one":       {"-one", filepath.Join(dir, "missing.xml")},
		"out":       {"-dir", filepath.Join(dir, "gsdml"), "-out", filepath.Join(dir, "no", "such", "dir", "r.json")},
	} {
		if code := run(args, &so, &se); code != 2 {
			t.Errorf("%s: exit %d", name, code)
		}
	}
}

// A child that panics or hangs is a crash of its file, not of the run.
func TestRunCrash(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.xml"), []byte("<x"), 0o644); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for mode, want := range map[string]string{"panic": "test panic", "hang": "timeout after"} {
		t.Setenv("GSDMLCORPUS_TEST_CHILD", mode)
		r := child(t.Context(), self, input{Path: filepath.Join(dir, "a.xml")}, 2*time.Second, "1GiB")
		if !strings.Contains(r.Crash, want) {
			t.Fatalf("%s: %+v", mode, r)
		}
	}
}

func TestLimited(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	l := &limited{w: &b, n: 3}
	if n, err := l.Write([]byte("abcd")); n != 4 || err != nil || b.String() != "abc" {
		t.Fatalf("%d %v %q", n, err, b.String())
	}
	if n, _ := l.Write([]byte("e")); n != 1 || b.String() != "abc" {
		t.Fatalf("%q", b.String())
	}
}

// Configurable submodules are counted per DAP, a module several
// references name once, and the summary tells refused DAPs from those
// converted without their optional ones.
func TestConfigurableSubmodules(t *testing.T) {
	t.Parallel()
	di16 := "</VirtualSubmoduleList>\n        </ModuleItem>\n        <ModuleItem ID=\"MOD_DO16\""
	opt := analyze(fixture(t, modular, di16, "</VirtualSubmoduleList><UseableSubmodules><SubmoduleItemRef SubmoduleItemTarget=\"A\" AllowedInSubslots=\"2..3\"/><SubmoduleItemRef SubmoduleItemTarget=\"B\" AllowedInSubslots=\"4 5\"/></UseableSubmodules>\n        </ModuleItem>\n        <ModuleItem ID=\"MOD_DO16\""))
	for _, d := range opt.DAPs {
		if d.Outcome != gsdml.OutcomeConverted || d.ConfigurableSubmodules != 2 || d.RequiredSubmodules != 0 {
			t.Fatalf("optional %+v", d)
		}
	}
	req := analyze(fixture(t, modular, "</SystemDefinedSubmoduleList>", "</SystemDefinedSubmoduleList><UseableSubmodules><SubmoduleItemRef SubmoduleItemTarget=\"C\" FixedInSubslots=\"2\"/></UseableSubmodules>"))
	if d := req.DAPs[0]; d.Outcome != gsdml.OutcomeUnsupported || d.ConfigurableSubmodules != 1 || d.RequiredSubmodules != 1 || req.Stage != StageModel {
		t.Fatalf("required %+v", d)
	}
	if d := req.DAPs[1]; d.ConfigurableSubmodules != 0 || d.Outcome != gsdml.OutcomeConverted {
		t.Fatalf("DAP_2P %+v", d)
	}
	plain := analyze(fixture(t, modular))
	// MOD_DIO8 needs one: DAP_1P only lists it (omitted from its
	// catalog), DAP_2P plugs it (refused).
	cat := analyze(fixture(t, modular, "</VirtualSubmoduleList>\n        </ModuleItem>\n        <ModuleItem ID=\"MOD_AI4\"",
		"</VirtualSubmoduleList><UseableSubmodules><SubmoduleItemRef SubmoduleItemTarget=\"D\" UsedInSubslots=\"2\"/></UseableSubmodules>\n        </ModuleItem>\n        <ModuleItem ID=\"MOD_AI4\""))
	if d := cat.DAPs[0]; d.Outcome != gsdml.OutcomeConverted || d.OmittedModules != 1 || d.RequiredSubmodules != 1 || d.Blocked || len(d.Failures) != 0 {
		t.Fatalf("DAP_1P %+v", d)
	}
	if d := cat.DAPs[1]; d.Outcome != gsdml.OutcomeUnsupported || d.OmittedModules != 0 || !d.Blocked || len(d.Failures) != 1 || d.Failures[0].Class != gsdml.OutcomeUnsupported {
		t.Fatalf("DAP_2P %+v", d)
	}
	s := Summarize([]FileResult{opt, req, plain, cat})
	if f := s.Fidelity; f.FilesConfigurable != 3 || f.DAPsConfigurable != 5 || f.DAPsRequiredSubmodules != 3 || f.ConvertedConfigurable != 3 || f.DAPsOmittedModules != 1 || f.OmittedModules != 1 {
		t.Fatalf("%+v", f)
	}
	md := Markdown(s, "")
	if !strings.Contains(md, "| a configurable submodule (UseableSubmodules) in the DAP or a module it references; ToModel converts none | 3 | 5 | 3 |") ||
		!strings.Contains(md, "(FixedInSubslots or UsedInSubslots), in the DAP or a module it references: 3;") || !strings.Contains(md, "needs one: 1 (1 module references).") {
		t.Fatal(md)
	}
}
