// SPDX-License-Identifier: MIT

package gsdml

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/pnio"
)

// ParameterRecord describes a parameter record of a submodule as its GSDML
// file declares it (a ParameterRecordDataItem): its fields, its constant
// octets and its default value. Unlike model.Record it keeps what a value
// of a field must be, so that a record can be built from chosen values.
type ParameterRecord struct {
	Index pnio.Index `json:"index"`
	// Name is the text of the record's Name in the primary language.
	Name   string `json:"name,omitempty"`
	Length uint16 `json:"length"`
	// TransferSequence is the order the record is written in; 0, the
	// schema default, when the file does not give one.
	TransferSequence uint16 `json:"transfer_sequence"`
	// Default is the record as ToModel builds it (model.Record.Default):
	// zeros, the Const octets, then every field's DefaultValue. It is nil
	// when a field has no encoding this package has verified; Encode then
	// refuses the record.
	Default []byte           `json:"default,omitempty"`
	Consts  []ParameterConst `json:"consts,omitempty"`
	Fields  []ParameterField `json:"fields"`
}

// ParameterConst is a Const of a parameter record: octets at an offset.
type ParameterConst struct {
	ByteOffset uint32 `json:"byte_offset"`
	Data       []byte `json:"data"`
}

// ParameterField is a field of a parameter record (a Ref).
//
// BitOffset, BitLength, Changeable and Visible take the defaults of PI's
// GSDML schemas when the file does not give them: BitOffset 0, BitLength 1,
// Changeable and Visible true. These are the defaults of the Ref of a
// ParameterRecordDataItem in every schema V1.0 to V2.46 (ValueItemReferenceT
// in V1.0 to V2.1, ParameterRecordDataT/Ref in V2.2 and V2.25,
// RecordDataRefT from V2.3).
type ParameterField struct {
	// ID is the Ref's ID, when it has one (from V2.3).
	ID string `json:"id,omitempty"`
	// Name is the text of TextId in the primary language, or TextId when
	// the file has no such text.
	Name     string   `json:"name"`
	DataType DataType `json:"data_type"`
	// ByteOffset is the offset of the field in the record. For Bit and
	// BitArea, BitOffset is the first bit in that octet (0 the least
	// significant) and BitLength the number of bits; they are 0 for the
	// other types.
	ByteOffset uint32 `json:"byte_offset"`
	BitOffset  uint8  `json:"bit_offset,omitempty"`
	BitLength  uint8  `json:"bit_length,omitempty"`
	// Length is the size of the field in octets: 0 for Bit and BitArea,
	// and for a DataType without a known size.
	Length int `json:"length,omitempty"`
	// Default and AllowedValues are as the file writes them; AllowedValues
	// is empty when the file does not restrict the value.
	Default       string `json:"default"`
	AllowedValues string `json:"allowed_values,omitempty"`
	// Changeable and Visible are reported, not enforced: Set writes a
	// field that is not Changeable. No source this package has states
	// what a device or a tool does with them (LIMITATIONS.md).
	Changeable bool `json:"changeable"`
	Visible    bool `json:"visible"`
	// Values are the texts the file's ValueItem (ValueItemTarget) assigns
	// to values, in its order.
	Values []ParameterValue `json:"values,omitempty"`
	// Encodable reports whether Set can write the field: false for a
	// DataType without a verified encoding (the _S, composite, time and
	// PROFIdrive types), as for a DefaultValue in ToModel.
	Encodable bool `json:"encodable"`

	ref *Ref
}

// ParameterValue is an Assign of a ValueItem: a value and its text.
type ParameterValue struct {
	// Content is the value, as the file writes it.
	Content string `json:"content"`
	// Text is the text of its TextId in the primary language.
	Text string `json:"text"`
}

// maxAllowedValues bounds the values and ranges of an AllowedValues that
// Set compares a value with.
const maxAllowedValues = 4096

