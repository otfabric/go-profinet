// SPDX-License-Identifier: MIT

package typed

import (
	"encoding/base64"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// builtin is an XML Schema built-in simple type.
type builtin uint8

// The built-ins the GSDML schemas use, and their ancestors.
const (
	bAnySimple builtin = iota
	bString
	bNormalizedString
	bToken
	bLanguage
	bName
	bNCName
	bID
	bIDREF
	bNMTOKEN
	bAnyURI
	bQName
	bBoolean
	bFloat
	bDouble
	bDecimal
	bInteger
	bNonNegativeInteger
	bPositiveInteger
	bNonPositiveInteger
	bNegativeInteger
	bLong
	bInt
	bShort
	bByte
	bUnsignedLong
	bUnsignedInt
	bUnsignedShort
	bUnsignedByte
	bDate
	bDateTime
	bTime
	bHexBinary
	bBase64Binary
)

var builtinNames = [...]string{
	"anySimpleType", "string", "normalizedString", "token", "language", "Name", "NCName", "ID", "IDREF", "NMTOKEN",
	"anyURI", "QName", "boolean", "float", "double", "decimal", "integer", "nonNegativeInteger", "positiveInteger",
	"nonPositiveInteger", "negativeInteger", "long", "int", "short", "byte", "unsignedLong", "unsignedInt",
	"unsignedShort", "unsignedByte", "date", "dateTime", "time", "hexBinary", "base64Binary",
}

func (b builtin) String() string { return builtinNames[b] }

func (b builtin) numeric() bool  { return b >= bDecimal && b <= bUnsignedByte }
func (b builtin) floating() bool { return b == bFloat || b == bDouble }

// White space handling.
const (
	wsPreserve uint8 = iota
	wsReplace
	wsCollapse
)

// Varieties.
const (
	vAtomic uint8 = iota
	vList
	vUnion
)

// Facet presence bits of simpleSpec.has.
const (
	hasMinIncl uint16 = 1 << iota
	hasMaxIncl
	hasMinExcl
	hasMaxExcl
	hasLength
	hasMinLength
	hasMaxLength
	hasEnums
)

// simpleSpec is one derivation step of a simple type, compiled from the
// schemas by gsdmlc. A value must satisfy the built-in and every step
// from the type up to the built-in (base).
type simpleSpec struct {
	name    string
	variety uint8
	builtin builtin // atomic: the nearest built-in
	ws      uint8   // the effective white space of this type
	base    int32   // the next user-defined step, or -1
	item    int32   // list
	members []int32 // union
	has     uint16
	enums   []string
	// patterns are indices into patterns, ORed.
	patterns          []int32
	minIncl, maxIncl  string
	minExcl, maxExcl  string
	length, minLength int
	maxLength         int
}

// pattern is an XML Schema pattern translated to RE2 by gsdmlc and
// compiled on first use.
type pattern struct {
	xsd  string // the pattern as the schema writes it
	re2  string // the anchored translation
	once sync.Once
	re   *regexp.Regexp
}

func (p *pattern) match(s string) bool {
	p.once.Do(func() { p.re = regexp.MustCompile(p.re2) })
	return p.re.MatchString(s)
}

func isXMLSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\r' || r == '\n' }

func collapse(s string) string {
	if !strings.ContainsAny(s, " \t\r\n") {
		return s
	}
	return strings.Join(strings.FieldsFunc(s, isXMLSpace), " ")
}

func replaceWS(s string) string {
	if !strings.ContainsAny(s, "\t\r\n") {
		return s
	}
	return strings.Map(func(r rune) rune {
		if isXMLSpace(r) {
			return ' '
		}
		return r
	}, s)
}

func normalizeWS(s string, ws uint8) string {
	switch ws {
	case wsReplace:
		return replaceWS(s)
	case wsCollapse:
		return collapse(s)
	}
	return s
}

// checkSimple validates a lexical value against simples[i]. scope
// resolves QName prefixes; it may be nil.
func (sc *schemaTables) checkSimple(i int32, raw string, scope *Node) error {
	t := &sc.simples[i]
	switch t.variety {
	case vUnion:
		if len(t.members) == 0 {
			// a restriction of a union: the base, then this step's facets
			if err := sc.checkSimple(t.base, raw, scope); err != nil {
				return err
			}
			return sc.enumPattern(t, raw, bString)
		}
		ok := false
		var first error
		for _, m := range t.members {
			if err := sc.checkSimple(m, raw, scope); err == nil {
				ok = true
				break
			} else if first == nil {
				first = err
			}
		}
		if !ok {
			return fmt.Errorf("%q is not valid for any member type of %s", raw, sc.simpleName(i))
		}
		return sc.facets(i, raw, bString)
	case vList:
		v := collapse(raw)
		items := strings.Fields(v)
		item := t.item
		for j := i; item < 0 && j >= 0; j = sc.simples[j].base {
			item = sc.simples[j].item
		}
		for _, it := range items {
			if err := sc.checkSimple(item, it, scope); err != nil {
				return err
			}
		}
		return sc.listFacets(i, v, len(items))
	}
	v := normalizeWS(raw, t.ws)
	if err := checkBuiltin(t.builtin, v, scope); err != nil {
		return err
	}
	return sc.facets(i, v, t.builtin)
}

func (sc *schemaTables) simpleName(i int32) string {
	for ; i >= 0; i = sc.simples[i].base {
		if n := sc.simples[i].name; n != "" {
			return n
		}
	}
	return "an anonymous type"
}

func (sc *schemaTables) listFacets(i int32, v string, n int) error {
	for ; i >= 0; i = sc.simples[i].base {
		t := &sc.simples[i]
		if t.has&hasLength != 0 && n != t.length {
			return fmt.Errorf("%q has %d items, not %d", v, n, t.length)
		}
		if t.has&hasMinLength != 0 && n < t.minLength {
			return fmt.Errorf("%q has %d items, fewer than %d", v, n, t.minLength)
		}
		if t.has&hasMaxLength != 0 && n > t.maxLength {
			return fmt.Errorf("%q has %d items, more than %d", v, n, t.maxLength)
		}
		if err := sc.enumPattern(t, v, bString); err != nil {
			return err
		}
	}
	return nil
}

func (sc *schemaTables) enumPattern(t *simpleSpec, v string, b builtin) error {
	if t.has&hasEnums != 0 {
		found := false
		for _, e := range t.enums {
			if equalValue(b, e, v) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%q is not one of the values of %s", v, nameOr(t.name))
		}
	}
	if len(t.patterns) > 0 {
		found := false
		for _, p := range t.patterns {
			if sc.patterns[p].match(v) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%q does not match the pattern of %s (%s)", v, nameOr(t.name), sc.patterns[t.patterns[0]].xsd)
		}
	}
	return nil
}

func nameOr(n string) string {
	if n == "" {
		return "an anonymous type"
	}
	return n
}

// facets checks every derivation step from i up.
func (sc *schemaTables) facets(i int32, v string, b builtin) error {
	for ; i >= 0; i = sc.simples[i].base {
		t := &sc.simples[i]
		if t.variety == vUnion && t.has&hasEnums == 0 && len(t.patterns) == 0 {
			continue
		}
		if err := sc.enumPattern(t, v, b); err != nil {
			return err
		}
		if t.has&(hasMinIncl|hasMaxIncl|hasMinExcl|hasMaxExcl) != 0 {
			if err := bounds(t, b, v); err != nil {
				return err
			}
		}
		if t.has&(hasLength|hasMinLength|hasMaxLength) != 0 {
			n := valueLength(b, v)
			if t.has&hasLength != 0 && n != t.length {
				return fmt.Errorf("%q has length %d, not %d", v, n, t.length)
			}
			if t.has&hasMinLength != 0 && n < t.minLength {
				return fmt.Errorf("%q has length %d, less than %d", v, n, t.minLength)
			}
			if t.has&hasMaxLength != 0 && n > t.maxLength {
				return fmt.Errorf("%q has length %d, more than %d", v, n, t.maxLength)
			}
		}
	}
	return nil
}

func valueLength(b builtin, v string) int {
	switch b {
	case bHexBinary:
		return len(v) / 2
	case bBase64Binary:
		d, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(v, " ", ""))
		if err != nil {
			return -1
		}
		return len(d)
	default:
		return utf8.RuneCountInString(v)
	}
}

