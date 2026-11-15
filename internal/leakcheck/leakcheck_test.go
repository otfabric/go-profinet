// SPDX-License-Identifier: MIT

package leakcheck

import (
	"flag"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// blocked starts a goroutine that waits for the returned function.
func blocked() (release func()) {
	gate, started := make(chan struct{}), make(chan struct{})
	go func() {
		close(started)
		<-gate
	}()
	<-started
	return func() { close(gate) }
}

func TestLeftNamesTheGoroutine(t *testing.T) {
	before := Snapshot()
	if left := Left(before); len(left) != 0 {
		t.Fatalf("goroutines without a test starting one:\n%s", Report(left))
	}
	release := blocked()
	left := Wait(before, 20*time.Millisecond)
	if len(left) != 1 {
		release()
		t.Fatalf("%d goroutines left, want the one that is blocked:\n%s", len(left), Report(left))
	}
	g := left[0]
	if g.ID == "" || !strings.Contains(g.Top(), "leakcheck.blocked.func1") || !strings.Contains(g.Creator(), "leakcheck.blocked") {
		t.Errorf("goroutine %q in %q, started by %q", g.ID, g.Top(), g.Creator())
	}
	rep := Report(left)
	for _, want := range []string{"1 goroutines left behind", "goroutine " + g.ID + " in ", "started by ", "leakcheck.blocked", "chan receive"} {
		if !strings.Contains(rep, want) {
			t.Errorf("report without %q:\n%s", want, rep)
		}
	}
	// Once it ends, nothing is left; Wait returns as soon as that is so.
	release()
	start := time.Now()
	if left := Wait(before, Grace); len(left) != 0 {
		t.Fatalf("after the goroutine ended:\n%s", Report(left))
	}
	if waited := time.Since(start); waited > Grace/2 {
		t.Errorf("Wait took %s for a goroutine that had ended", waited)
	}
}

// recorder is a testing.TB that keeps what Errorf was given.
type recorder struct {
	testing.TB
	errors []string
}

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, a ...any) {
	r.errors = append(r.errors, strings.SplitN(format, "%", 2)[0]+strings.Join(strings.Fields(strings.TrimSpace(a[0].(string))), " "))
}

func TestCheck(t *testing.T) {
	// Nothing started: nothing reported.
	r := &recorder{TB: t}
	Check(r)()
	if len(r.errors) != 0 {
		t.Fatalf("reported %q", r.errors)
	}
	// A goroutine that ends within the grace period is none either.
	done := Check(r)
	release := blocked()
	time.AfterFunc(10*time.Millisecond, release)
	done()
	if len(r.errors) != 0 {
		t.Fatalf("reported %q for a goroutine that ended", r.errors)
	}
}

func TestRun(t *testing.T) {
	var out []string
	write := func(s string) (int, error) { out = append(out, s); return len(s), nil }
	// The tests' own result comes first.
	if code := run(func() int { return 3 }, false, write); code != 3 || len(out) != 0 {
		t.Fatalf("failing tests: code %d, output %q", code, out)
	}
	if code := run(func() int { return 0 }, false, write); code != 0 || len(out) != 0 {
		t.Fatalf("passing tests: code %d, output %q", code, out)
	}
	// Passing tests that leave a goroutine fail, and say which.
	var release func()
	leaky := func() int { release = blocked(); return 0 }
	if code := runWithin(leaky, false, write, 20*time.Millisecond); code != 1 || len(out) != 1 || !strings.Contains(out[0], "leakcheck.blocked") {
		t.Fatalf("leaking tests: code %d, output %q", code, out)
	}
	release()
	// A fuzzing run is not checked.
	out = nil
	if code := runWithin(leaky, true, write, 20*time.Millisecond); code != 0 || len(out) != 0 {
		t.Fatalf("fuzzing run: code %d, output %q", code, out)
	}
	release()
}

func TestFuzzing(t *testing.T) {
	if fuzzing() {
		t.Fatal("this run is not a fuzzing run")
	}
	f := flag.Lookup("test.fuzz")
	if f == nil {
		t.Skip("no -test.fuzz flag")
	}
	old := f.Value.String()
	if err := f.Value.Set("FuzzX"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Value.Set(old) }()
	if !fuzzing() {
		t.Fatal("-test.fuzz=FuzzX is a fuzzing run")
	}
}

func TestParse(t *testing.T) {
	gs := parse("goroutine 1 [running]:\nmain.main()\n\t/x/main.go:1 +0x1\n\n" +
		"goroutine 7 [chan receive]:\npkg.(*T).loop(0xc000010000)\n\t/x/t.go:9 +0x20\ncreated by pkg.New in goroutine 1\n\t/x/t.go:5 +0x50\n\n" +
		"not a goroutine\n\n" +
		"goroutine 8 [select]:\ntesting.(*M).Run(0x0)\n\t/go/testing.go:1 +0x1\n")
	if len(gs) != 2 || gs[0].ID != "7" || gs[0].Top() != "pkg.(*T).loop" || gs[0].Creator() != "pkg.New" {
		t.Fatalf("%+v", gs)
	}
	if isBackground(gs[0]) || !isBackground(gs[1]) || gs[1].Creator() != "" {
		t.Fatalf("background: %v %v, creator %q", isBackground(gs[0]), isBackground(gs[1]), gs[1].Creator())
	}
	if (Goroutine{Stack: "goroutine 3 [running]:"}).Top() != "" {
		t.Fatal("a goroutine without frames has no top function")
	}
}

func TestTogether(t *testing.T) {
	var calls, sum atomic.Int64
	Together(8, func(i int) {
		calls.Add(1)
		sum.Add(int64(i))
	})
	if calls.Load() != 8 || sum.Load() != 28 {
		t.Fatalf("%d calls, indexes add up to %d", calls.Load(), sum.Load())
	}
	Together(0, func(int) { t.Error("called") })
}