// SubmoduleParameters describes the parameter records of a submodule: the
// submodule with ident number submoduleIdent of the device access point
// dapID (the first one when empty), when moduleIdent is the DAP's
// ModuleIdentNumber, or of the module with that ident number that the DAP
// can use. These are the submodules and records ToModel converts.
//
// It returns an error matching profinet.ErrNotFound when the DAP, the
// module or the submodule does not exist, and profinet.ErrMalformed for a
// record ToModel refuses as malformed. A field without a verified encoding
// is not an error: it is not Encodable, and its record has no Default.
func SubmoduleParameters(doc *Document, dapID string, moduleIdent, submoduleIdent uint32) ([]ParameterRecord, error) {
	if doc.ProfileBody == nil || doc.ProfileBody.ApplicationProcess == nil || doc.ProfileBody.ApplicationProcess.DeviceAccessPointList == nil {
		return nil, fmt.Errorf("gsdml: %w: no device access point", profinet.ErrMalformed)
	}
	ap := doc.ProfileBody.ApplicationProcess
	var dap *DeviceAccessPointItem
	for _, d := range ap.DeviceAccessPointList.DeviceAccessPointItems {
		if dapID == "" || d.ID == dapID {
			dap = d
			break
		}
	}
	if dap == nil {
		return nil, fmt.Errorf("gsdml: device access point %q: %w", dapID, profinet.ErrNotFound)
	}
	var subs *VirtualSubmoduleList
	if id, err := ParseUint32(dap.ModuleIdentNumber); err == nil && id == moduleIdent {
		subs = dap.VirtualSubmoduleList
	} else if mi := useableModule(ap, dap, moduleIdent); mi != nil {
		subs = mi.VirtualSubmoduleList
	} else {
		return nil, fmt.Errorf("gsdml: device access point %s: module 0x%08X: %w", clip(dap.ID), moduleIdent, profinet.ErrNotFound)
	}
	if subs != nil {
		for _, vs := range subs.VirtualSubmoduleItems {
			if id, err := ParseUint32(vs.SubmoduleIdentNumber); err != nil || id != submoduleIdent {
				continue
			}
			c := newConv(doc)
			out := []ParameterRecord{}
			if rl := vs.RecordDataList; rl != nil {
				for _, r := range rl.ParameterRecordDataItems {
					pr, err := c.parameterRecord(r)
					if err != nil {
						return nil, fmt.Errorf("submodule %s: %w", clip(vs.ID), err)
					}
					out = append(out, pr)
				}
			}
			return out, nil
		}
	}
	return nil, fmt.Errorf("gsdml: module 0x%08X: submodule 0x%08X: %w", moduleIdent, submoduleIdent, profinet.ErrNotFound)
}

// useableModule returns the module with ident number ident among those the
// device access point can use, nil when there is none.
func useableModule(ap *ApplicationProcess, dap *DeviceAccessPointItem, ident uint32) *ModuleItem {
	if dap.UseableModules == nil || ap.ModuleList == nil {
		return nil
	}
	useable := map[string]bool{}
	for _, ref := range dap.UseableModules.ModuleItemRefs {
		useable[collapseSpace(ref.ModuleItemTarget)] = true
	}
	for _, mi := range ap.ModuleList.ModuleItems {
		if !useable[collapseSpace(mi.ID)] {
			continue
		}
		if id, err := ParseUint32(mi.ModuleIdentNumber); err == nil && id == ident {
			return mi
		}
	}
	return nil
}

// parameterRecord describes a record. Its Default is the one toRecord
// builds, so it is the model's.
func (c *conv) parameterRecord(r *ParameterRecordDataItem) (ParameterRecord, error) {
	rec, err := c.toRecord(r)
	if err != nil && !errors.Is(err, profinet.ErrUnsupported) {
		return ParameterRecord{}, err
	}
	pr := ParameterRecord{Index: rec.Index, Name: rec.Name, Length: rec.Length, Fields: []ParameterField{}}
	if err == nil {
		pr.Default = rec.Default
	} else {
		// toRecord stopped at the first Ref it cannot encode: the Refs
		// after it are checked here, each on its own.
		scratch := make([]byte, rec.Length)
		for _, ref := range r.Refs {
			if err := applyRef(scratch, ref); err != nil && !errors.Is(err, profinet.ErrUnsupported) {
				return ParameterRecord{}, fmt.Errorf("record %d: %w", rec.Index, err)
			}
		}
	}
	if ts := strings.TrimSpace(r.TransferSequence); ts != "" {
		v, err := ParseUint16(ts)
		if err != nil {
			return ParameterRecord{}, fmt.Errorf("gsdml: %w: record %d TransferSequence %q", profinet.ErrMalformed, rec.Index, clip(r.TransferSequence))
		}
		pr.TransferSequence = v
	}
	for _, k := range r.Consts {
		// toRecord checked the offset and the octets of every Const.
		off, _ := optionalUint32(k.ByteOffset)
		data, _ := parseOctets(k.Data)
		pr.Consts = append(pr.Consts, ParameterConst{ByteOffset: off, Data: data})
	}
	for _, ref := range r.Refs {
		f, err := c.parameterField(ref)
		if err != nil {
			return ParameterRecord{}, fmt.Errorf("record %d: %w", rec.Index, err)
		}
		pr.Fields = append(pr.Fields, f)
	}
	return pr, nil
}

