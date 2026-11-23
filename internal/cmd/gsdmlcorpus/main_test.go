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
	write("index.jsonl", []byte(`{"id":"gh-1","path":"gsdml/gh-1/GSDML-a.xml","sha256":"abc","member":""}`+"\n"))
	so.Reset()
	if code := run([]string{"-cache", dir}, &so, &se); code != 0 || !strings.Contains(so.String(), "| ok | 1 |") {
		t.Fatalf("exit %d: %s %s", code, so.String(), se.String())
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
