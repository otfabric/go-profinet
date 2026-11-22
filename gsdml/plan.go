// SPDX-License-Identifier: MIT

package gsdml

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/pnio"
)

// RecordWrite is a parameter record to write to a plugged submodule. Its
// fields are those of controller.Parameter, so controller.Parameter(w)
// converts it; gsdml does not import the controller.
type RecordWrite struct {
	Slot    uint16     `json:"slot"`
	Subslot uint16     `json:"subslot"`
	Index   pnio.Index `json:"index"`
	Data    []byte     `json:"data"`
}

// PlannedWrite is a write of a ParameterPlan, with where it comes from.
type PlannedWrite struct {
	Write RecordWrite `json:"write"`
	// Name is the record's name in the primary language.
	Name           string `json:"name,omitempty"`
	ModuleIdent    uint32 `json:"module_ident"`
	SubmoduleIdent uint32 `json:"submodule_ident"`
	// TransferSequence is the record's, 0 when the file gives none.
	TransferSequence uint16 `json:"transfer_sequence"`
	// FilePosition is the record's position in the submodule's
	// RecordDataList, from 0.
	FilePosition int `json:"file_position"`
	// Values is the number of field values the plan's options set in the
	// record; 0 for a record written with its defaults only.
	Values int `json:"values"`
}

// ParameterValues sets fields of a record of a plugged submodule.
type ParameterValues struct {
	Slot    uint16     `json:"slot"`
	Subslot uint16     `json:"subslot"`
	Index   pnio.Index `json:"index"`
	// Values are the fields' values by ParameterRecord.Field key (ID, or
	// a name only one field of the record has), in the lexical form of
	// ParameterRecord.Set.
	Values map[string]string `json:"values"`
}

// PlanOptions configures PlanParameters.
type PlanOptions struct {
	// DAP is the ID of the device access point the model was built from
	// (ToModel's dapID); empty means the first.
	DAP string
	// Defaults writes every record of every plugged submodule, with its
	// defaults where no value is given. Without it, only the records of
	// Values are written: a device keeps its own state of the others.
	Defaults bool
	// Values are the field values to write.
	Values []ParameterValues
	// AllowSequenceErrors writes the records of a submodule whose
	// TransferSequence values are not 1 to N (gaps, repeats) in ascending
	// order instead of refusing the plan. The issues are reported either
	// way.
	AllowSequenceErrors bool
}

// ParameterPlan is the parameter records to write to a device, in the
// order to write them, and what PlanParameters found on the way.
type ParameterPlan struct {
	Writes []PlannedWrite `json:"writes"`
	// Issues are the records' Check issues and the plan's own, with
	// paths "Slot[s]/Subslot[0xSSSS]/ParameterRecordDataItem[i]/...".
	// Warnings and unvalidated issues do not stop a plan; the caller
	// decides whether the plan may be written. An unvalidated issue is
	// not evidence that a record is right.
	Issues []Issue `json:"issues"`
}

// Rules of the issues PlanParameters adds to those of
// ParameterRecord.Check.
const (
	// RuleTransferSequence: the TransferSequence values of a submodule's
	// records that are not 1 to N without gaps or repeats (an error;
	// ISO 15745-4:2003/Amd 1:2006, Annex D, D.4.7.1, Table D.28), or that
	// mix 0, an undefined order, with sequenced records (a warning: where
	// the records of 0 go is a policy of this package).
	RuleTransferSequence = "transfer-sequence"
	// RuleParameterPlan: a record Defaults would write that cannot be
	// built, a field without a verified encoding (unvalidated: it is not
	// written, and the device keeps its own state of it).
	RuleParameterPlan = "parameter-plan"
)

