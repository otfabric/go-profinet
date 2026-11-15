// SPDX-License-Identifier: MIT

package gsdml

import (
	"bytes"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// TestPathID: an issue path quotes an ID up to maxPathID octets.
func TestPathID(t *testing.T) {
	for n, want := range map[int]int{0: 0, 1: 1, maxPathID - 1: maxPathID - 1, maxPathID: maxPathID, maxPathID + 1: maxPathID + 3, 100000: maxPathID + 3} {
		if got := len(pathID(strings.Repeat("i", n))); got != want {
			t.Errorf("an ID of %d octets is quoted in %d, want %d", n, got, want)
		}
	}
}

// TestIssueRoom: a severity has room for maxIssues issues; one outside
// the three is listed and not counted.
func TestIssueRoom(t *testing.T) {
	v := &validator{}
	for i := range maxIssues + 5 {
		if got, want := v.room(SeverityWarning), i < maxIssues; got != want {
			t.Fatalf("issue %d: room %v", i, got)
		}
	}
	if v.listed[SeverityWarning] != maxIssues || v.omitted[SeverityWarning] != 5 || v.listed[SeverityError] != 0 {
		t.Fatalf("listed %v, omitted %v", v.listed, v.omitted)
	}
	if !v.room(Severity(severities)) || !v.room(Severity(-1)) {
		t.Fatal("no room for a severity outside the three")
	}
	v.closeIssues()
	if len(v.issues) != 1 || v.issues[0].Severity != SeverityWarning || v.issues[0].Rule != ruleLimit {
		t.Fatalf("closing issues %+v", v.issues)
	}
}

// TestWithin: the boundaries of an offset and a length in a record.
func TestWithin(t *testing.T) {
	for _, tc := range []struct {
		off     uint32
		n, size int
		want    bool
	}{
		{0, 0, 0, true}, {0, 1, 0, false}, {1, 0, 0, false},
		{0, 4, 4, true}, {0, 5, 4, false}, {1, 3, 4, true}, {1, 4, 4, false}, {3, 1, 4, true}, {4, 0, 4, true}, {4, 1, 4, false}, {5, 0, 4, false},
		{0, -1, 4, false},
		{1<<31 - 1, 1, 4, false}, {1 << 31, 1, 4, false}, {1<<32 - 1, 1, 4, false}, {1<<32 - 1, 0, 4, false},
		{65534, 1, 65535, true}, {65535, 0, 65535, true}, {65535, 1, 65535, false},
	} {
		if got := within(tc.off, tc.n, tc.size); got != tc.want {
			t.Errorf("within(%d, %d, %d) = %v", tc.off, tc.n, tc.size, got)
		}
	}
}

// TestConvTextWithoutTexts: a document without texts names everything by
// its ID.
func TestConvTextWithoutTexts(t *testing.T) {
	c := newConv(&Document{})
	if c.text("x") != "x" || c.text("y") != "y" {
		t.Fatal("text without a text list")
	}
}

// TestSlotSetParse: a slotSet takes the lists ParseValueList takes and
// holds their slot numbers.
func TestSlotSetParse(t *testing.T) {
	var s slotSet
	s.reset()
	for _, list := range []string{
		"", " ", "0", "65535", "65536", "0..65535", "0..65536", "0..65535 0", "1 1 1", "5..3", "x", "1..x", "1...2", "-1", "4294967295", "4294967296",
		"63 64", "0..63", "64..127", "1..126", "0 65535", "65530..65540", "4294967290..4294967295", "1 2 3 70000", "  7   9..11\t13\n",
	} {
		checkSlotSet(t, &s, list)
	}
}

// FuzzSlotSet: for any text, a slotSet takes what ParseValueList takes,
// holds the slot numbers of its values and counts the values above them.
func FuzzSlotSet(f *testing.F) {
	for _, s := range []string{"1..4 6", "0..65535", "0..65536", "65535 65536", "4294967295", "1 2 3", "5..1", "..", "1..", "-1", " ", "1..4 1..4", "63..64 127..128"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, list string) {
		var s slotSet
		s.reset()
		checkSlotSet(t, &s, list)
		// A set that held another list before holds this one alone.
		s.parse("0..65535")
		checkSlotSet(t, &s, list)
	})
}

