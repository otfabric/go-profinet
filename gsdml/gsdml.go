// SPDX-License-Identifier: MIT

// Package gsdml reads, validates and writes GSDML (General Station
// Description Markup Language) files, the XML device descriptions of
// PROFINET IO devices, and converts between them and the device models of
// package model.
//
// The document types (zz_generated.go) are generated from the catalog
// schema/catalog.json by internal/cmd/gengsdml; SCHEMA.md lists what they
// model. Elements and attributes the catalog does not define are kept in
// the Unknown and Attrs fields and written back, so reading and writing a
// file does not lose information. Comments and the order of unknown
// elements among known ones are not preserved.
//
// The official GSDML schemas are licensed by PROFIBUS & PROFINET
// International to its members and are not part of this package; Validate
// checks the structural and lexical rules of the catalog and the
// references between elements, which covers what the device model relies
// on. It is not a substitute for PI's GSDML checker.
package gsdml

//go:generate go run ../internal/cmd/gengsdml -root ..

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/otfabric/go-profinet"
)

// MaxDocumentSize bounds the size of a document Parse reads. Vendor files
// reach 15 MB; the bound leaves room.
const MaxDocumentSize = 64 << 20

// maxElements bounds the elements of a document ParseBytes reads. They
// are counted as the start tags of the text before anything is decoded,
// so a start tag inside a comment or a CDATA section counts as well.
// Vendor files of 15 MB hold about 230 000 elements; a document of
// MaxDocumentSize with their density holds about a million.
const maxElements = 1 << 20

// Node is an XML element the catalog does not define, kept verbatim.
type Node struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Content []byte     `xml:",innerxml"`
}

// Document is a parsed GSDML file.
type Document = ISO15745Profile

// Parse reads a GSDML document. UTF-8 (with or without a byte order mark)
// and ISO-8859-1 encodings are accepted. A document larger than
// MaxDocumentSize, or with more elements than ParseBytes reads, is refused
// with an error matching profinet.ErrResourceExhausted.
func Parse(r io.Reader) (*Document, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxDocumentSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxDocumentSize {
		return nil, fmt.Errorf("gsdml: %w: document larger than %d octets", profinet.ErrResourceExhausted, MaxDocumentSize)
	}
	return ParseBytes(data)
}

// ParseBytes reads a GSDML document from data. A document with more than
// 1 048 576 elements is refused with an error matching
// profinet.ErrResourceExhausted before it is decoded.
func ParseBytes(data []byte) (*Document, error) {
	return parseBytes(data, maxElements)
}

// startTags counts the start tags of an XML text: every '<' that is not
// followed by '/', '!' or '?'. It stops counting above limit.
func startTags(data []byte, limit int) int {
	n := 0
	for n <= limit {
		i := bytes.IndexByte(data, '<')
		if i < 0 || i+1 >= len(data) {
			break
		}
		if c := data[i+1]; c != '/' && c != '!' && c != '?' {
			n++
		}
		data = data[i+1:]
	}
	return n
}

// parseBytes is ParseBytes with the element bound as an argument.
func parseBytes(data []byte, elements int) (*Document, error) {
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	if startTags(data, elements) > elements {
		return nil, fmt.Errorf("gsdml: %w: document with more than %d elements", profinet.ErrResourceExhausted, elements)
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	d.CharsetReader = charsetReader
	var doc Document
	if err := d.Decode(&doc); err != nil {
		return nil, fmt.Errorf("gsdml: %w: %v", profinet.ErrMalformed, err)
	}
	if doc.XMLName.Space != Namespace {
		return nil, fmt.Errorf("gsdml: %w: namespace %q is not the GSDML namespace", profinet.ErrMalformed, doc.XMLName.Space)
	}
	normalize(&doc)
	return &doc, nil
}

const nsXML = "http://www.w3.org/XML/1998/namespace"

// normalize rewrites the namespaced names of preserved attributes and
// elements into their prefixed form ("xsi:schemaLocation") with the
// prefixes the document declared, and drops the namespace declarations
// encoding/xml would otherwise write twice. Encoding the result writes
// the same names back, so that parsing and encoding are stable.
func normalize(doc *Document) {
	prefixes := map[string]string{nsXML: "xml"}
	walk(reflect.ValueOf(doc), func(attrs *[]xml.Attr, _ *Node) {
		if attrs == nil {
			return
		}
		for _, a := range *attrs {
			if a.Name.Space == "xmlns" {
				if _, ok := prefixes[a.Value]; !ok {
					prefixes[a.Value] = a.Name.Local
				}
			}
		}
	})
	walk(reflect.ValueOf(doc), func(attrs *[]xml.Attr, n *Node) {
		if n != nil {
			if p, ok := prefixes[n.XMLName.Space]; ok && n.XMLName.Space != "" {
				n.XMLName = xml.Name{Local: p + ":" + n.XMLName.Local}
			}
			attrs = &n.Attrs
		}
		out := (*attrs)[:0]
		for _, a := range *attrs {
			switch {
			case a.Name.Space == "" && a.Name.Local == "xmlns":
				continue // the default namespace is written by the encoder
			case a.Name.Space == "xmlns":
				a.Name = xml.Name{Local: "xmlns:" + a.Name.Local}
			case a.Name.Space != "":
				if p, ok := prefixes[a.Name.Space]; ok {
					a.Name = xml.Name{Local: p + ":" + a.Name.Local}
				}
			}
			out = append(out, a)
		}
		*attrs = out
	})
}

// walk calls fn with the Attrs of every element and with every preserved
// Node of the document.
func walk(v reflect.Value, fn func(attrs *[]xml.Attr, n *Node)) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		if n, ok := v.Interface().(*Node); ok {
			fn(nil, n)
			return
		}
		walk(v.Elem(), fn)
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			walk(v.Index(i), fn)
		}
	case reflect.Struct:
		for _, f := range v.Fields() {
			if a, ok := f.Addr().Interface().(*[]xml.Attr); ok {
				fn(a, nil)
				continue
			}
			if f.Kind() == reflect.Pointer || f.Kind() == reflect.Slice {
				walk(f, fn)
			}
		}
	default:
	}
}

