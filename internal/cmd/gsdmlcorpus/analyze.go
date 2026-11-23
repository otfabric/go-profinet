// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/gsdml"
)

// Schema is the name and version of the result document.
const Schema = "gsdml-corpus-result-v1"

// Stages of a file's failure, the first that fails: the file does not
// parse, or a device access point does not convert. Validation is
// reported, not a stage: ToModel does not need a valid document.
const (
	StageParse = "parse"
	StageModel = "model"
)

// FileResult is the analysis of one GSDML file: one row per device access
// point (DAPs), as docs/research/INTEROP-COMPLIANCE-PLAN.md §7 plans.
type FileResult struct {
	ID     string `json:"id,omitempty"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int    `json:"size"`
	Member string `json:"member,omitempty"`
	// SchemaVersion is the version the file declares.
	SchemaVersion string `json:"schema_version,omitempty"`
	// Stage is the first stage that failed, empty when every DAP converts.
	Stage string `json:"stage,omitempty"`
	// ParseError is the error of ParseBytes, with its class.
	ParseError   string        `json:"parse_error,omitempty"`
	ParseOutcome gsdml.Outcome `json:"parse_outcome,omitempty"`
	// Check counts CheckBytes' issues by severity and rule.
	Check *CheckCounts `json:"check,omitempty"`
	// Sizes of the document.
	Modules    int `json:"modules"`
	Submodules int `json:"submodules"`
	Records    int `json:"records"`
	// DAPs are AnalyzeModels' reports, one per device access point.
	DAPs []DAPRow `json:"daps"`
	// Crash is set when the analysis did not finish: a timeout, a panic
	// or an exit, from the parent process.
	Crash string `json:"crash,omitempty"`
	// Millis is the time of the analysis.
	Millis int64 `json:"millis"`
}

// CheckCounts are the issues of CheckBytes by severity, and the errors and
// warnings by rule ("structure" for the empty rule).
type CheckCounts struct {
	Errors       int            `json:"errors"`
	Warnings     int            `json:"warnings"`
	Unvalidated  int            `json:"unvalidated"`
	ErrorRules   map[string]int `json:"error_rules,omitempty"`
	WarningRules map[string]int `json:"warning_rules,omitempty"`
	CheckError   string         `json:"check_error,omitempty"`
}

// DAPRow is one device access point: its outcome and the references that
// do not convert.
type DAPRow struct {
	DAP      string        `json:"dap"`
	Outcome  gsdml.Outcome `json:"outcome"`
	Blocked  bool          `json:"blocked"`
	Error    string        `json:"error,omitempty"`
	DAPError string        `json:"dap_error,omitempty"`
	// References counts the UseableModules references, Plugged those
	// with configured slots.
	References int `json:"references"`
	Plugged    int `json:"plugged"`
	// RecordOctets sums the record octets of the plugged references, once
	// per configured slot.
	RecordOctets int `json:"record_octets"`
	// Failures are the references whose reference or module does not
	// convert.
	Failures []RefFailure `json:"failures,omitempty"`
}

// RefFailure is a reference that does not convert.
type RefFailure struct {
	Target string        `json:"target"`
	Path   string        `json:"path"`
	Kind   string        `json:"kind"` // "reference" or "module"
	Class  gsdml.Outcome `json:"outcome"`
	Error  string        `json:"error"`
}

// analyze is the analysis of one file's octets.
func analyze(data []byte) FileResult {
	r := FileResult{Size: len(data), DAPs: []DAPRow{}}
	doc, err := gsdml.ParseBytes(data)
	if err != nil {
		r.Stage, r.ParseError, r.ParseOutcome = StageParse, err.Error(), classify(err)
		return r
	}
	r.Modules, r.Submodules, r.Records = sizes(doc)
	r.Check, r.SchemaVersion = checkCounts(data)
	reps, err := gsdml.AnalyzeModels(doc)
	if err != nil {
		r.Stage = StageModel
		r.DAPs = append(r.DAPs, DAPRow{Outcome: classify(err), Error: err.Error()})
		return r
	}
	for _, rep := range reps {
		row := DAPRow{DAP: rep.DAP, Outcome: rep.Outcome, Blocked: rep.Blocked, Error: rep.Error, DAPError: rep.DAPError, References: len(rep.Modules)}
		for _, m := range rep.Modules {
			if len(m.ConfiguredSlots) > 0 {
				row.Plugged++
				row.RecordOctets += m.RecordOctets * len(m.ConfiguredSlots)
			}
			if m.ReferenceErr != nil {
				row.Failures = append(row.Failures, RefFailure{Target: m.Target, Path: m.Path, Kind: "reference", Class: m.ReferenceOutcome, Error: m.ReferenceError})
			}
			if m.ModuleErr != nil {
				row.Failures = append(row.Failures, RefFailure{Target: m.Target, Path: m.ModulePath, Kind: "module", Class: m.ModuleOutcome, Error: m.ModuleError})
			}
		}
		if rep.Outcome != gsdml.OutcomeConverted && r.Stage == "" {
			r.Stage = StageModel
		}
		r.DAPs = append(r.DAPs, row)
	}
	return r
}

// classify is the outcome class of an error that is not a DAP's.
func classify(err error) gsdml.Outcome {
	switch {
	case errors.Is(err, profinet.ErrMalformed):
		return gsdml.OutcomeMalformed
	case errors.Is(err, profinet.ErrResourceExhausted):
		return gsdml.OutcomeResourceExhausted
	case errors.Is(err, profinet.ErrUnsupported):
		return gsdml.OutcomeUnsupported
	default:
		return gsdml.OutcomeMalformed
	}
}

// checkCounts counts the issues of CheckBytes, and returns the version
// the document declares.
func checkCounts(data []byte) (*CheckCounts, string) {
	rep, err := gsdml.CheckBytes(data)
	c := &CheckCounts{}
	if err != nil {
		c.CheckError = err.Error()
		return c, ""
	}
	for _, i := range rep.Issues {
		rule := i.Rule
		if rule == "" {
			rule = "structure"
		}
		switch i.Severity {
		case gsdml.SeverityError:
			c.Errors++
			if c.ErrorRules == nil {
				c.ErrorRules = map[string]int{}
			}
			c.ErrorRules[rule]++
		case gsdml.SeverityWarning:
			c.Warnings++
			if c.WarningRules == nil {
				c.WarningRules = map[string]int{}
			}
			c.WarningRules[rule]++
		default:
			c.Unvalidated++
		}
	}
	return c, rep.SchemaVersion
}

func sizes(doc *gsdml.Document) (modules, submodules, records int) {
	if doc.ProfileBody == nil || doc.ProfileBody.ApplicationProcess == nil {
		return 0, 0, 0
	}
	ap := doc.ProfileBody.ApplicationProcess
	count := func(l *gsdml.VirtualSubmoduleList) {
		if l == nil {
			return
		}
		submodules += len(l.VirtualSubmoduleItems)
		for _, s := range l.VirtualSubmoduleItems {
			if s.RecordDataList != nil {
				records += len(s.RecordDataList.ParameterRecordDataItems)
			}
		}
	}
	if ap.DeviceAccessPointList != nil {
		for _, d := range ap.DeviceAccessPointList.DeviceAccessPointItems {
			count(d.VirtualSubmoduleList)
		}
	}
	if ap.ModuleList != nil {
		modules = len(ap.ModuleList.ModuleItems)
		for _, m := range ap.ModuleList.ModuleItems {
			count(m.VirtualSubmoduleList)
		}
	}
	return modules, submodules, records
}

var (
	quoted = regexp.MustCompile(`"[^"]*"|'[^']*'|\[[^\]]*\]`)
	number = regexp.MustCompile(`\b(0[xX][0-9a-fA-F]+|\d+)\b`)
)

