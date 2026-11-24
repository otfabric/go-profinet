// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/gsdml"
	"github.com/otfabric/go-profinet/model"
)

// Schema is the name and version of the result document.
const Schema = "gsdml-corpus-result-v1"

// Stages of a file's failure, the first that fails: the file does not
// parse, or a device access point does not convert. Validation is
// reported, not a stage: ToModel does not need a valid document.
const (
	StageParse = "parse"
	StageModel = "model"
	// StageIntegrity: the file is not the one the index names (its
	// SHA-256 differs); it is not analysed.
	StageIntegrity = "integrity"
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
	// APIs counts the virtual submodules by their API other than 0
	// ("0x4E01"; "invalid" for one that is no Unsigned32).
	APIs map[string]int `json:"apis,omitempty"`
	// RecordIndexes counts the parameter records by index range: the user
	// range "0x0000-0x7FFF", above it by 4096 ("0xB000-0xBFFF"), "invalid"
	// for an index that is no Unsigned16. The ranges are numeric, not a
	// claim about what an index above 0x7FFF means.
	RecordIndexes map[string]int `json:"record_indexes,omitempty"`
	// DAPs are AnalyzeModels' reports, one per device access point.
	DAPs []DAPRow `json:"daps"`
	// Crash is set when the analysis did not finish: a timeout, a panic
	// or an exit, from the parent process.
	Crash string `json:"crash,omitempty"`
	// Integrity says why the file is not the one the index names.
	Integrity string `json:"integrity,omitempty"`
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
	// APIs are the APIs other than 0 of the submodules of the DAP and of
	// the modules it can use, sorted.
	APIs []string `json:"apis,omitempty"`
	// PluggedAPIs are those of APIs that are in the DAP's plugged
	// configuration: the DAP's submodules and the modules its references
	// put in slots, not the modules only in the catalog.
	PluggedAPIs []string `json:"plugged_apis,omitempty"`
	// ModelAPIs are the APIs other than 0 of the converted model's
	// plugged submodules (model.Device.APIs); a converted DAP whose
	// ModelAPIs differ from its PluggedAPIs lost or invented an API.
	ModelAPIs []string `json:"model_apis,omitempty"`
	// HighRecords counts the records of the DAP and of the modules it can
	// use whose index is a number above 0x7FFF; InvalidRecords those whose
	// index is no Unsigned16.
	HighRecords    int `json:"high_records,omitempty"`
	InvalidRecords int `json:"invalid_records,omitempty"`
	// Addresses compares the addresses of the DAP's plugged configuration
	// with its model's; nil for a DAP ToModel does not convert.
	Addresses *AddressCheck `json:"addresses,omitempty"`
	// Failures are the references whose reference or module does not
	// convert, less the omitted modules (OmittedModules), which do not
	// refuse the DAP.
	Failures []RefFailure `json:"failures,omitempty"`
	// ConfigurableSubmodules counts the configurable submodule references
	// (UseableSubmodules) of the DAP and of the modules it references,
	// once per module; ToModel converts none. Of the required ones (fixed
	// or used): RequiredSubmodules are those of the selected
	// configuration, the DAP's and those of the modules a reference
	// plugs, for which ToModel refuses the DAP; CatalogRequiredSubmodules
	// those of modules only in the catalog, which ToModel leaves out of it
	// and which do not refuse the DAP.
	ConfigurableSubmodules    int `json:"configurable_submodules,omitempty"`
	RequiredSubmodules        int `json:"required_submodules,omitempty"`
	CatalogRequiredSubmodules int `json:"catalog_required_submodules,omitempty"`
	// OmittedModules counts the references whose module ToModel leaves
	// out of the catalog (gsdml.ModuleReport.Omitted).
	OmittedModules int `json:"omitted_modules,omitempty"`
	// Config is the default configuration of the DAP with
	// gsdml.NewConfiguration and Resolve.
	Config ConfigResult `json:"config"`
}