func charsetReader(label string, in io.Reader) (io.Reader, error) {
	switch strings.ToLower(label) {
	case "utf-8", "utf8", "us-ascii", "ascii":
		return in, nil
	case "iso-8859-1", "latin1", "iso8859-1", "iso_8859-1", "windows-1252", "cp1252":
		b, err := io.ReadAll(in)
		if err != nil {
			return nil, err
		}
		var out bytes.Buffer
		out.Grow(len(b))
		for _, c := range b {
			out.WriteRune(rune(c))
		}
		return &out, nil
	default:
		return nil, fmt.Errorf("gsdml: %w: encoding %q", profinet.ErrUnsupported, label)
	}
}

// Encode writes the document as indented UTF-8 XML with an XML
// declaration.
func Encode(w io.Writer, doc *Document) error {
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	e := xml.NewEncoder(w)
	e.Indent("", "  ")
	doc.XMLName = xml.Name{Space: Namespace, Local: "ISO15745Profile"}
	if err := e.Encode(doc); err != nil {
		return fmt.Errorf("gsdml: encode: %w", err)
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// Marshal returns the encoded document.
func Marshal(doc *Document) ([]byte, error) {
	var b bytes.Buffer
	if err := Encode(&b, doc); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

type simpleType struct {
	doc      string
	pattern  *regexp.Regexp
	enum     map[string]bool
	uintBits int
}

// Issue is a problem Validate found.
type Issue struct {
	// Path is the element path, for example
	// "ISO15745Profile/ProfileBody/DeviceIdentity".
	Path    string `json:"path"`
	Message string `json:"message"`
	// Severity grades the issue; Validate reports only errors.
	Severity Severity `json:"severity"`
	// Rule names the rule (Rule*); empty means RuleStructure. The issue
	// that closes a severity with more issues than Check lists has the
	// rule "limit".
	Rule string `json:"rule,omitempty"`
}

func (i Issue) String() string { return i.Path + ": " + i.Message }

// ValidationError lists the issues of a document. It matches
// profinet.ErrMalformed.
type ValidationError struct {
	Issues []Issue
}

func (e *ValidationError) Error() string {
	if len(e.Issues) == 1 {
		return "gsdml: " + e.Issues[0].String()
	}
	return fmt.Sprintf("gsdml: %d issues, the first: %s", len(e.Issues), e.Issues[0])
}

// Is makes a ValidationError match profinet.ErrMalformed.
func (e *ValidationError) Is(target error) bool { return target == profinet.ErrMalformed }

// maxIssues is the number of issues of one severity Check lists. Further
// issues of that severity are counted, and one closing issue with the rule
// ruleLimit says how many there are.
const maxIssues = 1 << 14

// ruleLimit is the Rule of the issue that closes a severity of which
// Check found more than maxIssues issues.
const ruleLimit = "limit"

// severities is the number of Severity values.
const severities = 3

type validator struct {
	path []string
	// joined is the path as one string, empty until where builds it.
	joined string
	issues []Issue
	ids    map[string]string
	refs   []ref
	texts  map[string]bool
	tids   []ref
	// listed and omitted count the issues per severity: those in issues
	// and those beyond maxIssues.
	listed, omitted [severities]int
	// slots is the working memory of the slot rules, made when a device
	// access point first needs it.
	slots *slotWork
}

// room reports whether one more issue of severity s is listed, and
// counts it either way. The caller builds the issue only when it is.
func (v *validator) room(s Severity) bool {
	if s < 0 || int(s) >= severities {
		return true
	}
	if v.listed[s] < maxIssues {
		v.listed[s]++
		return true
	}
	v.omitted[s]++
	return false
}

// add lists an issue unless its severity is full.
func (v *validator) add(path string, s Severity, rule, format string, a ...any) {
	if v.room(s) {
		v.issues = append(v.issues, Issue{Path: path, Message: fmt.Sprintf(format, a...), Severity: s, Rule: rule})
	}
}

// closeIssues adds, for every severity with more issues than are listed,
// the issue that says how many were left out. An error among them keeps
// Validate failing.
func (v *validator) closeIssues() {
	for s, n := range v.omitted {
		if n > 0 {
			v.issues = append(v.issues, Issue{Path: "ISO15745Profile", Severity: Severity(s), Rule: ruleLimit,
				Message: fmt.Sprintf("%d more issues of severity %s are not listed: Check lists the first %d of a severity", n, Severity(s), maxIssues)})
		}
	}
}

type ref struct {
	path, kind, id string
}

func (v *validator) push(e string) { v.path, v.joined = append(v.path, e), "" }
func (v *validator) pop()          { v.path, v.joined = v.path[:len(v.path)-1], "" }

// where returns the path of the element being validated. It is built once
// per element, whatever the number of its attributes and issues.
func (v *validator) where() string {
	if v.joined == "" {
		v.joined = strings.Join(v.path, "/")
	}
	return v.joined
}

func (v *validator) report(format string, a ...any) {
	if v.room(SeverityError) {
		v.issues = append(v.issues, Issue{Path: v.where(), Message: fmt.Sprintf(format, a...), Rule: RuleStructure})
	}
}

func (v *validator) missing(child string) { v.report("missing required element %s", child) }

func (v *validator) attr(name, value, typ string, required bool) {
	if value == "" {
		if required {
			v.report("missing required attribute %s", name)
		}
		return
	}
	t := simpleTypes[typ]
	switch {
	case t.enum != nil && !t.enum[value]:
		v.report("attribute %s: %q is not a %s", name, value, typ)
		return
	case t.pattern != nil && !t.pattern.MatchString(value):
		v.report("attribute %s: %q is not a valid %s", name, value, typ)
		return
	case t.uintBits > 0:
		if _, err := parseUint(value, t.uintBits); err != nil {
			v.report("attribute %s: %v", name, err)
			return
		}
	}
	if !utf8.ValidString(value) {
		v.report("attribute %s: invalid UTF-8", name)
	}
	path := v.where()
	switch typ {
	case "ID":
		if name == "TextId" {
			v.texts[value] = true
			return
		}
		if prev, dup := v.ids[value]; dup {
			v.report("ID %q already used at %s", value, prev)
			return
		}
		v.ids[value] = path
	case "IDRef":
		if name == "TextId" {
			v.tids = append(v.tids, ref{path, name, value})
		} else {
			v.refs = append(v.refs, ref{path, name, value})
		}
	}
}

// Validate checks the document against the catalog's structural and
// lexical rules, checks that every ID is unique and every reference
// (module, submodule, value item, graphic, category and text references)
// resolves, and that no submodule's IO data exceed an RT frame. It returns
// nil or a *ValidationError with the errors; Check reports the warnings
// and the unvalidated content as well.
func Validate(doc *Document) error {
	r := Check(doc)
	return r.Err()
}

// parseUint parses a decimal or 0x hexadecimal unsigned value of bits
// bits.
func parseUint(s string, bits int) (uint64, error) {
	s = strings.TrimSpace(s)
	base := 10
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		s, base = s[2:], 16
	}
	v, err := strconv.ParseUint(s, base, bits)
	if err != nil {
		return 0, fmt.Errorf("%q is not an unsigned %d-bit value", s, bits)
	}
	return v, nil
}

// parseInt parses a decimal or 0x hexadecimal signed value of bits bits:
// an optional sign, then the digits. Decimal is the lexical space of
// xsd:integer, where leading zeros are decimal ("010" is ten); 0x is the
// hexadecimal form parseUint accepts. Go's other integer literals (octal
// 0 and 0o, binary 0b, digit separators _) are refused.
func parseInt(s string, bits int) (int64, error) {
	s = strings.TrimSpace(s)
	neg := false
	digits := s
	if digits != "" && (digits[0] == '-' || digits[0] == '+') {
		neg, digits = digits[0] == '-', digits[1:]
	}
	if digits == "" || digits[0] < '0' || digits[0] > '9' {
		return 0, fmt.Errorf("%q is not a signed %d-bit value", s, bits)
	}
	m, err := parseUint(digits, 64)
	limit := uint64(1) << (bits - 1) // the magnitude of the minimum
	if err != nil || m > limit || (!neg && m == limit) {
		return 0, fmt.Errorf("%q is not a signed %d-bit value", s, bits)
	}
	if neg {
		return -int64(m-1) - 1, nil
	}
	return int64(m), nil
}

// ParseUint32 parses a decimal or 0x hexadecimal attribute value.
func ParseUint32(s string) (uint32, error) {
	v, err := parseUint(s, 32)
	if err != nil || v > math.MaxUint32 {
		return 0, err
	}
	return uint32(v), nil
}

// ParseUint16 parses a decimal or 0x hexadecimal attribute value.
func ParseUint16(s string) (uint16, error) {
	v, err := parseUint(s, 16)
	if err != nil || v > math.MaxUint16 {
		return 0, err
	}
	return uint16(v), nil
}

// ParseBool parses a Boolean attribute value; empty is def, and so is a
// value that is no Boolean (ToModel refuses one; Validate reports it).
func ParseBool(s string, def bool) bool {
	switch strings.TrimSpace(s) {
	case "true", "1":
		return true
	case "false", "0":
		return false
	default:
		return def
	}
}

// parseBool parses a Boolean attribute value of the xsd:boolean lexical
// space (true, false, 1, 0, with surrounding white space): an empty value
// (absent, as the generated types cannot tell the two apart) is def; any
// other value is an error.
func parseBool(s string, def bool) (bool, error) {
	if s == "" {
		return def, nil
	}
	switch strings.TrimSpace(s) {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	default:
		return def, fmt.Errorf("%q is not a Boolean", clip(s))
	}
}

// ParseValueList parses a ValueList attribute ("1..4 6") into its values.
// It bounds the expansion to limit values; a negative limit allows none.
func ParseValueList(s string, limit int) ([]uint32, error) {
	var out []uint32
	maxValues := uint64(0)
	if limit > 0 {
		maxValues = uint64(limit)
	}
	for tok := range strings.FieldsSeq(s) {
		lo, hi := tok, tok
		if before, after, ok := strings.Cut(tok, ".."); ok {
			lo, hi = before, after
		}
		a, err := strconv.ParseUint(lo, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("gsdml: %w: value list %q", profinet.ErrMalformed, s)
		}
		b, err := strconv.ParseUint(hi, 10, 32)
		if err != nil || b < a {
			return nil, fmt.Errorf("gsdml: %w: value list %q", profinet.ErrMalformed, s)
		}
		if uint64(len(out))+b-a+1 > maxValues {
			return nil, fmt.Errorf("gsdml: %w: value list %q has more than %d values", profinet.ErrResourceExhausted, s, limit)
		}
		for x := a; x <= b && x <= math.MaxUint32; x++ {
			out = append(out, uint32(x))
		}
	}
	return out, nil
}

// FormatValueList writes values as a ValueList, joining consecutive values
// into ranges.
func FormatValueList(values []uint32) string {
	var parts []string
	for i := 0; i < len(values); {
		j := i
		for j+1 < len(values) && values[j+1] == values[j]+1 {
			j++
		}
		if j > i {
			parts = append(parts, fmt.Sprintf("%d..%d", values[i], values[j]))
		} else {
			parts = append(parts, strconv.FormatUint(uint64(values[i]), 10))
		}
		i = j + 1
	}
	return strings.Join(parts, " ")
}

// Text returns the text of a TextId in the primary language, or the id
// itself when there is none. It goes through the texts for every call;
// ToModel looks its texts up in a table it makes once.
func (doc *Document) Text(id string) string {
	pl := doc.primaryLanguage()
	if pl == nil {
		return id
	}
	for i, t := range pl.Texts {
		if t.TextID == id {
			if textVisits != nil {
				textVisits(i + 1)
			}
			return t.Value
		}
	}
	if textVisits != nil {
		textVisits(len(pl.Texts))
	}
	return id
}

// primaryLanguage returns the texts of the primary language, nil when the
// document has none.
func (doc *Document) primaryLanguage() *PrimaryLanguage {
	if doc.ProfileBody == nil || doc.ProfileBody.ApplicationProcess == nil || doc.ProfileBody.ApplicationProcess.ExternalTextList == nil {
		return nil
	}
	return doc.ProfileBody.ApplicationProcess.ExternalTextList.PrimaryLanguage
}