// PlanParameters builds the parameter records to write to a device whose
// plugged modules m describes, from the file doc m was built from. It
// writes nothing: the plan is for controller.DeviceConfig.Parameters
// (converted with controller.Parameter(w.Write)) or for review.
//
//   - Every plugged module must be one the DAP can use, in a slot of the
//     DAP's PhysicalSlots and of the module's AllowedInSlots, and every
//     plugged submodule must be in the file. The DAP's interface and port
//     submodules have no parameter records here: their PDev records are
//     the controller's (PeerChecks, MRP).
//   - A record is written when PlanOptions.Values sets a field of it, or,
//     with PlanOptions.Defaults, always. A record with values must build
//     (ParameterRecord.Encode); a record written with its defaults only
//     that cannot be built for a field without a verified encoding is left
//     out and reported.
//   - A written record with an error of ParameterRecord.Check refuses the
//     plan; its warnings and unvalidated issues are reported.
//   - The records of a submodule are written in ascending TransferSequence
//     (1, 2, …, scoped to the submodule: Table D.28), then those of 0, an
//     undefined order, in file order. Submodules are written in slot and
//     subslot order, the DAP first: a policy of this package, not of
//     Table D.28.
//
// It returns an error matching profinet.ErrInvalidConfig for a model or
// values that do not match the file, a record that does not build or has
// a Check error, or TransferSequence values that are not 1 to N (unless
// AllowSequenceErrors); profinet.ErrNotFound for a DAP the file does not
// have; and profinet.ErrUnsupported for values in a record with a field
// without a verified encoding. With an error, the plan holds the issues
// found and no writes.
func PlanParameters(doc *Document, m *model.Device, opts PlanOptions) (*ParameterPlan, error) {
	if m == nil {
		return nil, fmt.Errorf("gsdml: %w: no model", profinet.ErrInvalidConfig)
	}
	dap, ap, err := findDAP(doc, opts.DAP)
	if err != nil {
		return nil, err
	}
	if id, err := ParseUint32(dap.ModuleIdentNumber); err != nil || id != m.DAP.IdentNumber {
		return nil, fmt.Errorf("gsdml: %w: model DAP 0x%08X is not device access point %s", profinet.ErrInvalidConfig, m.DAP.IdentNumber, clip(dap.ID))
	}
	values, err := indexValues(opts.Values)
	if err != nil {
		return nil, err
	}
	plan := &ParameterPlan{Writes: []PlannedWrite{}, Issues: []Issue{}}
	fail := func(err error) (*ParameterPlan, error) {
		plan.Writes = nil
		return plan, err
	}
	// Both sets are a fixed 8 KiB: parsed once for the DAP, and once per
	// plugged module for its AllowedInSlots.
	var physicalSet, slots slotSet
	physical := physicalSet.parse(dap.PhysicalSlots)
	var writes []PlannedWrite
	var planErr error
	m.Each(func(p model.PluggedSubmodule) {
		if planErr != nil {
			return
		}
		pos := fmt.Sprintf("Slot[%d]/Subslot[0x%04X]", p.Slot, p.Submodule.Subslot)
		if p.Slot != 0 {
			if physical && !physicalSet.has(p.Slot) {
				planErr = fmt.Errorf("gsdml: %w: %s: slot %d is not in the PhysicalSlots %q of device access point %s", profinet.ErrInvalidConfig, pos, p.Slot, clip(dap.PhysicalSlots), clip(dap.ID))
				return
			}
			if list, ok := allowedInSlots(ap, dap, p.ModuleIdent); ok && slots.parse(list) && !slots.has(p.Slot) {
				planErr = fmt.Errorf("gsdml: %w: %s: module 0x%08X is not allowed in slot %d (AllowedInSlots %q)", profinet.ErrInvalidConfig, pos, p.ModuleIdent, p.Slot, clip(list))
				return
			}
		}
		if p.Slot == 0 && systemDefined(dap, p.Submodule.IdentNumber) {
			// An interface or a port: no ParameterRecordDataItem in the
			// file (ToModel converts none); its PDev records are the
			// controller's (PeerChecks, MRP).
			return
		}
		recs, err := SubmoduleParameters(doc, dap.ID, p.ModuleIdent, p.Submodule.IdentNumber)
		if err != nil {
			if errors.Is(err, profinet.ErrNotFound) {
				err = fmt.Errorf("%w: %s: the model does not match the file: %w", profinet.ErrInvalidConfig, pos, err)
			}
			planErr = err
			return
		}
		ws, issues, err := planSubmodule(p, pos, recs, values, opts)
		plan.Issues = append(plan.Issues, issues...)
		if err != nil {
			planErr = err
			return
		}
		writes = append(writes, ws...)
	})
	if planErr != nil {
		return fail(planErr)
	}
	for _, v := range opts.Values {
		if !values[valuesKey{v.Slot, v.Subslot, v.Index}].used {
			return fail(fmt.Errorf("gsdml: %w: values for slot %d subslot 0x%04X record %d: no such record of a plugged submodule", profinet.ErrInvalidConfig, v.Slot, v.Subslot, v.Index))
		}
	}
	plan.Writes = append(plan.Writes, writes...)
	return plan, nil
}

