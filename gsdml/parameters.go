// SPDX-License-Identifier: MIT

package gsdml

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strconv"
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
	// Default is the record as ToModel built it (model.Record.Default)
	// when the record was described: zeros, the Const octets, then every
	// field's DefaultValue. It is nil when a field has no encoding this
	// package has verified. It is reported only: Encode builds the record
	// from Consts and Fields as they are, and Check reports a Default that
	// differs from that.
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
// A field is plain data: Set writes it where its exported fields say, so
// a ParameterRecord read back from its JSON, or changed by its caller,
// encodes as it reads. Check reports a field whose Length or bits do not
// agree with its DataType, and Set refuses it.
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
	// Changeable and Visible are reported. Set, a low-level encoder,
	// writes a field that is not Changeable; PlanParameters refuses a
	// value for it (Table D.31). Visible is not used.
	Changeable bool `json:"changeable"`
	Visible    bool `json:"visible"`
	// ValueItem is the Ref's ValueItemTarget, the ID of a ValueItem of
	// the file; empty when the Ref has none.
	ValueItem string `json:"value_item,omitempty"`
	// ValueItemMissing reports a ValueItemTarget that names no ValueItem
	// of the file. The field's permitted values are then not known: Set
	// refuses a value for it and Check reports it, rather than taking the
	// field as unrestricted.
	ValueItemMissing bool `json:"value_item_missing,omitempty"`
	// Values are the texts the file's ValueItem (ValueItemTarget) assigns
	// to values, in its order. Without AllowedValues they are the
	// permitted values; with neither, the DataType alone limits the value
	// (Siemens, GSDML Getting Started V1.10, 02/2024, section 15.2;
	// Table D.31 of ISO 15745-4:2003/Amd 1:2006, Annex D).
	Values []ParameterValue `json:"values,omitempty"`
	// Encodable reports whether Set can write the field: false for a
	// DataType without a verified encoding (the _S, composite, time and
	// PROFIdrive types), as for a DefaultValue in ToModel. It is reported
	// only: Set decides from DataType.
	Encodable bool `json:"encodable"`
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
		Default: r.DefaultValue, AllowedValues: strings.TrimSpace(r.AllowedValues)}
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
	if f.ValueItem = collapseSpace(r.ValueItemTarget); f.ValueItem != "" {
		var found bool
		f.Values, found = c.valueTexts(f.ValueItem)
		f.ValueItemMissing = !found
	}
	return f, nil
}

// valueTexts returns the assignments of the ValueItem with ID id; found is
// false when the file has no such ValueItem. A ValueItem without
// Assignments is found and has none.
func (c *conv) valueTexts(id string) (out []ParameterValue, found bool) {
	ap := c.doc.ProfileBody.ApplicationProcess
	if ap.ValueList == nil {
		return nil, false
	}
	for _, vi := range ap.ValueList.ValueItems {
		if collapseSpace(vi.ID) != id {
			continue
		}
		if vi.Assignments == nil {
			return nil, true
		}
		for _, a := range vi.Assignments.Assigns {
			out = append(out, ParameterValue{Content: a.Content, Text: c.text(a.TextID)})
		}
		return out, true
	}
	return nil, false
}

// Field returns the index of the field with ID key, or else of the only
// field with Name key. IDs exist from schema V2.3; a name is the text of
// the primary language, and two fields can share one. It returns an
// error matching profinet.ErrNotFound when no field has that ID or name,
// and profinet.ErrInvalidConfig when several fields have that ID, or that
// name and none that ID.
func (r *ParameterRecord) Field(key string) (int, error) {
	byID := -1
	for i := range r.Fields {
		if r.Fields[i].ID == "" || r.Fields[i].ID != key {
			continue
		}
		if byID >= 0 {
			// IDs are unique in a valid file (xsd:ID); a record from
			// elsewhere, its JSON, can repeat one.
			return -1, fmt.Errorf("gsdml: %w: record %d has several fields with ID %q", profinet.ErrInvalidConfig, r.Index, clip(key))
		}
		byID = i
	}
	if byID >= 0 {
		return byID, nil
	}
	found := -1
	for i := range r.Fields {
		if r.Fields[i].Name != key {
			continue
		}
		if found >= 0 {
			return -1, fmt.Errorf("gsdml: %w: record %d has several fields named %q: use an ID or an index", profinet.ErrInvalidConfig, r.Index, clip(key))
		}
		found = i
	}
	if found < 0 {
		return -1, fmt.Errorf("gsdml: record %d field %q: %w", r.Index, clip(key), profinet.ErrNotFound)
	}
	return found, nil
}