// Signature is an error text without its values: quoted strings, path
// predicates and numbers replaced, so that the same failure in different
// files counts once per kind. An ID an error names without quotes stays.
func Signature(msg string) string {
	s := quoted.ReplaceAllStringFunc(msg, func(q string) string {
		switch q[0] {
		case '[':
			return "[…]"
		case '\'':
			return "'…'"
		}
		return `"…"`
	})
	return number.ReplaceAllString(s, "N")
}

// Summary counts the results.
type Summary struct {
	Files       int            `json:"files"`
	DAPs        int            `json:"daps"`
	FileStages  map[string]int `json:"file_stages"`  // "ok", "parse", "model", "crash"
	DAPOutcomes map[string]int `json:"dap_outcomes"` // by outcome, and "blocked"
	// Valid counts the files CheckBytes reports no error for.
	Valid int `json:"valid"`
	// ByVersion counts files and failing files by declared schema version.
	ByVersion map[string][2]int `json:"by_version"`
	// Signatures rank the failures: one per error signature, with the
	// number of files and DAPs it occurs in.
	Signatures []SignatureCount `json:"signatures"`
	// ErrorRules counts the files with an error of each Check rule.
	ErrorRules map[string]int `json:"error_rules"`
}

// SignatureCount is one kind of failure.
type SignatureCount struct {
	Signature string        `json:"signature"`
	Kind      string        `json:"kind"` // "parse", "dap", "reference", "module", "crash"
	Outcome   gsdml.Outcome `json:"outcome,omitempty"`
	Files     int           `json:"files"`
	DAPs      int           `json:"daps"`
}

