// SPDX-License-Identifier: MIT

package gsdml

import (
	"encoding/xml"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/otfabric/go-profinet"
)

// Severity grades an issue of a GSDML document.
type Severity int

const (
	// SeverityError: the document breaks a rule of the catalog (structure,
	// lexical form, references) or a physical limit; Validate fails.
	SeverityError Severity = iota
	// SeverityWarning: the document is valid for the catalog but probably
	// wrong; the rule says whether it is publicly substantiated or a local
	// heuristic of go-profinet.
	SeverityWarning
	// SeverityUnvalidated: content outside go-profinet's catalog (newer
	// schema versions, vendor extensions); kept as read, not checked.
	SeverityUnvalidated
)

// String returns the severity's name.
func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityUnvalidated:
		return "unvalidated"
	default:
		return "Severity(" + strconv.Itoa(int(s)) + ")"
	}
}

// MarshalText makes a Severity a JSON string.
func (s Severity) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// UnmarshalText reads the names MarshalText writes, so that a Report
// decodes from its own JSON.
func (s *Severity) UnmarshalText(b []byte) error {
	for _, v := range []Severity{SeverityError, SeverityWarning, SeverityUnvalidated} {
		if string(b) == v.String() {
			*s = v
			return nil
		}
	}
	return fmt.Errorf("gsdml: %w: severity %q", profinet.ErrMalformed, b)
}

// Rules of the issues. The catalog rules come from the independent
// catalog; the semantic rules each state their basis.
const (
	// RuleStructure: required elements and attributes, lexical forms,
	// unique IDs (the catalog).
	RuleStructure = "structure"
	// RuleReference: every ID and text reference resolves (the catalog).
	RuleReference = "reference"
	// RuleUnknown: an element or attribute outside the catalog.
	RuleUnknown = "unknown"
	// RuleSchemaVersion: the document declares a schema version newer
	// than the catalog's.
	RuleSchemaVersion = "schema-version"
	// RuleIODataLength: the input or the output data of a submodule and
	// its provider status do not fit the 1440 octets an RT frame carries
	// (a physical limit).
	RuleIODataLength = "io-data-length"
	// RuleSlots: a module is allowed, used or fixed in a slot the device
	// access point does not have (PhysicalSlots), used or fixed where it
	// is not allowed, or two modules are fixed in one slot (consistency of
	// the document's own declarations).
	RuleSlots = "slots"
	// RuleIdentNumber: two modules share a ModuleIdentNumber, so that a
	// controller cannot tell them apart (local heuristic).
	RuleIdentNumber = "ident-number"
	// RuleRecordIndex: a parameter record outside the user index range
	// 0x0000-0x7FFF (the range of record indexes a device defines).
	RuleRecordIndex = "record-index"
	// RuleSubslot: an interface or port submodule outside 0x8000-0x8FFF,
	// the subslots PROFINET reserves for them (as p-net and pnio-rs number
	// them; local heuristic).
	RuleSubslot = "subslot"
	// RuleReductionRatio: a reduction ratio that is not a power of two
	// from 1 to 512, the RT_CLASS_1 ratios (local heuristic).
	RuleReductionRatio = "reduction-ratio"
	// RuleDataTypeContext: a DataItem or parameter Ref DataType that the
	// audited schema of the document's version does not allow in that
	// context (third-party copies of the schemas; a warning), or whose
	// legality no audited schema decides (unvalidated, once per document).
	// See DataTypeLegality.
	RuleDataTypeContext = "datatype-context"
)

// Report is the result of Check: every issue, with its severity.
type Report struct {
	// SchemaVersion is the version the document declares (from
	// xsi:schemaLocation), empty when it declares none.
	SchemaVersion string  `json:"schemaVersion,omitempty"`
	Issues        []Issue `json:"issues"`
}

// Count returns the number of issues of a severity.
func (r *Report) Count(s Severity) int {
	n := 0
	for _, i := range r.Issues {
		if i.Severity == s {
			n++
		}
	}
	return n
}