// Set writes value into the field i of rec, a record of r's Length: in the
// lexical form of a DefaultValue of the field's DataType ("12", "-3",
// "1.5", "true", "0x01,0x02" for an OctetString, a string as it is). The
// octets and bits outside the field are not changed. SetUint, SetInt,
// SetFloat, SetBool, SetString and SetBytes take Go values instead.
//
// It returns an error matching profinet.ErrInvalidConfig for a value that
// does not fit the field, is outside its AllowedValues or, when it has
// none, is not a value its ValueItem assigns (Table D.31), and for a field
// whose Length or bits do not agree with its DataType;
// profinet.ErrUnsupported for a DataType without a verified encoding, an
// AllowedValues of a string or an OctetString (not compared) or beyond
// what is compared (see Check); and profinet.ErrMalformed for an
// AllowedValues that is not a SignedOrFloatValueListT, and for every value
// of a field whose ValueItemTarget names no ValueItem (ValueItemMissing).
func (r *ParameterRecord) Set(rec []byte, i int, value string) error {
	if i < 0 || i >= len(r.Fields) {
		return fmt.Errorf("gsdml: %w: record %d has no field %d", profinet.ErrInvalidConfig, r.Index, i)
	}
	if len(rec) != int(r.Length) {
		return fmt.Errorf("gsdml: %w: record %d of %d octets, its length is %d", profinet.ErrInvalidConfig, r.Index, len(rec), r.Length)
	}
	f := &r.Fields[i]
	ref, err := f.fieldRef()
	if err != nil {
		return fmt.Errorf("record %d field %q: %w", r.Index, clip(f.Name), err)
	}
	if err := f.allowed(value); err != nil {
		return fmt.Errorf("record %d field %q: %w", r.Index, clip(f.Name), err)
	}
	// Into a copy: a value putRef refuses leaves rec as it was.
	tmp := append([]byte(nil), rec...)
	if err := putRef(tmp, ref, value, "value"); err != nil {
		if errors.Is(err, profinet.ErrMalformed) {
			// fieldRef checked the field: what is refused now is the value
			// or the place of the field in the record.
			err = fmt.Errorf("%w: %w", profinet.ErrInvalidConfig, err)
		}
		return fmt.Errorf("record %d field %q: %w", r.Index, clip(f.Name), err)
	}
	copy(rec, tmp)
	return nil
}

// SetUint writes v into the field i, an Unsigned8 to Unsigned64, a Bit or
// a BitArea, as Set does.
func (r *ParameterRecord) SetUint(rec []byte, i int, v uint64) error {
	return r.setTyped(rec, i, "an unsigned integer", strconv.FormatUint(v, 10), DataTypeUnsigned8, DataTypeUnsigned16, DataTypeUnsigned32, DataTypeUnsigned64, DataTypeBit, DataTypeBitArea)
}

// SetInt writes v into the field i, an Integer8 to Integer64, as Set does.
func (r *ParameterRecord) SetInt(rec []byte, i int, v int64) error {
	return r.setTyped(rec, i, "a signed integer", strconv.FormatInt(v, 10), DataTypeInteger8, DataTypeInteger16, DataTypeInteger32, DataTypeInteger64)
}

// SetFloat writes v into the field i, a Float32 or a Float64, as Set does.
// For a Float32, v is rounded to the nearest Float32, and AllowedValues is
// compared with the rounded value, the one written.
func (r *ParameterRecord) SetFloat(rec []byte, i int, v float64) error {
	return r.setTyped(rec, i, "a float", strconv.FormatFloat(v, 'g', -1, 64), DataTypeFloat32, DataTypeFloat64)
}

// SetBool writes v into the field i, a Boolean, as Set does.
func (r *ParameterRecord) SetBool(rec []byte, i int, v bool) error {
	return r.setTyped(rec, i, "a boolean", strconv.FormatBool(v), DataTypeBoolean)
}

// SetString writes v into the field i, a VisibleString or a UTF8String,
// as Set does: zero-padded to the field's Length, and refused when longer.
func (r *ParameterRecord) SetString(rec []byte, i int, v string) error {
	return r.setTyped(rec, i, "a string", v, DataTypeVisibleString, DataTypeUTF8String)
}

// SetBytes writes v into the field i, an OctetString, as Set does:
// zero-padded to the field's Length, and refused when longer.
func (r *ParameterRecord) SetBytes(rec []byte, i int, v []byte) error {
	return r.setTyped(rec, i, "octets", formatOctets(v), DataTypeOctetString)
}

