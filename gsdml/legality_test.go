// SPDX-License-Identifier: MIT

package gsdml

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet"
)

// TestDataTypeLegality: the audited rules of docs/research/GSDML-XSD-AUDIT.md,
// and unverified for everything no audited schema decides.
func TestDataTypeLegality(t *testing.T) {
	const (
		A = LegalityAllowed
		F = LegalityForbidden
		U = LegalityUnverified
	)
	for _, tc := range []struct {
		version string
		ctx     DataTypeContext
		dt      DataType
		want    Legality
	}{
		// V2.35: Bit only in Refs; Float32+Status8 IO-only; Boolean not in
		// the unit diagnosis Ref; F_MessageTrailer IO-only; the four _S
		// types in IO and parameter Refs.
		{"2.35", DataTypeContextIO, DataTypeBit, F},
		{"2.35", DataTypeContextParameter, DataTypeBit, A},
		{"2.35", DataTypeContextUnitDiag, DataTypeBitArea, A},
		{"2.35", DataTypeContextIO, DataTypeFloat32PlusStatus8, A},
		{"2.35", DataTypeContextParameter, DataTypeFloat32PlusStatus8, F},
		{"2.35", DataTypeContextUnitDiag, DataTypeBoolean, F},
		{"2.35", DataTypeContextParameter, DataTypeBoolean, A},
		{"2.35", DataTypeContextIO, DataTypeFMessageTrailer4Byte, A},
		{"2.35", DataTypeContextParameter, DataTypeFMessageTrailer5Byte, F},
		{"2.35", DataTypeContextIO, DataTypeInteger16S, A},
		{"2.35", DataTypeContextParameter, DataTypeOctetStringS, A},
		{"2.35", DataTypeContextIO, DataTypeUnicodeString8, A},
		{"2.35", DataTypeContextParameter, DataType61131WSTRING, A},
		// In no audited schema, V2.46 included.
		{"2.35", DataTypeContextIO, DataTypeInteger32S, F},
		{"2.35", DataTypeContextParameter, DataTypeUnsigned32S, F},
		{"2.35", DataTypeContextIO, DataTypeFloat64PlusStatus8, F},
		{"2.35", DataTypeContextIO, DataTypeUTF8String, F},
		{"2.46", DataTypeContextIO, DataTypeInteger32S, F},
		{"2.46", DataTypeContextIO, DataTypeFloat64PlusStatus8, F},
		{"2.46", DataTypeContextIO, DataTypeUTF8String, F},
		// V2.46 adds four composites, in IO data only.
		{"2.45", DataTypeContextIO, DataTypeInteger16PlusUnsigned8, F},
		{"2.46", DataTypeContextIO, DataTypeInteger16PlusUnsigned8, A},
		{"2.46", DataTypeContextIO, DataTypeUnsigned32PlusUnsigned8, A},
		{"2.46", DataTypeContextParameter, DataTypeUnsigned32PlusUnsigned8, F},
		// V2.32 opens the parameter Ref to the profile types.
		{"2.31", DataTypeContextParameter, DataTypeUnipolar216, F},
		{"2.32", DataTypeContextParameter, DataTypeUnipolar216, A},
		// Not monotonic: removed from the channel diagnosis by V2.31 and
		// from the unit diagnosis by V2.32.
		{"2.3", DataTypeContextExtChannel, DataTypeOctetString, A},
		{"2.31", DataTypeContextExtChannel, DataTypeOctetString, F},
		{"2.2", DataTypeContextExtChannel, DataTypeVisibleString, A},
		{"2.35", DataTypeContextExtChannel, DataTypeVisibleString, F},
		{"2.31", DataTypeContextUnitDiag, DataTypeDate, A},
		{"2.32", DataTypeContextUnitDiag, DataTypeDate, F},
		{"2.35", DataTypeContextUnitDiag, DataTypeDate, F},
		// The process alarm context starts with V2.35.
		{"2.34", DataTypeContextProcessAlarm, DataTypeUnsigned8, F},
		{"2.35", DataTypeContextProcessAlarm, DataTypeUnsigned8, A},
		// Added by V2.1 to the parameter Ref.
		{"2.0", DataTypeContextParameter, DataTypeFloat32, F},
		{"2.1", DataTypeContextParameter, DataTypeFloat32, A},
		// A context the version does not have allows nothing.
		{"1.0", DataTypeContextExtChannel, DataTypeUnsigned8, F},
		{"2.2", DataTypeContextProcessAlarm, DataTypeUnsigned8, F},
		// V2.0's schema enumerates "F_MessageTrailer5Byte " with a space.
		{"2.0", DataTypeContextIO, DataTypeFMessageTrailer5Byte, U},
		{"2.0", DataTypeContextIO, DataTypeFMessageTrailer4Byte, A},
		// Every version from V1.0 to V2.46 is audited.
		{"2.3", DataTypeContextIO, DataTypeUnsigned8, A},
		{"2.34", DataTypeContextIO, DataTypeBit, F},
		{"2.45", DataTypeContextIO, DataTypeUnsigned8, A},
		// A version no audited schema covers, newer or unknown.
		{"2.5", DataTypeContextIO, DataTypeUnsigned8, U},
		{"2.47", DataTypeContextIO, DataTypeUnsigned8, U},
		{"2.27", DataTypeContextIO, DataTypeUnsigned8, U},
		{"", DataTypeContextIO, DataTypeUnsigned8, U},
		{"2.35", "other", DataTypeUnsigned8, U},
	} {
		if got := DataTypeLegality(tc.version, tc.ctx, tc.dt); got != tc.want {
			t.Errorf("%s %s %s: %v, want %v", tc.version, tc.ctx, tc.dt, got, tc.want)
		}
	}
	if got := DataTypeLegalityVersions(); !reflect.DeepEqual(got, []string{"1.0", "2.0", "2.1", "2.2", "2.25", "2.3", "2.31", "2.32", "2.33", "2.34", "2.35", "2.4", "2.41", "2.42", "2.43", "2.44", "2.45", "2.46"}) {
		t.Fatalf("versions %v", got)
	}
	// Every context of the tables has its constant.
	ctxs := map[DataTypeContext]bool{DataTypeContextIO: true, DataTypeContextParameter: true, DataTypeContextUnitDiag: true, DataTypeContextExtChannel: true, DataTypeContextProcessAlarm: true}
	for c := range dataTypeContextUses {
		if !ctxs[c] {
			t.Errorf("context %s has no constant", c)
		}
	}
	if len(dataTypeContextUses) != len(ctxs) {
		t.Errorf("contexts %v", dataTypeContextUses)
	}
}

