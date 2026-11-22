// SPDX-License-Identifier: MIT

package gsdml

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet"
)

// A record read back from its JSON sets and encodes as the one
// SubmoduleParameters returned: a field is plain data.
func TestParameterJSONRoundTrip(t *testing.T) {
	t.Parallel()
	for _, r := range fixtureParameters(t) {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		var back ParameterRecord
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatal(err)
		}
		for i := range r.Fields {
			want, wantErr := r.Encode(map[int]string{i: "1"})
			got, err := back.Encode(map[int]string{i: "1"})
			if !bytes.Equal(got, want) || (err == nil) != (wantErr == nil) {
				t.Errorf("record %d field %d: % X %v, want % X %v", r.Index, i, got, err, want, wantErr)
			}
		}
	}
}

// Set writes a field where its exported fields say, and refuses one whose
// Length or bits do not agree with its DataType.
func TestParameterFieldDescriptor(t *testing.T) {
	t.Parallel()
	r := fixtureParameters(t)[1]
	r.Fields = append([]ParameterField(nil), r.Fields...)
	r.Fields[0].ByteOffset = 22 // the octet no field takes
	rec := append([]byte(nil), r.Default...)
	if err := r.Set(rec, 0, "9"); err != nil || rec[22] != 9 || rec[0] != r.Default[0] {
		t.Fatalf("moved field: %v % X", err, rec)
	}
	r.Fields[0].ByteOffset = 24
	if err := r.Set(rec, 0, "9"); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("field beyond the record: %v", err)
	}
	for _, f := range []ParameterField{
		{DataType: DataTypeBit, BitLength: 2},
		{DataType: DataTypeBit, BitLength: 1, Length: 1},
		{DataType: DataTypeBitArea, BitOffset: 6, BitLength: 3},
		{DataType: DataTypeBitArea},
		{DataType: DataTypeUnsigned16, Length: 3},
		{DataType: DataTypeUnsigned16, Length: 2, BitLength: 1},
		{DataType: DataTypeVisibleString},
		{DataType: DataTypeOctetString, Length: math.MaxUint16 + 1},
		{DataType: DataTypeUTF8String, Length: 2, BitOffset: 1},
	} {
		if _, err := f.fieldRef(); !errors.Is(err, profinet.ErrInvalidConfig) {
			t.Errorf("%+v: %v", f, err)
		}
	}
	for _, d := range []DataType{"NoSuchType", DataTypeUnsigned16S, DataTypeTimeStamp} {
		f := ParameterField{DataType: d, Length: 2}
		if _, err := f.fieldRef(); !errors.Is(err, profinet.ErrUnsupported) {
			t.Errorf("%s: %v", d, err)
		}
	}
}

func TestParameterFieldLookup(t *testing.T) {
	t.Parallel()
	r := ParameterRecord{Index: 7, Fields: []ParameterField{
		{Name: "Mode"}, {Name: "Limit", ID: "R_A"}, {Name: "Limit"}, {Name: "R_A2", ID: "R_B"}, {Name: "x", ID: "R_A2"},
	}}
	for _, c := range []struct {
		key  string
		want int
		err  error
	}{
		{"Mode", 0, nil},
		{"R_A", 1, nil},
		{"R_A2", 4, nil}, // an ID before a name
		{"Limit", -1, profinet.ErrInvalidConfig},
		{"Nope", -1, profinet.ErrNotFound},
		{"", -1, profinet.ErrNotFound}, // an empty ID is no ID
	} {
		got, err := r.Field(c.key)
		if got != c.want || (c.err == nil) != (err == nil) || (c.err != nil && !errors.Is(err, c.err)) {
			t.Errorf("%q: %d %v, want %d %v", c.key, got, err, c.want, c.err)
		}
	}
}

