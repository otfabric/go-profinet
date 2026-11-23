// SPDX-License-Identifier: MIT

// Command gsdmlcorpus runs go-profinet's GSDML pipeline over a local
// corpus of GSDML files and writes the coverage matrix of
// docs/research/INTEROP-COMPLIANCE-PLAN.md §7: per file, whether it parses,
// what CheckBytes reports, and per device access point what AnalyzeModels
// reports, with the failures ranked by kind.
//
// Usage, from the module root:
//
//	go run ./internal/cmd/gsdmlcorpus -cache DIR [-out results.json] [-md matrix.md]
//	go run ./internal/cmd/gsdmlcorpus -dir DIR ...
//
// -cache reads the index.jsonl of profinet-interop's corpus-fetch
// (docs/CORPUS.md there); -dir analyses every .xml file under DIR. The
// files are untrusted: each is analysed in a child process of this
// command, with a timeout (-timeout) and a soft memory limit (GOMEMLIMIT);
// a file that times out, panics or exits is reported as a crash.
//
// The results name files and quote error messages, which can hold values
// of the vendor's file: they stay local, like the corpus. A regression
// case for go-profinet is written in our own words (§16 of the plan).
//
// Exit status: 0 when the run finished (whatever the files' outcomes), 2
// for a usage or I/O error.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
)

// maxFile bounds one file read for analysis.
const maxFile = 64 << 20

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// Result is the document -out writes.
type Result struct {
	Schema      string       `json:"schema"`
	GoProfinet  string       `json:"go_profinet"`
	GeneratedAt string       `json:"generated_at"`
	Source      string       `json:"source"`
	Summary     Summary      `json:"summary"`
	Files       []FileResult `json:"files"`
}

// input is one file to analyse.
type input struct {
	ID, Path, Member, SHA256 string
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gsdmlcorpus", flag.ContinueOnError)
	flags.SetOutput(stderr)
	one := flags.String("one", "", "analyse one file and print its result (the child process)")
	cache := flags.String("cache", "", "a corpus-fetch cache: read its index.jsonl")
	dir := flags.String("dir", "", "a directory: analyse every .xml file under it")
	out := flags.String("out", "", "write the results as JSON to this file")
	md := flags.String("md", "", "write the matrix as Markdown to this file")
	timeout := flags.Duration("timeout", 2*time.Minute, "the analysis of one file")
	jobs := flags.Int("jobs", 4, "files analysed at once")
	memLimit := flags.String("memlimit", "2GiB", "GOMEMLIMIT of a child process")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *one != "" {
		return runOne(*one, stdout, stderr)
	}
	if flags.NArg() > 0 || (*cache == "") == (*dir == "") || *jobs < 1 || *timeout <= 0 {
		diag(stderr, "usage: gsdmlcorpus (-cache DIR | -dir DIR) [-out FILE] [-md FILE] [-timeout D] [-jobs N]")
		return 2
	}
	var inputs []input
	var err error
	source := ""
	if *cache != "" {
		inputs, err = fromIndex(*cache)
		source = "corpus-fetch cache"
	} else {
		inputs, err = fromDir(*dir)
		source = "directory"
	}
	if err != nil {
		diag(stderr, err)
		return 2
	}
	self, err := os.Executable()
	if err != nil {
		diag(stderr, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	results := analyzeAll(ctx, self, inputs, *timeout, *jobs, *memLimit)
	sum := Summarize(results)
	version := goProfinetVersion()
	src := fmt.Sprintf("go-profinet %s over %d files of a %s.", version, len(results), source)
	doc := Result{Schema: Schema, GoProfinet: version, GeneratedAt: time.Now().UTC().Format(time.RFC3339), Source: source, Summary: sum, Files: results}
	if *out != "" {
		b, err := json.MarshalIndent(doc, "", "  ")
		if err == nil {
			err = os.WriteFile(*out, append(b, '\n'), 0o644)
		}
		if err != nil {
			diag(stderr, err)
			return 2
		}
	}
	matrix := Markdown(sum, src)
	if *md != "" {
		if err := os.WriteFile(*md, []byte(matrix), 0o644); err != nil {
			diag(stderr, err)
			return 2
		}
	}
	var werr error
	if *out == "" && *md == "" {
		_, werr = fmt.Fprint(stdout, matrix)
	} else {
		_, werr = fmt.Fprintf(stdout, "%d files: %v\n", sum.Files, sum.FileStages)
	}
	if werr != nil {
		diag(stderr, werr)
		return 2
	}
	return 0
}

// runOne analyses one file and prints its FileResult as JSON.
func runOne(path string, stdout, stderr io.Writer) int {
	data, err := readBounded(path)
	if err != nil {
		diag(stderr, err)
		return 2
	}
	start := time.Now()
	r := analyze(data)
	r.Millis = time.Since(start).Milliseconds()
	if err := json.NewEncoder(stdout).Encode(r); err != nil {
		diag(stderr, err)
		return 2
	}
	return 0
}

func readBounded(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFile {
		return nil, fmt.Errorf("%s: more than %d octets", path, maxFile)
	}
	return data, nil
}

// analyzeAll analyses every input in a child process, jobs at a time, and
// returns the results in the order of the inputs.
func analyzeAll(ctx context.Context, self string, inputs []input, timeout time.Duration, jobs int, memLimit string) []FileResult {
	results := make([]FileResult, len(inputs))
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	for i, in := range inputs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = child(ctx, self, in, timeout, memLimit)
		}()
	}
	wg.Wait()
	return results
}

