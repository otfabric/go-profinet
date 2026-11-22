// SPDX-License-Identifier: MIT

package typed

import (
	"math"
	"strings"
	"testing"
)

func TestCheckBuiltin(t *testing.T) {
	scope := &Node{ns: map[string]string{"p": "urn:p"}}
	cases := []struct {
		b    builtin
		v    string
		good bool
	}{
		{bString, " any ", true}, {bAnyURI, "x y", true},
		{bInteger, "-0012", true}, {bInteger, "1.0", false}, {bInteger, "", false},
		{bNonNegativeInteger, "0", true}, {bNonNegativeInteger, "-1", false},
		{bPositiveInteger, "0", false}, {bNonPositiveInteger, "1", false}, {bNegativeInteger, "-1", true},
		{bLong, "9223372036854775808", false}, {bInt, "-2147483648", true}, {bShort, "32768", false},
		{bByte, "+127", true}, {bUnsignedLong, "18446744073709551615", true}, {bUnsignedInt, "4294967296", false},
		{bUnsignedShort, "65535", true}, {bUnsignedByte, "256", false},
		{bLanguage, "en-GB", true}, {bLanguage, "toolonglanguage", false},
		{bName, "a:b", true}, {bName, "1a", false}, {bNCName, "a:b", false}, {bID, "_x.y-z", true}, {bIDREF, "", false},
		{bNMTOKEN, "1-a", true}, {bNMTOKEN, "a b", false}, {bNMTOKEN, "", false},
		{bQName, "p:x", true}, {bQName, "x", true}, {bQName, "q:x", false}, {bQName, "p:", false}, {bQName, ":x", false},
		{bBoolean, "1", true}, {bBoolean, "TRUE", false},
		{bFloat, "1.5e3", true}, {bDouble, "-INF", true}, {bDouble, "NaN", true}, {bDouble, "inf", false}, {bDouble, "1e999", true},
		{bDecimal, "-.5", true}, {bDecimal, "5.", true}, {bDecimal, "1e3", false}, {bDecimal, ".", false},
		{bDate, "2024-02-29", true}, {bDate, "2023-02-29", false}, {bDate, "1900-02-29", false}, {bDate, "2000-02-29Z", true},
		{bDate, "0000-01-01", false}, {bDate, "02024-01-01", false}, {bDate, "2024-13-01", false}, {bDate, "2024-04-31", false},
		{bDate, "2024-01-01+14:00", true}, {bDate, "2024-01-01+14:01", false}, {bDate, "2024-01-01+13:60", false},
		{bDateTime, "2024-01-01T24:00:00", true}, {bDateTime, "2024-01-01T24:00:01", false}, {bDateTime, "2024-01-01T23:59:60", false},
		{bDateTime, "2024-01-01T12:00:00.5-05:00", true}, {bDateTime, "2024-01-01 12:00:00", false},
		{bTime, "23:59:59Z", true}, {bTime, "23:60:00", false}, {bTime, "1:00:00", false},
		{bHexBinary, "0aFF", true}, {bHexBinary, "abc", false},
		{bBase64Binary, "AAEC", true}, {bBase64Binary, "AA==", true}, {bBase64Binary, "AAE=", true}, {bBase64Binary, "A===", false}, {bBase64Binary, "AB==", false},
		{builtin(200), "x", false},
	}
	for _, c := range cases {
		err := checkBuiltin(c.b, c.v, scope)
		if (err == nil) != c.good {
			name := "unknown"
			if int(c.b) < len(builtinNames) {
				name = c.b.String()
			}
			t.Errorf("%s %q: %v", name, c.v, err)
		}
	}
}

func TestEqualValue(t *testing.T) {
	cases := []struct {
		b    builtin
		x, y string
		eq   bool
	}{
		{bDecimal, "1.0", "+1", true}, {bInteger, "01", "1", true}, {bInteger, "1", "2", false},
		{bDouble, "1e0", "1.0", true}, {bDouble, "NaN", "NaN", true}, {bDouble, "NaN", "1", false}, {bDouble, "x", "x", false},
		{bBoolean, "1", "true", true}, {bBoolean, "0", "true", false},
		{bString, "a", "a ", false},
	}
	for _, c := range cases {
		if got := equalValue(c.b, c.x, c.y); got != c.eq {
			t.Errorf("%s %q %q: %v", c.b, c.x, c.y, got)
		}
	}
}

func TestBounds(t *testing.T) {
	num := &simpleSpec{has: hasMinIncl | hasMaxExcl, minIncl: "0", maxExcl: " 10 "}
	flt := &simpleSpec{has: hasMinExcl | hasMaxIncl, minExcl: "0", maxIncl: "1.5"}
	cases := []struct {
		t    *simpleSpec
		b    builtin
		v    string
		good bool
	}{
		{num, bDecimal, "0", true}, {num, bDecimal, "-0.1", false}, {num, bDecimal, "9.99", true}, {num, bDecimal, "10", false},
		{num, bDecimal, "x", false}, {num, bString, "1", false},
		{&simpleSpec{has: hasMinIncl, minIncl: "x"}, bDecimal, "1", false},
		{&simpleSpec{has: hasMinExcl | hasMaxIncl, minExcl: "0", maxIncl: "5"}, bInteger, "5", true},
		{flt, bDouble, "0", false}, {flt, bDouble, "1.5", true}, {flt, bDouble, "1.6", false}, {flt, bDouble, "NaN", false},
		{flt, bDouble, "x", false}, {&simpleSpec{has: hasMinIncl, minIncl: "x"}, bDouble, "1", false},
		{&simpleSpec{has: hasMaxExcl, maxExcl: "INF"}, bFloat, "1e38", true},
	}
	for i, c := range cases {
		if err := bounds(c.t, c.b, c.v); (err == nil) != c.good {
			t.Errorf("%d: %s %q: %v", i, c.b, c.v, err)
		}
	}
	if f, err := parseXSDFloat("INF"); err != nil || !math.IsInf(f, 1) {
		t.Error("INF")
	}
}

