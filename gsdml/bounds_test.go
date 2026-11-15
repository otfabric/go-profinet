// SPDX-License-Identifier: MIT

package gsdml

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand/v2"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet"
)

// allocated returns the octets fn allocates (runtime.MemStats.TotalAlloc).
func allocated(fn func()) uint64 {
	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	fn()
	runtime.ReadMemStats(&m1)
	return m1.TotalAlloc - m0.TotalAlloc
}

func mustParse(t *testing.T, b []byte) *Document {
	t.Helper()
	doc, err := ParseBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func countRule(r Report, rule string) int {
	n := 0
	for _, i := range r.Issues {
		if i.Rule == rule {
			n++
		}
	}
	return n
}

const docHead = `<?xml version="1.0"?><ISO15745Profile xmlns="` + Namespace + `">`

// ratioDoc has items interface submodules with the reduction ratio list.
func ratioDoc(items int, list string) []byte {
	var b strings.Builder
	b.WriteString(docHead + `<ProfileBody><ApplicationProcess><DeviceAccessPointList><DeviceAccessPointItem ID="D"><SystemDefinedSubmoduleList>`)
	for i := range items {
		fmt.Fprintf(&b, `<InterfaceSubmoduleItem ID="I%d" SubslotNumber="32768"><ApplicationRelations><TimingProperties ReductionRatio="%s"/></ApplicationRelations></InterfaceSubmoduleItem>`, i, list)
	}
	b.WriteString(`</SystemDefinedSubmoduleList></DeviceAccessPointItem></DeviceAccessPointList></ApplicationProcess></ProfileBody></ISO15745Profile>`)
	return []byte(b.String())
}

// TestCheckReductionRatiosBounded: a reduction ratio list gets at most
// maxSlotWarnings warnings and one for the rest, a list of more than
// maxReductionRatios values one in all, and what Check allocates follows
// the size of the document, not the ranges in it.
func TestCheckReductionRatiosBounded(t *testing.T) {
	seq := func(first, n int) string {
		if n == 0 {
			return ""
		}
		return fmt.Sprintf("%d..%d", first, first+n-1)
	}
	for _, tc := range []struct {
		name, list string
		want       int
		last       string
	}{
		{"the ten ratios", "1 2 4 8 16 32 64 128 256 512", 0, ""},
		{"one value that is no ratio", "3", 1, "reduction ratio 3 is not a power of two from 1 to 512"},
		{"zero", "0", 1, "reduction ratio 0 is not a power of two from 1 to 512"},
		{"eight", seq(1000, maxSlotWarnings), maxSlotWarnings, "reduction ratio 1007 is not a power of two from 1 to 512"},
		{"nine", seq(1000, maxSlotWarnings+1), maxSlotWarnings + 1, "1 more reduction ratios are not a power of two from 1 to 512"},
		{"one below the list bound", seq(1000, maxReductionRatios-1), maxSlotWarnings + 1, "55 more reduction ratios are not a power of two from 1 to 512"},
		{"the list bound", seq(1000, maxReductionRatios), maxSlotWarnings + 1, "56 more reduction ratios are not a power of two from 1 to 512"},
		{"one above the list bound", seq(1000, maxReductionRatios+1), 1, `reduction ratio list "1000..1064" has more than 64 values: the ratios are the powers of two from 1 to 512`},
		{"the widest range", "0..4294967295", 1, `reduction ratio list "0..4294967295" has more than 64 values: the ratios are the powers of two from 1 to 512`},
		{"no value list", "1..x", 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Check(mustParse(t, ratioDoc(1, tc.list)))
			got := issuesOf(r, RuleReductionRatio, SeverityWarning)
			if len(got) != tc.want {
				t.Fatalf("%d warnings, want %d: %+v", len(got), tc.want, got)
			}
			if tc.want > 0 && got[len(got)-1].Message != tc.last {
				t.Fatalf("last warning %q, want %q", got[len(got)-1].Message, tc.last)
			}
		})
	}

	const items = 32
	in := ratioDoc(items, "0..65535")
	doc := mustParse(t, in)
	var r Report
	a := allocated(func() { r = Check(doc) })
	if n := countRule(r, RuleReductionRatio); n != items {
		t.Errorf("%d warnings for %d lists, want one each", n, items)
	}
	if budget := uint64(256<<10 + 64*len(in)); a > budget {
		t.Errorf("Check allocated %d octets for a document of %d, budget %d", a, len(in), budget)
	}
}

// unknownDoc has n elements the catalog does not define.
func unknownDoc(n int) []byte {
	return []byte(docHead + strings.Repeat("<x/>", n) + `</ISO15745Profile>`)
}