// equalValue compares two lexical values of a built-in in its value space.
func equalValue(b builtin, x, y string) bool {
	switch {
	case b.numeric():
		rx, okx := new(big.Rat).SetString(trimPlus(collapse(x)))
		ry, oky := new(big.Rat).SetString(trimPlus(collapse(y)))
		return okx && oky && rx.Cmp(ry) == 0
	case b.floating():
		fx, ex := parseXSDFloat(collapse(x))
		fy, ey := parseXSDFloat(collapse(y))
		if ex != nil || ey != nil {
			return false
		}
		if fx != fx && fy != fy { // NaN equals NaN in XML Schema 1.0 enumerations
			return true
		}
		return fx == fy
	case b == bBoolean:
		return boolValue(collapse(x)) == boolValue(collapse(y))
	}
	return x == y
}

func boolValue(s string) bool { return s == "true" || s == "1" }

func trimPlus(s string) string { return strings.TrimPrefix(s, "+") }

func bounds(t *simpleSpec, b builtin, v string) error {
	if b.floating() {
		f, err := parseXSDFloat(v)
		if err != nil {
			return err
		}
		chk := func(bit uint16, lim string, ok func(c int) bool, rel string) error {
			if t.has&bit == 0 {
				return nil
			}
			l, err := parseXSDFloat(lim)
			if err != nil {
				return err
			}
			c := 0
			switch {
			case f < l:
				c = -1
			case f > l:
				c = 1
			case f != f || l != l:
				return fmt.Errorf("%s is not comparable with %s", v, lim)
			}
			if !ok(c) {
				return fmt.Errorf("%s is %s %s", v, rel, lim)
			}
			return nil
		}
		for _, e := range []error{
			chk(hasMinIncl, t.minIncl, func(c int) bool { return c >= 0 }, "less than"),
			chk(hasMaxIncl, t.maxIncl, func(c int) bool { return c <= 0 }, "greater than"),
			chk(hasMinExcl, t.minExcl, func(c int) bool { return c > 0 }, "not greater than"),
			chk(hasMaxExcl, t.maxExcl, func(c int) bool { return c < 0 }, "not less than"),
		} {
			if e != nil {
				return e
			}
		}
		return nil
	}
	if !b.numeric() {
		return fmt.Errorf("bounds on %s are not implemented", b)
	}
	r, ok := new(big.Rat).SetString(trimPlus(v))
	if !ok {
		return fmt.Errorf("%q is not a number", v)
	}
	chk := func(bit uint16, lim string, ok func(c int) bool, rel string) error {
		if t.has&bit == 0 {
			return nil
		}
		l, good := new(big.Rat).SetString(trimPlus(collapse(lim)))
		if !good {
			return fmt.Errorf("bound %q is not a number", lim)
		}
		if !ok(r.Cmp(l)) {
			return fmt.Errorf("%s is %s %s", v, rel, collapse(lim))
		}
		return nil
	}
	for _, e := range []error{
		chk(hasMinIncl, t.minIncl, func(c int) bool { return c >= 0 }, "less than"),
		chk(hasMaxIncl, t.maxIncl, func(c int) bool { return c <= 0 }, "greater than"),
		chk(hasMinExcl, t.minExcl, func(c int) bool { return c > 0 }, "not greater than"),
		chk(hasMaxExcl, t.maxExcl, func(c int) bool { return c < 0 }, "not less than"),
	} {
		if e != nil {
			return e
		}
	}
	return nil
}