// testTables is a hand-made schema of simple types, for the derivation chains
// gsdmlc emits.
func testTables() *schemaTables {
	sc := &schemaTables{
		patterns: []pattern{{xsd: "[A-Z]+", re2: `^(?:[A-Z]+)$`}, {xsd: "[0-9]+", re2: `^(?:[0-9]+)$`}},
	}
	sc.simples = []simpleSpec{
		/*0*/ {name: "Upper", variety: vAtomic, builtin: bToken, ws: wsCollapse, base: -1, item: -1, patterns: []int32{0}, has: hasMinLength | hasMaxLength, minLength: 2, maxLength: 4},
		/*1*/ {name: "Small", variety: vAtomic, builtin: bUnsignedByte, ws: wsCollapse, base: -1, item: -1, has: hasMaxIncl | hasEnums, maxIncl: "9", enums: []string{"1", "02", "9"}},
		/*2*/ {name: "List", variety: vList, base: -1, item: 1, has: hasMinLength | hasMaxLength, minLength: 1, maxLength: 3},
		/*3*/ {name: "", variety: vList, base: 2, item: -1, has: hasLength, length: 2},
		/*4*/ {name: "Union", variety: vUnion, base: -1, item: -1, members: []int32{0, 1}},
		/*5*/ {name: "", variety: vUnion, base: 4, item: -1, has: hasEnums, enums: []string{"AB", "9"}},
		/*6*/ {name: "Hex", variety: vAtomic, builtin: bHexBinary, ws: wsCollapse, base: -1, item: -1, has: hasLength, length: 2},
		/*7*/ {name: "B64", variety: vAtomic, builtin: bBase64Binary, ws: wsCollapse, base: -1, item: -1, has: hasMaxLength, maxLength: 2},
		/*8*/ {name: "Norm", variety: vAtomic, builtin: bNormalizedString, ws: wsReplace, base: -1, item: -1, has: hasLength, length: 3},
		/*9*/ {name: "", variety: vAtomic, builtin: bToken, ws: wsCollapse, base: 0, item: -1, patterns: []int32{1}},
		/*10*/ {name: "UnionPat", variety: vUnion, base: -1, item: -1, members: []int32{1}, patterns: []int32{1}},
		/*11*/ {name: "ListPat", variety: vList, base: -1, item: 1, patterns: []int32{1}},
	}
	return sc
}

func TestCheckSimple(t *testing.T) {
	sc := testTables()
	cases := []struct {
		i    int32
		v    string
		good string // "" valid, else a substring of the error
	}{
		{0, " AB ", ""}, {0, "A", "length 1, less than 2"}, {0, "ABCDE", "more than 4"}, {0, "Ab", "pattern of Upper"},
		{1, "2", ""}, {1, "02", ""}, {1, "3", "not one of the values of Small"}, {1, "300", "out of the range"},
		{2, "1 2 9", ""}, {2, "", "fewer than 1"}, {2, "1 1 1 1", "more than 3"}, {2, "1 x", "not an integer"},
		{3, "1 2", ""}, {3, "1", "not 2"},
		{4, "AB", ""}, {4, "9", ""}, {4, "x", "not valid for any member type of Union"},
		{5, "AB", ""}, {5, "9", ""}, {5, "2", "not one of the values of an anonymous type"}, {5, "x", "any member type"},
		{6, "0aff", ""}, {6, "0a", "length 1, not 2"},
		{7, "AAE=", ""}, {7, "AAEC", "more than 2"},
		{8, "a\tb", ""}, {8, "a\tbc", "not 3"},
		{9, "AB", "pattern of an anonymous type"}, {9, "12", "pattern of Upper"},
		{10, "3", "not valid for any"}, {10, "2", ""},
		{11, "1 2", "pattern of ListPat"},
	}
	for _, c := range cases {
		err := sc.checkSimple(c.i, c.v, nil)
		switch {
		case c.good == "" && err != nil:
			t.Errorf("%d %q: %v", c.i, c.v, err)
		case c.good != "" && (err == nil || !strings.Contains(err.Error(), c.good)):
			t.Errorf("%d %q: %v, want %q", c.i, c.v, err, c.good)
		}
	}
	if sc.simpleName(3) != "List" || sc.simpleName(5) != "Union" {
		t.Error("simpleName")
	}
	sc.simples = append(sc.simples, simpleSpec{base: -1})
	if sc.simpleName(int32(len(sc.simples)-1)) != "an anonymous type" {
		t.Error("simpleName of an anonymous type")
	}
	if valueLength(bBase64Binary, "!!") != -1 || valueLength(bString, "äb") != 2 {
		t.Error("valueLength")
	}
	if replaceWS("a\nb\r") != "a b " || normalizeWS(" a  b ", wsPreserve) != " a  b " {
		t.Error("white space")
	}
}
