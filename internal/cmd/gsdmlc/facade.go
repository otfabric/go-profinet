// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/otfabric/go-profinet/internal/xsdc"
)

func (g *gen) genVersions() []byte {
	var b bytes.Buffer
	b.WriteString(header)
	b.WriteString("package typed\n\nimport \"fmt\"\n\n")
	b.WriteString("// build decodes a document tree as a schema version. It is a switch, not\n")
	b.WriteString("// a table, so that a program that never decodes links none of the\n")
	b.WriteString("// decoders. An unknown version is ErrUnknownVersion.\n")
	b.WriteString("func build(version string, n *Node) (Versioned, error) {\n\tswitch version {\n")
	for vi, v := range g.c.Versions {
		fmt.Fprintf(&b, "\tcase %q:\n\t\td, err := asVersioned(buildDocument[%s](n, %q))\n\t\treturn d, err\n", v, g.roots[vi].name, v)
	}
	b.WriteString("\t}\n\treturn nil, fmt.Errorf(\"%w: %q\", ErrUnknownVersion, version)\n")
	b.WriteString("}\n")
	return b.Bytes()
}

// sampleValue is a value of a Go kind that every parser accepts.
func (g *gen) sampleValue(goType string) string {
	switch goType {
	case "bool":
		return "true"
	case "float64", "Decimal":
		return "1.5"
	case "uint8", "uint16", "uint32", "uint64", "int8", "int16", "int32", "int64", "Integer":
		return "1"
	}
	if en, ok := g.enumByName(goType); ok && len(en.values) > 0 {
		return en.values[0]
	}
	return "x"
}

func nsLit(ns string) string { return strconv.Quote(ns) }

func (g *gen) genSamples() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(header)
	b.WriteString("package typed\n\nimport \"encoding/xml\"\n\n")
	b.WriteString("// samples has, for every struct, a node with every attribute and element\n// of the struct, which the tests build, encode and build again.\n")
	b.WriteString("var samples = []sample{\n")
	for _, st := range g.structs {
		sh := st.shape
		var attrs []string
		for _, f := range attrFields(st) {
			attrs = append(attrs, fmt.Sprintf("{Name: xml.Name{Space: %s, Local: %q}, Value: %q}", nsLit(f.ns), f.local, g.sampleValue(f.goType)))
		}
		var kids []string
		switch {
		case sh.Raw:
			kids = append(kids, `{Text: "t"}`, `{Name: xml.Name{Space: "urn:sample", Local: "s"}}`)
		case sh.Value != nil:
			kids = append(kids, fmt.Sprintf("{Text: %q}", g.sampleValue(fieldOf(st, fValue).goType)))
		default:
			for _, f := range elemFields(st) {
				n := 1
				if f.many {
					n = 2
				}
				for i := 0; i < n; i++ {
					if f.simple != nil {
						kids = append(kids, fmt.Sprintf("{Name: xml.Name{Space: %s, Local: %q}, Children: []*Node{{Text: %q}}}", nsLit(f.ns), f.local, g.sampleValue(f.goType)))
					} else {
						kids = append(kids, fmt.Sprintf("{Name: xml.Name{Space: %s, Local: %q}}", nsLit(f.ns), f.local))
					}
				}
			}
			if fieldOf(st, fAny) != nil {
				kids = append(kids, `{Name: xml.Name{Space: "urn:sample", Local: "s"}}`)
			}
		}
		var defaults []string
		for _, f := range attrFields(st) {
			if f.def != nil && f.ptr {
				defaults = append(defaults, fmt.Sprintf("_ = x.%sOrDefault(); _ = (&%s{}).%sOrDefault()", f.name, st.name, f.name))
			}
		}
		check := "nil"
		if len(defaults) > 0 {
			check = fmt.Sprintf("func(v any) { x := v.(*%s); %s }", st.name, strings.Join(defaults, "; "))
		}
		fmt.Fprintf(&b, "\t{name: %q, build: func(n *Node, b *builder) root { return new(%s).from(n, b) }, anyAttr: %v, anyElem: %v, text: %v, check: %s,\n\t\tnode: &Node{Name: xml.Name{Space: \"urn:sample\", Local: %q}, Attrs: []xml.Attr{%s}, Children: []*Node{%s}}},\n",
			st.name, st.name, sh.AnyAttr, sh.Any || sh.Raw, sh.Raw || sh.Value != nil, check, st.base, strings.Join(attrs, ", "), strings.Join(kids, ", "))
	}
	b.WriteString("}\n")
	return b.Bytes(), nil
}

