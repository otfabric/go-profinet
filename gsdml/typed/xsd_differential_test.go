// SPDX-License-Identifier: MIT

//go:build gsdmlxsd

package typed_test

// The differential test of the generated validator against libxml2's
// xmllint (docs/research/GSDML-SCHEMA-PROPOSAL.md, A3; the typed model's
// acceptance criterion): every input is validated by both against a
// schema version, and the verdicts must be equal; there is no allowlist.
// The inputs are
//
//   - the committed fixtures, against every schema version;
//   - GSDML_CORPUS, a directory of further files, against their version;
//   - the hand-written mutants of TestMutants, each breaking one rule;
//   - seeded mutants of every input (an attribute dropped or given another
//     value, an element dropped, duplicated, moved or renamed, foreign
//     content, an ID duplicated), against the input's version;
//   - the encoding of every valid input by the typed model, which must be
//     valid again (Parse, then Encode).
//
// It needs xmllint and the schemas: make gsdml-differential.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/otfabric/go-profinet/gsdml/typed"
)

// mutantsPerInput is the number of mutants of each input.
const mutantsPerInput = 24

type oracle struct {
	t   *testing.T
	xsd string
	dir string
	n   int
}

func (o *oracle) schema(version string) string {
	for _, v := range []string{"v", "V"} {
		p := filepath.Join(o.xsd, "GSDML-DeviceProfile-"+v+version+".xsd")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	o.t.Fatalf("no schema for V%s", version)
	return ""
}

// xmllint validates data against version's schema, without network.
func (o *oracle) xmllint(data []byte, version string) (bool, string) {
	o.n++
	f := filepath.Join(o.dir, "in"+strconv.Itoa(o.n)+".xml")
	if err := os.WriteFile(f, data, 0o600); err != nil {
		o.t.Fatal(err)
	}
	out, err := exec.Command("xmllint", "--noout", "--nonet", "--schema", o.schema(version), f).CombinedOutput()
	_ = os.Remove(f)
	return err == nil, string(out)
}

func (o *oracle) compare(name string, data []byte, version string) bool {
	want, out := o.xmllint(data, version)
	err := typed.ValidateVersion(data, version)
	if want == (err == nil) {
		return want
	}
	if len(out) > 1500 {
		out = out[:1500]
	}
	var detail string
	var se *typed.SchemaError
	if errors.As(err, &se) {
		for i, is := range se.Issues {
			if i == 5 {
				break
			}
			detail += "\n  typed: " + is.String()
		}
	} else if err != nil {
		detail = "\n  typed: " + err.Error()
	}
	o.t.Errorf("%s against V%s: xmllint valid=%v, typed valid=%v%s\n  xmllint: %s", name, version, want, err == nil, detail, out)
	return want
}

func TestXSDDifferential(t *testing.T) {
	xsd := os.Getenv("GSDML_SCHEMAS_XSD")
	if xsd == "" {
		t.Fatal("GSDML_SCHEMAS_XSD is not set: the test needs the reference schemas (make gsdml-schemas)")
	}
	if _, err := exec.LookPath("xmllint"); err != nil {
		t.Fatal("the test needs xmllint (libxml2)")
	}
	o := &oracle{t: t, xsd: xsd, dir: t.TempDir()}
	fixtures, _ := filepath.Glob("../testdata/*.xml")
	more, _ := filepath.Glob("../testdata/fixtures/*.xml")
	fixtures = append(fixtures, more...)
	var corpus []string
	if dir := os.Getenv("GSDML_CORPUS"); dir != "" {
		corpus, _ = filepath.Glob(filepath.Join(dir, "*.xml"))
		if len(corpus) == 0 {
			t.Fatalf("GSDML_CORPUS %s has no .xml file", dir)
		}
	}
	versions := typed.Versions()
	stats := map[string]int{}
	// the hand-written mutants of TestMutants: their verdicts too
	base, err := os.ReadFile(modularFixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mutants {
		if valid := o.compare("mutant "+m.name, applyMutant(t, base, m), "2.43"); valid != (m.issue == "") {
			t.Errorf("mutant %s: xmllint valid=%v, TestMutants expects valid=%v", m.name, valid, m.issue == "")
		}
		stats["hand-written mutants"]++
	}
	inputs := append(append([]string(nil), fixtures...), corpus...)
	for i, f := range inputs {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(f)
		own, err := typed.DetectVersion(data)
		if err != nil {
			stats["no version"]++
			continue
		}
		targets := []string{own}
		if i < len(fixtures) {
			targets = versions
		}
		for _, v := range targets {
			if o.compare(name, data, v) {
				stats["valid"]++
			} else {
				stats["invalid"]++
			}
		}
		// round trip
		if doc, err := typed.Parse(data); err == nil {
			enc := doc.(interface{ Marshal() []byte }).Marshal()
			if ok := o.compare(name+" (encoded)", enc, own); !ok {
				t.Errorf("%s: the encoding of a valid document is not valid", name)
			}
			stats["round trips"]++
		}
		// mutants
		sum := sha256.Sum256(data)
		rng := rand.New(rand.NewPCG(binary.LittleEndian.Uint64(sum[:8]), binary.LittleEndian.Uint64(sum[8:16])))
		for m := 0; m < mutantsPerInput; m++ {
			root, err := typed.ParseNode(data)
			if err != nil {
				t.Fatal(err)
			}
			what := mutate(rng, root)
			if what == "" {
				continue
			}
			if o.compare(fmt.Sprintf("%s mutant %d (%s)", name, m, what), root.Marshal(), own) {
				stats["mutants valid"]++
			} else {
				stats["mutants invalid"]++
			}
		}
	}
	t.Logf("%d inputs (%d fixtures, %d corpus), %d xmllint runs: %v", len(inputs), len(fixtures), len(corpus), o.n, stats)
}

var weird = []string{"", " ", "x y", "-1", "0", "99999999999999999999", "0x", "0xZZ", "1.5", "true", "abc", "1..2", " ", "V1.0", "1 2 3", "INF"}

func elements(n *typed.Node, out *[]*typed.Node) {
	*out = append(*out, n)
	for _, c := range n.Children {
		if !c.IsText() {
			elements(c, out)
		}
	}
}

// mutate changes the tree once and says how, or returns "".
func mutate(rng *rand.Rand, root *typed.Node) string {
	var all []*typed.Node
	elements(root, &all)
	if len(all) < 2 {
		return ""
	}
	n := all[1+rng.IntN(len(all)-1)]
	parent := parentOf(root, n)
	switch rng.IntN(10) {
	case 0:
		if len(n.Attrs) == 0 {
			return ""
		}
		i := rng.IntN(len(n.Attrs))
		a := n.Attrs[i]
		n.Attrs = append(n.Attrs[:i:i], n.Attrs[i+1:]...)
		return "drop @" + a.Name.Local + " of " + n.Name.Local
	case 1, 2:
		if len(n.Attrs) == 0 {
			return ""
		}
		i := rng.IntN(len(n.Attrs))
		v := weird[rng.IntN(len(weird))]
		n.Attrs[i].Value = v
		return fmt.Sprintf("@%s of %s = %q", n.Attrs[i].Name.Local, n.Name.Local, v)
	case 3:
		removeChild(parent, n)
		return "drop " + n.Name.Local
	case 4:
		insertAfter(parent, n, clone(n))
		return "duplicate " + n.Name.Local
	case 5:
		sib := elementSiblings(parent)
		if len(sib) < 2 {
			return ""
		}
		i := rng.IntN(len(sib) - 1)
		swap(parent, sib[i], sib[i+1])
		return "swap " + sib[i].Name.Local + " and " + sib[i+1].Name.Local
	case 6:
		n.Children = append(n.Children, &typed.Node{Name: xml.Name{Space: n.Name.Space, Local: "Unknown"}})
		return "unknown element in " + n.Name.Local
	case 7:
		n.Attrs = append(n.Attrs, xml.Attr{Name: xml.Name{Local: "Unknown"}, Value: "1"})
		return "unknown attribute on " + n.Name.Local
	case 8:
		n.Children = append(n.Children, &typed.Node{Text: "x"})
		return "text in " + n.Name.Local
	default:
		// an ID of one element on another
		var ids []*typed.Node
		for _, e := range all {
			if _, ok := e.Attr("", "ID"); ok {
				ids = append(ids, e)
			}
		}
		if len(ids) < 2 {
			return ""
		}
		a, b := ids[rng.IntN(len(ids))], ids[rng.IntN(len(ids))]
		if a == b {
			return ""
		}
		v, _ := a.Attr("", "ID")
		for i := range b.Attrs {
			if b.Attrs[i].Name.Local == "ID" && b.Attrs[i].Name.Space == "" {
				b.Attrs[i].Value = v
			}
		}
		return "ID " + v + " twice"
	}
}

func parentOf(root, n *typed.Node) *typed.Node {
	for _, c := range root.Children {
		if c == n {
			return root
		}
		if !c.IsText() {
			if p := parentOf(c, n); p != nil {
				return p
			}
		}
	}
	return nil
}

func removeChild(p, n *typed.Node) {
	for i, c := range p.Children {
		if c == n {
			p.Children = append(p.Children[:i:i], p.Children[i+1:]...)
			return
		}
	}
}

func insertAfter(p, n, x *typed.Node) {
	for i, c := range p.Children {
		if c == n {
			p.Children = append(p.Children[:i+1:i+1], append([]*typed.Node{x}, p.Children[i+1:]...)...)
			return
		}
	}
}

func elementSiblings(p *typed.Node) []*typed.Node { return p.Elements() }

func swap(p, a, b *typed.Node) {
	ia, ib := -1, -1
	for i, c := range p.Children {
		if c == a {
			ia = i
		}
		if c == b {
			ib = i
		}
	}
	p.Children[ia], p.Children[ib] = p.Children[ib], p.Children[ia]
}

func clone(n *typed.Node) *typed.Node {
	c := &typed.Node{Name: n.Name, Text: n.Text, Line: n.Line}
	c.Attrs = append(c.Attrs, n.Attrs...)
	for _, k := range n.Children {
		c.Children = append(c.Children, clone(k))
	}
	return c
}
