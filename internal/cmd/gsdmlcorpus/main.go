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
// command, with a timeout (-timeout) and a memory target for the garbage
// collector (GOMEMLIMIT, -memlimit), which is not a hard limit: a file
// whose live data exceeds it can still use more. A file that times out,
// panics or exits is reported as a crash. Each child hashes the octets it
// reads and checks them against the index's SHA-256 before the analysis:
// a file that is not the one the index names is reported as integrity and
// not analysed.
//
// The results name files and quote error messages, which can hold values
// of the vendor's file: they stay local, like the corpus. A regression
// case for go-profinet is written in our own words (§16 of the plan).
//
// For every device access point ToModel converts, the run compares the
// addresses of its plugged configuration as the file states them (API,
// slot, subslot, SubmoduleIdentNumber, record indexes) with those a device
// built from the model serves (AddressCheck).
//
// Exit status: 0 when the run finished and every converted DAP's model
// keeps its addresses (whatever the files' other outcomes); 1 when the
// run finished and a converted DAP's model loses, adds or changes an
// address or a record index, or its addresses could not be read (the
// results are still written); 2 for a usage or I/O error.
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
	Schema      string `json:"schema"`
	GoProfinet  string `json:"go_profinet"`
	GeneratedAt string `json:"generated_at"`
	Source      string `json:"source"`
	// IndexSHA256 is the SHA-256 of the cache's index.jsonl the run read.
	IndexSHA256 string       `json:"index_sha256,omitempty"`
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
	want := flags.String("sha256", "", "with -one: the SHA-256 the file must have; a file that has another is not analysed")
	cache := flags.String("cache", "", "a corpus-fetch cache: read its index.jsonl")
	dir := flags.String("dir", "", "a directory: analyse every .xml file under it")
	out := flags.String("out", "", "write the results as JSON to this file")
	md := flags.String("md", "", "write the matrix as Markdown to this file")
	timeout := flags.Duration("timeout", 2*time.Minute, "the analysis of one file")
	jobs := flags.Int("jobs", 4, "files analysed at once")
	memLimit := flags.String("memlimit", "2GiB", "GOMEMLIMIT of a child process (a garbage-collector target, not a hard limit)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *one != "" {
		return runOne(*one, *want, stdout, stderr)
	}
	if flags.NArg() > 0 || (*cache == "") == (*dir == "") || *jobs < 1 || *timeout <= 0 {
		diag(stderr, "usage: gsdmlcorpus (-cache DIR | -dir DIR) [-out FILE] [-md FILE] [-timeout D] [-jobs N]")
		return 2
	}
	var inputs []input
	var err error
	source, indexSum := "", ""
	if *cache != "" {
		inputs, indexSum, err = fromIndex(*cache)
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
	if indexSum != "" {
		src = fmt.Sprintf("go-profinet %s over %d files of a %s (index.jsonl sha256 %s).", version, len(results), source, indexSum)
	}
	doc := Result{Schema: Schema, GoProfinet: version, GeneratedAt: time.Now().UTC().Format(time.RFC3339), Source: source, IndexSHA256: indexSum, Summary: sum, Files: results}
	var written []string
	if *out != "" {
		b, err := json.MarshalIndent(doc, "", "  ")
		if err == nil {
			err = os.WriteFile(*out, append(b, '\n'), 0o644)
			written = append(written, fmt.Sprintf("%s sha256 %s", *out, sha256Hex(append(b, '\n'))))
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
		written = append(written, fmt.Sprintf("%s sha256 %s", *md, sha256Hex([]byte(matrix))))
	}
	var werr error
	if *out == "" && *md == "" {
		_, werr = fmt.Fprint(stdout, matrix)
	} else {
		_, werr = fmt.Fprintf(stdout, "%d files (%d distinct): %v\n%s\n", sum.Files, sum.Distinct, sum.FileStages, strings.Join(written, "\n"))
	}
	if werr != nil {
		diag(stderr, werr)
		return 2
	}
	return addressGate(sum.Fidelity, stderr)
}

// addressGate is the exit status of a finished run: 1 when a converted
// DAP's model does not keep its addresses, else 0.
func addressGate(f Fidelity, stderr io.Writer) int {
	if f.DAPsAddressFailed == 0 {
		return 0
	}
	diag(stderr, fmt.Sprintf("address fidelity: %d of %d converted DAPs: %d lost, %d extra, %d changed, %d records lost, %d records extra, %d unread",
		f.DAPsAddressFailed, f.DAPsAddressChecked, f.AddressLost, f.AddressExtra, f.AddressChanged, f.AddressRecordsLost, f.AddressRecordsExtra, f.AddressUnread))
	return 1
}

// runOne analyses one file and prints its FileResult as JSON.
func runOne(path, want string, stdout, stderr io.Writer) int {
	data, err := readBounded(path)
	if err != nil {
		diag(stderr, err)
		return 2
	}
	start := time.Now()
	var r FileResult
	// The digest is checked before the analysis: a file that is not the
	// one the index names is not processed at all.
	if got := sha256Hex(data); want != "" && got != want {
		r = FileResult{Size: len(data), Stage: StageIntegrity, SHA256: got, DAPs: []DAPRow{},
			Integrity: fmt.Sprintf("the file has sha256 %s, the index %s", got, want)}
	} else {
		r = analyze(data)
		r.SHA256 = got
	}
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
	args := []string{"-one", in.Path}
	if in.SHA256 != "" {
		args = append(args, "-sha256", in.SHA256)
	}
	cmd := exec.CommandContext(cctx, self, args...)
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
	// r.SHA256 is that of the octets the child read, which checked it
	// before the analysis; checked again here, so that a result is never
	// attributed to a file it is not of.
	if in.SHA256 != "" && r.SHA256 != in.SHA256 && r.Stage != StageIntegrity {
		f := fail("")
		f.Crash, f.Stage, f.SHA256 = "", StageIntegrity, r.SHA256
		f.Integrity = fmt.Sprintf("the analysed file has sha256 %s, the index %s", r.SHA256, in.SHA256)
		return f
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
//
// It returns the SHA-256 of the octets it parsed: one read, so the digest
// is that of the index the run used.
func fromIndex(cache string) ([]input, string, error) {
	data, err := readBounded(filepath.Join(cache, "index.jsonl"))
	if err != nil {
		return nil, "", err
	}
	root, err := filepath.Abs(cache)
	if err != nil {
		return nil, "", err
	}
	var inputs []input
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for n := 1; sc.Scan(); n++ {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var e struct {
			ID, Path, SHA256, Member string
		}
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, "", fmt.Errorf("index.jsonl line %d: %w", n, err)
		}
		p := filepath.Join(root, filepath.FromSlash(e.Path))
		if e.Path == "" || !strings.HasPrefix(p, root+string(filepath.Separator)) {
			return nil, "", fmt.Errorf("index.jsonl line %d: path %q outside the cache", n, e.Path)
		}
		inputs = append(inputs, input{ID: e.ID, Path: p, Member: e.Member, SHA256: e.SHA256})
	}
	if err := sc.Err(); err != nil {
		return nil, "", err
	}
	if len(inputs) == 0 {
		return nil, "", errors.New("index.jsonl lists no file")
	}
	return inputs, sha256Hex(data), nil
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

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
