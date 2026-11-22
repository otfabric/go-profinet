// SPDX-License-Identifier: MIT

package gsdml

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/otfabric/go-profinet"
)

// onlyRecordErrors fails unless every error Check reports for doc is of
// the rule parameter-record, and there is one exactly when ToModel of the
// first DAP refuses doc as malformed. A field without a verified encoding,
// which ToModel refuses as unsupported, is an unvalidated
// parameter-encoding issue instead.
func onlyRecordErrors(t *testing.T, doc *Document) {
	t.Helper()
	rep := Check(doc)
	n := 0
	for _, i := range rep.Issues {
		if i.Severity != SeverityError {
			continue
		}
		if i.Rule != RuleParameterRecord {
			t.Fatalf("error of rule %q: %v", i.Rule, i)
		}
		n++
	}
	_, err := ToModel(doc, "")
	if errors.Is(err, profinet.ErrUnsupported) {
		if n > 0 || len(issuesOf(rep, RuleParameterEncoding, SeverityUnvalidated)) == 0 {
			t.Fatalf("ToModel %v, %d parameter-record errors: %v", err, n, rep.Issues)
		}
		return
	}
	if (err != nil) != (n > 0) {
		t.Fatalf("ToModel %v, %d parameter-record errors: %v", err, n, rep.Issues)
	}
}

// Check of a document applies the record rules to every module's records:
// what ParameterRecord.Check and the plan's TransferSequence rule report,
// at the record's path in the document.
func TestCheckRecords(t *testing.T) {
	t.Parallel()
	const sub = "ISO15745Profile/ProfileBody/ApplicationProcess/ModuleList/ModuleItem[MOD_IO]/VirtualSubmoduleList/VirtualSubmoduleItem[SM_IO]"
	check := func(edits ...string) Report {
		t.Helper()
		b := parameterFixture(t)
		for i := 0; i+1 < len(edits); i += 2 {
			if !bytes.Contains(b, []byte(edits[i])) {
				t.Fatalf("%q not in the fixture", edits[i])
			}
			b = bytes.Replace(b, []byte(edits[i]), []byte(edits[i+1]), 1)
		}
		doc, err := ParseBytes(b)
		if err != nil {
			t.Fatal(err)
		}
		return Check(doc)
	}
	// The fixture with record 2 sequenced 1: no errors.
	rep := check(sequenced...)
	if rep.Err() != nil {
		t.Fatalf("%v", rep.Issues)
	}
	// Its fields without a verified encoding, and the records without a
	// TransferSequence among sequenced ones, are reported as by the plan.
	if len(issuesOf(rep, RuleParameterEncoding, SeverityUnvalidated)) == 0 || len(issuesOf(rep, RuleTransferSequence, SeverityWarning)) != 1 {
		t.Fatalf("%v", rep.Issues)
	}
	// TransferSequence not 1 to N: the error, at the RecordDataList.
	if is := issuesOf(check(), RuleTransferSequence, SeverityError); len(is) != 1 || is[0].Path != sub+"/RecordDataList" {
		t.Fatalf("sequence %v", is)
	}
	// A ValueItemTarget that names no ValueItem: an error at the field.
	is := issuesOf(check(append(sequenced, `ValueItemTarget="V_MODE"`, `ValueItemTarget="V_NONE"`)...), RuleParameterAllowedValues, SeverityError)
	if len(is) != 1 || !strings.HasPrefix(is[0].Path, sub+"/RecordDataList/ParameterRecordDataItem[1]/Ref[") || !strings.Contains(is[0].Message, "V_NONE") {
		t.Fatalf("ValueItem %v", is)
	}
	// A record ToModel refuses: one error at the record, with the reason.
	is = issuesOf(check(append(sequenced, `TransferSequence="1"`, `TransferSequence="1" Length="1"`)...), RuleParameterRecord, SeverityError)
	if len(is) != 1 || !strings.HasPrefix(is[0].Path, sub+"/RecordDataList/ParameterRecordDataItem[") {
		t.Fatalf("record %v", is)
	}
	// An index that is no number is the structure's error only.
	rep = check(append(sequenced, `<ParameterRecordDataItem Index="2"`, `<ParameterRecordDataItem Index="x"`)...)
	if len(issuesOf(rep, RuleParameterRecord, SeverityError)) != 0 || rep.Err() == nil {
		t.Fatalf("index x %v", rep.Issues)
	}
	// A record index outside the user range is the record-index warning
	// only.
	rep = check(append(sequenced, `<ParameterRecordDataItem Index="2"`, `<ParameterRecordDataItem Index="0x8000"`)...)
	if len(issuesOf(rep, RuleRecordIndex, SeverityWarning)) != 1 || len(issuesOf(rep, RuleParameterRecord, SeverityError)) != 0 {
		t.Fatalf("index %v", rep.Issues)
	}
}