var (
	reDecimal  = regexp.MustCompile(`^[+-]?([0-9]+(\.[0-9]*)?|\.[0-9]+)$`)
	reInteger  = regexp.MustCompile(`^[+-]?[0-9]+$`)
	reFloat    = regexp.MustCompile(`^([+-]?([0-9]+(\.[0-9]*)?|\.[0-9]+)([Ee][+-]?[0-9]+)?|-?INF|NaN)$`)
	reLanguage = regexp.MustCompile(`^[a-zA-Z]{1,8}(-[a-zA-Z0-9]{1,8})*$`)
	reHex      = regexp.MustCompile(`^([0-9a-fA-F]{2})*$`)
	reDate     = regexp.MustCompile(`^(-?)([0-9]{4,})-([0-9]{2})-([0-9]{2})(Z|[+-][0-9]{2}:[0-9]{2})?$`)
	reDateTime = regexp.MustCompile(`^(-?)([0-9]{4,})-([0-9]{2})-([0-9]{2})T([0-9]{2}):([0-9]{2}):([0-9]{2})(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})?$`)
	reTime     = regexp.MustCompile(`^([0-9]{2}):([0-9]{2}):([0-9]{2})(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})?$`)
	reBase64   = regexp.MustCompile(`^(([A-Za-z0-9+/] ?){4})*(([A-Za-z0-9+/] ?){3}[A-Za-z0-9+/]|([A-Za-z0-9+/] ?){2}[AEIMQUYcgkosw048] ?=|[A-Za-z0-9+/] ?[AQgw] ?= ?=)?$`)
)

