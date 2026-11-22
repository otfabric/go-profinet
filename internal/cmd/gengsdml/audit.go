// SPDX-License-Identifier: MIT

package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/otfabric/go-profinet/internal/xsd"
)

// auditSchema names the format of the -audit report; a change to its
// fields that a reader would notice changes the name.
const auditSchema = "otfabric-gsdml-xsd-audit-v1"

// auditReport is the -audit output: for each schema set given, which
// enumeration every DataType attribute occurrence takes, compared with the
// catalog's DataType enumeration and with the previous schema set given.
// It holds no timestamps and no absolute paths, so the same schemas give
// the same bytes.
type auditReport struct {
	Schema    string `json:"schema"`
	Attribute string `json:"attribute"`
	// CatalogVersions are the schema versions the catalog was written for,
	// CatalogValues its DataType enumeration.
	CatalogVersions []string       `json:"catalogVersions"`
	CatalogValues   []string       `json:"catalogValues"`
	Versions        []auditVersion `json:"versions"`
	// UnverifiedCatalogVersions are the catalog versions no schema set in
	// this report declares: nothing here is evidence about them.
	UnverifiedCatalogVersions []string `json:"unverifiedCatalogVersions"`
}

type auditVersion struct {
	// Version is the GSDML version the operator gave for the set, or the
	// root document's version attribute when none was given;
	// SchemaVersionAttribute is that attribute, which the GSDML V2.0 to
	// V2.2 schemas leave at "1.0".
	Version                string         `json:"version"`
	SchemaVersionAttribute string         `json:"schemaVersionAttribute"`
	Root                   string         `json:"root"`
	Files                  []xsd.File     `json:"files"`
	Missing                []string       `json:"missing,omitempty"`
	Contexts               []auditContext `json:"contexts"`
	// RemovedContexts are the "Parent/Element" contexts of the previous
	// schema set of the report that this one does not have.
	RemovedContexts []string `json:"removedContexts,omitempty"`
	// Types are the enumerations the contexts use, by type name.
	Types map[string]auditType `json:"types"`
}

// auditContext is one element occurrence with the attribute: Element in
// Parent.
type auditContext struct {
	Parent  string `json:"parent"`
	Element string `json:"element"`
	Type    string `json:"type"`
	// NotInCatalog are the values the schema allows here that the catalog
	// lacks; CatalogNotInSchema the catalog values it does not allow here.
	NotInCatalog       []string `json:"notInCatalog"`
	CatalogNotInSchema []string `json:"catalogNotInSchema"`
	// Added and Removed compare with the same context in the previous
	// schema set of the report; New marks a context it did not have.
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
	New     bool     `json:"new,omitempty"`
}

type auditType struct {
	Base   string   `json:"base,omitempty"`
	Values []string `json:"values"`
	// Whitespace are the values with leading or trailing white space: on
	// an xsd:string base an instance must carry it too.
	Whitespace []string `json:"whitespace,omitempty"`
	// Notes are the schema comments after a value, such as "obsolete".
	Notes map[string]string `json:"notes,omitempty"`
}