// setTyped checks that the field i has one of the DataTypes ts and sets
// it to value. A Go value of the wrong kind is refused with
// profinet.ErrInvalidConfig, rather than read as a lexical form.
func (r *ParameterRecord) setTyped(rec []byte, i int, kind, value string, ts ...DataType) error {
	if i >= 0 && i < len(r.Fields) && !slices.Contains(ts, r.Fields[i].DataType) {
		f := &r.Fields[i]
		return fmt.Errorf("gsdml: %w: record %d field %q is a %s, not %s", profinet.ErrInvalidConfig, r.Index, clip(f.Name), clip(string(f.DataType)), kind)
	}
	return r.Set(rec, i, value)
}

// Encode builds the record as ToModel does, from Consts and Fields as
// they are (zeros, the Const octets, every field's Default), and then sets
// the values of the fields given by their index, as Set does.
//
// Encode encodes; it does not judge. A field's Default is written as the
// file gives it, also when Check warns that it is outside the field's
// AllowedValues, while a value given to Encode must be within them. What
// may be written to a device is the caller's decision, with Check.
//
// It returns an error matching profinet.ErrUnsupported when a field has
// no verified encoding (the record cannot be built without it), and
// profinet.ErrInvalidConfig for a Const outside the record, a field or a
// Default that does not encode, or two given fields that share bits:
// which one would be written last is not a choice Encode makes.
func (r *ParameterRecord) Encode(values map[int]string) ([]byte, error) {
	spans := make([]span, 0, len(values))
	for i := range values {
		if i < 0 || i >= len(r.Fields) {
			return nil, fmt.Errorf("gsdml: %w: record %d has no field %d", profinet.ErrInvalidConfig, r.Index, i)
		}
		if s, ok := r.Fields[i].span(i); ok {
			spans = append(spans, s)
		}
	}
	if o := overlaps(spans); len(o) > 0 {
		a, b := &r.Fields[o[0][0]], &r.Fields[o[0][1]]
		return nil, fmt.Errorf("gsdml: %w: record %d: fields %q and %q share bits", profinet.ErrInvalidConfig, r.Index, clip(a.Name), clip(b.Name))
	}
	rec, err := r.defaults()
	if err != nil {
		return nil, err
	}
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

// defaults builds the record from its Consts and the Defaults of its
// fields, in the order of toRecord.
func (r *ParameterRecord) defaults() ([]byte, error) {
	rec := make([]byte, r.Length)
	for k, c := range r.Consts {
		if !within(c.ByteOffset, len(c.Data), len(rec)) {
			return nil, fmt.Errorf("gsdml: %w: record %d: Const[%d] beyond the record", profinet.ErrInvalidConfig, r.Index, k)
		}
		copy(rec[c.ByteOffset:], c.Data)
	}
	for i := range r.Fields {
		f := &r.Fields[i]
		ref, err := f.fieldRef()
		if errors.Is(err, profinet.ErrUnsupported) {
			return nil, fmt.Errorf("gsdml: %w: record %d field %q: no verified encoding for DataType %s, so the record cannot be built", profinet.ErrUnsupported, r.Index, clip(f.Name), clip(string(f.DataType)))
		}
		if err == nil {
			err = putRef(rec, ref, f.Default, "default")
		}
		if err != nil {
			if errors.Is(err, profinet.ErrMalformed) {
				err = fmt.Errorf("%w: %w", profinet.ErrInvalidConfig, err)
			}
			return nil, fmt.Errorf("record %d field %q: %w", r.Index, clip(f.Name), err)
		}
	}
	return rec, nil
}

// fieldRef returns the Ref Set writes the field through, built from the
// field's exported fields. It returns an error matching
// profinet.ErrUnsupported for a DataType without a verified encoding, and
// profinet.ErrInvalidConfig for a Length or bits that do not agree with
// the DataType.
func (f *ParameterField) fieldRef() (*Ref, error) {
	r := &Ref{ByteOffset: strconv.FormatUint(uint64(f.ByteOffset), 10), DataType: string(f.DataType)}
	switch {
	case f.DataType == DataTypeBit || f.DataType == DataTypeBitArea:
		if f.Length != 0 || f.BitLength == 0 || (f.DataType == DataTypeBit && f.BitLength != 1) || int(f.BitOffset)+int(f.BitLength) > 8 {
			return nil, fmt.Errorf("gsdml: %w: %s at %d.%d of %d bits and length %d", profinet.ErrInvalidConfig, f.DataType, f.ByteOffset, f.BitOffset, f.BitLength, f.Length)
		}
		r.BitOffset = strconv.Itoa(int(f.BitOffset))
		r.BitLength = strconv.Itoa(int(f.BitLength))
	case !refDefaultEncoded[f.DataType]:
		return nil, fmt.Errorf("gsdml: %w: no verified encoding for DataType %s", profinet.ErrUnsupported, clip(string(f.DataType)))
	case isStringType(f.DataType):
		if f.Length < 1 || f.Length > math.MaxUint16 || f.BitOffset != 0 || f.BitLength != 0 {
			return nil, fmt.Errorf("gsdml: %w: %s at %d of length %d", profinet.ErrInvalidConfig, f.DataType, f.ByteOffset, f.Length)
		}
		r.Length = strconv.Itoa(f.Length)
	default:
		// Every other encoded DataType has a fixed size.
		if n, _ := DataItemSize(r.DataType, ""); n != f.Length || f.BitOffset != 0 || f.BitLength != 0 {
			return nil, fmt.Errorf("gsdml: %w: %s at %d of length %d, its size is %d", profinet.ErrInvalidConfig, f.DataType, f.ByteOffset, f.Length, n)
		}
	}
	return r, nil
}

// span is the bits [lo, hi) of a record a field or a Const takes; item is
// the index of the field, or -1-k for the Const k.
type span struct {
	lo, hi uint64
	item   int
}

// span returns the bits of the field i; ok is false for a field without a
// size (a DataType without a verified encoding), or one that does not
// agree with its DataType.
func (f *ParameterField) span(i int) (span, bool) {
	if _, err := f.fieldRef(); err != nil {
		return span{}, false
	}
	lo := uint64(f.ByteOffset) * 8
	if f.DataType == DataTypeBit || f.DataType == DataTypeBitArea {
		lo += uint64(f.BitOffset)
		return span{lo, lo + uint64(f.BitLength), i}, true
	}
	return span{lo, lo + uint64(f.Length)*8, i}, true
}

// overlaps returns pairs of items that share bits: each item that starts
// before the end of an earlier one is paired with the earlier one that
// ends last, so every overlapping item is reported once.
func overlaps(spans []span) [][2]int {
	s := slices.Clone(spans)
	slices.SortStableFunc(s, func(a, b span) int { return cmp.Compare(a.lo, b.lo) })
	var out [][2]int
	for i := 1; i < len(s); i++ {
		if s[i].lo < s[i-1].hi {
			out = append(out, [2]int{s[i-1].item, s[i].item})
		}
		if s[i].hi < s[i-1].hi {
			// Keep the one that ends last in front of the next.
			s[i] = span{s[i].lo, s[i-1].hi, s[i-1].item}
		}
	}
	return out
}

// Rules of the issues ParameterRecord.Check reports.
const (
	// RuleParameterLayout: a field or a Const outside the record, a field
	// whose Length or bits do not agree with its DataType, or a Default
	// not of the record's Length (an error).
	RuleParameterLayout = "parameter-layout"
	// RuleParameterOverlap: two Consts that share octets (an error: ISO
	// 15745-4:2003/Amd 1:2006, Annex D, D.4.7.3 does not allow it), or two
	// fields that share bits (a warning: no source this package has states
	// whether it is allowed; the one written last decides the bits). A
	// field over a Const is not reported: D.4.7.4 allows it, and the Ref
	// takes precedence, as ToModel and Encode write the Consts first. The
	// amendment is not in this repository: these clauses are as the
	// maintainer's review states them.
	RuleParameterOverlap = "parameter-overlap"
	// RuleParameterDefault: a DefaultValue that does not encode (an error),
	// or is outside the field's AllowedValues, or, without AllowedValues,
	// not a value of its ValueItem (warnings: consistency of the file's own
	// declarations).
	RuleParameterDefault = "parameter-default"
	// RuleParameterAllowedValues: an AllowedValues that is not a
	// SignedOrFloatValueListT, a ValueItemTarget that names no ValueItem, a
	// ValueItem value that the field cannot hold (its DataType and width),
	// or an AllowedValues that is not a subset of the
	// values of the field's ValueItem (errors; Table D.31), or an
	// AllowedValues this package does not compare (unvalidated).
	RuleParameterAllowedValues = "parameter-allowed-values"
	// RuleParameterEncoding: a field whose DataType has no verified
	// encoding (unvalidated).
	RuleParameterEncoding = "parameter-encoding"
	// RuleParameterRecord: a record of the document that
	// SubmoduleParameters, and so ToModel, refuses: a DefaultValue that
	// does not encode, a field outside the record, a malformed attribute
	// (an error; unvalidated for content go-profinet does not support).
	// Check of a document reports it; ParameterRecord.Check does not, as
	// its record was built.
	RuleParameterRecord = "parameter-record"
)

// maxRecordIssues is the number of issues Check lists for one record;
// one more issue with the rule "limit" counts those beyond it.
const maxRecordIssues = 1024

// Check reports what makes the record unfit to build records from, beyond
// the schema: see the RuleParameter rules. SubmoduleParameters already
// refuses a malformed layout or a DefaultValue that does not encode, so for
// its records Check reports overlaps, AllowedValues, and fields without a
// verified encoding; it checks a record from elsewhere (its JSON) in full.
// The issues' Path is "ParameterRecordDataItem[index]/Ref[i]" or
// ".../Const[k]", with i and k counted from 0.
func (r *ParameterRecord) Check() []Issue {
	path := fmt.Sprintf("ParameterRecordDataItem[%d]", r.Index)
	var out []Issue
	more := 0
	// item is a field index, -1-k for the Const k, or noItem for the
	// record; the path is built only for an issue.
	const noItem = math.MinInt
	add := func(item int, s Severity, rule, format string, args ...any) {
		if len(out) == maxRecordIssues {
			more++
			return
		}
		p := path
		switch {
		case item == noItem:
		case item < 0:
			p = fmt.Sprintf("%s/Const[%d]", path, -1-item)
		default:
			p = fmt.Sprintf("%s/Ref[%d]", path, item)
		}
		out = append(out, Issue{Path: p, Message: fmt.Sprintf(format, args...), Severity: s, Rule: rule})
	}
	if r.Default != nil && len(r.Default) != int(r.Length) {
		add(noItem, SeverityError, RuleParameterLayout, "default of %d octets, the record's length is %d", len(r.Default), r.Length)
	}
	bits := uint64(r.Length) * 8
	// Fields and Consts are compared among themselves: a field over a
	// Const is allowed (RuleParameterOverlap).
	var spans, constSpans []span
	for k, c := range r.Consts {
		if !within(c.ByteOffset, len(c.Data), int(r.Length)) {
			add(-1-k, SeverityError, RuleParameterLayout, "%d octets at %d beyond the record of %d", len(c.Data), c.ByteOffset, r.Length)
			continue
		}
		lo := uint64(c.ByteOffset) * 8
		constSpans = append(constSpans, span{lo, lo + uint64(len(c.Data))*8, -1 - k})
	}
	// One scratch record for every field's Default: putRef writes only the
	// field's octets, and what it writes is not read.
	scratch := make([]byte, r.Length)
	for i := range r.Fields {
		f := &r.Fields[i]
		ref, err := f.fieldRef()
		if err != nil {
			if errors.Is(err, profinet.ErrUnsupported) {
				add(i, SeverityUnvalidated, RuleParameterEncoding, "DataType %s has no verified encoding: the field cannot be set", clip(string(f.DataType)))
			} else {
				add(i, SeverityError, RuleParameterLayout, "%v", err)
			}
			continue
		}
		s, _ := f.span(i)
		if s.hi > bits {
			add(i, SeverityError, RuleParameterLayout, "field %q beyond the record of %d octets", clip(f.Name), r.Length)
			continue
		}
		spans = append(spans, s)
		ranges, aerr := parseAllowedValues(f.AllowedValues)
		switch {
		case errors.Is(aerr, profinet.ErrMalformed):
			add(i, SeverityError, RuleParameterAllowedValues, "AllowedValues %q is not a list of numbers and ranges", clip(f.AllowedValues))
		case aerr != nil:
			add(i, SeverityUnvalidated, RuleParameterAllowedValues, "%v", aerr)
		case f.AllowedValues != "" && isStringType(f.DataType):
			add(i, SeverityUnvalidated, RuleParameterAllowedValues, "AllowedValues of a %s is not compared", f.DataType)
		}
		if err := putRef(scratch, ref, f.Default, "default"); err != nil {
			add(i, SeverityError, RuleParameterDefault, "field %q: %v", clip(f.Name), err)
			continue
		}
		if aerr == nil && f.AllowedValues != "" && !isStringType(f.DataType) && !f.inAllowed(f.Default, ranges) {
			add(i, SeverityWarning, RuleParameterDefault, "DefaultValue %q of field %q is not in its AllowedValues %q", clip(f.Default), clip(f.Name), clip(f.AllowedValues))
		}
		if f.ValueItemMissing {
			add(i, SeverityError, RuleParameterAllowedValues, "field %q: ValueItemTarget %q names no ValueItem of the file", clip(f.Name), clip(f.ValueItem))
		}
		if f.AllowedValues == "" && len(f.Values) > 0 && !f.assigned(f.Default) {
			add(i, SeverityWarning, RuleParameterDefault, "DefaultValue %q of field %q is not one of the values of its ValueItem", clip(f.Default), clip(f.Name))
		}
		if aerr == nil {
			for _, msg := range f.valueItemIssues(ranges, ref, scratch) {
				add(i, SeverityError, RuleParameterAllowedValues, "field %q: %s", clip(f.Name), msg)
			}
		}
	}
	if seen := duplicateID(r.Fields); seen >= 0 {
		add(seen, SeverityError, RuleParameterLayout, "ID %q of field %q is the ID of an earlier field", clip(r.Fields[seen].ID), clip(r.Fields[seen].Name))
	}
	if r.Default != nil && len(r.Default) == int(r.Length) {
		if built, err := r.defaults(); err == nil && !bytes.Equal(built, r.Default) {
			add(noItem, SeverityError, RuleParameterDefault, "Default is not the record its Consts and fields build: Encode builds the latter")
		}
	}
	for _, o := range overlaps(constSpans) {
		add(noItem, SeverityError, RuleParameterOverlap, "%s and %s share octets", r.itemName(o[0]), r.itemName(o[1]))
	}
	for _, o := range overlaps(spans) {
		add(noItem, SeverityWarning, RuleParameterOverlap, "%s and %s share bits", r.itemName(o[0]), r.itemName(o[1]))
	}
	if more > 0 {
		out = append(out, Issue{Path: path, Severity: SeverityWarning, Rule: ruleLimit,
			Message: fmt.Sprintf("%d more issues are not listed: Check lists the first %d of a record", more, maxRecordIssues)})
	}
	return out
}

// duplicateID returns the index of the first field whose nonempty ID an
// earlier field has, -1 when there is none.
func duplicateID(fields []ParameterField) int {
	seen := make(map[string]bool, len(fields))
	for i := range fields {
		if id := fields[i].ID; id != "" {
			if seen[id] {
				return i
			}
			seen[id] = true
		}
	}
	return -1
}

// itemName names a field or a Const of a span in an issue.
func (r *ParameterRecord) itemName(item int) string {
	if item < 0 {
		return fmt.Sprintf("Const[%d]", -1-item)
	}
	return fmt.Sprintf("Ref[%d] %q", item, clip(r.Fields[item].Name))
}

// allowed checks a value against the field's AllowedValues. The whole list
// is parsed first, so a malformed list is refused whatever the value. The
// comparison is exact, in rational numbers, so an Unsigned64 or a Float64
// is compared without rounding; a float at the precision of its DataType
// (inAllowed).
func (f *ParameterField) allowed(value string) error {
	if f.ValueItemMissing {
		return fmt.Errorf("gsdml: %w: ValueItemTarget %q names no ValueItem: the field's permitted values are not known", profinet.ErrMalformed, clip(f.ValueItem))
	}
	if f.AllowedValues == "" {
		if len(f.Values) > 0 && !f.assigned(value) {
			// Without AllowedValues, the values a ValueItem assigns are
			// the permitted ones (Table D.31).
			return fmt.Errorf("gsdml: %w: value %q is not one of the %d values of the field's ValueItem", profinet.ErrInvalidConfig, clip(value), len(f.Values))
		}
		return nil
	}
	if isStringType(f.DataType) {
		return fmt.Errorf("gsdml: %w: AllowedValues of a %s is not compared", profinet.ErrUnsupported, f.DataType)
	}
	ranges, err := parseAllowedValues(f.AllowedValues)
	if err != nil {
		return err
	}
	if _, ok := f.numeric(value); !ok {
		return fmt.Errorf("gsdml: %w: %s value %q", profinet.ErrInvalidConfig, clip(string(f.DataType)), clip(value))
	}
	if !f.inAllowed(value, ranges) {
		return fmt.Errorf("gsdml: %w: value %q is not in AllowedValues %q", profinet.ErrInvalidConfig, clip(value), clip(f.AllowedValues))
	}
	return nil
}

// assigned reports whether a value is one of the Contents of the field's
// ValueItem: compared as numbers for a numeric or Boolean field (a Content
// that is no number matches nothing; Check reports it), as text for a
// string field.
func (f *ParameterField) assigned(value string) bool {
	if isStringType(f.DataType) {
		for _, a := range f.Values {
			if a.Content == value {
				return true
			}
		}
		return false
	}
	v, ok := f.numeric(value)
	if !ok {
		return false
	}
	for _, a := range f.Values {
		if c, ok := f.numeric(a.Content); ok && c.Cmp(v) == 0 {
			return true
		}
	}
	return false
}

// maxSubsetValues bounds the integers of an AllowedValues range that
// Check compares with a ValueItem one by one: a range of more integers
// than the ValueItem has values is not a subset whatever they are.
const maxSubsetValues = 1 << 16

// valueItemIssues reports the Contents of a field's ValueItem that are no
// value of its DataType, and, with an AllowedValues, the allowed values the
// ValueItem does not assign: AllowedValues must be a subset of the values
// of the ValueItem (Table D.31). It returns the messages.
//
// Each Content is written through the field's encoder into scratch, a
// record of the field's record, so that it is checked for the field's type
// and width as Set checks a value: 300 is no Unsigned8, -1 no unsigned
// value, 2 no Bit.
func (f *ParameterField) valueItemIssues(ranges []valueRange, ref *Ref, scratch []byte) []string {
	if len(f.Values) == 0 {
		return nil
	}
	var out []string
	set := make(map[string]bool, len(f.Values))
	for _, a := range f.Values {
		if err := putRef(scratch, ref, a.Content, "ValueItem value"); err != nil {
			out = append(out, fmt.Sprintf("ValueItem value %q is no value of the field: %v", clip(a.Content), err))
			continue
		}
		if c, ok := f.numeric(a.Content); ok {
			set[c.RatString()] = true
		}
	}
	if f.AllowedValues == "" || isStringType(f.DataType) {
		return out
	}
	for _, r := range ranges {
		if !f.rangeAssigned(r, set) {
			out = append(out, fmt.Sprintf("AllowedValues %q is not a subset of the values of the field's ValueItem", clip(f.AllowedValues)))
			break
		}
	}
	return out
}

// rangeAssigned reports whether every value of an AllowedValues range is in
// set, the ValueItem's values: for an integer field each integer from lo to
// hi, for a float field a single value.
func (f *ParameterField) rangeAssigned(r valueRange, set map[string]bool) bool {
	if f.DataType == DataTypeFloat32 || f.DataType == DataTypeFloat64 {
		return r.lo.Cmp(r.hi) == 0 && set[r.lo.RatString()]
	}
	// The integers from ceil(lo) to floor(hi); big.Int.Div rounds toward
	// minus infinity for a positive divisor, and a Rat's is positive.
	lo := new(big.Int).Div(r.lo.Num(), r.lo.Denom())
	if !r.lo.IsInt() {
		lo.Add(lo, big.NewInt(1))
	}
	hi := new(big.Int).Div(r.hi.Num(), r.hi.Denom())
	n := new(big.Int).Sub(hi, lo)
	if n.Sign() < 0 {
		return true // an empty range
	}
	if !n.IsInt64() || n.Int64() >= int64(len(set)) || n.Int64() >= maxSubsetValues {
		return false
	}
	for v := new(big.Int).Set(lo); v.Cmp(hi) <= 0; v.Add(v, big.NewInt(1)) {
		if !set[v.String()] {
			return false
		}
	}
	return true
}

// inAllowed reports whether a value is in ranges. An integer, Bit,
// BitArea or Boolean value is compared exactly. A float is compared at the
// precision of its DataType, the only one a device receives: the value as
// written, rounded to a Float32 or a Float64, with the bounds rounded the
// same way. So a bound such as 1.1, which no float holds exactly, admits
// the float nearest to it, and a value that rounds onto a bound is the
// bound (package policy; docs/GSDML.md).
func (f *ParameterField) inAllowed(value string, ranges []valueRange) bool {
	size := 0
	switch f.DataType {
	case DataTypeFloat32:
		size = 32
	case DataTypeFloat64:
		size = 64
	default:
		v, ok := f.numeric(value)
		return ok && inRanges(v, ranges)
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(value), size)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return false
	}
	for _, r := range ranges {
		// A bound beyond the float range rounds to an infinity, which
		// bounds nothing on that side.
		if lo, hi := roundBound(r.lo, size), roundBound(r.hi, size); v >= lo && v <= hi {
			return true
		}
	}
	return false
}