func parseXSDFloat(s string) (float64, error) {
	if !reFloat.MatchString(s) {
		return 0, fmt.Errorf("%q is not a floating-point number", s)
	}
	switch s {
	case "INF":
		s = "+Inf"
	case "-INF":
		s = "-Inf"
	}
	// reFloat admits only what ParseFloat reads, so its one error is
	// ErrRange, with the nearest value (an infinity or zero): XML Schema
	// rounds to it.
	f, _ := strconv.ParseFloat(s, 64)
	return f, nil
}

var integerRanges = map[builtin][2]string{
	bNonNegativeInteger: {"0", ""},
	bPositiveInteger:    {"1", ""},
	bNonPositiveInteger: {"", "0"},
	bNegativeInteger:    {"", "-1"},
	bLong:               {"-9223372036854775808", "9223372036854775807"},
	bInt:                {"-2147483648", "2147483647"},
	bShort:              {"-32768", "32767"},
	bByte:               {"-128", "127"},
	bUnsignedLong:       {"0", "18446744073709551615"},
	bUnsignedInt:        {"0", "4294967295"},
	bUnsignedShort:      {"0", "65535"},
	bUnsignedByte:       {"0", "255"},
}

// checkBuiltin checks the lexical space of a built-in (v is normalised).
func checkBuiltin(b builtin, v string, scope *Node) error {
	switch b {
	case bAnySimple, bString, bNormalizedString, bToken, bAnyURI:
		return nil
	case bInteger, bNonNegativeInteger, bPositiveInteger, bNonPositiveInteger, bNegativeInteger,
		bLong, bInt, bShort, bByte, bUnsignedLong, bUnsignedInt, bUnsignedShort, bUnsignedByte:
		return checkInteger(b, v)
	case bLanguage:
		if !reLanguage.MatchString(v) {
			return fmt.Errorf("%q is not a language tag", v)
		}
	case bName:
		if !isName(v, true) {
			return fmt.Errorf("%q is not an XML name", v)
		}
	case bNCName, bID, bIDREF:
		if !isName(v, false) {
			return fmt.Errorf("%q is not an XML name without a colon (NCName)", v)
		}
	case bNMTOKEN:
		if v == "" || strings.IndexFunc(v, func(r rune) bool { return !isNameChar(r) }) >= 0 {
			return fmt.Errorf("%q is not a name token", v)
		}
	case bQName:
		prefix, local, ok := strings.Cut(v, ":")
		if !ok {
			local, prefix = prefix, ""
		}
		if !isName(local, false) || (ok && !isName(prefix, false)) {
			return fmt.Errorf("%q is not a QName", v)
		}
		if ok && scope != nil {
			if _, found := scope.lookupPrefix(prefix); !found {
				return fmt.Errorf("%q: the prefix %q is not declared", v, prefix)
			}
		}
	case bBoolean:
		if v != "true" && v != "false" && v != "1" && v != "0" {
			return fmt.Errorf("%q is not a boolean", v)
		}
	case bFloat, bDouble:
		_, err := parseXSDFloat(v)
		return err
	case bDecimal:
		if !reDecimal.MatchString(v) {
			return fmt.Errorf("%q is not a decimal number", v)
		}
	case bDate:
		m := reDate.FindStringSubmatch(v)
		if m == nil || !validDate(m[2], m[3], m[4]) || !validZone(m[5]) {
			return fmt.Errorf("%q is not a date", v)
		}
	case bDateTime:
		m := reDateTime.FindStringSubmatch(v)
		if m == nil || !validDate(m[2], m[3], m[4]) || !validClock(m[5], m[6], m[7]) || !validZone(m[9]) {
			return fmt.Errorf("%q is not a date and time", v)
		}
	case bTime:
		m := reTime.FindStringSubmatch(v)
		if m == nil || !validClock(m[1], m[2], m[3]) || !validZone(m[5]) {
			return fmt.Errorf("%q is not a time", v)
		}
	case bHexBinary:
		if !reHex.MatchString(v) {
			return fmt.Errorf("%q is not hexBinary", v)
		}
	case bBase64Binary:
		if !reBase64.MatchString(v) {
			return fmt.Errorf("%q is not base64Binary", v)
		}
	default:
		return fmt.Errorf("built-in %s is not implemented", b)
	}
	return nil
}

