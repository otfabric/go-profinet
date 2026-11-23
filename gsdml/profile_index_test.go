// SPDX-License-Identifier: MIT

package gsdml

import (
	"bytes"
	"errors"
	"strconv"
	"testing"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/pnio"
)

// profileIndexes are the indexes of the profile range 0xB000-0xBFFF the
// tests use: the IO-Link Wireless masters' of the corpus (0xB600,
// 0xB701-0xB703, 0xB900), PROFIdrive's parameter access (0xB02E, 0xB02F),
// and the range's ends.
var profileIndexes = []pnio.Index{0xB600, 0xB701, 0xB702, 0xB703, 0xB900, 0xB02E, 0xB02F, 0xB000, 0xBFFF}

// profileModel is apiModel with the IO-Link submodule (API 0x4E01)
// carrying a record at each of profileIndexes and a user record.
func profileModel() *model.Device {
	m := apiModel()
	sm := &m.Slots[0].Module.Submodules[1]
	sm.Records = append(sm.Records, model.Record{Index: 0x20, Name: "User", Length: 2, Default: []byte{0, 1}})
	for i, idx := range profileIndexes {
		sm.Records = append(sm.Records, model.Record{Index: idx, Name: "P" + strconv.Itoa(i), Length: 4, Default: []byte{0x80, 0x01, byte(i), 0xFF}})
	}
	return m
}

// Records of the profile range survive FromModel and ToModel, with their
// defaults and in their submodule's API, next to the user records.
func TestProfileIndexRoundTrip(t *testing.T) {
	b := profileGSDML(t)
	doc, err := ParseBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(doc); err != nil {
		t.Fatal(err)
	}
	m, err := ToModel(doc, "")
	if err != nil {
		t.Fatal(err)
	}
	sm := m.Slots[0].Module.Submodules[1]
	if sm.API != 0x4E01 || len(sm.Records) != len(profileIndexes)+1 {
		t.Fatalf("submodule %+v", sm)
	}
	for i, idx := range profileIndexes {
		r, ok := sm.Record(idx)
		if !ok || r.Length != 4 || !bytes.Equal(r.Default, []byte{0x80, 0x01, byte(i), 0xFF}) {
			t.Errorf("record %s: %+v %v", idx, r, ok)
		}
	}
	if r, ok := sm.Record(0x20); !ok || !bytes.Equal(r.Default, []byte{0, 1}) {
		t.Errorf("user record %+v %v", r, ok)
	}
	// Check: no record-index warning; the record rules run.
	if rep := Check(doc); len(issuesOf(rep, RuleRecordIndex, SeverityWarning)) != 0 || rep.Err() != nil {
		t.Fatalf("check %+v", rep.Issues)
	}
}

// An index above 0x7FFF outside the profile range stays refused by
// ToModel (ErrMalformed) and is the record-index warning of Check.
func TestProfileIndexOutside(t *testing.T) {
	b := profileGSDML(t)
	for _, idx := range []string{"32768", "45055", "49152", "65535"} { // 0x8000, 0xAFFF, 0xC000, 0xFFFF
		d, err := ParseBytes(bytes.Replace(b, []byte(`Index="46592"`), []byte(`Index="`+idx+`"`), 1))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ToModel(d, ""); !errors.Is(err, profinet.ErrMalformed) {
			t.Errorf("%s: ToModel %v", idx, err)
		}
		if is := issuesOf(Check(d), RuleRecordIndex, SeverityWarning); len(is) != 1 {
			t.Errorf("%s: check %+v", idx, is)
		}
	}
}

func profileGSDML(t *testing.T) []byte {
	t.Helper()
	doc, err := FromModel(profileModel(), GenerateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`Index="46592"`)) || !bytes.Contains(b, []byte(`Index="45102"`)) {
		t.Fatalf("profile indexes not written:\n%s", b)
	}
	return b
}