// The typed setters write what Set writes for the same value, and refuse
// a field of another DataType.
func TestParameterTypedSetters(t *testing.T) {
	t.Parallel()
	r := fixtureParameters(t)[1]
	for _, c := range []struct {
		field int
		set   func(rec []byte, i int) error
		as    string
	}{
		{0, func(rec []byte, i int) error { return r.SetUint(rec, i, 9) }, "9"},
		{1, func(rec []byte, i int) error { return r.SetInt(rec, i, -32768) }, "-32768"},
		{3, func(rec []byte, i int) error { return r.SetFloat(rec, i, 2.5) }, "2.5"},
		{4, func(rec []byte, i int) error { return r.SetBool(rec, i, false) }, "false"},
		{5, func(rec []byte, i int) error { return r.SetUint(rec, i, 0) }, "0"},
		{6, func(rec []byte, i int) error { return r.SetUint(rec, i, 7) }, "7"},
		{8, func(rec []byte, i int) error { return r.SetString(rec, i, "OK") }, "OK"},
		{9, func(rec []byte, i int) error { return r.SetBytes(rec, i, []byte{0xFF}) }, "0xFF"},
		{9, func(rec []byte, i int) error { return r.SetBytes(rec, i, nil) }, ""},
	} {
		got := append([]byte(nil), r.Default...)
		want := append([]byte(nil), r.Default...)
		if err := c.set(got, c.field); err != nil {
			t.Fatalf("field %d: %v", c.field, err)
		}
		if err := r.Set(want, c.field, c.as); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("field %d: % X, Set %q % X %v", c.field, got, c.as, want, err)
		}
	}
	rec := append([]byte(nil), r.Default...)
	for name, err := range map[string]error{
		"uint into Integer16":   r.SetUint(rec, 1, 1),
		"int into Unsigned8":    r.SetInt(rec, 0, 1),
		"float into Unsigned8":  r.SetFloat(rec, 0, 1),
		"bool into Bit":         r.SetBool(rec, 5, true),
		"string into Octet":     r.SetString(rec, 9, "a"),
		"bytes into Visible":    r.SetBytes(rec, 8, []byte("a")),
		"uint not allowed":      r.SetUint(rec, 0, 10),
		"float beyond Float32":  r.SetFloat(rec, 3, math.MaxFloat64),
		"string too long":       r.SetString(rec, 8, "abcde"),
		"uint no field":         r.SetUint(rec, len(r.Fields), 1),
		"uint negative field":   r.SetUint(rec, -1, 1),
		"float NaN not allowed": r.SetFloat(rec, 3, math.NaN()),
	} {
		if !errors.Is(err, profinet.ErrInvalidConfig) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if !bytes.Equal(rec, r.Default) {
		t.Fatalf("a refused value changed the record: % X", rec)
	}
}

// AllowedValues is parsed as the schemas' SignedOrFloatValueListT, all of
// it before a value is compared.
func TestParameterAllowedValuesForm(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		allowed string
		err     error
	}{
		{"1", nil}, {"-1..-0", nil}, {"1.5e-3..2E4", nil}, {"0..1e400", nil}, {"1e-0400", nil},
		{"1..2 x", profinet.ErrMalformed}, // after a value that matches
		{"0x10", profinet.ErrMalformed}, {"1/2", profinet.ErrMalformed}, {"1_0", profinet.ErrMalformed},
		{"+1", profinet.ErrMalformed}, {".5", profinet.ErrMalformed}, {"1.", profinet.ErrMalformed},
		{"1e", profinet.ErrMalformed}, {"-", profinet.ErrMalformed}, {"1..", profinet.ErrMalformed},
		{"1..2..3", profinet.ErrMalformed}, {"1  2", profinet.ErrMalformed}, {" 1", profinet.ErrMalformed},
		{"1\t2", profinet.ErrMalformed}, {"Inf", profinet.ErrMalformed},
		{"1e401", profinet.ErrUnsupported}, {"1e-401", profinet.ErrUnsupported},
		{"1e" + strings.Repeat("9", 64), profinet.ErrUnsupported},
		{"1e00000000000001", nil},
	} {
		_, err := parseAllowedValues(c.allowed)
		if (c.err == nil) != (err == nil) || (c.err != nil && !errors.Is(err, c.err)) {
			t.Errorf("%q: %v, want %v", c.allowed, err, c.err)
		}
		f := ParameterField{DataType: DataTypeUnsigned8, AllowedValues: c.allowed}
		if err := f.allowed("1"); c.err != nil && !errors.Is(err, c.err) {
			t.Errorf("value 1 in %q: %v, want %v", c.allowed, err, c.err)
		}
	}
	// A float is compared as it is written: rounded to a Float32, 1.50000001
	// is 1.5.
	for _, c := range []struct {
		t   DataType
		err error
	}{{DataTypeFloat32, nil}, {DataTypeFloat64, profinet.ErrInvalidConfig}} {
		f := ParameterField{DataType: c.t, AllowedValues: "0..1.5"}
		if err := f.allowed("1.50000001"); (c.err == nil) != (err == nil) || (c.err != nil && !errors.Is(err, c.err)) {
			t.Errorf("%s: %v, want %v", c.t, err, c.err)
		}
	}
}