// findDAP returns the device access point with ID id, the first when id is
// empty.
func findDAP(doc *Document, id string) (*DeviceAccessPointItem, *ApplicationProcess, error) {
	if doc == nil || doc.ProfileBody == nil || doc.ProfileBody.ApplicationProcess == nil || doc.ProfileBody.ApplicationProcess.DeviceAccessPointList == nil {
		return nil, nil, fmt.Errorf("gsdml: %w: no device access point", profinet.ErrMalformed)
	}
	ap := doc.ProfileBody.ApplicationProcess
	for _, d := range ap.DeviceAccessPointList.DeviceAccessPointItems {
		if id == "" || d.ID == id {
			return d, ap, nil
		}
	}
	return nil, nil, fmt.Errorf("gsdml: device access point %q: %w", clip(id), profinet.ErrNotFound)
}

// allowedInSlots returns the AllowedInSlots of the DAP's reference to the
// module with ident number ident; ok is false when it gives none.
func allowedInSlots(ap *ApplicationProcess, dap *DeviceAccessPointItem, ident uint32) (string, bool) {
	mi := useableModule(ap, dap, ident)
	if mi == nil {
		return "", false
	}
	for _, ref := range dap.UseableModules.ModuleItemRefs {
		if collapseSpace(ref.ModuleItemTarget) == collapseSpace(mi.ID) && ref.AllowedInSlots != "" {
			return ref.AllowedInSlots, true
		}
	}
	return "", false
}

// systemDefined reports whether the DAP's SystemDefinedSubmoduleList has
// an interface or a port submodule with ident number ident.
func systemDefined(dap *DeviceAccessPointItem, ident uint32) bool {
	sd := dap.SystemDefinedSubmoduleList
	if sd == nil {
		return false
	}
	for _, it := range sd.InterfaceSubmoduleItems {
		if id, err := ParseUint32(it.SubmoduleIdentNumber); err == nil && id == ident {
			return true
		}
	}
	for _, it := range sd.PortSubmoduleItems {
		if id, err := ParseUint32(it.SubmoduleIdentNumber); err == nil && id == ident {
			return true
		}
	}
	return false
}

// has reports whether v is in the set.
func (s *slotSet) has(v uint16) bool { return s.bits[v>>6]&(1<<(v&63)) != 0 }

type valuesKey struct {
	slot, subslot uint16
	index         pnio.Index
}

type indexedValues struct {
	values map[string]string
	used   bool
}

func indexValues(vs []ParameterValues) (map[valuesKey]*indexedValues, error) {
	out := make(map[valuesKey]*indexedValues, len(vs))
	for _, v := range vs {
		k := valuesKey{v.Slot, v.Subslot, v.Index}
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("gsdml: %w: two sets of values for slot %d subslot 0x%04X record %d", profinet.ErrInvalidConfig, v.Slot, v.Subslot, v.Index)
		}
		out[k] = &indexedValues{values: v.Values}
	}
	return out, nil
}