// TestCheckIssuesBounded: Check lists maxIssues issues of a severity and
// says in one more how many it left out.
func TestCheckIssuesBounded(t *testing.T) {
	for _, n := range []int{0, 1, maxIssues - 1, maxIssues, maxIssues + 1, 3 * maxIssues} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			r := Check(mustParse(t, unknownDoc(n)))
			unknown, limit := issuesOf(r, RuleUnknown, SeverityUnvalidated), issuesOf(r, ruleLimit, SeverityUnvalidated)
			if len(unknown) != min(n, maxIssues) {
				t.Fatalf("%d unknown elements listed, want %d", len(unknown), min(n, maxIssues))
			}
			if n <= maxIssues {
				if len(limit) != 0 {
					t.Fatalf("closing issue for %d issues: %+v", n, limit)
				}
				return
			}
			want := fmt.Sprintf("%d more issues of severity unvalidated are not listed: Check lists the first %d of a severity", n-maxIssues, maxIssues)
			if len(limit) != 1 || limit[0].Message != want || r.Issues[len(r.Issues)-1] != limit[0] {
				t.Fatalf("closing issue %+v, want the last issue to say %q", limit, want)
			}
			if got := r.Count(SeverityUnvalidated); got != maxIssues+1 {
				t.Fatalf("%d unvalidated issues, want %d", got, maxIssues+1)
			}
		})
	}

	// Errors beyond the bound still fail Validate, and are counted.
	doc := &Document{ProfileBody: &ProfileBody{ApplicationProcess: &ApplicationProcess{ModuleList: &ModuleList{}}}}
	const modules = maxIssues + 10
	for range modules {
		doc.ProfileBody.ApplicationProcess.ModuleList.ModuleItems = append(doc.ProfileBody.ApplicationProcess.ModuleList.ModuleItems, &ModuleItem{})
	}
	var ve *ValidationError
	if err := Validate(doc); !errors.As(err, &ve) || !errors.Is(err, profinet.ErrMalformed) {
		t.Fatalf("Validate: %v", err)
	}
	last := ve.Issues[len(ve.Issues)-1]
	if len(ve.Issues) != maxIssues+1 || last.Rule != ruleLimit || last.Severity != SeverityError || !strings.Contains(last.Message, " more issues of severity error are not listed") {
		t.Errorf("%d errors, the last %+v", len(ve.Issues), last)
	}

	// The memory of a report does not follow the number of issues.
	const many = 150000
	in := unknownDoc(many)
	big := mustParse(t, in)
	var r Report
	a := allocated(func() { r = Check(big) })
	if len(r.Issues) > severities*(maxIssues+1) {
		t.Errorf("%d issues listed", len(r.Issues))
	}
	if budget := uint64(16 << 20); a > budget {
		t.Errorf("Check allocated %d octets for %d issues, budget %d", a, many, budget)
	}
}

// TestCheckLongID: the issues of the semantic rules quote a bounded part
// of an ID, in their path and in their message.
func TestCheckLongID(t *testing.T) {
	long := strings.Repeat("m", 100000)
	doc := &Document{ProfileBody: &ProfileBody{ApplicationProcess: &ApplicationProcess{ModuleList: &ModuleList{ModuleItems: []*ModuleItem{
		{ID: long, ModuleIdentNumber: "1"}, {ID: long + "2", ModuleIdentNumber: "1"},
	}}}}}
	v := &validator{}
	semantic(doc, v)
	if len(v.issues) != 1 || v.issues[0].Rule != RuleIdentNumber {
		t.Fatalf("issues %d", len(v.issues))
	}
	if p, m := len(v.issues[0].Path), len(v.issues[0].Message); p+m > 1024 {
		t.Fatalf("an issue for IDs of %d octets has a path of %d and a message of %d octets", len(long), p, m)
	}
}

// TestCheckPaths: the paths of the issues of the semantic rules.
func TestCheckPaths(t *testing.T) {
	r := checkDoc(t, func(doc *Document) {
		d := dap(doc)
		d.PhysicalSlots = "0"
		d.VirtualSubmoduleList.VirtualSubmoduleItems[0].RecordDataList = &RecordDataList{ParameterRecordDataItems: []*ParameterRecordDataItem{{Index: "0x8000", Length: "1", Name: &Name{TextID: d.ModuleInfo.Name.TextID}}}}
		sd := d.SystemDefinedSubmoduleList
		sd.InterfaceSubmoduleItems[0].SubslotNumber = "28672"
		sd.InterfaceSubmoduleItems[0].ApplicationRelations.TimingProperties = &TimingProperties{ReductionRatio: "3"}
		sd.PortSubmoduleItems[0].SubslotNumber = "36864"
		ms := modules(doc)
		ms[1].ModuleIdentNumber = ms[0].ModuleIdentNumber
		ms[0].VirtualSubmoduleList.VirtualSubmoduleItems[0].RecordDataList.ParameterRecordDataItems[0].Index = "0x8000"
	})
	const ap = "ISO15745Profile/ProfileBody/ApplicationProcess"
	want := map[string]string{
		RuleIdentNumber:    ap + "/ModuleList/ModuleItem[M2]",
		RuleRecordIndex:    ap + "/ModuleList/ModuleItem[M1]/VirtualSubmoduleList/VirtualSubmoduleItem[M1_SM1]",
		RuleSubslot:        ap + "/DeviceAccessPointList/DeviceAccessPointItem[DAP]/SystemDefinedSubmoduleList/InterfaceSubmoduleItem[IF32768]",
		RuleReductionRatio: ap + "/DeviceAccessPointList/DeviceAccessPointItem[DAP]/SystemDefinedSubmoduleList/InterfaceSubmoduleItem[IF32768]",
		RuleSlots:          ap + "/DeviceAccessPointList/DeviceAccessPointItem[DAP]/UseableModules/ModuleItemRef[M1]",
	}
	also := map[string]bool{
		ap + "/DeviceAccessPointList/DeviceAccessPointItem[DAP]/VirtualSubmoduleList/VirtualSubmoduleItem[DAP_SM1]":      false,
		ap + "/DeviceAccessPointList/DeviceAccessPointItem[DAP]/SystemDefinedSubmoduleList/PortSubmoduleItem[Port32769]": false,
	}
	first := map[string]string{}
	for _, is := range r.Issues {
		if is.Severity != SeverityWarning {
			t.Fatalf("issue %+v", is)
		}
		if _, seen := first[is.Rule]; !seen {
			first[is.Rule] = is.Path
		}
		if _, ok := also[is.Path]; ok {
			also[is.Path] = true
		}
	}
	for rule, path := range want {
		if first[rule] != path {
			t.Errorf("first issue of rule %s at %q, want %q", rule, first[rule], path)
		}
	}
	for path, seen := range also {
		if !seen {
			t.Errorf("no issue at %q in %+v", path, r.Issues)
		}
	}
}