// ConfigResult is the default configuration of a DAP: NewConfiguration
// and Resolve without choices.
type ConfigResult struct {
	// Outcome is the class of the error (OutcomeInvalidConfig for one
	// matching profinet.ErrInvalidConfig, as for a subslot several
	// submodules are used in by default), converted when it resolves;
	// Error its text.
	Outcome gsdml.Outcome `json:"outcome"`
	Error   string        `json:"error,omitempty"`
	// Submodules counts the configurable submodules the resolved model
	// plugs (the DAP's and its plugged modules'), Ports those of them that
	// are ports; CatalogSubmodules those of its catalog modules.
	Submodules        int `json:"submodules,omitempty"`
	Ports             int `json:"ports,omitempty"`
	CatalogSubmodules int `json:"catalog_submodules,omitempty"`
	// CatalogModules is the size of the resolved catalog, ModelCatalog
	// that of ToModel's when it converts.
	CatalogModules int `json:"catalog_modules"`
	ModelCatalog   int `json:"model_catalog,omitempty"`
	// SameAsModel reports, for a DAP ToModel converts, that the resolved
	// identity, capabilities, DAP and slots are ToModel's model; Equal
	// that the catalog is too. False for a DAP ToModel refuses.
	SameAsModel bool `json:"same_as_model"`
	Equal       bool `json:"equal"`
	// ModelInvalid reports a DAP ToModel converts whose model
	// model.Device.Validate refuses, which Resolve refuses too (ToModel
	// does not validate its model).
	ModelInvalid bool `json:"model_invalid,omitempty"`
	// Plan is the outcome of gsdml.PlanParameters with Defaults on the
	// resolved model (empty when it does not resolve), PlanError its
	// error.
	Plan      gsdml.Outcome `json:"plan,omitempty"`
	PlanError string        `json:"plan_error,omitempty"`
}

// OutcomeInvalidConfig classifies a default configuration that needs a
// choice, or that model.Validate refuses (profinet.ErrInvalidConfig).
const OutcomeInvalidConfig gsdml.Outcome = "invalid_config"

