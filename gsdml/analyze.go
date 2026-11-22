// SPDX-License-Identifier: MIT

package gsdml

import (
	"errors"

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
	// OutcomeBlocked: a device access point whose own content converts,
	// but that ToModel refuses because a module it can use does not.
	OutcomeBlocked Outcome = "blocked"
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

// ModelReport is what AnalyzeModels found for one device access point.
type ModelReport struct {
	// DAP is the device access point's ID, and Path its element path.
	DAP  string `json:"dap"`
	Path string `json:"path"`
	// Outcome is ToModel's for the DAP; Err is ToModel's error and Error
	// its text, empty when it converted.
	Outcome Outcome `json:"outcome"`
	Error   string  `json:"error,omitempty"`
	Err     error   `json:"-"`
	// DAPError is the error of the DAP's own content (identity,
	// capabilities, submodules), without its modules; empty when that
	// converts. A DAP that is blocked has none.
	DAPError string `json:"dap_error,omitempty"`
	// Modules are the DAP's UseableModules references, in file order.
	Modules []ModuleReport `json:"modules"`
	// Model is ToModel's model when Outcome is OutcomeConverted, nil
	// otherwise: AnalyzeModels never returns a partial model.
	Model *model.Device `json:"-"`
}

// ModuleReport is what AnalyzeModels found for one module reference of a
// device access point.
type ModuleReport struct {
	// Target is the reference's ModuleItemTarget, Path the reference's
	// element path, and ModulePath the path of the module it names (empty
	// when it names none).
	Target     string `json:"target"`
	Path       string `json:"path"`
	ModulePath string `json:"module_path,omitempty"`
	// ModuleIdent is the module's ModuleIdentNumber, 0 when it does not
	// parse.
	ModuleIdent uint32 `json:"module_ident"`
	// Outcome is the module's conversion on its own, with the whole
	// MaxModelRecordOctets; Err and Error as in ModelReport.
	Outcome Outcome `json:"outcome"`
	Error   string  `json:"error,omitempty"`
	Err     error   `json:"-"`
	// Slots are the slots ToModel plugs the module in (FixedInSlots, else
	// UsedInSlots, else a single AllowedInSlots; a slot an earlier
	// reference took is left out); none for a module that is only in the
	// catalog. A reference whose slot list does not parse has none, and
	// SlotsError says why.
	Slots      []uint16 `json:"slots,omitempty"`
	SlotsError string   `json:"slots_error,omitempty"`
	// RecordOctets are the octets of the module's parameter records, for
	// one slot, when it converts.
	RecordOctets int `json:"record_octets"`
}

// Plugged reports whether ToModel plugs the module in a slot, rather
// than only listing it in the catalog.
func (r ModuleReport) Plugged() bool { return len(r.Slots) > 0 }

// moduleResult is the conversion of one module, shared by the references
// and device access points that name it.
type moduleResult struct {
	ident  uint32
	err    error
	octets int
}

// AnalyzeModels converts every device access point of a document with
// ToModel and reports, for each, the outcome and why: a DAP that does not
// convert gets the conversion of each module it can use, on its own, so
// that one module that does not convert does not hide the others.
//
// It is a diagnostic: ToModel stays the conversion to use. A report's
// Model is ToModel's, and only for a DAP that converts.
//
//   - A DAP whose own content does not convert has that outcome, with
//     DAPError.
//   - A DAP whose own content converts but one of whose modules does not
//     is OutcomeBlocked.
//   - A DAP whose modules all convert on their own but whose model has
//     parameter records beyond MaxModelRecordOctets is
//     OutcomeResourceExhausted: each module's RecordOctets keep what each
//     one adds.
//
// A module several references name is converted once. It returns an
// error matching profinet.ErrMalformed for a document without device
// access points.
func AnalyzeModels(doc *Document) ([]ModelReport, error) {
	if doc.ProfileBody == nil || doc.ProfileBody.ApplicationProcess == nil || doc.ProfileBody.ApplicationProcess.DeviceAccessPointList == nil {
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
		}
		results[k] = r
		return r
	}
	var out []ModelReport
	for _, dap := range ap.DeviceAccessPointList.DeviceAccessPointItems {
		rep := ModelReport{DAP: dap.ID, Path: root + "/DeviceAccessPointList/DeviceAccessPointItem[" + pathID(dap.ID) + "]", Modules: []ModuleReport{}}
		m, err := ToModel(doc, dap.ID)
		rep.Outcome, rep.Err = outcomeOf(err), err
		if err != nil {
			rep.Error = err.Error()
		} else {
			rep.Model = m
		}
		c.budget = MaxModelRecordOctets
		_, _, dapErr := c.dapModel(dap.ID)
		if dapErr != nil {
			rep.DAPError = dapErr.Error()
		}
		blocked := false
		if um := dap.UseableModules; um != nil {
			used := map[uint16]bool{}
			for _, ref := range um.ModuleItemRefs {
				mr := ModuleReport{Target: ref.ModuleItemTarget, Path: rep.Path + "/UseableModules/ModuleItemRef[" + pathID(ref.ModuleItemTarget) + "]"}
				mi := modules[collapseSpace(ref.ModuleItemTarget)]
				if mi == nil {
					mr.Err = malformed("ModuleItemRef %q: no such module", clip(ref.ModuleItemTarget))
				} else {
					mr.ModulePath = root + "/ModuleList/ModuleItem[" + pathID(mi.ID) + "]"
					r := convert(mi)
					mr.ModuleIdent, mr.Err, mr.RecordOctets = r.ident, r.err, r.octets
				}
				mr.Outcome = outcomeOf(mr.Err)
				if mr.Err != nil {
					mr.Error = mr.Err.Error()
					blocked = true
				}
				// The slots as ToModel plugs them: the first reference to
				// a slot takes it.
				list, err := configuredSlots(ref)
				if err != nil {
					mr.SlotsError = err.Error()
					blocked = true
				}
				for _, s := range list {
					if s == 0 || s > 0x7FFF || used[uint16(s)] {
						continue
					}
					used[uint16(s)] = true
					mr.Slots = append(mr.Slots, uint16(s))
				}
				rep.Modules = append(rep.Modules, mr)
			}
		}
		if err != nil && dapErr == nil && blocked {
			rep.Outcome = OutcomeBlocked
		}
		out = append(out, rep)
	}
	return out, nil
}