func TestParameterEncodeOverlap(t *testing.T) {
	t.Parallel()
	r := ParameterRecord{Index: 9, Length: 2, Default: []byte{0, 0}, Fields: []ParameterField{
		{Name: "Octet", DataType: DataTypeUnsigned8, Length: 1},
		{Name: "Bits", DataType: DataTypeBitArea, BitOffset: 2, BitLength: 2},
		{Name: "Next", DataType: DataTypeUnsigned8, ByteOffset: 1, Length: 1},
	}}
	if _, err := r.Encode(map[int]string{0: "1", 1: "1"}); !errors.Is(err, profinet.ErrInvalidConfig) || !strings.Contains(err.Error(), "share bits") {
		t.Fatalf("overlap: %v", err)
	}
	if got, err := r.Encode(map[int]string{1: "3", 2: "5"}); err != nil || !bytes.Equal(got, []byte{0x0C, 0x05}) {
		t.Fatalf("no overlap: % X %v", got, err)
	}
	r.Default = []byte{0}
	if _, err := r.Encode(nil); !errors.Is(err, profinet.ErrInvalidConfig) {
		t.Fatalf("short default: %v", err)
	}
}

func TestParameterCheck(t *testing.T) {
	t.Parallel()
	recs := fixtureParameters(t)
	for _, r := range recs[:2] {
		if is := r.Check(); len(is) != 0 {
			t.Errorf("record %d: %v", r.Index, is)
		}
	}
	if is := recs[2].Check(); len(is) != 1 || is[0].Rule != RuleParameterEncoding || is[0].Severity != SeverityUnvalidated ||
		is[0].Path != "ParameterRecordDataItem[3]/Ref[0]" {
		t.Errorf("record 3: %v", is)
	}
	r := ParameterRecord{Index: 5, Length: 4, Default: []byte{0}, Consts: []ParameterConst{
		{ByteOffset: 3, Data: []byte{1}}, {ByteOffset: 4, Data: []byte{1}},
	}, Fields: []ParameterField{
		{Name: "Word", DataType: DataTypeUnsigned16, Length: 2, Default: "1"},                                       // 0: overlaps 1
		{Name: "Bit", DataType: DataTypeBit, ByteOffset: 1, BitOffset: 7, BitLength: 1, Default: "1"},               // 1
		{Name: "Over", DataType: DataTypeUnsigned8, ByteOffset: 3, Length: 1, Default: "0"},                         // 2: overlaps Const 0
		{Name: "Range", DataType: DataTypeUnsigned8, ByteOffset: 2, Length: 1, Default: "9", AllowedValues: "1..5"}, // 3: default outside
		{Name: "Bad", DataType: DataTypeUnsigned8, ByteOffset: 2, Length: 2, Default: "1"},                          // 4: layout
		{Name: "Far", DataType: DataTypeUnsigned8, ByteOffset: 4, Length: 1, Default: "1"},                          // 5: beyond
		{Name: "Huge", DataType: DataTypeBit, ByteOffset: 2, BitLength: 1, Default: "2"},                            // 6: default
		{Name: "List", DataType: DataTypeBit, ByteOffset: 2, BitOffset: 1, BitLength: 1, Default: "1", AllowedValues: "x"},
		{Name: "Exp", DataType: DataTypeBit, ByteOffset: 2, BitOffset: 2, BitLength: 1, Default: "1", AllowedValues: "1e999"},
		{Name: "Text", DataType: DataTypeVisibleString, ByteOffset: 2, Length: 1, AllowedValues: "1"},
		{Name: "Status", DataType: DataTypeUnsigned16S, Length: 2},
	}}
	got := map[string]Severity{}
	for _, is := range r.Check() {
		got[is.Path+" "+is.Rule] = is.Severity
		if strings.Contains(is.Message, "%!") {
			t.Errorf("message %q", is.Message)
		}
	}
	p := "ParameterRecordDataItem[5]"
	want := map[string]Severity{
		p + " parameter-layout":                SeverityError,
		p + "/Const[1] parameter-layout":       SeverityError,
		p + "/Ref[4] parameter-layout":         SeverityError,
		p + "/Ref[5] parameter-layout":         SeverityError,
		p + "/Ref[3] parameter-default":        SeverityWarning,
		p + "/Ref[6] parameter-default":        SeverityError,
		p + "/Ref[7] parameter-allowed-values": SeverityError,
		p + "/Ref[8] parameter-allowed-values": SeverityUnvalidated,
		p + "/Ref[9] parameter-allowed-values": SeverityUnvalidated,
		p + "/Ref[10] parameter-encoding":      SeverityUnvalidated,
		p + " parameter-overlap":               SeverityWarning,
	}
	for k, s := range want {
		if got[k] != s || (s == SeverityError && got[k] != SeverityError) {
			if _, ok := got[k]; !ok {
				t.Errorf("missing %s", k)
				continue
			}
			t.Errorf("%s: %v, want %v", k, got[k], s)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected %s", k)
		}
	}
	var overlaps []string
	for _, is := range r.Check() {
		if is.Rule == RuleParameterOverlap {
			overlaps = append(overlaps, is.Message)
		}
	}
	// Word and Bit; Const[0] and Over; the bits of octet 2 with Range.
	if len(overlaps) < 2 || !strings.Contains(strings.Join(overlaps, "\n"), `Ref[0] "Word" and Ref[1] "Bit"`) ||
		!strings.Contains(strings.Join(overlaps, "\n"), `Const[0] and Ref[2] "Over"`) {
		t.Errorf("overlaps %q", overlaps)
	}
}