// planSubmodule builds the writes of one plugged submodule, in transfer
// order.
func planSubmodule(p model.PluggedSubmodule, pos string, recs []ParameterRecord, values map[valuesKey]*indexedValues, opts PlanOptions) ([]PlannedWrite, []Issue, error) {
	var issues []Issue
	prefix := func(is []Issue) {
		for _, i := range is {
			i.Path = pos + "/" + i.Path
			issues = append(issues, i)
		}
	}
	seqErr := sequenceIssues(pos, recs, &issues)
	if seqErr != nil && !opts.AllowSequenceErrors {
		return nil, issues, seqErr
	}
	var out []PlannedWrite
	for i := range recs {
		r := &recs[i]
		v := values[valuesKey{p.Slot, p.Submodule.Subslot, r.Index}]
		if v == nil && !opts.Defaults {
			continue
		}
		set := map[int]string{}
		if v != nil {
			v.used = true
			for key, val := range v.values {
				f, err := r.Field(key)
				if errors.Is(err, profinet.ErrNotFound) {
					err = fmt.Errorf("%w: %w", profinet.ErrInvalidConfig, err)
				}
				if err != nil {
					return nil, issues, fmt.Errorf("%s: %w", pos, err)
				}
				set[f] = val
			}
		}
		is := r.Check()
		prefix(is)
		for _, i := range is {
			if i.Severity == SeverityError {
				return nil, issues, fmt.Errorf("gsdml: %w: %s/%s: %s", profinet.ErrInvalidConfig, pos, i.Path, i.Message)
			}
		}
		data, err := r.Encode(set)
		if errors.Is(err, profinet.ErrUnsupported) && v == nil {
			issues = append(issues, Issue{Path: fmt.Sprintf("%s/ParameterRecordDataItem[%d]", pos, r.Index), Severity: SeverityUnvalidated, Rule: RuleParameterPlan,
				Message: fmt.Sprintf("record %d is not written: %v", r.Index, err)})
			continue
		}
		if err != nil {
			return nil, issues, fmt.Errorf("%s: %w", pos, err)
		}
		out = append(out, PlannedWrite{
			Write:            RecordWrite{Slot: p.Slot, Subslot: p.Submodule.Subslot, Index: r.Index, Data: data},
			Name:             r.Name,
			ModuleIdent:      p.ModuleIdent,
			SubmoduleIdent:   p.Submodule.IdentNumber,
			TransferSequence: r.TransferSequence,
			FilePosition:     i,
			Values:           len(set),
		})
	}
	sortTransfer(out)
	return out, issues, nil
}

// sequenceIssues checks the TransferSequence values of a submodule's
// records: the positive ones must be 1 to N, each once (Table D.28). It
// reports a mix of 0 and positive values as a warning, and returns an
// error for values that are not 1 to N.
func sequenceIssues(pos string, recs []ParameterRecord, issues *[]Issue) error {
	var seq []uint16
	zeros := 0
	for i := range recs {
		if s := recs[i].TransferSequence; s > 0 {
			seq = append(seq, s)
		} else {
			zeros++
		}
	}
	if len(seq) == 0 {
		return nil
	}
	path := pos + "/RecordDataList"
	if zeros > 0 {
		*issues = append(*issues, Issue{Path: path, Severity: SeverityWarning, Rule: RuleTransferSequence,
			Message: fmt.Sprintf("%d records without a TransferSequence (0, an undefined order) among %d sequenced ones: the plan writes them after the sequenced ones, in file order (a policy of go-profinet)", zeros, len(seq))})
	}
	slices.Sort(seq)
	for i, s := range seq {
		if int(s) != i+1 {
			msg := fmt.Sprintf("TransferSequence values %v are not 1 to %d, each once (ISO 15745-4 Amd 1, Table D.28)", clipSeq(seq), len(seq))
			*issues = append(*issues, Issue{Path: path, Severity: SeverityError, Rule: RuleTransferSequence, Message: msg})
			return fmt.Errorf("gsdml: %w: %s: %s", profinet.ErrInvalidConfig, pos, msg)
		}
	}
	return nil
}

// clipSeq bounds a sequence for a message.
func clipSeq(s []uint16) []uint16 {
	if len(s) > 16 {
		return s[:16]
	}
	return s
}

// sortTransfer orders the writes of one submodule: ascending positive
// TransferSequence, repeats in file order, then those of 0 in file order.
func sortTransfer(ws []PlannedWrite) {
	slices.SortStableFunc(ws, func(a, b PlannedWrite) int {
		switch {
		case a.TransferSequence == 0 && b.TransferSequence == 0:
			return cmp.Compare(a.FilePosition, b.FilePosition)
		case a.TransferSequence == 0:
			return 1
		case b.TransferSequence == 0:
			return -1
		}
		if c := cmp.Compare(a.TransferSequence, b.TransferSequence); c != 0 {
			return c
		}
		return cmp.Compare(a.FilePosition, b.FilePosition)
	})
}
