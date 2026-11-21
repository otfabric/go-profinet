// SPDX-License-Identifier: MIT

package gsdml

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet"
)

// TestEmptyAttrs: only unqualified attributes among the names, and only
// when their value is "".
func TestEmptyAttrs(t *testing.T) {
	attrs := []xml.Attr{
		{Name: xml.Name{Local: "BitLength"}, Value: ""},
		{Name: xml.Name{Local: "BitOffset"}, Value: "3"},
		{Name: xml.Name{Space: "urn:x", Local: "DataType"}, Value: ""},
		{Name: xml.Name{Local: "Other"}, Value: ""},
	}
	if got := emptyAttrs(attrs, []trackedAttr{{"BitLength", "Unsigned8"}, {"BitOffset", "Unsigned8"}, {"DataType", "DataType"}}); !slices.Equal(got, []string{"BitLength"}) {
		t.Fatalf("%q", got)
	}
	if got := emptyAttrs(attrs, []trackedAttr{{"Visible", "Boolean"}}); got != nil {
		t.Fatalf("%q", got)
	}
}

// fixtureVariant returns the modular fixture with old replaced by new,
// failing when old is not in it.
func fixtureVariant(t *testing.T, old, new string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/fixtures/GSDML-V2.43-OTFabric-ModularFixture-20261027.xml")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(old)) {
		t.Fatalf("the fixture no longer holds %s", old)
	}
	return bytes.Replace(b, []byte(old), []byte(new), 1)
}

// TestEmptyAttributeIsNotAbsent: a typed optional attribute a document
// gives as "" is neither absent nor valid: Validate reports it, the Ref
// BitLength and BitOffset take no default for it, and the Boolean
// capabilities ToModel reads are refused. Before v0.12.0 the generated
// types could not tell "" from absent, and both took the default.
func TestEmptyAttributeIsNotAbsent(t *testing.T) {
	const ref = `<Ref ByteOffset="0" BitOffset="0" BitLength="2" DataType="BitArea" DefaultValue="0" TextId="T_Substitute" ValueItemTarget="V_SUBST"/>`
	for _, tc := range []struct {
		name, old, new, dapID, attr string
	}{
		{"BitLength", ref, `<Ref ByteOffset="0" BitOffset="0" BitLength="" DataType="BitArea" DefaultValue="0" TextId="T_Substitute" ValueItemTarget="V_SUBST"/>`, "DAP_2P", "BitLength"},
		{"BitOffset", ref, `<Ref ByteOffset="0" BitOffset="" BitLength="2" DataType="BitArea" DefaultValue="0" TextId="T_Substitute" ValueItemTarget="V_SUBST"/>`, "DAP_2P", "BitOffset"},
		{"LLDP_NoD_Supported", `LLDP_NoD_Supported="true" ResetToFactoryModes="2">`, `LLDP_NoD_Supported="" ResetToFactoryModes="2">`, "DAP_1P", "LLDP_NoD_Supported"},
		{"DCP_HelloSupported", `<InterfaceSubmoduleItem ID="IF_1P"`, `<InterfaceSubmoduleItem ID="IF_1P" DCP_HelloSupported=""`, "DAP_1P", "DCP_HelloSupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := ParseBytes(fixtureVariant(t, tc.old, tc.new))
			if err != nil {
				t.Fatal(err)
			}
			verr := Validate(doc)
			if verr == nil || !strings.Contains(verr.Error(), "attribute "+tc.attr+": empty") {
				t.Errorf("Validate: %v", verr)
			}
			if _, err := ToModel(doc, tc.dapID); !errors.Is(err, profinet.ErrMalformed) {
				t.Errorf("ToModel: %v", err)
			}
		})
	}
}