// Check lists at most maxRecordIssues issues of a record, and counts the
// others.
func TestParameterCheckLimit(t *testing.T) {
	t.Parallel()
	r := ParameterRecord{Index: 1, Length: 1}
	for i := range maxRecordIssues + 5 {
		r.Fields = append(r.Fields, ParameterField{Name: fmt.Sprint(i), DataType: DataTypeUnsigned16S})
	}
	is := r.Check()
	if len(is) != maxRecordIssues+1 || is[maxRecordIssues].Rule != ruleLimit || !strings.Contains(is[maxRecordIssues].Message, "5 more") {
		t.Fatalf("%d issues, last %+v", len(is), is[len(is)-1])
	}
}

// overlaps pairs every item that starts inside an earlier one with the
// earlier one that ends last.
func TestOverlaps(t *testing.T) {
	t.Parallel()
	got := overlaps([]span{{0, 16, 0}, {2, 4, 1}, {8, 9, 2}, {16, 24, 3}, {20, 21, 4}, {30, 31, 5}})
	want := [][2]int{{0, 1}, {0, 2}, {3, 4}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%v, want %v", got, want)
	}
}

// FuzzParameterAllowedValues: a list parseAllowedValues accepts is of the
// schemas' form, and the comparison never panics.
func FuzzParameterAllowedValues(f *testing.F) {
	for _, s := range []string{"1", "0..9 20", "-2..2.5 1e2", "1e400", "1e401", "0x10", "1..2 x", "", " "} {
		f.Add(s, "1")
	}
	f.Fuzz(func(t *testing.T, list, value string) {
		ranges, err := parseAllowedValues(list)
		if err == nil && list != "" {
			if len(ranges) != strings.Count(list, " ")+1 {
				t.Fatalf("%q: %d ranges", list, len(ranges))
			}
			if strings.ContainsAny(list, "+_/xX\t\n") || strings.HasPrefix(list, " ") || strings.HasSuffix(list, " ") {
				t.Fatalf("%q accepted", list)
			}
		}
		for _, d := range []DataType{DataTypeUnsigned64, DataTypeInteger8, DataTypeFloat32, DataTypeBoolean} {
			_ = (&ParameterField{DataType: d, AllowedValues: list}).allowed(value)
		}
	})
}