func (c *conv) parameterField(r *Ref) (ParameterField, error) {
	// applyRef checked the offset of every Ref of the record.
	off, _ := ParseUint32(r.ByteOffset)
	t := DataType(r.DataType)
	f := ParameterField{ID: r.ID, Name: c.text(r.TextID), DataType: t, ByteOffset: off,
		Default: r.DefaultValue, AllowedValues: strings.TrimSpace(r.AllowedValues), ref: r}
	var err error
	if f.Changeable, err = parseBool(r.Changeable, false, true); err != nil {
		return ParameterField{}, fmt.Errorf("gsdml: %w: Ref at %d Changeable: %v", profinet.ErrMalformed, off, err)
	}
	if f.Visible, err = parseBool(r.Visible, false, true); err != nil {
		return ParameterField{}, fmt.Errorf("gsdml: %w: Ref at %d Visible: %v", profinet.ErrMalformed, off, err)
	}
	switch t {
	case DataTypeBit, DataTypeBitArea:
		// putRef refuses the malformed ones when it writes the default.
		bit, _ := parseUint(r.bitOffsetOrDefault(), 8)
		f.BitOffset, f.BitLength = uint8(bit), 1
		if t == DataTypeBitArea {
			w, _ := parseUint(r.bitLengthOrDefault(), 8)
			f.BitLength = uint8(w)
		}
		f.Encodable = true
	default:
		if n, err := DataItemSize(r.DataType, r.Length); err == nil {
			f.Length = n
		}
		f.Encodable = refDefaultEncoded[t]
	}
	f.Values = c.valueTexts(r.ValueItemTarget)
	return f, nil
}

// valueTexts returns the assignments of the ValueItem with ID id.
func (c *conv) valueTexts(id string) []ParameterValue {
	ap := c.doc.ProfileBody.ApplicationProcess
	if id = collapseSpace(id); id == "" || ap.ValueList == nil {
		return nil
	}
	for _, vi := range ap.ValueList.ValueItems {
		if collapseSpace(vi.ID) != id || vi.Assignments == nil {
			continue
		}
		var out []ParameterValue
		for _, a := range vi.Assignments.Assigns {
			out = append(out, ParameterValue{Content: a.Content, Text: c.text(a.TextID)})
		}
		return out
	}
	return nil
}

// Set writes value into the field i of rec, a record of r's Length: in the
// lexical form of a DefaultValue of the field's DataType ("12", "-3",
// "1.5", "true", "0x01,0x02" for an OctetString, a string as it is). The
// octets and bits outside the field are not changed.
//
// It returns an error matching profinet.ErrInvalidConfig for a value that
// does not fit the field or is outside its AllowedValues, and
// profinet.ErrUnsupported for a field that is not Encodable or whose
// AllowedValues this package does not compare (AllowedValues of a string,
// an OctetString).
func (r *ParameterRecord) Set(rec []byte, i int, value string) error {
	if i < 0 || i >= len(r.Fields) {
		return fmt.Errorf("gsdml: %w: record %d has no field %d", profinet.ErrInvalidConfig, r.Index, i)
	}
	if len(rec) != int(r.Length) {
		return fmt.Errorf("gsdml: %w: record %d of %d octets, its length is %d", profinet.ErrInvalidConfig, r.Index, len(rec), r.Length)
	}
	f := &r.Fields[i]
	if !f.Encodable {
		return fmt.Errorf("gsdml: %w: record %d field %q: no verified encoding for DataType %s", profinet.ErrUnsupported, r.Index, clip(f.Name), clip(string(f.DataType)))
	}
	if err := f.allowed(value); err != nil {
		return fmt.Errorf("record %d field %q: %w", r.Index, clip(f.Name), err)
	}
	// Into a copy: a value putRef refuses leaves rec as it was.
	tmp := append([]byte(nil), rec...)
	if err := putRef(tmp, f.ref, value, "value"); err != nil {
		if errors.Is(err, profinet.ErrMalformed) {
			// The file was checked when the record was described: what is
			// refused now is the value.
			err = fmt.Errorf("%w: %w", profinet.ErrInvalidConfig, err)
		}
		return fmt.Errorf("record %d field %q: %w", r.Index, clip(f.Name), err)
	}
	copy(rec, tmp)
	return nil
}

