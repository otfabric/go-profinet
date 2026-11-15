// SPDX-License-Identifier: MIT

// Package leakcheck reports the goroutines a test left behind, by name:
// it compares the stacks of all goroutines before and after, instead of
// their number.
//
// A package whose tests start goroutines uses it from TestMain:
//
//	func TestMain(m *testing.M) { leakcheck.Main(m) }
//
// and a single test with
//
//	defer leakcheck.Check(t)()
//
// Together is the barrier of the contract tests that call Close from
// several goroutines.
package leakcheck

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Grace is how long a goroutine may take to end after the tests did:
// what Close started winding down is given this long.
const Grace = 5 * time.Second

// Goroutine is one goroutine of a snapshot.
type Goroutine struct {
	// ID is the number the runtime gave it.
	ID string
	// Stack is its stack as runtime.Stack prints it, with the header
	// line "goroutine N [state]:".
	Stack string
}

// Top returns the function the goroutine is in.
func (g Goroutine) Top() string {
	lines := strings.Split(g.Stack, "\n")
	if len(lines) < 2 {
		return ""
	}
	return funcName(lines[1])
}

// Creator returns the function that started the goroutine, "" for the
// main goroutine.
func (g Goroutine) Creator() string {
	const by = "created by "
	i := strings.LastIndex(g.Stack, by)
	if i < 0 {
		return ""
	}
	line, _, _ := strings.Cut(g.Stack[i+len(by):], "\n")
	name, _, _ := strings.Cut(line, " in goroutine ")
	return strings.TrimSpace(name)
}

func funcName(line string) string {
	line = strings.TrimSpace(line)
	if i := strings.LastIndex(line, "("); i > 0 {
		line = line[:i]
	}
	return line
}

// Snapshot returns the goroutines that exist now, except the one that
// calls it.
func Snapshot() []Goroutine {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	return parse(string(buf))
}

// parse splits the output of runtime.Stack(buf, true). The first block
// is the calling goroutine; it is left out.
func parse(all string) []Goroutine {
	var out []Goroutine
	for i, block := range strings.Split(strings.TrimSpace(all), "\n\n") {
		const prefix = "goroutine "
		if i == 0 || !strings.HasPrefix(block, prefix) {
			continue
		}
		id, _, _ := strings.Cut(block[len(prefix):], " ")
		out = append(out, Goroutine{ID: id, Stack: block})
	}
	return out
}

// background are the goroutines of the runtime and of the testing
// package that a test did not start.
var background = []string{
	"testing.Main(",
	"testing.(*M).",
	"testing.runFuzzing(",
	"testing.(*F).",
	"testing.tRunner(",
	"testing.runTests(",
	"os/signal.signal_recv(",
	"os/signal.loop(",
	"runtime.ensureSigM",
	"runtime.ReadTrace(",
	"runtime/trace.",
	"runtime.goexit0",
}

func isBackground(g Goroutine) bool {
	for _, b := range background {
		if strings.Contains(g.Stack, b) {
			return true
		}
	}
	return false
}

// Left returns the goroutines of now that were not in before and are not
// the runtime's or the testing package's.
func Left(before []Goroutine) []Goroutine {
	known := make(map[string]bool, len(before))
	for _, g := range before {
		known[g.ID] = true
	}
	var left []Goroutine
	for _, g := range Snapshot() {
		if !known[g.ID] && !isBackground(g) {
			left = append(left, g)
		}
	}
	return left
}

// Wait returns the goroutines that are still there after grace; it
// returns as soon as none is.
func Wait(before []Goroutine, grace time.Duration) []Goroutine {
	deadline := time.Now().Add(grace)
	pause := time.Millisecond
	for {
		left := Left(before)
		if len(left) == 0 || !time.Now().Before(deadline) {
			return left
		}
		runtime.Gosched()
		time.Sleep(pause)
		if pause < 50*time.Millisecond {
			pause *= 2
		}
	}
}

// Report formats the goroutines that were left: one line that names each
// by where it is and what started it, then the stacks.
func Report(left []Goroutine) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d goroutines left behind:\n", len(left))
	for _, g := range left {
		fmt.Fprintf(&b, "  goroutine %s in %s, started by %s\n", g.ID, g.Top(), g.Creator())
	}
	for _, g := range left {
		b.WriteString("\n")
		b.WriteString(g.Stack)
		b.WriteString("\n")
	}
	return b.String()
}

// Together calls f from n goroutines that are let go at the same moment
// and returns when all of them have returned: the shape of a contract
// test for "two goroutines call Close".
func Together(n int, f func(i int)) {
	start := make(chan struct{})
	done := make(chan struct{}, n)
	for i := range n {
		go func() {
			defer func() { done <- struct{}{} }()
			<-start
			f(i)
		}()
	}
	close(start)
	for range n {
		<-done
	}
}

// Check returns a function for defer: it fails t when goroutines started
// since Check was called are still there Grace after it runs.
func Check(t testing.TB) func() {
	t.Helper()
	before := Snapshot()
	return func() {
		t.Helper()
		if left := Wait(before, Grace); len(left) > 0 {
			t.Errorf("leakcheck: %s", Report(left))
		}
	}
}

// Main runs the tests of a package and exits with their result, or with 1
// when they passed and left goroutines behind. It does not check a
// fuzzing run, whose workers outlive m.Run.
func Main(m *testing.M) {
	os.Exit(run(m.Run, fuzzing(), os.Stderr.WriteString))
}

// fuzzing reports whether the test binary was started to fuzz.
func fuzzing() bool {
	for _, name := range []string{"test.fuzz", "test.fuzzworker"} {
		if f := flag.Lookup(name); f != nil && f.Value.String() != "" && f.Value.String() != "false" {
			return true
		}
	}
	return false
}

// run is Main without the exit: tests is m.Run, out receives the report.
func run(tests func() int, skip bool, out func(string) (int, error)) int {
	return runWithin(tests, skip, out, Grace)
}

func runWithin(tests func() int, skip bool, out func(string) (int, error), grace time.Duration) int {
	before := Snapshot()
	code := tests()
	if code != 0 || skip {
		return code
	}
	if left := Wait(before, grace); len(left) > 0 {
		_, _ = out("leakcheck: " + Report(left))
		return 1
	}
	return 0
}