// slotDoc has refs module references of different modules, each with the
// three slot lists.
func slotDoc(refs int, physical, list string) []byte {
	var b strings.Builder
	b.WriteString(docHead + `<ProfileBody><ApplicationProcess><DeviceAccessPointList>`)
	fmt.Fprintf(&b, `<DeviceAccessPointItem ID="D" PhysicalSlots="%s" FixedInSlots="0"><UseableModules>`, physical)
	for i := range refs {
		fmt.Fprintf(&b, `<ModuleItemRef ModuleItemTarget="M%d" AllowedInSlots="%s" UsedInSlots="%s" FixedInSlots="%s"/>`, i, list, list, list)
	}
	b.WriteString(`</UseableModules></DeviceAccessPointItem></DeviceAccessPointList></ApplicationProcess></ProfileBody></ISO15745Profile>`)
	return []byte(b.String())
}

// TestCheckSlotListsBounded: what Check allocates for slot lists follows
// the size of the document, not the ranges in them, and a module reference
// gets a bounded number of warnings.
func TestCheckSlotListsBounded(t *testing.T) {
	const refs = 24
	for _, tc := range []struct{ physical, list string }{
		{"0", "0..65535"},
		{"0..65535", "0..65535"},
		{"1 3 5", "0..255 300..65000"},
	} {
		in := slotDoc(refs, tc.physical, tc.list)
		doc := mustParse(t, in)
		var r Report
		a := allocated(func() { r = Check(doc) })
		n := countRule(r, RuleSlots)
		// Per reference: three lists against the PhysicalSlots, two
		// against AllowedInSlots, one against the slots fixed before.
		if n == 0 || n > refs*6*(maxSlotWarnings+1) {
			t.Errorf("PhysicalSlots %q, lists %q: %d slot warnings", tc.physical, tc.list, n)
		}
		if budget := uint64(2<<20 + 64*len(in)); a > budget {
			t.Errorf("PhysicalSlots %q, lists %q: Check allocated %d octets for a document of %d, budget %d", tc.physical, tc.list, a, len(in), budget)
		}
	}
}

// oldSlotSet is the set of a slot list by its values, as ParseValueList
// gives them.
func oldSlotSet(s string) (map[uint32]bool, bool) {
	if strings.TrimSpace(s) == "" {
		return nil, false
	}
	vs, err := ParseValueList(s, maxSlotValues)
	if err != nil {
		return nil, false
	}
	m := map[uint32]bool{}
	for _, x := range vs {
		m[x] = true
	}
	return m, true
}