// TestEmptyAttributeAllowed: an empty attribute whose type admits "" (a
// string, a TokenList, or one always written, such as ApplicationClass) is
// not reported, and the fixture as published validates.
func TestEmptyAttributeAllowed(t *testing.T) {
	src := fixtureVariant(t,
		`<Ref ByteOffset="0" BitOffset="0" BitLength="2" DataType="BitArea" DefaultValue="0" TextId="T_Substitute" ValueItemTarget="V_SUBST"/>`,
		`<Ref ByteOffset="0" BitOffset="0" BitLength="2" DataType="BitArea" DefaultValue="0" AllowedValues="" TextId="T_Substitute" ValueItemTarget="V_SUBST"/>`)
	if !bytes.Contains(src, []byte(`ApplicationClass=""`)) {
		t.Fatal("the fixture no longer has an empty ApplicationClass")
	}
	doc, err := ParseBytes(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(doc); err != nil {
		t.Fatal(err)
	}
	if _, err := ToModel(doc, "DAP_2P"); err != nil {
		t.Fatal(err)
	}
}

// TestEmptyAttributeBuilt: a document built in Go cannot say "present but
// empty", so "" there is absent, as it always was: the defaults apply.
func TestEmptyAttributeBuilt(t *testing.T) {
	r := &Ref{BitOffset: "", BitLength: ""}
	if r.bitOffsetOrDefault() != "0" || r.bitLengthOrDefault() != "1" {
		t.Fatalf("%q %q", r.bitOffsetOrDefault(), r.bitLengthOrDefault())
	}
	var parsed Ref
	if err := xml.Unmarshal([]byte(`<Ref BitLength="" BitOffset="4"/>`), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.bitLengthOrDefault() != "" || parsed.bitOffsetOrDefault() != "4" || !emptyIn(parsed.empty, "BitLength") || emptyIn(parsed.empty, "BitOffset") {
		t.Fatalf("%q %q %q", parsed.bitLengthOrDefault(), parsed.bitOffsetOrDefault(), parsed.empty)
	}
	// A malformed element is refused, as without the tracking.
	if err := xml.Unmarshal([]byte(`<Ref BitLength=""><x></Ref>`), &parsed); err == nil {
		t.Fatal("malformed Ref decoded")
	}
}

// TestEveryTrackedAttribute: for every element of the catalog, each
// optional, typed, unqualified attribute (the rule of gengsdml's
// trackedAttrs) given as "" is recorded by the element's UnmarshalXML and
// reported by its validate when its type does not admit "", and no other
// attribute is; a typed attribute that is present with a value is not
// recorded.
func TestEveryTrackedAttribute(t *testing.T) {
	raw, err := os.ReadFile("schema/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var cat struct {
		Namespace, Root string
		Elements        []struct {
			Name  string
			Attrs []string
		}
	}
	if err := json.Unmarshal(raw, &cat); err != nil {
		t.Fatal(err)
	}
	types := elementTypes(reflect.TypeFor[Document]())
	n := 0
	for _, e := range cat.Elements {
		var tracked []trackedAttr
		for _, s := range e.Attrs {
			decl, _, _ := strings.Cut(s, "=")
			i := strings.LastIndex(decl, ":")
			name, typ := decl[:i], decl[i+1:]
			if strings.HasSuffix(typ, "!") || strings.HasSuffix(typ, "~") || typ == "string" || strings.HasPrefix(name, "xml:") {
				continue
			}
			tracked = append(tracked, trackedAttr{name, typ})
		}
		if len(tracked) == 0 {
			continue
		}
		typ, ok := types[e.Name]
		if !ok {
			t.Errorf("%s: no Go type reachable from Document", e.Name)
			continue
		}
		n++
		var src strings.Builder
		src.WriteString("<" + e.Name)
		if e.Name == cat.Root {
			src.WriteString(` xmlns="` + cat.Namespace + `"`)
		}
		var want, reported []string
		for _, a := range tracked {
			src.WriteString(" " + a.name + `=""`)
			want = append(want, a.name)
			st := simpleTypes[a.typ]
			if (st.enum != nil && !st.enum[""]) || (st.pattern != nil && !st.pattern.MatchString("")) || (st.pattern == nil && st.enum == nil && st.uintBits > 0) {
				reported = append(reported, a.name)
			}
		}
		src.WriteString("/>")
		x := reflect.New(typ)
		if _, ok := x.Interface().(xml.Unmarshaler); !ok {
			t.Errorf("%s has no UnmarshalXML", e.Name)
			continue
		}
		if err := xml.Unmarshal([]byte(src.String()), x.Interface()); err != nil {
			t.Errorf("%s: %v", e.Name, err)
			continue
		}
		emptyField := x.Elem().FieldByName("empty")
		var got []string
		for i := range emptyField.Len() {
			got = append(got, emptyField.Index(i).String())
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: recorded %q, want %q", e.Name, got, want)
		}
		v := &validator{ids: map[string]string{}, texts: map[string]bool{}}
		x.Interface().(interface{ validate(*validator) }).validate(v)
		for _, a := range reported {
			found := false
			for _, is := range v.issues {
				found = found || strings.Contains(is.Message, "attribute "+a+": empty")
			}
			if !found {
				t.Errorf("%s: empty %s not reported", e.Name, a)
			}
		}
		// The same attributes with a value are not recorded.
		withValue := strings.ReplaceAll(src.String(), `=""`, `="1"`)
		y := reflect.New(typ)
		if err := xml.Unmarshal([]byte(withValue), y.Interface()); err != nil || y.Elem().FieldByName("empty").Len() != 0 {
			t.Errorf("%s with values: %v, recorded %d", e.Name, err, y.Elem().FieldByName("empty").Len())
		}
	}
	if n == 0 {
		t.Fatal("no element with tracked attributes")
	}
}

// elementTypes maps the XML name of every struct type reachable from t to
// the type.
func elementTypes(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	var visit func(reflect.Type)
	visit = func(t reflect.Type) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return
		}
		f, ok := t.FieldByName("XMLName")
		if !ok {
			return
		}
		tag := f.Tag.Get("xml")
		name := tag[strings.LastIndex(tag, " ")+1:]
		if _, seen := out[name]; seen {
			return
		}
		out[name] = t
		for i := range t.NumField() {
			if t.Field(i).IsExported() {
				visit(t.Field(i).Type)
			}
		}
	}
	visit(t)
	return out
}