// configResult resolves the default configuration of a DAP and compares
// it with ToModel's model (nil when ToModel refuses the DAP).
func configResult(doc *gsdml.Document, dap string, toModel *model.Device) ConfigResult {
	var r ConfigResult
	r.ModelInvalid = toModel != nil && toModel.Validate() != nil
	cfg, err := gsdml.NewConfiguration(doc, dap)
	var m *model.Device
	if err == nil {
		m, err = cfg.Resolve()
	}
	if err != nil {
		r.Outcome, r.Error = classify(err), err.Error()
		if errors.Is(err, profinet.ErrInvalidConfig) {
			r.Outcome = OutcomeInvalidConfig
		}
		return r
	}
	r.Outcome = gsdml.OutcomeConverted
	r.CatalogModules = len(m.Catalog)
	r.Plan = gsdml.OutcomeConverted
	if _, err := gsdml.PlanParameters(doc, m, gsdml.PlanOptions{DAP: dap, Defaults: true}); err != nil {
		r.Plan, r.PlanError = classify(err), err.Error()
		if errors.Is(err, profinet.ErrInvalidConfig) {
			r.Plan = OutcomeInvalidConfig
		}
	}
	// The configurable submodules are those after a module's virtual
	// ones (ModuleOption.Module) and the DAP's placed by default. A
	// catalog module is matched by ident number, and counted only when
	// one module of the DAP has it.
	byID, byIdent, idents := map[string]int{}, map[uint32]int{}, map[uint32]int{}
	for _, mo := range cfg.Modules() {
		byID[mo.ID] = len(mo.Module.Submodules)
		byIdent[mo.Module.IdentNumber] = len(mo.Module.Submodules)
		idents[mo.Module.IdentNumber]++
	}
	for _, o := range cfg.DAP() {
		r.Submodules += len(o.Defaults)
		if o.Port {
			r.Ports += len(o.Defaults)
		}
	}
	for _, s := range m.Slots {
		id, _ := cfg.Module(s.Number)
		r.Submodules += len(s.Module.Submodules) - byID[id]
	}
	for _, mod := range m.Catalog {
		if idents[mod.IdentNumber] == 1 {
			r.CatalogSubmodules += len(mod.Submodules) - byIdent[mod.IdentNumber]
		}
	}
	if toModel != nil {
		r.ModelCatalog = len(toModel.Catalog)
		r.SameAsModel = reflect.DeepEqual(m.Identity, toModel.Identity) && reflect.DeepEqual(m.Capabilities, toModel.Capabilities) &&
			reflect.DeepEqual(m.DAP, toModel.DAP) && reflect.DeepEqual(m.Slots, toModel.Slots)
		r.Equal = r.SameAsModel && reflect.DeepEqual(m.Catalog, toModel.Catalog)
	}
	return r
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
	r.APIs, r.RecordIndexes = map[string]int{}, map[string]int{}
	forEachSubmodule(doc, func(sm *gsdml.VirtualSubmoduleItem) {
		if a := apiOf(sm); a != "" {
			r.APIs[a]++
		}
		for _, rec := range recordsOf(sm) {
			r.RecordIndexes[indexRange(rec.Index)]++
		}
	})
	if len(r.APIs) == 0 {
		r.APIs = nil
	}
	if len(r.RecordIndexes) == 0 {
		r.RecordIndexes = nil
	}
	r.Check, r.SchemaVersion = checkCounts(data)
	ix := indexOf(doc)
	reps, err := gsdml.AnalyzeModels(doc)
	if err != nil {
		r.Stage = StageModel
		r.DAPs = append(r.DAPs, DAPRow{Outcome: classify(err), Error: err.Error()})
		return r
	}
	for _, rep := range reps {
		row := DAPRow{DAP: rep.DAP, Outcome: rep.Outcome, Blocked: rep.Blocked, Error: rep.Error, DAPError: rep.DAPError, References: len(rep.Modules)}
		// The DAP's own submodules are plugged; a module is plugged when
		// its reference has configured slots, else only in the catalog.
		apis, plugged := map[string]bool{}, map[string]bool{}
		visit := func(l *gsdml.VirtualSubmoduleList, isPlugged bool) {
			if l == nil {
				return
			}
			for _, sm := range l.VirtualSubmoduleItems {
				if a := apiOf(sm); a != "" {
					apis[a] = true
					if isPlugged {
						plugged[a] = true
					}
				}
				for _, rec := range recordsOf(sm) {
					switch indexRange(rec.Index) {
					case userRange:
					case invalidIndex:
						row.InvalidRecords++
					default:
						row.HighRecords++
					}
				}
			}
		}
		if d := ix.daps[rep.DAP]; d != nil {
			visit(d.VirtualSubmoduleList, true)
		}
		seen := map[*gsdml.ModuleItem]bool{}
		for _, m := range rep.Modules {
			mi := ix.modules[strings.Join(strings.Fields(m.Target), " ")]
			if mi == nil {
				continue
			}
			p := len(m.ConfiguredSlots) > 0
			// A module several references name counts once, plugged when
			// one of them plugs it.
			if seen[mi] && !p {
				continue
			}
			if seen[mi] {
				for a := range apisOf(mi) {
					plugged[a] = true
				}
				continue
			}
			seen[mi] = true
			visit(mi.VirtualSubmoduleList, p)
		}
		row.APIs, row.PluggedAPIs = sortedSet(apis), sortedSet(plugged)
		if rep.Model != nil {
			m := map[string]bool{}
			for _, a := range rep.Model.APIs() {
				if a != 0 {
					m[fmt.Sprintf("0x%X", a)] = true
				}
			}
			row.ModelAPIs = sortedSet(m)
		}
		for _, m := range rep.Modules {
			if len(m.ConfiguredSlots) > 0 {
				row.Plugged++
				row.RecordOctets += m.RecordOctets * len(m.ConfiguredSlots)
			}
			if m.ReferenceErr != nil {
				row.Failures = append(row.Failures, RefFailure{Target: m.Target, Path: m.Path, Kind: "reference", Class: m.ReferenceOutcome, Error: m.ReferenceError})
			}
			if m.ModuleErr != nil && !m.Omitted {
				row.Failures = append(row.Failures, RefFailure{Target: m.Target, Path: m.ModulePath, Kind: "module", Class: m.ModuleOutcome, Error: m.ModuleError})
			}
		}
		count := func(refs []gsdml.SubmoduleRef, inConfiguration bool) {
			for _, sr := range refs {
				row.ConfigurableSubmodules++
				switch {
				case !sr.Required:
				case inConfiguration:
					row.RequiredSubmodules++
				default:
					row.CatalogRequiredSubmodules++
				}
			}
		}
		count(rep.ConfigurableSubmodules, true)
		// A module is in the configuration when one of its references
		// plugs it, else only in the catalog; it counts once.
		inConfiguration, order := map[string]bool{}, []string{}
		refsOf := map[string][]gsdml.SubmoduleRef{}
		for _, m := range rep.Modules {
			if m.Omitted {
				row.OmittedModules++
			}
			if m.ModulePath == "" {
				continue
			}
			if _, ok := refsOf[m.ModulePath]; !ok {
				order = append(order, m.ModulePath)
				refsOf[m.ModulePath] = m.ConfigurableSubmodules
			}
			inConfiguration[m.ModulePath] = inConfiguration[m.ModulePath] || len(m.ConfiguredSlots) > 0
		}
		for _, p := range order {
			count(refsOf[p], inConfiguration[p])
		}
		row.Addresses = checkAddresses(ix, rep)
		row.Config = configResult(doc, rep.DAP, rep.Model)
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

// userRange is the record index range a device defines; invalidIndex
// the range of an index that is no Unsigned16.
const (
	userRange    = "0x0000-0x7FFF"
	invalidIndex = "invalid"
)

// docIndex finds the DAPs and modules of a document as ToModel does: the
// first DAP of an ID, the last module of an ID (IDs compared after
// collapsing white space).
type docIndex struct {
	daps    map[string]*gsdml.DeviceAccessPointItem
	modules map[string]*gsdml.ModuleItem
}

func indexOf(doc *gsdml.Document) docIndex {
	ix := docIndex{daps: map[string]*gsdml.DeviceAccessPointItem{}, modules: map[string]*gsdml.ModuleItem{}}
	if doc.ProfileBody == nil || doc.ProfileBody.ApplicationProcess == nil {
		return ix
	}
	ap := doc.ProfileBody.ApplicationProcess
	if ap.DeviceAccessPointList != nil {
		for _, d := range ap.DeviceAccessPointList.DeviceAccessPointItems {
			if ix.daps[d.ID] == nil {
				ix.daps[d.ID] = d
			}
		}
	}
	if ap.ModuleList != nil {
		for _, m := range ap.ModuleList.ModuleItems {
			ix.modules[strings.Join(strings.Fields(m.ID), " ")] = m
		}
	}
	return ix
}

// apisOf is the set of APIs other than 0 of a module's submodules.
func apisOf(m *gsdml.ModuleItem) map[string]bool {
	out := map[string]bool{}
	if m.VirtualSubmoduleList != nil {
		for _, sm := range m.VirtualSubmoduleList.VirtualSubmoduleItems {
			if a := apiOf(sm); a != "" {
				out[a] = true
			}
		}
	}
	return out
}

func sortedSet(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// indexRange is the range of a record index, by 4096 above 0x7FFF.
func indexRange(index string) string {
	idx, err := gsdml.ParseUint16(index)
	switch {
	case err != nil:
		return invalidIndex
	case idx <= 0x7FFF:
		return userRange
	}
	return fmt.Sprintf("0x%X000-0x%XFFF", idx>>12, idx>>12)
}

// apiOf is a submodule's API other than 0 in hexadecimal, "invalid" for
// one that is no Unsigned32, empty for API 0 or none.
func apiOf(sm *gsdml.VirtualSubmoduleItem) string {
	if sm.API == "" {
		return ""
	}
	a, err := gsdml.ParseUint32(sm.API)
	switch {
	case err != nil:
		return "invalid"
	case a == 0:
		return ""
	}
	return fmt.Sprintf("0x%X", a)
}

func recordsOf(sm *gsdml.VirtualSubmoduleItem) []*gsdml.ParameterRecordDataItem {
	if sm.RecordDataList == nil {
		return nil
	}
	return sm.RecordDataList.ParameterRecordDataItems
}

// forEachSubmodule calls fn for the virtual submodules of the document's
// DAPs and modules.
func forEachSubmodule(doc *gsdml.Document, fn func(*gsdml.VirtualSubmoduleItem)) {
	if doc.ProfileBody == nil || doc.ProfileBody.ApplicationProcess == nil {
		return
	}
	ap := doc.ProfileBody.ApplicationProcess
	each := func(l *gsdml.VirtualSubmoduleList) {
		if l != nil {
			for _, sm := range l.VirtualSubmoduleItems {
				fn(sm)
			}
		}
	}
	if ap.DeviceAccessPointList != nil {
		for _, d := range ap.DeviceAccessPointList.DeviceAccessPointItems {
			each(d.VirtualSubmoduleList)
		}
	}
	if ap.ModuleList != nil {
		for _, m := range ap.ModuleList.ModuleItems {
			each(m.VirtualSubmoduleList)
		}
	}
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
	Files int `json:"files"`
	DAPs  int `json:"daps"`
	// Distinct counts the files by content (SHA-256).
	Distinct    int            `json:"distinct"`
	FileStages  map[string]int `json:"file_stages"`  // "ok", "parse", "model", "integrity", "crash"
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
	// Fidelity: what a converted model leaves out.
	Fidelity Fidelity `json:"fidelity"`
}

// Fidelity counts content that tests a ToModel conversion: submodules of
// an API other than 0, which the model must keep, and records above 0x7FFF
// (ToModel keeps those of the profile range 0xB000-0xBFFF and refuses the
// others).
type Fidelity struct {
	// FilesAPI and DAPsAPI count the files and DAPs with a submodule of
	// an API other than 0; ConvertedAPI the DAPs among them ToModel
	// converts.
	FilesAPI     int `json:"files_api"`
	DAPsAPI      int `json:"daps_api"`
	ConvertedAPI int `json:"converted_api"`
	// DAPsPluggedAPI and ConvertedPluggedAPI count the same for an API in
	// the DAP's plugged configuration, not only in a catalog module.
	DAPsPluggedAPI      int `json:"daps_plugged_api"`
	ConvertedPluggedAPI int `json:"converted_plugged_api"`
	// APIMismatch counts the converted DAPs whose model's APIs are not
	// the APIs of their plugged configuration (ModelAPIs, PluggedAPIs);
	// zero when the model keeps every API.
	APIMismatch int `json:"api_mismatch"`
	// APIs counts the files by API.
	APIs map[string]int `json:"apis"`
	// FilesHighRecords and DAPsHighRecords count the files and DAPs with
	// a record above 0x7FFF; ConvertedHighRecords the DAPs among them
	// ToModel converts.
	FilesHighRecords     int `json:"files_high_records"`
	DAPsHighRecords      int `json:"daps_high_records"`
	ConvertedHighRecords int `json:"converted_high_records"`
	// RecordIndexes counts the records by index range.
	RecordIndexes map[string]int `json:"record_indexes"`
	// DAPsAddressChecked counts the converted DAPs whose addresses were
	// compared with their model's (AddressCheck), Addresses and
	// AddressRecords the submodule addresses and record indexes compared;
	// DAPsAddressFailed the DAPs whose model loses, adds or changes one,
	// or whose addresses could not be read. The run fails when it is not
	// zero.
	DAPsAddressChecked int `json:"daps_address_checked"`
	Addresses          int `json:"addresses"`
	AddressRecords     int `json:"address_records"`
	DAPsAddressFailed  int `json:"daps_address_failed"`
	// AddressLost … AddressCollisions sum the AddressCheck counts.
	AddressLost         int `json:"address_lost"`
	AddressExtra        int `json:"address_extra"`
	AddressChanged      int `json:"address_changed"`
	AddressRecordsLost  int `json:"address_records_lost"`
	AddressRecordsExtra int `json:"address_records_extra"`
	AddressUnread       int `json:"address_unread"`
	DAPsAddressCollide  int `json:"daps_address_collisions"`
	AddressCollisions   int `json:"address_collisions"`
	// FilesConfigurable and DAPsConfigurable count the files and DAPs
	// with a configurable submodule (UseableSubmodules) in the DAP or a
	// module it references; ConvertedConfigurable those ToModel converts
	// (without their configurable submodules). DAPsRequiredSubmodules
	// counts the DAPs whose selected configuration needs one (the DAP's or
	// a plugged module's: ToModel refuses them), DAPsCatalogRequired those
	// with a module only in the catalog that needs one.
	FilesConfigurable      int `json:"files_configurable"`
	DAPsConfigurable       int `json:"daps_configurable"`
	DAPsRequiredSubmodules int `json:"daps_required_submodules"`
	DAPsCatalogRequired    int `json:"daps_catalog_required_submodules"`
	ConvertedConfigurable  int `json:"converted_configurable"`
	// DAPsOmittedModules counts the converted DAPs whose catalog lacks a
	// module the file lets them use (it needs a configurable submodule),
	// OmittedModules those references.
	DAPsOmittedModules int `json:"daps_omitted_modules"`
	OmittedModules     int `json:"omitted_modules"`
	// Configuration counts the default configurations
	// (gsdml.NewConfiguration, Resolve) by outcome. ConfigRefusedResolved
	// counts the DAPs ToModel refuses whose default configuration
	// resolves, ConfigRequiredResolved those of them ToModel refuses for a
	// configurable submodule (DAPsRequiredSubmodules). ConfigSubmodules
	// and ConfigPorts count the configurable submodules (and ports) the
	// resolved defaults plug, ConfigCatalogSubmodules those of their
	// catalog modules.
	Configuration map[string]int `json:"configuration"`
	// ConfigPlans counts the resolved defaults by the outcome of
	// PlanParameters with Defaults (planned: converted), ConfigPlansRefused
	// the same for the DAPs ToModel refuses.
	ConfigPlans             map[string]int `json:"config_plans"`
	ConfigPlansRefused      map[string]int `json:"config_plans_refused"`
	ConfigRefusedResolved   int            `json:"config_refused_resolved"`
	ConfigRequiredResolved  int            `json:"config_required_resolved"`
	ConfigSubmodules        int            `json:"config_submodules"`
	ConfigPorts             int            `json:"config_ports"`
	ConfigCatalogSubmodules int            `json:"config_catalog_submodules"`
	// Of the DAPs ToModel converts: ConfigSame those whose resolved
	// default is ToModel's model but for the catalog, ConfigDiffer those
	// whose is not or that do not resolve (zero when the default
	// configuration extends ToModel), ConfigGainedCatalog those whose
	// resolved catalog has more modules (GainedModules more in all), and
	// ConfigGainedSubmodules those whose resolved model has configurable
	// submodules, plugged or in the catalog.
	// ConfigModelInvalid counts those whose ToModel model
	// model.Device.Validate refuses, and which do not resolve for it.
	ConfigModelInvalid     int `json:"config_model_invalid"`
	ConfigSame             int `json:"config_same"`
	ConfigDiffer           int `json:"config_differ"`
	ConfigGainedCatalog    int `json:"config_gained_catalog"`
	GainedModules          int `json:"gained_modules"`
	ConfigGainedSubmodules int `json:"config_gained_submodules"`
}

// countConfig adds a DAP's default configuration to the fidelity counts.
func countConfig(f *Fidelity, d DAPRow) {
	c := d.Config
	if c.Outcome == "" {
		return // no DAP: the document has none
	}
	f.Configuration[string(c.Outcome)]++
	resolved := c.Outcome == gsdml.OutcomeConverted
	if resolved {
		f.ConfigPlans[string(c.Plan)]++
		if d.Outcome != gsdml.OutcomeConverted {
			f.ConfigPlansRefused[string(c.Plan)]++
		}
		f.ConfigSubmodules += c.Submodules
		f.ConfigPorts += c.Ports
		f.ConfigCatalogSubmodules += c.CatalogSubmodules
	}
	if d.Outcome != gsdml.OutcomeConverted {
		if resolved {
			f.ConfigRefusedResolved++
			if d.RequiredSubmodules > 0 {
				f.ConfigRequiredResolved++
			}
		}
		return
	}
	if !c.SameAsModel {
		if c.ModelInvalid && !resolved {
			f.ConfigModelInvalid++
		} else {
			f.ConfigDiffer++
		}
		return
	}
	f.ConfigSame++
	if c.CatalogModules > c.ModelCatalog {
		f.ConfigGainedCatalog++
		f.GainedModules += c.CatalogModules - c.ModelCatalog
	}
	if c.Submodules+c.CatalogSubmodules > 0 {
		f.ConfigGainedSubmodules++
	}
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
	s := Summary{FileStages: map[string]int{}, DAPOutcomes: map[string]int{}, ByVersion: map[string][2]int{}, ErrorRules: map[string]int{},
		Fidelity: Fidelity{APIs: map[string]int{}, RecordIndexes: map[string]int{}, Configuration: map[string]int{},
			ConfigPlans: map[string]int{}, ConfigPlansRefused: map[string]int{}}}
	contents := map[string]bool{}
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
		if r.SHA256 != "" {
			contents[r.SHA256] = true
		}
		if len(r.APIs) > 0 {
			s.Fidelity.FilesAPI++
		}
		for a := range r.APIs {
			s.Fidelity.APIs[a]++
		}
		high := false
		for k, n := range r.RecordIndexes {
			s.Fidelity.RecordIndexes[k] += n
			if k != userRange && k != invalidIndex {
				high = true
			}
		}
		if high {
			s.Fidelity.FilesHighRecords++
		}
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
		configurable := false
		for _, d := range r.DAPs {
			if d.ConfigurableSubmodules > 0 {
				configurable = true
				s.Fidelity.DAPsConfigurable++
				if d.RequiredSubmodules > 0 {
					s.Fidelity.DAPsRequiredSubmodules++
				}
				if d.CatalogRequiredSubmodules > 0 {
					s.Fidelity.DAPsCatalogRequired++
				}
				if d.Outcome == gsdml.OutcomeConverted {
					s.Fidelity.ConvertedConfigurable++
				}
			}
			if d.OmittedModules > 0 && d.Outcome == gsdml.OutcomeConverted {
				s.Fidelity.DAPsOmittedModules++
				s.Fidelity.OmittedModules += d.OmittedModules
			}
		}
		if configurable {
			s.Fidelity.FilesConfigurable++
		}
		for _, d := range r.DAPs {
			s.DAPs++
			s.DAPOutcomes[string(d.Outcome)]++
			countConfig(&s.Fidelity, d)
			if d.Blocked {
				s.DAPOutcomes["blocked"]++
			}
			if len(d.APIs) > 0 {
				s.Fidelity.DAPsAPI++
				if d.Outcome == gsdml.OutcomeConverted {
					s.Fidelity.ConvertedAPI++
				}
			}
			if len(d.PluggedAPIs) > 0 {
				s.Fidelity.DAPsPluggedAPI++
				if d.Outcome == gsdml.OutcomeConverted {
					s.Fidelity.ConvertedPluggedAPI++
				}
			}
			if d.Outcome == gsdml.OutcomeConverted && !slices.Equal(d.ModelAPIs, d.PluggedAPIs) {
				s.Fidelity.APIMismatch++
			}
			if a := d.Addresses; a != nil {
				f := &s.Fidelity
				f.DAPsAddressChecked++
				f.Addresses += a.Addresses
				f.AddressRecords += a.Records
				f.AddressLost += a.Lost
				f.AddressExtra += a.Extra
				f.AddressChanged += a.Changed
				f.AddressRecordsLost += a.RecordsLost
				f.AddressRecordsExtra += a.RecordsExtra
				f.AddressCollisions += a.Collisions
				if a.Unread {
					f.AddressUnread++
				}
				if a.Failed() {
					f.DAPsAddressFailed++
				}
				if a.Collisions > 0 {
					f.DAPsAddressCollide++
				}
			}
			if d.HighRecords > 0 {
				s.Fidelity.DAPsHighRecords++
				if d.Outcome == gsdml.OutcomeConverted {
					s.Fidelity.ConvertedHighRecords++
				}
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
	s.Distinct = len(contents)
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
	w("%d files (%d distinct by content), %d device access points; %d files without a CheckBytes error.\n\n", s.Files, s.Distinct, s.DAPs, s.Valid)
	w("The outcomes are **ToModel conversion** outcomes: a converted DAP is one `gsdml.ToModel` turns into a model, not a device shown to be supported or interoperable. The fidelity table lists what a converted model leaves out.\n\n")
	w("| Files (ToModel conversion) | Count |\n|---|---|\n")
	for _, k := range sortedKeys(s.FileStages) {
		w("| %s | %d |\n", k, s.FileStages[k])
	}
	w("\n| Device access points (ToModel conversion) | Count |\n|---|---|\n")
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
	f := s.Fidelity
	w("\n## Fidelity\n\n| Content | Files | DAPs | of them converted |\n|---|---|---|---|\n")
	w("| a submodule of an API other than 0, in the DAP or a module it can use | %d | %d | %d |\n", f.FilesAPI, f.DAPsAPI, f.ConvertedAPI)
	w("| … of them in the plugged configuration (the DAP's submodules, modules in slots) | | %d | %d |\n", f.DAPsPluggedAPI, f.ConvertedPluggedAPI)
	w("| a record whose index is a number above 0x7FFF (ToModel keeps 0xB000-0xBFFF, refuses the others) | %d | %d | %d |\n", f.FilesHighRecords, f.DAPsHighRecords, f.ConvertedHighRecords)
	w("| a configurable submodule (UseableSubmodules) in the DAP or a module it references; ToModel converts none | %d | %d | %d |\n", f.FilesConfigurable, f.DAPsConfigurable, f.ConvertedConfigurable)
	w("\nConverted DAPs whose model does not keep the APIs of their plugged configuration: %d.\n", f.APIMismatch)
	w("\nDAPs whose selected configuration (the DAP, its plugged modules) needs a configurable submodule (FixedInSubslots or UsedInSubslots), which ToModel refuses: %d. DAPs with a module only in the catalog that needs one: %d; converted DAPs whose catalog leaves such a module out: %d (%d module references). A converted DAP leaves its optional configurable submodules out of the model, and the address comparison below does not see them.\n", f.DAPsRequiredSubmodules, f.DAPsCatalogRequired, f.DAPsOmittedModules, f.OmittedModules)
	w("\n### Default configurations\n\nEach DAP's default configuration, `gsdml.NewConfiguration` and `Resolve` without choices: the modules ToModel plugs, and each configurable submodule in the subslots it is fixed or used in.\n\n| Outcome | DAPs |\n|---|---|\n")
	for _, k := range sortedKeys(f.Configuration) {
		w("| %s | %d |\n", k, f.Configuration[k])
	}
	w("\n| PlanParameters (Defaults) on the resolved default | DAPs | of them refused by ToModel |\n|---|---|---|\n")
	for _, k := range sortedKeys(f.ConfigPlans) {
		w("| %s | %d | %d |\n", k, f.ConfigPlans[k], f.ConfigPlansRefused[k])
	}
	w("\nOf the DAPs ToModel refuses, %d resolve their default configuration; %d of them ToModel refuses for a configurable submodule (of %d). The resolved defaults plug %d configurable submodules (%d ports) and have %d in their catalog modules.\n", f.ConfigRefusedResolved, f.ConfigRequiredResolved, f.DAPsRequiredSubmodules, f.ConfigSubmodules, f.ConfigPorts, f.ConfigCatalogSubmodules)
	w("\nOf the DAPs ToModel converts: %d resolve to ToModel's model but for the catalog, %d do not (none should), %d do not resolve because their ToModel model fails model.Device.Validate too; %d gain catalog modules (%d modules), %d gain configurable submodules.\n", f.ConfigSame, f.ConfigDiffer, f.ConfigModelInvalid, f.ConfigGainedCatalog, f.GainedModules, f.ConfigGainedSubmodules)
	w("\n### Addresses\n\nEvery converted DAP's plugged configuration, as the file states it (API, slot, subslot, SubmoduleIdentNumber, record indexes), against the submodules a device built from its model serves.\n\n")
	w("| Converted DAPs compared | Submodule addresses | Record indexes | DAPs failed | Lost | Extra | Changed | Records lost | Records extra | Unread |\n|---|---|---|---|---|---|---|---|---|---|\n")
	w("| %d | %d | %d | %d | %d | %d | %d | %d | %d | %d |\n", f.DAPsAddressChecked, f.Addresses, f.AddressRecords, f.DAPsAddressFailed, f.AddressLost, f.AddressExtra, f.AddressChanged, f.AddressRecordsLost, f.AddressRecordsExtra, f.AddressUnread)
	w("\nCollisions (two submodules at one address, or one record index twice in a submodule, in the file; a device built from the model serves the first): %d in %d DAPs.\n", f.AddressCollisions, f.DAPsAddressCollide)
	if len(f.APIs) > 0 {
		w("\n| API | Files |\n|---|---|\n")
		for _, k := range sortedKeys(f.APIs) {
			w("| %s | %d |\n", k, f.APIs[k])
		}
	}
	if len(f.RecordIndexes) > 0 {
		w("\n| Record index range | Records |\n|---|---|\n")
		for _, k := range sortedKeys(f.RecordIndexes) {
			w("| %s | %d |\n", k, f.RecordIndexes[k])
		}
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