// slotRulesByValue applies the slot rules value by value and returns the
// messages of the warnings, sorted.
func slotRulesByValue(dap *DeviceAccessPointItem) []string {
	var out []string
	emit := func(slots []uint32, one func(uint32) string, rest string) {
		slices.Sort(slots)
		for i, s := range slots {
			if i == maxSlotWarnings {
				out = append(out, fmt.Sprintf(rest, len(slots)-i))
				return
			}
			out = append(out, one(s))
		}
	}
	physical, havePhysical := oldSlotSet(dap.PhysicalSlots)
	fixedBy := map[uint32]string{}
	for _, ref := range dap.UseableModules.ModuleItemRefs {
		allowed, haveAllowed := oldSlotSet(ref.AllowedInSlots)
		for i, list := range []string{ref.AllowedInSlots, ref.UsedInSlots, ref.FixedInSlots} {
			name := slotLists[i].name
			set, ok := oldSlotSet(list)
			if !ok {
				continue
			}
			var notPhysical, notAllowed, fixedTwice []uint32
			prev := map[uint32]string{}
			for s := range set {
				if havePhysical && !physical[s] {
					notPhysical = append(notPhysical, s)
				}
				if i > 0 && haveAllowed && !allowed[s] {
					notAllowed = append(notAllowed, s)
				}
				if i == 2 {
					if p, dup := fixedBy[s]; dup && p != ref.ModuleItemTarget {
						fixedTwice = append(fixedTwice, s)
						prev[s] = p
					}
					fixedBy[s] = ref.ModuleItemTarget
				}
			}
			emit(notPhysical, func(s uint32) string {
				return fmt.Sprintf("%s: slot %d is not among the PhysicalSlots %q", name, s, clip(dap.PhysicalSlots))
			}, name+": %d more slots are not among the PhysicalSlots")
			emit(notAllowed, func(s uint32) string {
				return fmt.Sprintf("%s: slot %d is not in AllowedInSlots %q", name, s, clip(ref.AllowedInSlots))
			}, name+": %d more slots are not in AllowedInSlots")
			emit(fixedTwice, func(s uint32) string {
				return fmt.Sprintf("slot %d is also fixed by module %s", s, clip(prev[s]))
			}, "%d more slots are also fixed by another module")
		}
	}
	sort.Strings(out)
	return out
}

// TestCheckSlotsByValue: for slot numbers, the slot rules on sets say what
// the rules applied value by value say.
func TestCheckSlotsByValue(t *testing.T) {
	rng := rand.New(rand.NewPCG(16, 2026))
	points := []uint32{0, 1, 2, 3, 5, 8, 62, 63, 64, 65, 127, 128, 254, 255, 256, 257, 300, 511, 512, 513, 1000, 4095, 4096, 32767, 32768, 65534, 65535}
	list := func() string {
		switch rng.IntN(24) {
		case 0, 1:
			return ""
		case 2:
			return "0..65535"
		case 3, 4:
			return "7..3" // no value list
		case 5:
			return "0..65535 9" // more values than are looked at
		}
		var toks []string
		for range 1 + rng.IntN(5) {
			a := points[rng.IntN(len(points))]
			switch rng.IntN(8) {
			case 0, 1, 2, 3:
				toks = append(toks, strconv.FormatUint(uint64(a), 10))
			case 4, 5, 6:
				toks = append(toks, fmt.Sprintf("%d..%d", a, min(a+uint32(rng.IntN(600)), slotNumbers-1)))
			default:
				b := points[rng.IntN(len(points))]
				toks = append(toks, fmt.Sprintf("%d..%d", min(a, b), max(a, b)))
			}
		}
		return strings.Join(toks, " ")
	}
	compared := 0
	for n := range 120 {
		dap := &DeviceAccessPointItem{ID: "D", PhysicalSlots: list(), UseableModules: &UseableModules{}}
		for range 1 + rng.IntN(5) {
			dap.UseableModules.ModuleItemRefs = append(dap.UseableModules.ModuleItemRefs, &ModuleItemRef{
				ModuleItemTarget: "M" + strconv.Itoa(rng.IntN(3)), AllowedInSlots: list(), UsedInSlots: list(), FixedInSlots: list(),
			})
		}
		doc := &Document{ProfileBody: &ProfileBody{ApplicationProcess: &ApplicationProcess{
			DeviceAccessPointList: &DeviceAccessPointList{DeviceAccessPointItems: []*DeviceAccessPointItem{dap, dap}},
		}}}
		v := &validator{}
		semantic(doc, v)
		var got []string
		for _, is := range v.issues {
			if is.Rule != RuleSlots || is.Severity != SeverityWarning {
				t.Fatalf("case %d: issue %+v", n, is)
			}
			got = append(got, is.Message)
		}
		// The document has the device access point twice: the second one
		// starts with no slot fixed.
		want := slotRulesByValue(dap)
		want = append(want, want...)
		sort.Strings(got)
		sort.Strings(want)
		if !slices.Equal(got, want) {
			var refs []string
			for _, r := range dap.UseableModules.ModuleItemRefs {
				refs = append(refs, fmt.Sprintf("%s allowed %q used %q fixed %q", r.ModuleItemTarget, r.AllowedInSlots, r.UsedInSlots, r.FixedInSlots))
			}
			t.Fatalf("case %d: PhysicalSlots %q\n%s\ngot  %q\nwant %q", n, dap.PhysicalSlots, strings.Join(refs, "\n"), got, want)
		}
		compared += len(want)
	}
	if compared < 1000 {
		t.Fatalf("%d warnings compared: the cases say too little", compared)
	}
	t.Logf("%d warnings compared", compared)
}