// Err returns the errors of the report as a *ValidationError, or nil.
func (r *Report) Err() error {
	var errs []Issue
	for _, i := range r.Issues {
		if i.Severity == SeverityError {
			errs = append(errs, i)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return &ValidationError{Issues: errs}
}

// Check validates a document against the catalog (as Validate does) and
// the semantic rules, and lists the content it did not validate. Nothing
// it reports makes it PI's GSDML checker.
//
// The report is bounded: Check lists the first 16 384 issues of each
// severity, and one more issue of that severity with the rule "limit"
// then says how many it left out; a module reference gets at most eight
// warnings per slot list and rule and an interface at most eight per
// reduction ratio list, each followed by one that counts the rest; an ID
// in the path of an issue of the semantic rules is quoted up to 256
// octets. The slot rules look at the slot numbers 0 to 65535: a list is
// warned about once for the values above them.
func Check(doc *Document) Report {
	v := &validator{ids: map[string]string{}, texts: map[string]bool{}}
	doc.validate(v)
	for _, r := range v.refs {
		if _, ok := v.ids[r.id]; !ok {
			v.add(r.path, SeverityError, RuleReference, "attribute %s: no element with ID %q", r.kind, r.id)
		}
	}
	for _, r := range v.tids {
		if !v.texts[r.id] {
			v.add(r.path, SeverityError, RuleReference, "attribute TextId: no text %q in the ExternalTextList", r.id)
		}
	}
	rep := Report{SchemaVersion: schemaVersion(doc)}
	if rep.SchemaVersion != "" && newerThanCatalog(rep.SchemaVersion) {
		v.add("ISO15745Profile", SeverityUnvalidated, RuleSchemaVersion,
			"schema version %s is newer than go-profinet's catalog (up to %s): what that version added is kept, not validated", rep.SchemaVersion, SchemaVersions[len(SchemaVersions)-1])
	}
	v.version = rep.SchemaVersion
	semantic(doc, v)
	if v.unverified && v.room(SeverityUnvalidated) {
		msg := "the document declares no schema version"
		if rep.SchemaVersion != "" {
			msg = "schema version " + rep.SchemaVersion + " was not audited"
		}
		v.issues = append(v.issues, Issue{Path: "ISO15745Profile", Severity: SeverityUnvalidated, Rule: RuleDataTypeContext,
			Message: fmt.Sprintf("%s: which DataType values its contexts allow is unverified (audited: %s)", msg, strings.Join(DataTypeLegalityVersions(), ", "))})
	}
	v.closeIssues()
	rep.Issues = v.issues
	return rep
}

var schemaLocation = regexp.MustCompile(`GSDML-DeviceProfile-[Vv](\d+\.\d+)\.xsd`)

// schemaVersion returns the version a document declares in
// xsi:schemaLocation ("2.35").
func schemaVersion(doc *Document) string {
	for _, a := range doc.Attrs {
		if a.Name.Local == "schemaLocation" || strings.HasSuffix(a.Name.Local, ":schemaLocation") {
			if m := schemaLocation.FindStringSubmatch(a.Value); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

// versionNumber turns "2.4" into 240 and "2.45" into 245.
func versionNumber(v string) int {
	major, minor, ok := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(v, "V"), "v"), ".")
	if !ok {
		return 0
	}
	for len(minor) < 2 {
		minor += "0"
	}
	a, err1 := strconv.Atoi(major)
	b, err2 := strconv.Atoi(minor[:2])
	if err1 != nil || err2 != nil {
		return 0
	}
	return a*100 + b
}

func newerThanCatalog(v string) bool {
	n := versionNumber(v)
	return n > 0 && n > versionNumber(SchemaVersions[len(SchemaVersions)-1])
}

func (v *validator) unknown(nodes []*Node) {
	for _, n := range nodes {
		if v.room(SeverityUnvalidated) {
			v.issues = append(v.issues, Issue{Path: v.where(), Severity: SeverityUnvalidated, Rule: RuleUnknown,
				Message: fmt.Sprintf("element %s is not in the catalog: kept, not validated", n.XMLName.Local)})
		}
	}
}

func (v *validator) unknownAttrs(attrs []xml.Attr) {
	for _, a := range attrs {
		// Namespace declarations and the XML Schema instance attributes
		// (the parser keeps their prefixes in the name) are not content.
		if a.Name.Space == "xmlns" || a.Name.Local == "xmlns" || strings.HasPrefix(a.Name.Local, "xmlns:") ||
			a.Name.Space == "http://www.w3.org/2001/XMLSchema-instance" || a.Name.Space == "xsi" || strings.HasPrefix(a.Name.Local, "xsi:") {
			continue
		}
		if v.room(SeverityUnvalidated) {
			v.issues = append(v.issues, Issue{Path: v.where(), Severity: SeverityUnvalidated, Rule: RuleUnknown,
				Message: fmt.Sprintf("attribute %s is not in the catalog: kept, not validated", a.Name.Local)})
		}
	}
}

// warn lists a warning at the path that path returns; the path is built
// only for a warning that is listed.
func (v *validator) warn(path func() string, rule, format string, a ...any) {
	if v.room(SeverityWarning) {
		v.issues = append(v.issues, Issue{Path: path(), Severity: SeverityWarning, Rule: rule, Message: fmt.Sprintf(format, a...)})
	}
}

// maxPathID is the number of octets of an ID an issue path of the
// semantic rules quotes.
const maxPathID = 256

// pathID returns an ID as an issue path quotes it.
func pathID(id string) string {
	if len(id) <= maxPathID {
		return id
	}
	return id[:maxPathID] + "..."
}

// maxIOData is the most data of one submodule an RT frame carries: its
// 1440-octet C_SDU holds the data and the submodule's provider status.
const maxIOData = 1439

// dataLength adds up the sizes of data items; items of a type without a
// known size are left out. The sum is 64 bits wide on every platform.
func dataLength(items []*DataItem) int64 {
	var total int64
	for _, it := range items {
		if n, err := DataItemSize(it.DataType, it.Length); err == nil {
			total += int64(n)
		}
	}
	return total
}

func (v *validator) checkSubmodules(path func() string, items []*VirtualSubmoduleItem) {
	for _, sm := range items {
		p := func() string { return path() + "/VirtualSubmoduleItem[" + pathID(sm.ID) + "]" }
		if sm.IOData != nil {
			if sm.IOData.Input != nil {
				v.checkDataItems(func() string { return p() + "/IOData/Input" }, sm.IOData.Input.DataItems)
			}
			if sm.IOData.Output != nil {
				v.checkDataItems(func() string { return p() + "/IOData/Output" }, sm.IOData.Output.DataItems)
			}
			if sm.IOData.Input != nil {
				if n := dataLength(sm.IOData.Input.DataItems); n > maxIOData && v.room(SeverityError) {
					v.issues = append(v.issues, Issue{Path: p(), Rule: RuleIODataLength, Message: fmt.Sprintf("%d octets of input data: an RT frame carries at most %d with the provider status", n, maxIOData)})
				}
			}
			if sm.IOData.Output != nil {
				if n := dataLength(sm.IOData.Output.DataItems); n > maxIOData && v.room(SeverityError) {
					v.issues = append(v.issues, Issue{Path: p(), Rule: RuleIODataLength, Message: fmt.Sprintf("%d octets of output data: an RT frame carries at most %d with the provider status", n, maxIOData)})
				}
			}
		}
		if sm.RecordDataList != nil {
			for _, r := range sm.RecordDataList.ParameterRecordDataItems {
				if idx, err := parseUint(r.Index, 16); err == nil && idx > 0x7FFF {
					v.warn(p, RuleRecordIndex, "parameter record index %#x is outside the user range 0x0000-0x7FFF", idx)
				}
				for i, ref := range r.Refs {
					v.checkDataType(func() string {
						return p() + "/RecordDataList/ParameterRecordDataItem[" + clip(r.Index) + "]/Ref[" + strconv.Itoa(i+1) + "]"
					}, DataTypeContextParameter, ref.DataType)
				}
			}
		}
	}
}

// checkDataItems applies the DataType rule to the items of an Input or
// Output.
func (v *validator) checkDataItems(path func() string, items []*DataItem) {
	for i, it := range items {
		v.checkDataType(func() string { return path() + "/DataItem[" + strconv.Itoa(i+1) + "]" }, DataTypeContextIO, it.DataType)
	}
}

// checkDataType warns about a DataType the audited schema of the
// document's version forbids in ctx, and records that a legality was not
// decided. A value outside the vocabulary is the structure rule's.
func (v *validator) checkDataType(path func() string, ctx DataTypeContext, dt string) {
	if !slices.Contains(DataTypes, DataType(dt)) {
		return
	}
	switch DataTypeLegality(v.version, ctx, DataType(dt)) {
	case LegalityForbidden:
		v.warn(path, RuleDataTypeContext, "DataType %s is not allowed in the %s context by the audited GSDML V%s schema", dt, ctx, v.version)
	case LegalityUnverified:
		v.unverified = true
	default:
	}
}

// maxSlotWarnings is the number of slots one list of one module reference
// is warned about one by one; the rest share one warning. The values of a
// reduction ratio list are warned about the same way.
const maxSlotWarnings = 8

// slotWarnings warns about total slots: about the first ones, of which
// there are at most maxSlotWarnings, one by one with the text one makes,
// and about the others in one warning with the format rest.
func slotWarnings(v *validator, path func() string, first []uint32, total int, one func(i int, slot uint32) string, rest string) {
	for i, s := range first {
		if v.room(SeverityWarning) {
			v.issues = append(v.issues, Issue{Path: path(), Severity: SeverityWarning, Rule: RuleSlots, Message: one(i, s)})
		}
	}
	if total > len(first) {
		v.warn(path, RuleSlots, rest, total-len(first))
	}
}

// clip shortens a value quoted in an issue to 64 octets.
func clip(s string) string {
	const n = 64
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// slotLists are the slot lists of a module reference in the order the
// slot rules look at them, with the formats of the warnings that count
// the slots beyond maxSlotWarnings.
var slotLists = [...]struct{ name, morePhysical, moreAllowed string }{
	{"AllowedInSlots", "AllowedInSlots: %d more slots are not among the PhysicalSlots", ""},
	{"UsedInSlots", "UsedInSlots: %d more slots are not among the PhysicalSlots", "UsedInSlots: %d more slots are not in AllowedInSlots"},
	{"FixedInSlots", "FixedInSlots: %d more slots are not among the PhysicalSlots", "FixedInSlots: %d more slots are not in AllowedInSlots"},
}

// maxReductionRatios is the number of values of a ReductionRatio list
// that are looked at one by one; ToModel takes as many. The RT_CLASS_1
// ratios are ten.
const maxReductionRatios = 64

// semantic applies the semantic rules.
func semantic(doc *Document, v *validator) {
	if doc.ProfileBody == nil || doc.ProfileBody.ApplicationProcess == nil {
		return
	}
	ap := doc.ProfileBody.ApplicationProcess
	const root = "ISO15745Profile/ProfileBody/ApplicationProcess"
	if ml := ap.ModuleList; ml != nil {
		idents := map[string]string{}
		for _, m := range ml.ModuleItems {
			p := func() string { return root + "/ModuleList/ModuleItem[" + pathID(m.ID) + "]" }
			if n, err := parseUint(m.ModuleIdentNumber, 32); err == nil {
				key := strconv.FormatUint(n, 16)
				if prev, dup := idents[key]; dup {
					v.warn(p, RuleIdentNumber, "ModuleIdentNumber %#x is also the one of module %s", n, clip(prev))
				} else {
					idents[key] = m.ID
				}
			}
			if m.VirtualSubmoduleList != nil {
				v.checkSubmodules(func() string { return p() + "/VirtualSubmoduleList" }, m.VirtualSubmoduleList.VirtualSubmoduleItems)
			}
		}
	}
	if ap.DeviceAccessPointList == nil {
		return
	}
	for _, dap := range ap.DeviceAccessPointList.DeviceAccessPointItems {
		p := func() string { return root + "/DeviceAccessPointList/DeviceAccessPointItem[" + pathID(dap.ID) + "]" }
		if dap.VirtualSubmoduleList != nil {
			v.checkSubmodules(func() string { return p() + "/VirtualSubmoduleList" }, dap.VirtualSubmoduleList.VirtualSubmoduleItems)
		}
		if um := dap.UseableModules; um != nil {
			v.checkSlots(p, dap, um)
		}
		sd := dap.SystemDefinedSubmoduleList
		if sd == nil {
			continue
		}
		for _, it := range sd.InterfaceSubmoduleItems {
			ip := func() string {
				return p() + "/SystemDefinedSubmoduleList/InterfaceSubmoduleItem[" + pathID(it.ID) + "]"
			}
			if n, err := parseUint(it.SubslotNumber, 16); err == nil && (n < 0x8000 || n > 0x8FFF) {
				v.warn(ip, RuleSubslot, "interface submodule in subslot %#x, outside 0x8000-0x8FFF", n)
			}
			if ar := it.ApplicationRelations; ar != nil && ar.TimingProperties != nil && ar.TimingProperties.ReductionRatio != "" {
				v.checkReductionRatios(ip, ar.TimingProperties.ReductionRatio)
			}
		}
		for _, it := range sd.PortSubmoduleItems {
			if n, err := parseUint(it.SubslotNumber, 16); err == nil && (n < 0x8000 || n > 0x8FFF) {
				v.warn(func() string { return p() + "/SystemDefinedSubmoduleList/PortSubmoduleItem[" + pathID(it.ID) + "]" },
					RuleSubslot, "port submodule in subslot %#x, outside 0x8000-0x8FFF", n)
			}
		}
	}
}

// checkReductionRatios warns about the values of a ReductionRatio list
// that are no RT_CLASS_1 ratio: about the first maxSlotWarnings one by
// one, about the others in one warning. A list of more than
// maxReductionRatios values is not expanded: it gets one warning.
func (v *validator) checkReductionRatios(path func() string, list string) {
	rrs, err := ParseValueList(list, maxReductionRatios)
	if errors.Is(err, profinet.ErrResourceExhausted) {
		v.warn(path, RuleReductionRatio, "reduction ratio list %q has more than %d values: the ratios are the powers of two from 1 to 512", clip(list), maxReductionRatios)
		return
	}
	if err != nil {
		return
	}
	bad := 0
	for _, rr := range rrs {
		if rr >= 1 && rr <= 512 && rr&(rr-1) == 0 {
			continue
		}
		if bad++; bad <= maxSlotWarnings {
			v.warn(path, RuleReductionRatio, "reduction ratio %d is not a power of two from 1 to 512", rr)
		}
	}
	if bad > maxSlotWarnings {
		v.warn(path, RuleReductionRatio, "%d more reduction ratios are not a power of two from 1 to 512", bad-maxSlotWarnings)
	}
}