// checkSlotSet compares what a slotSet makes of a list with what
// ParseValueList makes of it.
func checkSlotSet(t *testing.T, s *slotSet, list string) {
	t.Helper()
	vs, err := ParseValueList(list, maxSlotValues)
	wantOK := err == nil && strings.TrimSpace(list) != ""
	if ok := s.parse(list); ok != wantOK {
		t.Errorf("%q: taken %v, ParseValueList: %v", list, ok, err)
		return
	}
	if !wantOK {
		return
	}
	want := map[uint32]bool{}
	beyond := uint64(0)
	for _, x := range vs {
		if x >= slotNumbers {
			beyond++
		} else {
			want[x] = true
		}
	}
	got := map[uint32]bool{}
	prevEnd := int64(-2)
	s.ranges(func(lo, hi uint32) {
		if int64(lo) <= prevEnd+1 || hi < lo {
			t.Errorf("%q: run %d..%d after one that ends at %d", list, lo, hi, prevEnd)
		}
		prevEnd = int64(hi)
		for x := lo; x <= hi; x++ {
			got[x] = true
		}
	})
	if len(got) != len(want) || s.beyond != beyond {
		t.Errorf("%q: %d slot numbers and %d values above them, want %d and %d", list, len(got), s.beyond, len(want), beyond)
	}
	for x := range want {
		if !got[x] {
			t.Errorf("%q: slot %d not in the set", list, x)
		}
	}
	// Every slot of the set is missing from an empty set.
	var empty slotSet
	empty.reset()
	first, total := s.missing(&empty, nil)
	if total != len(want) || len(first) != min(total, maxSlotWarnings) || !slices.IsSorted(first) {
		t.Errorf("%q: %d slots missing from the empty set, the first %v; the set has %d", list, total, first, len(want))
	}
}

// TestFixedSlots: fixedSlots keeps the owner of every slot, reports the
// runs that change owner, and reports a range of whole pages page by page.
func TestFixedSlots(t *testing.T) {
	var f fixedSlots
	f.reset()
	owner := make([]int, slotNumbers)
	for i := range owner {
		owner[i] = noOwner
	}
	rng := rand.New(rand.NewPCG(7, 11))
	points := []uint32{0, 1, 255, 256, 257, 511, 512, 4000, 32768, 65279, 65280, 65534, 65535}
	for n := range 400 {
		a, b := points[rng.IntN(len(points))], points[rng.IntN(len(points))]
		if rng.IntN(3) == 0 {
			b = min(a+uint32(rng.IntN(700)), slotNumbers-1)
		}
		lo, hi, who := min(a, b), max(a, b), rng.IntN(4)
		if n%50 == 49 {
			lo, hi = 0, slotNumbers-1
		}
		var want int
		for s := lo; s <= hi; s++ {
			if owner[s] != noOwner && owner[s] != who {
				want++
			}
		}
		got, next := 0, lo
		f.fix(lo, hi, who, func(x, y uint32, prev int) {
			if x < next || y < x || y > hi {
				t.Fatalf("op %d: run %d..%d after %d in %d..%d", n, x, y, next, lo, hi)
			}
			next = y + 1
			for s := x; s <= y; s++ {
				if owner[s] != prev || prev == who || prev == noOwner {
					t.Fatalf("op %d: slot %d reported with owner %d, it had %d (new owner %d)", n, s, prev, owner[s], who)
				}
			}
			got += int(y - x + 1)
		})
		if got != want {
			t.Fatalf("op %d: %d slots changed owner, want %d", n, got, want)
		}
		for s := lo; s <= hi; s++ {
			owner[s] = who
		}
	}
	// All slots to one owner, then to another: the second pass sees whole
	// pages, one run each.
	f.fix(0, slotNumbers-1, 8, func(uint32, uint32, int) {})
	calls, slots := 0, 0
	f.fix(0, slotNumbers-1, 9, func(x, y uint32, prev int) {
		calls++
		slots += int(y - x + 1)
		if prev != 8 {
			t.Fatalf("owner %d", prev)
		}
	})
	if slots != slotNumbers || calls != slotNumbers/pageSlots {
		t.Fatalf("%d slots in %d runs, want %d in %d", slots, calls, slotNumbers, slotNumbers/pageSlots)
	}
}

// TestStartTags: the elements of a document are counted as its start
// tags.
func TestStartTags(t *testing.T) {
	for in, want := range map[string]int{
		"": 0, "<": 0, "a<": 0, "<a": 1, "<a/>": 1, "</a>": 0, "<a></a>": 1, "<a><b/><c>x</c></a>": 3,
		`<?xml version="1.0"?><a/>`: 1, "<!DOCTYPE a><a/>": 1, "<!-- <a> --><b/>": 2, "<![CDATA[<a>]]>": 1, "<<<": 2,
	} {
		if got := startTags([]byte(in), 100); got != want {
			t.Errorf("startTags(%q) = %d, want %d", in, got, want)
		}
	}
	// Counting stops above the limit.
	many := bytes.Repeat([]byte("<a/>"), 50)
	for limit, want := range map[int]int{0: 1, 1: 2, 49: 50, 50: 50, 51: 50} {
		if got := startTags(many, limit); got != want {
			t.Errorf("limit %d: %d, want %d", limit, got, want)
		}
	}
}