// roundBound rounds a bound to the nearest Float32 or Float64.
func roundBound(r *big.Rat, size int) float64 {
	if size == 32 {
		f, _ := r.Float32()
		return float64(f)
	}
	f, _ := r.Float64()
	return f
}

// valueRange is a value or a range "lo..hi" of an AllowedValues.
type valueRange struct{ lo, hi *big.Rat }

func inRanges(v *big.Rat, ranges []valueRange) bool {
	for _, r := range ranges {
		if v.Cmp(r.lo) >= 0 && v.Cmp(r.hi) <= 0 {
			return true
		}
	}
	return false
}

// maxAllowedExponent bounds the exponent of a number of an AllowedValues:
// a Float64 is below 1e309 and above 1e-325, so a larger exponent decides
// nothing a smaller one does not, and would cost memory to compute.
const maxAllowedExponent = 400

// maxAllowedDigits bounds the digits of the mantissa of a number of an
// AllowedValues, so that the cost of a comparison does not grow with the
// file: an Unsigned64 has 20 digits and the shortest decimal form of a
// Float64 17.
const maxAllowedDigits = 128

// parseAllowedValues parses an AllowedValues as the schemas' pattern of
// SignedOrFloatValueListT writes it (the same in V1.0 to V2.46): numbers
// -?[0-9]+(\.[0-9]+)?([eE]-?[0-9]+)? and ranges "lo..hi" of them,
// separated by single spaces. An empty one is no list (nil). It returns
// an error matching profinet.ErrMalformed for another form, and
// profinet.ErrUnsupported for more than maxAllowedValues values or an
// exponent beyond maxAllowedExponent.
func parseAllowedValues(s string) ([]valueRange, error) {
	if s == "" {
		return nil, nil
	}
	if strings.Count(s, " ") >= maxAllowedValues {
		return nil, fmt.Errorf("gsdml: %w: AllowedValues of more than %d values", profinet.ErrUnsupported, maxAllowedValues)
	}
	var out []valueRange
	for tok := range strings.SplitSeq(s, " ") {
		lo, hi, isRange := strings.Cut(tok, "..")
		if !isRange {
			hi = lo
		}
		a, err := parseAllowedNumber(lo)
		if err != nil {
			return nil, fmt.Errorf("gsdml: %w: AllowedValues %q", err, clip(s))
		}
		b, err := parseAllowedNumber(hi)
		if err != nil {
			return nil, fmt.Errorf("gsdml: %w: AllowedValues %q", err, clip(s))
		}
		out = append(out, valueRange{a, b})
	}
	return out, nil
}

