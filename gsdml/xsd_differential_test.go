// SPDX-License-Identifier: MIT

//go:build gsdmlxsd

package gsdml_test

// The differential test of Validate against the GSDML reference schemas
// (docs/research/GSDML-SCHEMA-PROPOSAL.md, A3): every file is validated by
// libxml2's xmllint against the schema version it names, by
// gsdml.ValidateBytes, which must agree with xmllint on every file, and by
// gsdml.Validate (the catalog's rules), which must agree except where
// testdata/xsd-differential.json explains the difference by a known
// finding. It needs xmllint and the schemas (make gsdml-schemas):
//
//	GSDML_SCHEMAS_XSD=.gsdml-schemas/xsd go test -tags gsdmlxsd -run TestXSDDifferential ./gsdml
//
// GSDML_CORPUS adds a directory of further GSDML files (vendor files,
// which cannot be committed). make gsdml-differential runs it.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet/gsdml"
)

// differential is testdata/xsd-differential.json.
type differential struct {
	// Known are the findings that explain an error Validate reports on a
	// file the schema accepts.
	Known []knownFinding `json:"known"`
	// Lenient are the files the schema rejects and Validate accepts, by
	// SHA-256, each with the reason.
	Lenient map[string]string `json:"lenient"`
}

type knownFinding struct {
	Finding string `json:"finding"`
	// Message and Path are regular expressions an issue must match (its
	// message, and the last element of its path).
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
	// Before, when set, limits the finding to files of a schema version
	// before it (an attribute that version does not require yet).
	Before string `json:"before,omitempty"`

	message, path *regexp.Regexp
	used          bool
}

var schemaVersion = regexp.MustCompile(`GSDML-DeviceProfile-[vV]([0-9]+\.[0-9]+)\.xsd`)

// versionNumber orders GSDML versions: 2.4 is 2.40, after 2.35.
func versionNumber(v string) int {
	major, minor, _ := strings.Cut(v, ".")
	for len(minor) < 2 {
		minor += "0"
	}
	a, _ := strconv.Atoi(major)
	b, _ := strconv.Atoi(minor[:2])
	return a*100 + b
}

func TestXSDDifferential(t *testing.T) {
	xsdDir := os.Getenv("GSDML_SCHEMAS_XSD")
	if xsdDir == "" {
		t.Fatal("GSDML_SCHEMAS_XSD is not set: the test needs the reference schemas (make gsdml-schemas)")
	}
	if _, err := exec.LookPath("xmllint"); err != nil {
		t.Fatal("the test needs xmllint (libxml2)")
	}
	raw, err := os.ReadFile("testdata/xsd-differential.json")
	if err != nil {
		t.Fatal(err)
	}
	var d differential
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	for i := range d.Known {
		k := &d.Known[i]
		k.message = regexp.MustCompile(k.Message)
		if k.Path != "" {
			k.path = regexp.MustCompile(k.Path)
		}
	}
	files, _ := filepath.Glob("testdata/*.xml")
	fixtures, _ := filepath.Glob("testdata/fixtures/*.xml")
	files = append(files, fixtures...)
	if dir := os.Getenv("GSDML_CORPUS"); dir != "" {
		corpus, _ := filepath.Glob(filepath.Join(dir, "*.xml"))
		if len(corpus) == 0 {
			t.Fatalf("GSDML_CORPUS %s has no .xml file", dir)
		}
		files = append(files, corpus...)
	}
	var agree, explained, lenient, skipped, bytesAgree int
	seenLenient := map[string]bool{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		head := b[:min(len(b), 4096)]
		m := schemaVersion.FindSubmatch(head)
		if m == nil {
			skipped++ // no schema version to validate against
			continue
		}
		version := string(m[1])
		schema := filepath.Join(xsdDir, "GSDML-DeviceProfile-v"+version+".xsd")
		if _, err := os.Stat(schema); err != nil {
			schema = filepath.Join(xsdDir, "GSDML-DeviceProfile-V"+version+".xsd")
		}
		if _, err := os.Stat(schema); err != nil {
			t.Errorf("%s: no schema for version %s", f, version)
			continue
		}
		// --nonet: the schemas' imports resolve in xsdDir, never remotely.
		out, xerr := exec.Command("xmllint", "--noout", "--nonet", "--schema", schema, f).CombinedOutput()
		schemaValid := xerr == nil
		var verr error
		doc, perr := gsdml.ParseBytes(b)
		if perr == nil {
			verr = gsdml.Validate(doc)
		}
		sum := sha256.Sum256(b)
		id := hex.EncodeToString(sum[:])
		// ValidateBytes validates against the document's own schema
		// version: it must agree with xmllint exactly, with no allowlist.
		if berr := gsdml.ValidateBytes(b); schemaValid != (berr == nil) {
			t.Errorf("%s (V%s): xmllint valid %v, ValidateBytes: %v\n%s", f, version, schemaValid, berr, firstLines(string(out), 3))
		} else {
			bytesAgree++
		}
		switch {
		case schemaValid == (perr == nil && verr == nil):
			agree++
		case !schemaValid:
			if _, ok := d.Lenient[id]; !ok {
				t.Errorf("%s (V%s, sha256 %s): the schema rejects it and Validate accepts it:\n%s", f, version, id, firstLines(string(out), 3))
			}
			seenLenient[id] = true
			lenient++
		case perr != nil:
			t.Errorf("%s (V%s): the schema accepts it and ParseBytes fails: %v", f, version, perr)
		default:
			var ve *gsdml.ValidationError
			if !errors.As(verr, &ve) {
				t.Errorf("%s: %v", f, verr)
				continue
			}
			ok := true
			for _, is := range ve.Issues {
				if is.Rule == "limit" {
					continue
				}
				if !explain(d.Known, is, version) {
					t.Errorf("%s (V%s): the schema accepts it; Validate: %s", f, version, is)
					ok = false
					break
				}
			}
			if ok {
				explained++
			}
		}
	}
	for id, reason := range d.Lenient {
		if !seenLenient[id] && os.Getenv("GSDML_CORPUS") != "" {
			t.Logf("lenient entry %s (%s) matched no file", id, reason)
		}
	}
	for _, k := range d.Known {
		if !k.used && os.Getenv("GSDML_CORPUS") != "" {
			t.Errorf("known finding %s (%q at %q) explains no difference any more: remove it", k.Finding, k.Message, k.Path)
		}
	}
	t.Logf("ValidateBytes: %d of %d files with a schema version agree with xmllint", bytesAgree, len(files)-skipped)
	t.Logf("%d files: %d agree, %d differ by known findings, %d the schema rejects and Validate accepts (known), %d name no schema version", len(files), agree, explained, lenient, skipped)
}

func explain(known []knownFinding, is gsdml.Issue, version string) bool {
	last := is.Path
	if i := strings.LastIndex(last, "/"); i >= 0 {
		last = last[i+1:]
	}
	for i := range known {
		k := &known[i]
		if !k.message.MatchString(is.Message) || (k.path != nil && !k.path.MatchString(last)) {
			continue
		}
		if k.Before != "" && versionNumber(version) >= versionNumber(k.Before) {
			continue
		}
		k.used = true
		return true
	}
	return false
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	return strings.Join(lines[:min(n, len(lines))], "\n")
}