func checkInteger(b builtin, v string) error {
	if !reInteger.MatchString(v) {
		return fmt.Errorf("%q is not an integer", v)
	}
	r, ok := integerRanges[b]
	if !ok {
		return nil
	}
	n, _ := new(big.Int).SetString(trimPlus(v), 10)
	if r[0] != "" {
		lo, _ := new(big.Int).SetString(r[0], 10)
		if n.Cmp(lo) < 0 {
			return fmt.Errorf("%s is out of the range of %s", v, b)
		}
	}
	if r[1] != "" {
		hi, _ := new(big.Int).SetString(r[1], 10)
		if n.Cmp(hi) > 0 {
			return fmt.Errorf("%s is out of the range of %s", v, b)
		}
	}
	return nil
}

func validDate(y, m, d string) bool {
	year, _ := strconv.Atoi(y)
	mon, _ := strconv.Atoi(m)
	day, _ := strconv.Atoi(d)
	if year == 0 || mon < 1 || mon > 12 || day < 1 {
		return false
	}
	if len(y) > 4 && y[0] == '0' {
		return false
	}
	dim := [...]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}[mon-1]
	if mon == 2 && (year%4 == 0 && (year%100 != 0 || year%400 == 0)) {
		dim = 29
	}
	return day <= dim
}

func validClock(h, m, s string) bool {
	hh, _ := strconv.Atoi(h)
	mm, _ := strconv.Atoi(m)
	ss, _ := strconv.Atoi(s)
	if hh == 24 {
		return mm == 0 && ss == 0
	}
	return hh < 24 && mm < 60 && ss < 60
}

func validZone(z string) bool {
	if z == "" || z == "Z" {
		return true
	}
	hh, _ := strconv.Atoi(z[1:3])
	mm, _ := strconv.Atoi(z[4:6])
	return mm < 60 && (hh < 14 || (hh == 14 && mm == 0))
}

// isName reports whether s is an XML 1.0 (Fifth Edition) Name; without
// colons, an NCName.
func isName(s string, colon bool) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == ':' && !colon {
			return false
		}
		if i == 0 {
			if !isNameStart(r) {
				return false
			}
		} else if !isNameChar(r) {
			return false
		}
	}
	return true
}

func isNameStart(r rune) bool {
	switch {
	case r == ':' || r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'):
		return true
	case r >= 0xC0 && r <= 0xD6, r >= 0xD8 && r <= 0xF6, r >= 0xF8 && r <= 0x2FF,
		r >= 0x370 && r <= 0x37D, r >= 0x37F && r <= 0x1FFF, r >= 0x200C && r <= 0x200D,
		r >= 0x2070 && r <= 0x218F, r >= 0x2C00 && r <= 0x2FEF, r >= 0x3001 && r <= 0xD7FF,
		r >= 0xF900 && r <= 0xFDCF, r >= 0xFDF0 && r <= 0xFFFD, r >= 0x10000 && r <= 0xEFFFF:
		return true
	}
	return false
}

func isNameChar(r rune) bool {
	return isNameStart(r) || r == '-' || r == '.' || (r >= '0' && r <= '9') || r == 0xB7 ||
		(r >= 0x300 && r <= 0x36F) || (r >= 0x203F && r <= 0x2040)
}