func (g *gen) genFacade(vi int, pkg string) ([]byte, error) {
	v := g.c.Versions[vi]
	var b bytes.Buffer
	b.WriteString(header)
	fmt.Fprintf(&b, "// Package %s is GSDML V%s: the names of that schema version for the\n", pkg, v)
	b.WriteString("// generated types of gsdml/typed, which are shared with the versions where\n// a type is the same. Parse and Validate check a document against the\n")
	fmt.Fprintf(&b, "// V%s schema.\npackage %s\n\n", v, pkg)
	b.WriteString("import \"github.com/otfabric/go-profinet/gsdml/typed\"\n\n")
	fmt.Fprintf(&b, "// Version is the schema version of this package.\nconst Version = %q\n\n", v)
	root := g.roots[vi]
	fmt.Fprintf(&b, "// Document is a typed GSDML V%s document.\ntype Document = typed.Document[*%s]\n\n", v, root.base)
	fmt.Fprintf(&b, "// Parse validates data against the GSDML V%s schema and returns its\n// typed model; the version the document names is not consulted.\nfunc Parse(data []byte) (*Document, error) { return typed.ParseAs[%s](data, Version) }\n\n", v, root.base)
	fmt.Fprintf(&b, "// Decode returns the typed model of data as GSDML V%s without validating\n// it (typed.Decode).\nfunc Decode(data []byte) (*Document, error) { return typed.DecodeAs[%s](data, Version) }\n\n", v, root.base)
	fmt.Fprintf(&b, "// Validate validates data against the GSDML V%s schema.\nfunc Validate(data []byte) error { return typed.ValidateVersion(data, Version) }\n\n", v)
	reservedHere := map[string]bool{"Version": true, "Document": true, "Parse": true, "Decode": true, "Validate": true}
	type alias struct{ name, target, doc string }
	var aliases []alias
	for _, st := range g.structs {
		for _, x := range st.versions {
			if x == vi {
				aliases = append(aliases, alias{st.base, st.name, g.describe(st.comp)})
				break
			}
		}
	}
	var consts []alias
	for _, en := range g.enumList {
		if en.perVersion[vi] == nil {
			continue
		}
		aliases = append(aliases, alias{en.name, en.name, "the enumeration " + en.comp.Name.Local})
		for _, val := range en.perVersion[vi] {
			consts = append(consts, alias{en.consts[val], en.consts[val], ""})
		}
	}
	sort.Slice(aliases, func(i, j int) bool { return aliases[i].name < aliases[j].name })
	seen := map[string]bool{}
	for _, a := range aliases {
		n := a.name
		if reservedHere[n] || seen[n] {
			return nil, fmt.Errorf("version %s: alias %s collides", v, n)
		}
		seen[n] = true
		fmt.Fprintf(&b, "// %s is %s.\ntype %s = typed.%s\n\n", n, a.doc, n, a.target)
	}
	if len(consts) > 0 {
		fmt.Fprintf(&b, "// The enumeration values of GSDML V%s.\nconst (\n", v)
		for _, c := range consts {
			if seen[c.name] || reservedHere[c.name] {
				return nil, fmt.Errorf("version %s: constant %s collides", v, c.name)
			}
			seen[c.name] = true
			fmt.Fprintf(&b, "\t%s = typed.%s\n", c.name, c.target)
		}
		b.WriteString(")\n")
	}
	return b.Bytes(), nil
}

func (g *gen) genFacadeTest(vi int, pkg string) []byte {
	var b bytes.Buffer
	b.WriteString(header)
	fmt.Fprintf(&b, "package %s\n\nimport \"testing\"\n\n", pkg)
	b.WriteString("func TestFacade(t *testing.T) {\n")
	fmt.Fprintf(&b, "\tif Version != %q {\n\t\tt.Fatal(Version)\n\t}\n", g.c.Versions[vi])
	b.WriteString("\tbad := []byte(`<ISO15745Profile xmlns=\"http://www.profibus.com/GSDML/2003/11/DeviceProfile\"/>`)\n")
	b.WriteString("\tif _, err := Parse(bad); err == nil {\n\t\tt.Error(\"Parse accepted an empty profile\")\n\t}\n")
	b.WriteString("\tif err := Validate(bad); err == nil {\n\t\tt.Error(\"Validate accepted an empty profile\")\n\t}\n")
	b.WriteString("\td, err := Decode(bad)\n\tif err != nil || d.Version != Version || d.Root == nil {\n\t\tt.Errorf(\"Decode: %v %v\", d, err)\n\t}\n")
	b.WriteString("}\n")
	return b.Bytes()
}

