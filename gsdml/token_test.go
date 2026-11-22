// SPDX-License-Identifier: MIT

package gsdml

import (
	"strings"
	"testing"
)

func TestCollapseSpace(t *testing.T) {
	for in, want := range map[string]string{
		"":                  "",
		"DAP 1":             "DAP 1",
		"  DAP \t\r\n 1  ":  "DAP 1",
		"V  1  2  1":        "V 1 2 1",
		"   ":               "",
		"a b":               "a b", // a no-break space is not XML white space
		"no-space_at.all-1": "no-space_at.all-1",
	} {
		if got := collapseSpace(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// TestTokenIDs: IDs and ID references are xsd:token in every schema
// version, so "DAP 1" is a valid ID, a reference matches it after white
// space is collapsed, and two IDs that collapse to the same value are
// duplicates. Vendor files use such IDs ("DAP 1", "DIM IO", "IRC5 Robot");
// Validate rejected them before.
func TestTokenIDs(t *testing.T) {
	rename := func(doc *Document) {
		m := modules(doc)[0]
		old := m.ID
		m.ID = "Module  One"
		for _, r := range dap(doc).UseableModules.ModuleItemRefs {
			if r.ModuleItemTarget == old {
				r.ModuleItemTarget = " Module One "
			}
		}
		// A text ID with spaces, referenced with other white space.
		name := dap(doc).ModuleInfo.Name
		for _, tx := range doc.primaryLanguage().Texts {
			if tx.TextID == name.TextID {
				tx.TextID = "Text\tOne"
			}
		}
		name.TextID = "Text One"
		dap(doc).ModuleInfo.SoftwareRelease = &SoftwareRelease{Value: "V 11.7.0"}
	}
	r := checkDoc(t, rename)
	if err := r.Err(); err != nil {
		t.Fatalf("token IDs: %v", err)
	}
	doc, err := FromModel(testModel(), GenerateOptions{SupervisorAR: true})
	if err != nil {
		t.Fatal(err)
	}
	want := doc.Text(dap(doc).ModuleInfo.Name.TextID)
	rename(doc)
	if got := doc.Text("Text One"); got != want {
		t.Fatalf("Text: %q, want %q", got, want)
	}
	if _, err := ToModel(doc, ""); err != nil {
		t.Fatalf("ToModel: %v", err)
	}
	// Two IDs that collapse to the same token are the same ID.
	r = checkDoc(t, func(doc *Document) {
		ms := modules(doc)
		if len(ms) < 2 {
			t.Fatal("the test model needs two modules")
		}
		ms[0].ID, ms[1].ID = "A B", "A  B"
	})
	if err := r.Err(); err == nil || !strings.Contains(err.Error(), `ID "A B" already used`) {
		t.Fatalf("duplicate after collapsing: %v", err)
	}
}

// TestInterfaceSubslotDefault: InterfaceSubmoduleItem's SubslotNumber is
// optional with the default 32768 in every schema version from V2.1 (V2.0
// fixes it at 32768). ToModel refused a document without it.
func TestInterfaceSubslotDefault(t *testing.T) {
	doc, err := FromModel(testModel(), GenerateOptions{SupervisorAR: true})
	if err != nil {
		t.Fatal(err)
	}
	it := dap(doc).SystemDefinedSubmoduleList.InterfaceSubmoduleItems[0]
	it.SubslotNumber = ""
	b, err := Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `SubslotNumber="32768"`) {
		t.Fatal("the attribute is still written")
	}
	if doc, err = ParseBytes(b); err != nil {
		t.Fatal(err)
	}
	if err := Validate(doc); err != nil {
		t.Fatal(err)
	}
	m, err := ToModel(doc, "")
	if err != nil {
		t.Fatalf("ToModel: %v", err)
	}
	found := false
	for _, s := range m.DAP.Submodules {
		if s.Subslot == 0x8000 {
			found = true
		}
	}
	if !found {
		t.Fatalf("no submodule in subslot 0x8000: %+v", m.DAP.Submodules)
	}
	// Given empty, it is not absent: ToModel still refuses it.
	it = dap(doc).SystemDefinedSubmoduleList.InterfaceSubmoduleItems[0]
	it.SubslotNumber = ""
	it.empty = append(it.empty, "SubslotNumber")
	if _, err := ToModel(doc, ""); err == nil {
		t.Fatal("an empty SubslotNumber converted")
	}
}
