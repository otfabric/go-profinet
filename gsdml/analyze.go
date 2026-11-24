// SPDX-License-Identifier: MIT

package gsdml

import (
	"errors"
	"fmt"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/model"
)

// Outcome is the result of converting a device access point or a module
// of a GSDML document to a model.
type Outcome string

// The outcomes of AnalyzeModels.
const (
	// OutcomeConverted: converted as ToModel converts it.
	OutcomeConverted Outcome = "converted"
	// OutcomeMalformed: the description is not valid (an error matching
	// profinet.ErrMalformed, or any error not one of the others).
	OutcomeMalformed Outcome = "malformed"
	// OutcomeUnsupported: the content may be valid, but go-profinet does
	// not implement it (profinet.ErrUnsupported), for example a field
	// without a verified encoding.
	OutcomeUnsupported Outcome = "unsupported"
	// OutcomeResourceExhausted: the conversion went beyond a limit of
	// go-profinet (profinet.ErrResourceExhausted), for example
	// MaxModelRecordOctets.
	OutcomeResourceExhausted Outcome = "resource_exhausted"
)

// outcomeOf classifies a conversion error. Malformed content is never
// reported as unsupported: ErrMalformed is tested first.
func outcomeOf(err error) Outcome {
	switch {
	case err == nil:
		return OutcomeConverted
	case errors.Is(err, profinet.ErrMalformed):
		return OutcomeMalformed
	case errors.Is(err, profinet.ErrResourceExhausted):
		return OutcomeResourceExhausted
	case errors.Is(err, profinet.ErrUnsupported):
		return OutcomeUnsupported
	default:
		return OutcomeMalformed
	}
}

// errText is the text of err, empty for nil.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ModelReport is what AnalyzeModels found for one device access point.
type ModelReport struct {
	// DAP is the device access point's ID, and Path its element path.
	DAP  string `json:"dap"`
	Path string `json:"path"`
	// Outcome is always the classification of ToModel's error for the
	// DAP; Err is that error and Error its text, empty when it converted.
	// What else AnalyzeModels finds never changes them.
	Outcome Outcome `json:"outcome"`
	Error   string  `json:"error,omitempty"`
	Err     error   `json:"-"`
	// DAPErr is the error of the DAP's own content (identity,
	// capabilities, submodules), without its modules, and DAPError its
	// text; nil when that converts.
	DAPError string `json:"dap_error,omitempty"`
	DAPErr   error  `json:"-"`
	// Blocked reports that at least one of Modules has a reference or
	// module error: each one ToModel would refuse the DAP for, whichever
	// it reaches first. A DAP can be blocked and have another Outcome,
	// for example resource_exhausted when the plugged records exceed
	// MaxModelRecordOctets before ToModel reaches the reference.
	Blocked bool `json:"blocked"`
	// Modules are the DAP's UseableModules references, in file order.
	Modules []ModuleReport `json:"modules"`
	// ConfigurableSubmodules are the DAP's own UseableSubmodules
	// references, in file order. ToModel does not convert configurable
	// submodules: a required one is the DAP's DAPErr and fails it, an
	// optional one is left out of its model. Those of the modules are in
	// each ModuleReport; OmitsSubmodules reports both.
	ConfigurableSubmodules []SubmoduleRef `json:"configurable_submodules,omitempty"`
	// Model is ToModel's model when Outcome is OutcomeConverted, nil
	// otherwise: AnalyzeModels never returns a partial model.
	Model *model.Device `json:"-"`
}

// ModuleReport is what AnalyzeModels found for one module reference of a
// device access point: the reference itself, and the module it names.
type ModuleReport struct {
	// Target is the reference's ModuleItemTarget, Path the reference's
	// element path, and ModulePath the path of the module it names (empty
	// when it names none).
	Target     string `json:"target"`
	Path       string `json:"path"`
	ModulePath string `json:"module_path,omitempty"`
	// ReferenceOutcome is the reference's own: malformed when it names no
	// module or its slot attributes do not parse. ReferenceErr is that
	// error and ReferenceError its text.
	ReferenceOutcome Outcome `json:"reference_outcome"`
	ReferenceError   string  `json:"reference_error,omitempty"`
	ReferenceErr     error   `json:"-"`
	// ModuleOutcome is the conversion of the module on its own, with the
	// whole MaxModelRecordOctets; empty when the reference names no
	// module. ModuleErr is its error and ModuleError its text.
	ModuleOutcome Outcome `json:"module_outcome,omitempty"`
	ModuleError   string  `json:"module_error,omitempty"`
	ModuleErr     error   `json:"-"`
	// ModuleIdent is the module's ModuleIdentNumber, 0 when it does not
	// parse or the reference names no module.
	ModuleIdent uint32 `json:"module_ident"`
	// ConfiguredSlots are the slots the reference asks for (FixedInSlots,
	// else UsedInSlots, else a single AllowedInSlots), less slot 0, slots
	// above 0x7FFF and slots an earlier reference asked for: the slots
	// ToModel would plug the module in. They are requested positions, not
	// a model: only when the DAP's Outcome is OutcomeConverted are they
	// the slots of its Model. None for a module that is only in the
	// catalog, or when the slot attributes do not parse.
	ConfiguredSlots []uint16 `json:"configured_slots,omitempty"`
	// RecordOctets are the octets of the module's parameter records, for
	// one slot, when it converts.
	RecordOctets int `json:"record_octets"`
	// ConfigurableSubmodules are the UseableSubmodules references of the
	// module it names, in file order: submodules ToModel does not convert,
	// so the model's module has none of them.
	ConfigurableSubmodules []SubmoduleRef `json:"configurable_submodules,omitempty"`
	// Omitted reports that ToModel leaves the module out of the model's
	// Catalog: it needs a configurable submodule (ModuleOutcome
	// unsupported, ModuleErr why) and this reference plugs it in no slot.
	// An omitted module does not block the DAP; plugged, it would.
	Omitted bool `json:"omitted"`
}