// TestCheckSlotsAboveSlotNumbers: the values of a slot list above 65535
// are no slot numbers: the list gets one warning for them, and its slot
// numbers are checked as usual.
func TestCheckSlotsAboveSlotNumbers(t *testing.T) {
	dap := &DeviceAccessPointItem{ID: "D", PhysicalSlots: "0..2 65536", UseableModules: &UseableModules{ModuleItemRefs: []*ModuleItemRef{
		{ModuleItemTarget: "M1", AllowedInSlots: "1 65535 65536 70000..70009", UsedInSlots: "1", FixedInSlots: "4294967295"},
		{ModuleItemTarget: "M2", AllowedInSlots: "2", FixedInSlots: "2 65530..65540"},
	}}}
	doc := &Document{ProfileBody: &ProfileBody{ApplicationProcess: &ApplicationProcess{
		DeviceAccessPointList: &DeviceAccessPointList{DeviceAccessPointItems: []*DeviceAccessPointItem{dap}},
	}}}
	v := &validator{}
	semantic(doc, v)
	var got []string
	for _, is := range v.issues {
		got = append(got, is.Message)
	}
	want := []string{
		"PhysicalSlots: 1 values are above 65535, the highest slot number",
		"AllowedInSlots: 11 values are above 65535, the highest slot number",
		`AllowedInSlots: slot 65535 is not among the PhysicalSlots "0..2 65536"`,
		"FixedInSlots: 1 values are above 65535, the highest slot number",
		"FixedInSlots: 5 values are above 65535, the highest slot number",
		`FixedInSlots: slot 65530 is not among the PhysicalSlots "0..2 65536"`,
		`FixedInSlots: slot 65531 is not among the PhysicalSlots "0..2 65536"`,
		`FixedInSlots: slot 65532 is not among the PhysicalSlots "0..2 65536"`,
		`FixedInSlots: slot 65533 is not among the PhysicalSlots "0..2 65536"`,
		`FixedInSlots: slot 65534 is not among the PhysicalSlots "0..2 65536"`,
		`FixedInSlots: slot 65535 is not among the PhysicalSlots "0..2 65536"`,
		`FixedInSlots: slot 65530 is not in AllowedInSlots "2"`,
		`FixedInSlots: slot 65531 is not in AllowedInSlots "2"`,
		`FixedInSlots: slot 65532 is not in AllowedInSlots "2"`,
		`FixedInSlots: slot 65533 is not in AllowedInSlots "2"`,
		`FixedInSlots: slot 65534 is not in AllowedInSlots "2"`,
		`FixedInSlots: slot 65535 is not in AllowedInSlots "2"`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// textDoc has n texts and a submodule with n records, each named by a
// text of its own; the text IDs are in the order given.
func textDoc(n int) *Document {
	pl := &PrimaryLanguage{}
	sm := &VirtualSubmoduleItem{ID: "S", SubmoduleIdentNumber: "3", RecordDataList: &RecordDataList{}}
	for i := range n {
		id := "T" + strconv.Itoa(i)
		pl.Texts = append(pl.Texts, &Text{TextID: id, Value: "text " + strconv.Itoa(i)})
		sm.RecordDataList.ParameterRecordDataItems = append(sm.RecordDataList.ParameterRecordDataItems,
			&ParameterRecordDataItem{Index: strconv.Itoa(i), Length: "0", Name: &Name{TextID: id}})
	}
	return &Document{ProfileBody: &ProfileBody{
		DeviceIdentity: &DeviceIdentity{VendorID: "1", DeviceID: "1"},
		ApplicationProcess: &ApplicationProcess{
			DeviceAccessPointList: &DeviceAccessPointList{DeviceAccessPointItems: []*DeviceAccessPointItem{{
				ID: "D", ModuleIdentNumber: "1", UseableModules: &UseableModules{ModuleItemRefs: []*ModuleItemRef{{ModuleItemTarget: "M", FixedInSlots: "1"}}},
			}}},
			ModuleList:       &ModuleList{ModuleItems: []*ModuleItem{{ID: "M", ModuleIdentNumber: "2", VirtualSubmoduleList: &VirtualSubmoduleList{VirtualSubmoduleItems: []*VirtualSubmoduleItem{sm}}}}},
			ExternalTextList: &ExternalTextList{PrimaryLanguage: pl},
		},
	}}
}

// TestToModelTextLookups: ToModel goes through the texts of the document
// once, whatever the number of names it looks up, and finds what
// Document.Text finds.
func TestToModelTextLookups(t *testing.T) {
	const n = 4000
	doc := textDoc(n)
	// A text ID used twice, and a name without a text.
	texts := doc.ProfileBody.ApplicationProcess.ExternalTextList.PrimaryLanguage
	texts.Texts = append(texts.Texts, &Text{TextID: "T7", Value: "second text of T7"})
	recs := doc.ProfileBody.ApplicationProcess.ModuleList.ModuleItems[0].VirtualSubmoduleList.VirtualSubmoduleItems[0].RecordDataList.ParameterRecordDataItems
	recs[9].Name.TextID = "no such text"

	visits := 0
	textVisits = func(k int) { visits += k }
	t.Cleanup(func() { textVisits = nil })
	m, err := ToModel(doc, "")
	if err != nil {
		t.Fatal(err)
	}
	if limit := 2 * len(texts.Texts); visits > limit {
		t.Errorf("ToModel went through %d texts for %d names of %d texts, want at most %d", visits, n, len(texts.Texts), limit)
	}
	textVisits = nil
	got := m.Slots[0].Module.Submodules[0].Records
	if len(got) != n {
		t.Fatalf("%d records", len(got))
	}
	for i, r := range got {
		if want := doc.Text(recs[i].Name.TextID); r.Name != want {
			t.Fatalf("record %d named %q, Document.Text says %q", i, r.Name, want)
		}
	}
	if got[7].Name != "text 7" || got[9].Name != "no such text" {
		t.Fatalf("names %q and %q", got[7].Name, got[9].Name)
	}
	// Document.Text reports its own steps to the same counter.
	visits = 0
	textVisits = func(k int) { visits += k }
	if doc.Text("T2") != "text 2" || visits != 3 {
		t.Fatalf("%d texts visited for the third text", visits)
	}
	if doc.Text("none") != "none" || visits != 3+len(texts.Texts) {
		t.Fatalf("%d texts visited", visits)
	}
}

// TestParseElementBound: a document with more elements than the bound is
// refused before it is decoded.
func TestParseElementBound(t *testing.T) {
	// The root is one element; n more follow in it.
	doc := func(n int) []byte { return unknownDoc(n) }
	for _, tc := range []struct {
		elements, bound int
		ok              bool
	}{
		{1, 0, false}, {1, 1, true}, {2, 1, false}, {99, 100, true}, {100, 100, true}, {101, 100, false}, {1000, 100, false},
	} {
		d, err := parseBytes(doc(tc.elements-1), tc.bound)
		switch {
		case tc.ok && (err != nil || len(d.Unknown) != tc.elements-1):
			t.Errorf("%d elements, bound %d: %v", tc.elements, tc.bound, err)
		case !tc.ok && !errors.Is(err, profinet.ErrResourceExhausted):
			t.Errorf("%d elements, bound %d: err %v, want ErrResourceExhausted", tc.elements, tc.bound, err)
		}
	}
	// The bound of ParseBytes and Parse, with a byte order mark.
	in := append([]byte("\xEF\xBB\xBF"), doc(maxElements)...)
	var err error
	a := allocated(func() { _, err = ParseBytes(in) })
	if !errors.Is(err, profinet.ErrResourceExhausted) || !strings.Contains(err.Error(), "more than 1048576 elements") {
		t.Fatalf("%d elements: err %v, want ErrResourceExhausted", maxElements+1, err)
	}
	if budget := uint64(64 << 10); a > budget {
		t.Errorf("ParseBytes allocated %d octets to refuse a document of %d, budget %d", a, len(in), budget)
	}
	if _, err := Parse(bytes.NewReader(in)); !errors.Is(err, profinet.ErrResourceExhausted) {
		t.Fatalf("Parse: %v", err)
	}
}

// pluggedDoc has a device access point with a record of dapLen octets in
// its own submodule, and a module with records of 65536 octets in all,
// fixed in the slots 1 to slots.
func pluggedDoc(dapLen, slots int) []byte {
	var b strings.Builder
	b.WriteString(docHead + `<ProfileBody><DeviceIdentity VendorID="0x1" DeviceID="0x1"/><ApplicationProcess>`)
	b.WriteString(`<DeviceAccessPointList><DeviceAccessPointItem ID="D" ModuleIdentNumber="1" PhysicalSlots="0..300" FixedInSlots="0">`)
	if dapLen > 0 {
		fmt.Fprintf(&b, `<VirtualSubmoduleList><VirtualSubmoduleItem ID="DS" SubmoduleIdentNumber="1"><RecordDataList><ParameterRecordDataItem Index="1" Length="%d"/></RecordDataList></VirtualSubmoduleItem></VirtualSubmoduleList>`, dapLen)
	}
	b.WriteString(`<UseableModules>`)
	if slots > 0 {
		fmt.Fprintf(&b, `<ModuleItemRef ModuleItemTarget="M" FixedInSlots="1..%d"/>`, slots)
	} else {
		b.WriteString(`<ModuleItemRef ModuleItemTarget="M"/>`)
	}
	b.WriteString(`</UseableModules></DeviceAccessPointItem></DeviceAccessPointList>`)
	b.WriteString(`<ModuleList><ModuleItem ID="M" ModuleIdentNumber="2"><VirtualSubmoduleList><VirtualSubmoduleItem ID="S" SubmoduleIdentNumber="3"><RecordDataList>`)
	b.WriteString(`<ParameterRecordDataItem Index="1" Length="65535"/><ParameterRecordDataItem Index="2" Length="1"/>`)
	b.WriteString(`</RecordDataList></VirtualSubmoduleItem></VirtualSubmoduleList></ModuleItem></ModuleList></ApplicationProcess></ProfileBody></ISO15745Profile>`)
	return []byte(b.String())
}

// TestToModelPluggedRecordOctets: MaxModelRecordOctets bounds the records
// of the modules as they are plugged: a module in several slots counts
// once per slot.
func TestToModelPluggedRecordOctets(t *testing.T) {
	const module = 65536
	full := MaxModelRecordOctets / module // slots that reach the bound exactly
	for _, tc := range []struct {
		dapLen, slots int
		ok            bool
	}{
		{0, 0, true}, {0, 1, true}, {65535, 1, true},
		{0, full - 1, true},
		{65535, full - 1, true}, // one octet below the bound
		{0, full, true},         // the bound
		{1, full, false},        // one octet above
		{0, full + 1, false},
		{65535, full + 40, false},
	} {
		m, err := ToModel(mustParse(t, pluggedDoc(tc.dapLen, tc.slots)), "")
		total := tc.dapLen + tc.slots*module
		switch {
		case tc.ok && err != nil:
			t.Errorf("%d octets plugged: %v", total, err)
		case tc.ok && (len(m.Slots) != tc.slots || len(m.Catalog) != 1 || (tc.dapLen > 0) != (len(m.DAP.Submodules) == 1)):
			t.Errorf("%d octets plugged: %d slots", total, len(m.Slots))
		case !tc.ok && !errors.Is(err, profinet.ErrResourceExhausted):
			t.Errorf("%d octets plugged: err %v, want ErrResourceExhausted", total, err)
		}
	}
}

// TestToModelSubmoduleBound: a module, and the device access point, has
// at most the submodules a C_SDU carries.
func TestToModelSubmoduleBound(t *testing.T) {
	subs := func(n int) *VirtualSubmoduleList {
		l := &VirtualSubmoduleList{}
		for i := range n {
			l.VirtualSubmoduleItems = append(l.VirtualSubmoduleItems, &VirtualSubmoduleItem{ID: "S" + strconv.Itoa(i), SubmoduleIdentNumber: "1"})
		}
		return l
	}
	base := func() *Document {
		return &Document{ProfileBody: &ProfileBody{
			DeviceIdentity: &DeviceIdentity{VendorID: "1", DeviceID: "1"},
			ApplicationProcess: &ApplicationProcess{
				DeviceAccessPointList: &DeviceAccessPointList{DeviceAccessPointItems: []*DeviceAccessPointItem{{
					ID: "D", ModuleIdentNumber: "1", UseableModules: &UseableModules{ModuleItemRefs: []*ModuleItemRef{{ModuleItemTarget: "M"}}},
				}}},
				ModuleList: &ModuleList{ModuleItems: []*ModuleItem{{ID: "M", ModuleIdentNumber: "2"}}},
			},
		}}
	}
	for _, n := range []int{0, 1, maxModuleSubmodules - 1, maxModuleSubmodules, maxModuleSubmodules + 1, 40 * maxModuleSubmodules} {
		ok := n <= maxModuleSubmodules
		// In a module.
		doc := base()
		doc.ProfileBody.ApplicationProcess.ModuleList.ModuleItems[0].VirtualSubmoduleList = subs(n)
		m, err := ToModel(doc, "")
		if ok && (err != nil || len(m.Catalog[0].Submodules) != n) || !ok && !errors.Is(err, profinet.ErrMalformed) {
			t.Errorf("a module with %d submodules: %v", n, err)
		}
		// In the device access point: virtual submodules, an interface
		// and a port together.
		doc = base()
		dap := doc.ProfileBody.ApplicationProcess.DeviceAccessPointList.DeviceAccessPointItems[0]
		if n >= 2 {
			dap.VirtualSubmoduleList = subs(n - 2)
			dap.SystemDefinedSubmoduleList = &SystemDefinedSubmoduleList{
				InterfaceSubmoduleItems: []*InterfaceSubmoduleItem{{ID: "I", SubslotNumber: "32768", SubmoduleIdentNumber: "2"}},
				PortSubmoduleItems:      []*PortSubmoduleItem{{ID: "P", SubslotNumber: "32769", SubmoduleIdentNumber: "3"}},
			}
		} else {
			dap.VirtualSubmoduleList = subs(n)
		}
		m, err = ToModel(doc, "")
		if ok && (err != nil || len(m.DAP.Submodules) != n) || !ok && !errors.Is(err, profinet.ErrMalformed) {
			t.Errorf("a device access point with %d submodules: %v", n, err)
		}
	}
}

// stopped runs fn and returns what it stopped with, nil when it returned.
func stopped(fn func()) (r any) {
	defer func() { r = recover() }()
	fn()
	return nil
}

// TestRecordOffsets: the offsets of Const and Ref are compared in 64
// bits, so an offset above the int range of a 32-bit platform lies beyond
// the record like any other; an absent offset is 0 and one that is no
// number is refused.
func TestRecordOffsets(t *testing.T) {
	const size = 4
	for _, tc := range []struct {
		name string
		off  string
		ok   bool
	}{
		{"absent", "", true}, {"zero", "0", true}, {"last octet", "3", true},
		{"the size", "4", false}, {"above the size", "5", false},
		{"the largest int32", "2147483647", false}, {"above int32", "2147483648", false}, {"the largest uint32", "4294967295", false},
		{"above uint32", "4294967296", false}, {"no number", "x", false}, {"negative", "-1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check := func(what string, ok bool, fn func() error) {
				t.Helper()
				var err error
				if r := stopped(func() { err = fn() }); r != nil {
					t.Errorf("%s at offset %q: stopped with %v", what, tc.off, r)
					return
				}
				if ok && err != nil || !ok && !errors.Is(err, profinet.ErrMalformed) {
					t.Errorf("%s at offset %q: err %v, want accepted %v", what, tc.off, err, ok)
				}
			}
			check("a Const of one octet", tc.ok, func() error {
				rec, err := newConv(&Document{}).toRecord(&ParameterRecordDataItem{Index: "1", Length: strconv.Itoa(size), Consts: []*Const{{ByteOffset: tc.off, Data: "0x55"}}})
				if err == nil && tc.ok {
					off, _ := strconv.Atoi("0" + tc.off)
					if rec.Default[off] != 0x55 {
						t.Errorf("record % x", rec.Default)
					}
				}
				return err
			})
			// A Ref must name its offset.
			check("a Ref of one octet", tc.ok && tc.off != "", func() error {
				return applyRef(make([]byte, size), &Ref{ByteOffset: tc.off, DataType: "Unsigned8", DefaultValue: "1"})
			})
			check("a Ref of two octets", tc.ok && tc.off != "" && tc.off != "3", func() error {
				return applyRef(make([]byte, size), &Ref{ByteOffset: tc.off, DataType: "Unsigned16", DefaultValue: "1"})
			})
			check("a bit Ref", tc.ok && tc.off != "", func() error {
				return applyRef(make([]byte, size), &Ref{ByteOffset: tc.off, DataType: "Bit", DefaultValue: "1"})
			})
			// The same value as the BitOffset of a bit in the first
			// octet: 0 to 7 are bits.
			check("a bit Ref with the value as BitOffset", tc.off == "" || tc.off == "0" || tc.off == "3" || tc.off == "4" || tc.off == "5", func() error {
				return applyRef(make([]byte, size), &Ref{ByteOffset: "0", BitOffset: tc.off, DataType: "Bit", DefaultValue: "1"})
			})
		})
	}
	// A Const with no data at the end of the record, and an empty record.
	for _, r := range []ParameterRecordDataItem{
		{Index: "1", Length: "4", Consts: []*Const{{ByteOffset: "4"}}},
		{Index: "1", Length: "0", Consts: []*Const{{}}},
	} {
		if _, err := newConv(&Document{}).toRecord(&r); err != nil {
			t.Errorf("%+v: %v", r.Consts[0], err)
		}
	}
}