// Encode builds the record from its Default with the values of the fields
// given by their index set, as Set does. It returns an error matching
// profinet.ErrUnsupported when the record has no Default.
func (r *ParameterRecord) Encode(values map[int]string) ([]byte, error) {
	if r.Default == nil {
		return nil, fmt.Errorf("gsdml: %w: record %d: a field has no verified encoding, so the record has no default", profinet.ErrUnsupported, r.Index)
	}
	for i := range values {
		if i < 0 || i >= len(r.Fields) {
			return nil, fmt.Errorf("gsdml: %w: record %d has no field %d", profinet.ErrInvalidConfig, r.Index, i)
		}
	}
	rec := append([]byte(nil), r.Default...)
	// In field order, so that the result does not depend on the map's.
	for i := range r.Fields {
		if v, ok := values[i]; ok {
			if err := r.Set(rec, i, v); err != nil {
				return nil, err
			}
		}
	}
	return rec, nil
}

// allowed checks a value against the field's AllowedValues
// (SignedOrFloatValueListT: values and ranges "lo..hi" separated by
// spaces). The comparison is exact, in rational numbers, so an Unsigned64
// or a Float64 is compared without rounding.
func (f *ParameterField) allowed(value string) error {
	if f.AllowedValues == "" {
		return nil
	}
	v, ok := f.numeric(value)
	if !ok {
		if isStringType(f.DataType) {
			return fmt.Errorf("gsdml: %w: AllowedValues of a %s is not compared", profinet.ErrUnsupported, f.DataType)
		}
		return fmt.Errorf("gsdml: %w: %s value %q", profinet.ErrInvalidConfig, f.DataType, clip(value))
	}
	toks := strings.Fields(f.AllowedValues)
	if len(toks) > maxAllowedValues {
		return fmt.Errorf("gsdml: %w: AllowedValues of more than %d values", profinet.ErrUnsupported, maxAllowedValues)
	}
	for _, tok := range toks {
		lo, hi, isRange := strings.Cut(tok, "..")
		if !isRange {
			hi = lo
		}
		a, okA := new(big.Rat).SetString(lo)
		b, okB := new(big.Rat).SetString(hi)
		if !okA || !okB {
			return fmt.Errorf("gsdml: %w: AllowedValues %q", profinet.ErrMalformed, clip(f.AllowedValues))
		}
		if v.Cmp(a) >= 0 && v.Cmp(b) <= 0 {
			return nil
		}
	}
	return fmt.Errorf("gsdml: %w: value %q is not in AllowedValues %q", profinet.ErrInvalidConfig, clip(value), clip(f.AllowedValues))
}

// numeric returns a value of a numeric or Boolean field as a number:
// false and true are 0 and 1. ok is false for the other types and for a
// value that is no number.
func (f *ParameterField) numeric(value string) (*big.Rat, bool) {
	s := strings.TrimSpace(value)
	if isStringType(f.DataType) {
		return nil, false
	}
	switch f.DataType {
	case DataTypeBoolean:
		b, err := parseBool(s, true, false)
		if err != nil {
			return nil, false
		}
		if b {
			return big.NewRat(1, 1), true
		}
		return new(big.Rat), true
	case DataTypeFloat32, DataTypeFloat64:
	default:
		// Integers as putRef reads them: decimal, or hexadecimal "0x".
		if u, err := parseUint(s, 64); err == nil {
			return new(big.Rat).SetInt(new(big.Int).SetUint64(u)), true
		}
		if n, err := parseInt(s, 64); err == nil {
			return new(big.Rat).SetInt64(n), true
		}
		return nil, false
	}
	// A float that SetString cannot read ("NaN", "Inf") is not in any range.
	r, ok := new(big.Rat).SetString(s)
	return r, ok
}

// isStringType reports the encodable types whose value is no number.
func isStringType(t DataType) bool {
	return t == DataTypeVisibleString || t == DataTypeUTF8String || t == DataTypeOctetString
}