func TestLegalityText(t *testing.T) {
	for _, l := range []Legality{LegalityUnverified, LegalityAllowed, LegalityForbidden} {
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		var back Legality
		if err := json.Unmarshal(b, &back); err != nil || back != l {
			t.Errorf("%s: %s %v %v", l, b, back, err)
		}
	}
	var l Legality
	if err := l.UnmarshalText([]byte("maybe")); !errors.Is(err, profinet.ErrMalformed) {
		t.Fatal(err)
	}
	if Legality(9).String() != "Legality(9)" {
		t.Fatal(Legality(9).String())
	}
}

// TestCheckDataTypeContext: a DataType the audited V2.35 schema forbids in
// its context is a warning, and Validate still passes; a document of a
// version no audited schema covers gets one unvalidated issue.
func TestCheckDataTypeContext(t *testing.T) {
	b, err := os.ReadFile("testdata/fixtures/GSDML-V2.35-OTFabric-CompactFixture-20261027.xml")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range [][2]string{
		{`<DataItem DataType="Unsigned32" TextId="T_Count"/>`, `<DataItem DataType="Bit" TextId="T_Count"/>`},
		{`DataType="Unsigned16" DefaultValue="100"`, `DataType="Float32+Status8" DefaultValue="100"`},
		{`<DataItem DataType="Unsigned8" UseAsBits="true" TextId="T_Outputs">`, `<DataItem DataType="Integer32_S" UseAsBits="true" TextId="T_Outputs">`},
	} {
		if !bytes.Contains(b, []byte(r[0])) {
			t.Fatalf("the fixture no longer holds %s", r[0])
		}
		b = bytes.Replace(b, []byte(r[0]), []byte(r[1]), 1)
	}
	doc, err := ParseBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	rep := Check(doc)
	w := issuesOf(rep, RuleDataTypeContext, SeverityWarning)
	if len(w) != 3 || len(rep.Issues) != 3 {
		t.Fatalf("%+v", rep.Issues)
	}
	if !strings.Contains(w[0].Path, "/IOData/Input/DataItem[1]") || !strings.Contains(w[0].Message, "DataType Bit is not allowed in the io context by the audited GSDML V2.35 schema") {
		t.Errorf("%+v", w[0])
	}
	var out bool
	for _, i := range w {
		out = out || strings.Contains(i.Path, "/IOData/Output/DataItem[1]") && strings.Contains(i.Message, "Integer32_S")
	}
	if !out {
		t.Errorf("no output warning: %+v", w)
	}
	w = slices.DeleteFunc(w, func(i Issue) bool { return strings.Contains(i.Path, "/IOData/Output/") })
	if !strings.Contains(w[1].Path, "/ParameterRecordDataItem[") || !strings.Contains(w[1].Path, "/Ref[1]") || !strings.Contains(w[1].Message, "Float32+Status8") {
		t.Errorf("%+v", w[1])
	}
	if err := Validate(doc); err != nil {
		t.Fatal(err)
	}
	// The same document without a declared version: unverified once.
	doc.Attrs = nil
	rep = Check(doc)
	u := issuesOf(rep, RuleDataTypeContext, SeverityUnvalidated)
	if len(u) != 1 || len(issuesOf(rep, RuleDataTypeContext, SeverityWarning)) != 0 || !strings.Contains(u[0].Message, "declares no schema version") {
		t.Fatalf("%+v", rep.Issues)
	}
}