// SubmoduleRef is a configurable submodule: a SubmoduleItemRef of a
// UseableSubmodules list, which ToModel does not convert.
type SubmoduleRef struct {
	// Target is the reference's SubmoduleItemTarget, and Path its element
	// path.
	Target string `json:"target"`
	Path   string `json:"path"`
	// ConfiguredSubslots are the subslots the reference puts its
	// submodule in (its FixedInSubslots, else UsedInSubslots), as listed;
	// none when it does not parse or is only allowed. Required reports
	// that there is at least one: ToModel then refuses the DAP, or the
	// module where it is plugged, with profinet.ErrUnsupported and leaves
	// a module only in the catalog out of it, since the model would lack a
	// configured submodule. An optional reference (Required false) is left
	// out of a model that still converts. Outcome and Error are the
	// reference's own: malformed when its subslot attributes do not parse,
	// unsupported when it is required, converted (left out) otherwise.
	ConfiguredSubslots []uint32 `json:"configured_subslots,omitempty"`
	Required           bool     `json:"required"`
	Outcome            Outcome  `json:"outcome"`
	Error              string   `json:"error,omitempty"`
}

// OmitsSubmodules reports whether the DAP or a module it references has
// configurable submodules (UseableSubmodules). ToModel converts none of
// them: it refuses a required one and leaves an optional one out of the
// model.
func (r ModelReport) OmitsSubmodules() bool {
	if len(r.ConfigurableSubmodules) > 0 {
		return true
	}
	for _, m := range r.Modules {
		if len(m.ConfigurableSubmodules) > 0 {
			return true
		}
	}
	return false
}

// OmitsModules reports whether ToModel leaves a module the DAP references
// out of the model's Catalog (ModuleReport.Omitted): a converted DAP's
// catalog is then not every module the file lets it use.
func (r ModelReport) OmitsModules() bool {
	for _, m := range r.Modules {
		if m.Omitted {
			return true
		}
	}
	return false
}

// submoduleRefs lists the references of a UseableSubmodules list at path
// (the path of its parent element).
func submoduleRefs(us *UseableSubmodules, path string) []SubmoduleRef {
	if us == nil {
		return nil
	}
	var out []SubmoduleRef
	for _, ref := range us.SubmoduleItemRefs {
		sr := SubmoduleRef{Target: ref.SubmoduleItemTarget,
			Path: path + "/UseableSubmodules/SubmoduleItemRef[" + pathID(ref.SubmoduleItemTarget) + "]"}
		l, err := configuredSubslots(ref)
		sr.ConfiguredSubslots, sr.Required = l, len(l) > 0
		if err == nil && sr.Required {
			err = fmt.Errorf("gsdml: %w: configurable submodule %s configured in subslot %d is not converted", profinet.ErrUnsupported, clip(ref.SubmoduleItemTarget), l[0])
		}
		sr.Outcome, sr.Error = outcomeOf(err), errText(err)
		if err != nil && !errors.Is(err, profinet.ErrUnsupported) {
			sr.ConfiguredSubslots, sr.Required = nil, false
		}
		out = append(out, sr)
	}
	return out
}

// moduleResult is the conversion of one module, shared by the references
// and device access points that name it.
type moduleResult struct {
	ident  uint32
	err    error
	octets int
	// omittable: err is the module's need of a configurable submodule,
	// which ToModel refuses only where the module is plugged.
	omittable bool
}