// child runs `self -one path` and reads its result; a timeout, a crash or
// output that is no result is reported as Crash.
func child(ctx context.Context, self string, in input, timeout time.Duration, memLimit string) FileResult {
	fail := func(msg string) FileResult {
		return FileResult{ID: in.ID, Path: in.Path, Member: in.Member, SHA256: in.SHA256, Crash: msg, DAPs: []DAPRow{}}
	}
	if ctx.Err() != nil {
		return fail("not run: " + ctx.Err().Error())
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, self, "-one", in.Path)
	cmd.Env = append(os.Environ(), "GOMEMLIMIT="+memLimit)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &limited{w: &stdout, n: 64 << 20}, &limited{w: &stderr, n: 64 << 10}
	start := time.Now()
	err := cmd.Run()
	switch {
	case errors.Is(cctx.Err(), context.DeadlineExceeded):
		return fail(fmt.Sprintf("timeout after %s", timeout))
	case err != nil:
		msg := strings.TrimSpace(stderr.String())
		if i := strings.IndexByte(msg, '\n'); i > 0 {
			msg = msg[:i] // a panic's first line, not its stack
		}
		return fail(fmt.Sprintf("%v: %s", err, msg))
	}
	var r FileResult
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		return fail("no result: " + err.Error())
	}
	r.ID, r.Path, r.Member = in.ID, in.Path, in.Member
	if r.SHA256 = in.SHA256; r.SHA256 == "" {
		if data, err := readBounded(in.Path); err == nil {
			s := sha256.Sum256(data)
			r.SHA256 = hex.EncodeToString(s[:])
		}
	}
	if r.Millis == 0 {
		r.Millis = time.Since(start).Milliseconds()
	}
	return r
}

// limited writes at most n octets and drops the rest.
type limited struct {
	w io.Writer
	n int
}

func (l *limited) Write(p []byte) (int, error) {
	k := len(p)
	if len(p) > l.n {
		p = p[:l.n]
	}
	l.n -= len(p)
	if _, err := l.w.Write(p); err != nil {
		return 0, err
	}
	return k, nil
}

// fromIndex reads a corpus-fetch index.jsonl; paths in it are relative to
// the cache and must stay inside it.
func fromIndex(cache string) ([]input, error) {
	f, err := os.Open(filepath.Join(cache, "index.jsonl"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	root, err := filepath.Abs(cache)
	if err != nil {
		return nil, err
	}
	var inputs []input
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for n := 1; sc.Scan(); n++ {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var e struct {
			ID, Path, SHA256, Member string
		}
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("index.jsonl line %d: %w", n, err)
		}
		p := filepath.Join(root, filepath.FromSlash(e.Path))
		if e.Path == "" || !strings.HasPrefix(p, root+string(filepath.Separator)) {
			return nil, fmt.Errorf("index.jsonl line %d: path %q outside the cache", n, e.Path)
		}
		inputs = append(inputs, input{ID: e.ID, Path: p, Member: e.Member, SHA256: e.SHA256})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(inputs) == 0 {
		return nil, errors.New("index.jsonl lists no file")
	}
	return inputs, nil
}

// fromDir lists the .xml files under dir, sorted.
func fromDir(dir string) ([]input, error) {
	var inputs []input
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && strings.EqualFold(filepath.Ext(p), ".xml") {
			inputs = append(inputs, input{Path: p})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(inputs) == 0 {
		return nil, fmt.Errorf("%s: no .xml file", dir)
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Path < inputs[j].Path })
	return inputs, nil
}

// goProfinetVersion is the version of go-profinet this command was built
// from: its module version, or the VCS revision of a build in the
// repository.
func goProfinetVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	rev, dirty := "", false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	v := bi.Main.Version
	if rev != "" && (v == "" || v == "(devel)") {
		v = rev
		if len(v) > 12 {
			v = v[:12]
		}
		if dirty {
			v += "+dirty"
		}
	}
	if v == "" {
		return "(devel)"
	}
	return v
}

// diag writes a diagnostic. A failed write to the diagnostics stream
// cannot be reported anywhere, so its error is dropped.
func diag(w io.Writer, a ...any) { _, _ = fmt.Fprintln(w, a...) }