// genReport writes docs/GSDML-TYPED.md: inventory, deduplication and
// evolution. Names and counts only.
func (g *gen) genReport() []byte {
	var b bytes.Buffer
	b.WriteString("<!-- Code generated by gsdmlc from the GSDML reference schemas. DO NOT EDIT. -->\n\n")
	b.WriteString("# GSDML typed model: inventory and evolution\n\n")
	b.WriteString("Generated by `internal/cmd/gsdmlc` (`make gsdml-generate`) from the GSDML\nreference schemas. It lists names and counts only; no schema text.\n\n")
	b.WriteString("## Inventory\n\n| Version | Complex types | Simple types | Global elements | Structs (`gsdml/typed`) |\n|---|---|---|---|---|\n")
	for vi, s := range g.c.Sets {
		ix := g.ix[vi]
		nc, ns := 0, 0
		for comp := range ix.Complex {
			if comp.Name.NS != nsXS {
				nc++
			}
		}
		for comp := range ix.Simple {
			if comp.Name.NS != nsXS {
				ns++
			}
		}
		structs := 0
		for _, st := range g.structs {
			for _, x := range st.versions {
				if x == vi {
					structs++
				}
			}
		}
		fmt.Fprintf(&b, "| V%s | %d | %d | %d | %d |\n", g.c.Versions[vi], nc, ns, len(s.Elements), structs)
	}
	b.WriteString("\nComplex and simple types include anonymous ones; the imported xmldsig\n(V2.0 and later) and XAdES (V2.41, V2.42) schemas are included.\n\n")
	b.WriteString("## Deduplication\n\nA (type, version) pair shares a generated artefact with another when the\npartition refinement of `internal/xsdc` (Analyze) puts them in one class:\nequal local signatures, and the types they refer to pairwise in one class,\nrecursively.\n\n")
	b.WriteString("| Level | Decides | One per version | Deduplicated | Saved |\n|---|---|---|---|---|\n")
	for _, x := range []struct {
		v    *xsdc.Variants
		what string
	}{{g.rep, "Go structs"}, {g.codec, "encoder order, content models"}, {g.full, "validation tables"}} {
		naive, dedup := 0, 0
		for comp, cl := range x.v.Classes {
			if comp.Kind != "complex" || comp.Name.NS == nsXS {
				continue
			}
			for _, k := range cl {
				if k != "" {
					naive++
				}
			}
			dedup += x.v.Count(comp)
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %.1f%% |\n", x.v.Level, x.what, naive, dedup, 100*(1-float64(dedup)/float64(naive)))
	}
	b.WriteString("\n## Evolution\n\nPer version, the complex types added, removed, and changed since the\nprevious version: in their Go representation (a new struct) or only in\ntheir validation (same struct, different rules).\n\n")
	b.WriteString("| Version | Added | Removed | New struct | Rules only |\n|---|---|---|---|---|\n")
	comps := g.complexComps()
	for vi := 1; vi < len(g.c.Versions); vi++ {
		var added, removed, rep, rules int
		for _, comp := range comps {
			r, f := g.rep.Classes[comp], g.full.Classes[comp]
			switch {
			case r[vi-1] == "" && r[vi] != "":
				added++
			case r[vi-1] != "" && r[vi] == "":
				removed++
			case r[vi] == "":
			case r[vi-1] != r[vi]:
				rep++
			case f[vi-1] != f[vi]:
				rules++
			}
		}
		fmt.Fprintf(&b, "| V%s | %d | %d | %d | %d |\n", g.c.Versions[vi], added, removed, rep, rules)
	}
	b.WriteString("\n## Types\n\nEvery complex type with its structs and the versions of each.\n\n| Type | Structs |\n|---|---|\n")
	for _, comp := range comps {
		var parts []string
		for _, st := range g.structs {
			if st.comp == comp {
				parts = append(parts, fmt.Sprintf("`%s` (%s)", st.name, g.versionList(st.versions)))
			}
		}
		name := comp.Name.Local
		if comp.Path != "" {
			name = comp.Path
		}
		if p := nsPrefix[comp.Name.NS]; p != "" {
			name = p + ": " + name
		}
		fmt.Fprintf(&b, "| %s | %s |\n", strings.ReplaceAll(name, "|", "\\|"), strings.Join(parts, "; "))
	}
	return b.Bytes()
}

func (g *gen) complexComps() []xsdc.Component {
	var comps []xsdc.Component
	for comp := range g.rep.Classes {
		if comp.Kind == "complex" && comp.Name.NS != nsXS {
			comps = append(comps, comp)
		}
	}
	sort.Slice(comps, func(i, j int) bool { return comps[i].String() < comps[j].String() })
	return comps
}