// Summarize counts results.
func Summarize(results []FileResult) Summary {
	s := Summary{FileStages: map[string]int{}, DAPOutcomes: map[string]int{}, ByVersion: map[string][2]int{}, ErrorRules: map[string]int{}}
	type key struct{ sig, kind, outcome string }
	sigs := map[key]*SignatureCount{}
	add := func(k key, file string, files map[key]map[string]bool) {
		c := sigs[k]
		if c == nil {
			c = &SignatureCount{Signature: k.sig, Kind: k.kind, Outcome: gsdml.Outcome(k.outcome)}
			sigs[k] = c
		}
		c.DAPs++
		if files[k] == nil {
			files[k] = map[string]bool{}
		}
		if !files[k][file] {
			files[k][file] = true
			c.Files++
		}
	}
	files := map[key]map[string]bool{}
	for _, r := range results {
		s.Files++
		stage := r.Stage
		switch {
		case r.Crash != "":
			stage = "crash"
			add(key{Signature(r.Crash), "crash", ""}, r.Path, files)
		case stage == "":
			stage = "ok"
		}
		s.FileStages[stage]++
		v := r.SchemaVersion
		if v == "" {
			v = "none"
		}
		bv := s.ByVersion[v]
		bv[0]++
		if stage != "ok" {
			bv[1]++
		}
		s.ByVersion[v] = bv
		if r.ParseError != "" {
			add(key{Signature(r.ParseError), "parse", string(r.ParseOutcome)}, r.Path, files)
		}
		if r.Check != nil {
			if r.Check.Errors == 0 && r.Check.CheckError == "" {
				s.Valid++
			}
			for rule := range r.Check.ErrorRules {
				s.ErrorRules[rule]++
			}
		}
		for _, d := range r.DAPs {
			s.DAPs++
			s.DAPOutcomes[string(d.Outcome)]++
			if d.Blocked {
				s.DAPOutcomes["blocked"]++
			}
			if d.Outcome == gsdml.OutcomeConverted {
				continue
			}
			switch {
			case d.DAPError != "":
				add(key{Signature(d.DAPError), "dap", string(d.Outcome)}, r.Path, files)
			case len(d.Failures) == 0:
				add(key{Signature(d.Error), "dap", string(d.Outcome)}, r.Path, files)
			}
			for _, f := range d.Failures {
				add(key{Signature(f.Error), f.Kind, string(f.Class)}, r.Path, files)
			}
		}
	}
	for _, c := range sigs {
		s.Signatures = append(s.Signatures, *c)
	}
	sort.Slice(s.Signatures, func(i, j int) bool {
		a, b := s.Signatures[i], s.Signatures[j]
		if a.Files != b.Files {
			return a.Files > b.Files
		}
		if a.DAPs != b.DAPs {
			return a.DAPs > b.DAPs
		}
		return a.Signature+a.Kind < b.Signature+b.Kind
	})
	return s
}

// Markdown is the summary as a Markdown matrix. It names no file; the
// error signatures can still hold an unquoted ID of a vendor's file, so
// the matrix stays local like the results.
func Markdown(s Summary, source string) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	w("# GSDML corpus run\n\n%s\n\n", source)
	w("%d files, %d device access points; %d files without a CheckBytes error.\n\n", s.Files, s.DAPs, s.Valid)
	w("| Files | Count |\n|---|---|\n")
	for _, k := range sortedKeys(s.FileStages) {
		w("| %s | %d |\n", k, s.FileStages[k])
	}
	w("\n| Device access points | Count |\n|---|---|\n")
	for _, k := range sortedKeys(s.DAPOutcomes) {
		w("| %s | %d |\n", k, s.DAPOutcomes[k])
	}
	w("\n| Schema version | Files | Not converted |\n|---|---|---|\n")
	versions := make([]string, 0, len(s.ByVersion))
	for v := range s.ByVersion {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	for _, v := range versions {
		w("| %s | %d | %d |\n", v, s.ByVersion[v][0], s.ByVersion[v][1])
	}
	if len(s.ErrorRules) > 0 {
		w("\n| Check error rule | Files |\n|---|---|\n")
		for _, k := range sortedKeys(s.ErrorRules) {
			w("| %s | %d |\n", k, s.ErrorRules[k])
		}
	}
	if len(s.Signatures) > 0 {
		w("\n## Failures, ranked\n\n| Files | DAPs | Kind | Outcome | Signature |\n|---|---|---|---|---|\n")
		for _, c := range s.Signatures {
			w("| %d | %d | %s | %s | %s |\n", c.Files, c.DAPs, c.Kind, c.Outcome, strings.ReplaceAll(c.Signature, "|", `\|`))
		}
	}
	return b.String()
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
