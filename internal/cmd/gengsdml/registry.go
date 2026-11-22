// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/otfabric/go-profinet/internal/xsd"
)

// registrySchema names the format of the -registry report; a change to its
// fields that a reader would notice changes the name.
const registrySchema = "otfabric-gsdml-xsd-registry-v1"

// registry is the -registry output: every GSDML version a directory of
// schemas holds, the files each one reads, and what the catalog claims for
// it. It holds facts about the files (names, sizes, checksums, namespaces,
// revision dates, imports), never their text, and no timestamps or
// absolute paths, so the same directory gives the same bytes.
type registry struct {
	Schema string `json:"schema"`
	// Order states how Versions are ordered.
	Order    string            `json:"order"`
	Versions []registryVersion `json:"versions"`
	// Files are the schema documents of the directory, by name, with
	// those no DeviceProfile reads.
	Files []registryFile `json:"files"`
}

type registryVersion struct {
	// Version is the GSDML version in the DeviceProfile's file name, the
	// one a document's xsi:schemaLocation names.
	Version       string `json:"version"`
	DeviceProfile string `json:"deviceProfile"`
	Primitives    string `json:"primitives"`
	// SchemaVersionAttribute is the DeviceProfile's version attribute,
	// "1.0" for V1.0 to V2.2.
	SchemaVersionAttribute string `json:"schemaVersionAttribute"`
	TargetNamespace        string `json:"targetNamespace"`
	// VersionDate is the DeviceProfile's revision date,
	// PrimitivesVersionDate the Primitives', when they differ.
	VersionDate           string `json:"versionDate,omitempty"`
	PrimitivesVersionDate string `json:"primitivesVersionDate,omitempty"`
	// Files are the documents the DeviceProfile reads, itself first;
	// Missing the ones it names that the directory lacks.
	Files   []string `json:"files"`
	Missing []string `json:"missing,omitempty"`
	// Catalog says that the catalog was written for this version,
	// DataTypeLegality that it has the version's DataType sets.
	Catalog          bool `json:"catalog"`
	DataTypeLegality bool `json:"dataTypeLegality"`
}

type registryFile struct {
	xsd.File
	// UsedBy are the versions whose DeviceProfile reads the file.
	UsedBy []string `json:"usedBy"`
}

var deviceProfileName = regexp.MustCompile(`^GSDML-DeviceProfile-[vV]([0-9]+\.[0-9]+)\.xsd$`)
var primitivesName = regexp.MustCompile(`^GSDML-Primitives-[vV][0-9]+\.[0-9]+\.xsd$`)

// schemaVersionNumber turns "2.4" into 240 and "2.45" into 245, so that
// versions sort as GSDML numbers them: neither as decimals (2.4 < 2.35
// would be wrong) nor as semantic versions (2.4 < 2.35 too). It repeats
// gsdml's versionNumber, which is unexported.
func schemaVersionNumber(v string) int {
	major, minor, ok := strings.Cut(v, ".")
	if !ok {
		return -1
	}
	for len(minor) < 2 {
		minor += "0"
	}
	a, err1 := strconv.Atoi(major)
	b, err2 := strconv.Atoi(minor[:2])
	if err1 != nil || err2 != nil {
		return -1
	}
	return a*100 + b
}

// buildRegistry reads every DeviceProfile schema in dir with what it
// includes and imports. When dir has a SHA256SUMS file, every schema
// document must be listed in it with its checksum.
func buildRegistry(c *catalog, dir string) (*registry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sums, err := readSums(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return nil, err
	}
	r := &registry{Schema: registrySchema, Order: "by GSDML version number: the major version, then the minor version as two digits (2.4 is 2.40, after 2.35)"}
	files := map[string]*registryFile{}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		if _, ok := files[e.Name()]; !ok && strings.HasSuffix(e.Name(), ".xsd") {
			files[e.Name()] = &registryFile{File: xsd.File{Name: e.Name()}, UsedBy: []string{}}
		}
		m := deviceProfileName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		s, err := xsd.LoadRoot(dir, e.Name())
		if err != nil {
			return nil, err
		}
		root := s.Files[0]
		v := registryVersion{
			Version: m[1], DeviceProfile: root.Name, SchemaVersionAttribute: root.Version,
			TargetNamespace: root.TargetNamespace, VersionDate: root.VersionDate,
			Files: []string{}, Missing: s.Missing,
			Catalog: slices.Contains(c.Versions, m[1]),
		}
		if c.DataTypeLegality != nil {
			_, v.DataTypeLegality = c.DataTypeLegality.Versions[m[1]]
		}
		for _, f := range s.Files {
			v.Files = append(v.Files, f.Name)
			if primitivesName.MatchString(f.Name) {
				if v.Primitives != "" {
					return nil, fmt.Errorf("%s: two Primitives schemas, %s and %s", root.Name, v.Primitives, f.Name)
				}
				v.Primitives = f.Name
				if f.VersionDate != v.VersionDate {
					v.PrimitivesVersionDate = f.VersionDate
				}
			}
			rf := files[f.Name]
			if rf == nil {
				rf = &registryFile{UsedBy: []string{}}
				files[f.Name] = rf
			}
			rf.File = f
			rf.UsedBy = append(rf.UsedBy, v.Version)
		}
		if v.Primitives == "" {
			return nil, fmt.Errorf("%s: no Primitives schema", root.Name)
		}
		r.Versions = append(r.Versions, v)
	}
	if len(r.Versions) == 0 {
		return nil, fmt.Errorf("%s: no GSDML-DeviceProfile-v*.xsd", dir)
	}
	slices.SortFunc(r.Versions, func(a, b registryVersion) int {
		return cmp.Compare(schemaVersionNumber(a.Version), schemaVersionNumber(b.Version))
	})
	for i := 1; i < len(r.Versions); i++ {
		if schemaVersionNumber(r.Versions[i-1].Version) == schemaVersionNumber(r.Versions[i].Version) {
			return nil, fmt.Errorf("%s: two DeviceProfile schemas for version %s", dir, r.Versions[i].Version)
		}
	}
	for _, f := range files {
		if f.SHA256 == "" {
			// No DeviceProfile reads it (Common-Primitives-v1.0.xsd, the
			// XML@PROFIBUS root schema, for one): recorded with no users.
			s, err := xsd.LoadRoot(dir, f.Name)
			if err != nil {
				return nil, err
			}
			f.File = s.Files[0]
		}
		if sums != nil && sums[f.Name] != f.SHA256 {
			return nil, fmt.Errorf("%s: SHA-256 %s, SHA256SUMS %q", f.Name, f.SHA256, sums[f.Name])
		}
		slices.SortFunc(f.UsedBy, func(a, b string) int { return cmp.Compare(schemaVersionNumber(a), schemaVersionNumber(b)) })
		r.Files = append(r.Files, *f)
	}
	slices.SortFunc(r.Files, func(a, b registryFile) int { return strings.Compare(a.Name, b.Name) })
	return r, nil
}

// readSums reads a sha256sum listing; nil when there is none.
func readSums(path string) (map[string]string, error) {
	f, err := os.Open(filepath.Clean(path))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		sum, name, ok := strings.Cut(line, "  ")
		if !ok || len(sum) != 64 {
			return nil, fmt.Errorf("%s: malformed line %q", path, line)
		}
		out[strings.TrimPrefix(name, "*")] = sum
	}
	return out, sc.Err()
}

// writeRegistry writes the registry of dir as JSON to w.
func writeRegistry(c *catalog, dir string, w io.Writer) error {
	r, err := buildRegistry(c, dir)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}