// AnalyzeModels converts every device access point of a document with
// ToModel and reports, for each, the outcome and why: every module
// reference gets its own status and that of the module it names,
// converted on its own, so that one that does not convert does not hide
// the others.
//
// Configurable submodules (UseableSubmodules) are not converted by
// ToModel, which refuses a required one of the DAP or of a plugged module
// and leaves a module only in the catalog that needs one out of the
// catalog. The report lists every one with why (ConfigurableSubmodules,
// OmitsSubmodules, ModuleReport.Omitted, OmitsModules), so that a
// converted DAP that leaves something out is told from one that describes
// all of it.
//
// It is a diagnostic: ToModel stays the conversion to use. A report's
// Outcome and Err are always ToModel's, and its Model is ToModel's, only
// for a DAP that converts.
//
// A module several references name is converted once. It returns an
// error matching profinet.ErrMalformed for a nil document and for one
// without device access points.
func AnalyzeModels(doc *Document) ([]ModelReport, error) {
	if doc == nil || doc.ProfileBody == nil || doc.ProfileBody.ApplicationProcess == nil ||
		doc.ProfileBody.ApplicationProcess.DeviceAccessPointList == nil ||
		len(doc.ProfileBody.ApplicationProcess.DeviceAccessPointList.DeviceAccessPointItems) == 0 {
		return nil, malformed("no device access point")
	}
	ap := doc.ProfileBody.ApplicationProcess
	const root = "ISO15745Profile/ProfileBody/ApplicationProcess"
	modules := map[string]*ModuleItem{}
	if ap.ModuleList != nil {
		for _, mi := range ap.ModuleList.ModuleItems {
			modules[collapseSpace(mi.ID)] = mi // the last of an ID, as in ToModel
		}
	}
	c := newConv(doc)
	results := map[string]*moduleResult{}
	convert := func(mi *ModuleItem) *moduleResult {
		k := collapseSpace(mi.ID)
		if r := results[k]; r != nil {
			return r
		}
		c.budget = MaxModelRecordOctets
		mod, err := c.toModule(mi)
		r := &moduleResult{err: err}
		r.ident, _ = ParseUint32(mi.ModuleIdentNumber)
		if err == nil {
			r.octets = recordOctets(mod.Submodules)
			r.err = moduleSubmodules(mi)
			r.omittable = errors.Is(r.err, profinet.ErrUnsupported)
		}
		results[k] = r
		return r
	}
	var out []ModelReport
	for _, dap := range ap.DeviceAccessPointList.DeviceAccessPointItems {
		rep := ModelReport{DAP: dap.ID, Path: root + "/DeviceAccessPointList/DeviceAccessPointItem[" + pathID(dap.ID) + "]", Modules: []ModuleReport{}}
		m, err := ToModel(doc, dap.ID)
		rep.Outcome, rep.Err, rep.Error = outcomeOf(err), err, errText(err)
		if err == nil {
			rep.Model = m
		}
		c.budget = MaxModelRecordOctets
		_, _, rep.DAPErr = c.dapModel(dap.ID)
		rep.DAPError = errText(rep.DAPErr)
		rep.ConfigurableSubmodules = submoduleRefs(dap.UseableSubmodules, rep.Path)
		if um := dap.UseableModules; um != nil {
			used := map[uint16]bool{}
			for _, ref := range um.ModuleItemRefs {
				mr := ModuleReport{Target: ref.ModuleItemTarget, Path: rep.Path + "/UseableModules/ModuleItemRef[" + pathID(ref.ModuleItemTarget) + "]"}
				if mi := modules[collapseSpace(ref.ModuleItemTarget)]; mi == nil {
					mr.ReferenceErr = malformed("ModuleItemRef %q: no such module", clip(ref.ModuleItemTarget))
				} else {
					mr.ModulePath = root + "/ModuleList/ModuleItem[" + pathID(mi.ID) + "]"
					r := convert(mi)
					mr.ModuleIdent, mr.ModuleErr, mr.RecordOctets = r.ident, r.err, r.octets
					mr.ModuleOutcome, mr.ModuleError = outcomeOf(r.err), errText(r.err)
					mr.ConfigurableSubmodules = submoduleRefs(mi.UseableSubmodules, mr.ModulePath)
				}
				// The slots as ToModel plugs them: the first reference to
				// a slot takes it.
				list, err := configuredSlots(ref)
				if mr.ReferenceErr == nil {
					mr.ReferenceErr = err
				}
				for _, s := range list {
					if s == 0 || s > 0x7FFF || used[uint16(s)] {
						continue
					}
					used[uint16(s)] = true
					mr.ConfiguredSlots = append(mr.ConfiguredSlots, uint16(s))
				}
				mr.ReferenceOutcome, mr.ReferenceError = outcomeOf(mr.ReferenceErr), errText(mr.ReferenceErr)
				if mi := modules[collapseSpace(ref.ModuleItemTarget)]; mi != nil && mr.ReferenceErr == nil {
					mr.Omitted = convert(mi).omittable && len(mr.ConfiguredSlots) == 0
				}
				if mr.ReferenceErr != nil || mr.ModuleErr != nil && !mr.Omitted {
					rep.Blocked = true
				}
				rep.Modules = append(rep.Modules, mr)
			}
		}
		out = append(out, rep)
	}
	return out, nil
}