// TestIODataLengthSum: the lengths of IO data items are added up without
// leaving the range of the sum, whatever their number.
func TestIODataLengthSum(t *testing.T) {
	// More items of the largest length than an int of 32 bits can add up.
	const n = 1<<31/65535 + 2
	items := make([]*DataItem, n)
	for i := range items {
		items[i] = &DataItem{DataType: "OctetString", Length: "65535"}
	}
	if got, want := int64(dataLength(items)), int64(n)*65535; got != want {
		t.Errorf("dataLength of %d items: %d, want %d", n, got, want)
	}
	if l, err := ioLength(items); !errors.Is(err, profinet.ErrMalformed) {
		t.Errorf("ioLength of %d items: %d, %v", n, l, err)
	}
	v := &validator{}
	semantic(&Document{ProfileBody: &ProfileBody{ApplicationProcess: &ApplicationProcess{ModuleList: &ModuleList{ModuleItems: []*ModuleItem{{ID: "M", VirtualSubmoduleList: &VirtualSubmoduleList{
		VirtualSubmoduleItems: []*VirtualSubmoduleItem{{ID: "S", IOData: &IOData{Input: &Input{DataItems: items}, Output: &Output{DataItems: items}}}},
	}}}}}}}, v)
	if len(v.issues) != 2 || v.issues[0].Rule != RuleIODataLength || !strings.HasPrefix(v.issues[0].Message, strconv.FormatInt(int64(n)*65535, 10)+" octets of input data") {
		t.Errorf("issues %+v", v.issues)
	}
	// The boundary of one submodule.
	for length, ok := range map[int]bool{0: true, 1: true, maxIOData - 1: true, maxIOData: true, maxIOData + 1: false} {
		var its []*DataItem
		if length > 0 {
			its = []*DataItem{{DataType: "OctetString", Length: strconv.Itoa(length)}}
		}
		l, err := ioLength(its)
		if ok && (err != nil || int(l) != length) || !ok && !errors.Is(err, profinet.ErrMalformed) {
			t.Errorf("IO data of %d octets: %d, %v", length, l, err)
		}
	}
	if l, err := ioLength([]*DataItem{{DataType: "OctetString", Length: "1000"}, {DataType: "OctetString", Length: "439"}, {DataType: "Unsigned8"}}); !errors.Is(err, profinet.ErrMalformed) {
		t.Errorf("IO data of 1440 octets in three items: %d, %v", l, err)
	}
}
