// SPDX-License-Identifier: MIT

package gsdml

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/otfabric/go-profinet"
)

// DataTypeContext names a place in a GSDML document where a DataType
// attribute occurs. Schema versions allow different values in different
// contexts (docs/research/GSDML-XSD-AUDIT.md).
type DataTypeContext string

// DataType contexts.
const (
	// DataTypeContextIO: the DataItem of an Input or Output.
	DataTypeContextIO DataTypeContext = "io"
	// DataTypeContextParameter: the Ref of a ParameterRecordDataItem (and
	// of a Request).
	DataTypeContextParameter DataTypeContext = "parameter"
	// DataTypeContextUnitDiag: the Ref of a UnitDiagTypeItem or
	// ProfileUnitDiagTypeItem.
	DataTypeContextUnitDiag DataTypeContext = "unitDiag"
	// DataTypeContextExtChannel: the DataItem of an ExtChannelAddValue.
	DataTypeContextExtChannel DataTypeContext = "extChannel"
	// DataTypeContextProcessAlarm: the DataItem of a
	// ProcessAlarmReasonAddValue.
	DataTypeContextProcessAlarm DataTypeContext = "processAlarm"
)

// Legality says whether a schema version allows a DataType in a context.
type Legality int

const (
	// LegalityUnverified: no audited schema decides it, because the
	// version was not audited (any version but V1.0, V2.0, V2.1, V2.2 and
	// V2.35, newer ones included) or its schema cannot decide the value.
	// It says nothing about whether the value is legal.
	LegalityUnverified Legality = iota
	// LegalityAllowed: the audited schema of the version allows it.
	LegalityAllowed
	// LegalityForbidden: the audited schema of the version does not allow
	// it, or does not have the context.
	LegalityForbidden
)

// String returns the legality's name.
func (l Legality) String() string {
	switch l {
	case LegalityUnverified:
		return "unverified"
	case LegalityAllowed:
		return "allowed"
	case LegalityForbidden:
		return "forbidden"
	default:
		return "Legality(" + strconv.Itoa(int(l)) + ")"
	}
}

// MarshalText makes a Legality a JSON string.
func (l Legality) MarshalText() ([]byte, error) { return []byte(l.String()), nil }

// UnmarshalText reads the names MarshalText writes.
func (l *Legality) UnmarshalText(b []byte) error {
	for _, v := range []Legality{LegalityUnverified, LegalityAllowed, LegalityForbidden} {
		if string(b) == v.String() {
			*l = v
			return nil
		}
	}
	return fmt.Errorf("gsdml: %w: legality %q", profinet.ErrMalformed, b)
}

// DataTypeLegality says whether GSDML schema version version ("2.35", as
// Report.SchemaVersion gives it) allows dt in ctx. Only exact audited
// versions decide: nothing is inferred for a version between or after
// them, because a value can be removed and added again between versions.
//
// The sets come from third-party copies of the schemas, not from PI's
// distribution (docs/research/GSDML-XSD-AUDIT.md), so Check reports a
// forbidden value as a warning, not an error. Legality is independent of
// a DataType's size (DataItemSize) and of whether ToModel can write its
// DefaultValue.
func DataTypeLegality(version string, ctx DataTypeContext, dt DataType) Legality {
	sets, ok := dataTypeSets[version]
	if !ok || dataTypeUnverified[version][ctx][dt] {
		return LegalityUnverified
	}
	if _, known := dataTypeContextUses[ctx]; !known {
		return LegalityUnverified
	}
	if sets[ctx][dt] {
		return LegalityAllowed
	}
	return LegalityForbidden
}

// DataTypeLegalityVersions returns the schema versions whose legality is
// verified, in ascending order.
func DataTypeLegalityVersions() []string {
	vs := make([]string, 0, len(dataTypeSets))
	for v := range dataTypeSets {
		vs = append(vs, v)
	}
	slices.SortFunc(vs, func(a, b string) int { return versionNumber(a) - versionNumber(b) })
	return vs
}