// parseAllowedNumber parses -?[0-9]+(\.[0-9]+)?([eE]-?[0-9]+)?. big.Rat
// reads more forms (hexadecimal, fractions, "_"), so the form is checked
// here first.
func parseAllowedNumber(s string) (*big.Rat, error) {
	i := 0
	digits := func() int {
		n := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			n++
		}
		return n
	}
	if i < len(s) && s[i] == '-' {
		i++
	}
	n := digits()
	ok := n > 0
	if ok && i < len(s) && s[i] == '.' {
		i++
		f := digits()
		ok, n = f > 0, n+f
	}
	if n > maxAllowedDigits {
		return nil, fmt.Errorf("%w: a number of more than %d digits", profinet.ErrUnsupported, maxAllowedDigits)
	}
	if ok && i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && s[i] == '-' {
			i++
		}
		start := i
		if ok = digits() > 0; ok {
			// Compared as a number of digits first, so that a long exponent
			// is not parsed.
			e := strings.TrimLeft(s[start:i], "0")
			if n, err := strconv.Atoi(e); len(e) > 4 || (err == nil && n > maxAllowedExponent) {
				return nil, fmt.Errorf("%w: exponent beyond %d", profinet.ErrUnsupported, maxAllowedExponent)
			}
		}
	}
	if !ok || i != len(s) {
		return nil, profinet.ErrMalformed
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, profinet.ErrMalformed
	}
	return r, nil
}

// numeric returns a value of a numeric or Boolean field as a number, as
// putRef writes it: false and true are 0 and 1, and a float is rounded to
// its DataType. ok is false for the other types and for a value that is
// no number, a NaN or an infinity.
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
		size := 32
		if f.DataType == DataTypeFloat64 {
			size = 64
		}
		v, err := strconv.ParseFloat(s, size)
		if err != nil {
			return nil, false
		}
		r := new(big.Rat).SetFloat64(v)
		return r, r != nil
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
}

// isStringType reports the encodable types whose value is no number.
func isStringType(t DataType) bool {
	return t == DataTypeVisibleString || t == DataTypeUTF8String || t == DataTypeOctetString
}