// audit loads each root schema document (with what it includes and
// imports) and writes the report as JSON to w. A root is PATH or
// VERSION=PATH, VERSION naming the GSDML version of the set, or a
// directory of schemas, which stands for every DeviceProfile in it, oldest
// version first (-registry).
func audit(c *catalog, roots []string, w io.Writer) error {
	roots, err := expandRoots(c, roots)
	if err != nil {
		return err
	}
	r := auditReport{Schema: auditSchema, Attribute: "DataType", CatalogVersions: c.Versions, UnverifiedCatalogVersions: []string{}}
	r.CatalogValues = slices.Clone(c.SimpleTypes["DataType"].Enum)
	slices.Sort(r.CatalogValues)
	prev := map[string][]string{}
	seen := map[string]bool{}
	for _, spec := range roots {
		version, root, ok := strings.Cut(spec, "=")
		if !ok {
			version, root = "", spec
		}
		s, err := xsd.LoadRoot(filepath.Dir(root), filepath.Base(root))
		if err != nil {
			return err
		}
		v, err := auditOne(s, version, filepath.Base(root), r.CatalogValues, prev)
		if err != nil {
			return err
		}
		if seen[v.Version] {
			return fmt.Errorf("%s: version %s is in the report twice", root, v.Version)
		}
		seen[v.Version] = true
		r.Versions = append(r.Versions, v)
	}
	for _, v := range c.Versions {
		if !seen[v] {
			r.UnverifiedCatalogVersions = append(r.UnverifiedCatalogVersions, v)
		}
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// expandRoots replaces each directory among roots with the
// VERSION=PATH roots of its DeviceProfile schemas.
func expandRoots(c *catalog, roots []string) ([]string, error) {
	var out []string
	for _, spec := range roots {
		if fi, err := os.Stat(spec); err != nil || !fi.IsDir() {
			out = append(out, spec)
			continue
		}
		r, err := buildRegistry(c, spec)
		if err != nil {
			return nil, err
		}
		for _, v := range r.Versions {
			out = append(out, v.Version+"="+filepath.Join(spec, v.DeviceProfile))
		}
	}
	return out, nil
}

// auditOne reports one schema set; prev maps "Parent/Element" to the
// values of the previous set and is updated to this one.
func auditOne(s *xsd.Schema, version, root string, catalogValues []string, prev map[string][]string) (auditVersion, error) {
	v := auditVersion{Version: version, Root: root, Files: s.Files, Missing: s.Missing, Types: map[string]auditType{}}
	if len(s.Files) > 0 {
		v.SchemaVersionAttribute = s.Files[0].Version
	}
	if v.Version == "" {
		v.Version = v.SchemaVersionAttribute
	}
	if v.Version == "" {
		return v, fmt.Errorf("%s: no version given and the root schema document has no version attribute", root)
	}
	cur := map[string][]string{}
	for _, u := range s.Uses {
		if u.Attribute != "DataType" {
			continue
		}
		values, ok := s.Enumeration(u.Type)
		if !ok {
			return v, fmt.Errorf("%s: %s in %s: DataType of type %q has no enumeration", root, u.Element, u.Parent, u.Type)
		}
		slices.Sort(values)
		if _, ok := v.Types[u.Type]; !ok {
			t := auditType{Values: values}
			if st := s.SimpleTypes[u.Type]; st != nil {
				t.Base = st.Base
				if len(st.Notes) > 0 {
					t.Notes = st.Notes
				}
			}
			for _, x := range values {
				if strings.TrimSpace(x) != x {
					t.Whitespace = append(t.Whitespace, x)
				}
			}
			v.Types[u.Type] = t
		}
		key := u.Parent + "/" + u.Element
		ctx := auditContext{
			Parent: u.Parent, Element: u.Element, Type: u.Type,
			NotInCatalog:       minus(values, catalogValues),
			CatalogNotInSchema: minus(catalogValues, values),
		}
		if old, ok := prev[key]; ok {
			ctx.Added, ctx.Removed = minus(values, old), minus(old, values)
		} else if len(prev) > 0 {
			ctx.New = true
		}
		cur[key] = values
		v.Contexts = append(v.Contexts, ctx)
	}
	if len(v.Contexts) == 0 {
		return v, fmt.Errorf("%s: no element has a DataType attribute", root)
	}
	slices.SortFunc(v.Contexts, func(a, b auditContext) int {
		return cmp.Or(strings.Compare(a.Parent, b.Parent), strings.Compare(a.Element, b.Element))
	})
	for k := range prev {
		if _, ok := cur[k]; !ok {
			v.RemovedContexts = append(v.RemovedContexts, k)
		}
	}
	slices.Sort(v.RemovedContexts)
	clear(prev)
	for k, vals := range cur {
		prev[k] = vals
	}
	return v, nil
}

// minus returns the values of a not in b, sorted; never nil, so the
// report shows an empty list rather than null.
func minus(a, b []string) []string {
	out := []string{}
	for _, x := range a {
		if !slices.Contains(b, x) {
			out = append(out, x)
		}
	}
	slices.Sort(out)
	return out
}
