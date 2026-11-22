// SPDX-License-Identifier: MIT

package gsdml

import (
	"errors"
	"fmt"
	"strings"

	"github.com/otfabric/go-profinet/gsdml/typed"
)

// RuleSchema: the document is not valid against the GSDML schema of the
// version it names (gsdml/typed, generated from PI's reference schemas
// and checked against xmllint), or CheckBytes could not apply one (an
// unvalidated issue then says why).
const RuleSchema = "schema"

// CheckBytes reads a GSDML document and checks it as Check does, with one
// difference: the structure (required and allowed elements and attributes,
// their order and number, lexical forms and facets, and the identity
// constraints, key, keyref and unique, in their scopes) is validated
// against the schema of the version the document names, by gsdml/typed,
// instead of the catalog's version-independent rules, and so are the
// references (the schema's keyrefs, in their scopes, replace the catalog's
// global ID table, which misses IDs in elements the catalog does not
// model). Those issues have the rule RuleSchema and the line of the
// element. The physical-limit and semantic rules of Check apply as before.
//
// A document that names no version typed has (none, or one newer than
// V2.46) is checked against the catalog, as Check does, and an
// unvalidated issue with the rule RuleSchema says so.
//
// The error is that of ParseBytes: the document is not well-formed or
// exceeds a limit. The original octets are validated, not a re-encoding:
// Marshal writes the catalog's form (defaults, the newest element order),
// which is not valid against every older version.
func CheckBytes(data []byte) (Report, error) {
	doc, err := ParseBytes(data)
	if err != nil {
		return Report{}, err
	}
	version := schemaVersion(doc)
	if version == "" || !hasSchema(version) {
		rep := Check(doc)
		msg := "the document names no schema version"
		if version != "" {
			msg = "go-profinet has no schema for version " + version
		}
		rep.Issues = append(rep.Issues, Issue{Path: "ISO15745Profile", Severity: SeverityUnvalidated, Rule: RuleSchema,
			Message: msg + ": checked against the catalog's rules only (schemas: " + typed.Versions()[0] + " to " + typed.Versions()[len(typed.Versions())-1] + ")"})
		return rep, nil
	}
	rep := check(doc, true)
	err = typed.ValidateVersion(data, version)
	var se *typed.SchemaError
	switch {
	case err == nil:
	case errors.As(err, &se):
		for _, i := range se.Issues {
			rep.Issues = append(rep.Issues, Issue{Path: strings.TrimPrefix(i.Path, "/"), Line: i.Line, Message: i.Message, Severity: SeverityError, Rule: RuleSchema})
		}
		if se.Truncated {
			rep.Issues = append(rep.Issues, Issue{Path: "ISO15745Profile", Severity: SeverityError, Rule: ruleLimit,
				Message: fmt.Sprintf("more schema issues are not listed: the schema validation lists the first %d", typed.MaxIssues)})
		}
	default:
		// ParseBytes read it, so typed's reader does too, with the same
		// limits; an error here is one of a limit typed alone has.
		rep.Issues = append(rep.Issues, Issue{Path: "ISO15745Profile", Severity: SeverityError, Rule: RuleSchema, Message: err.Error()})
	}
	return rep, nil
}

// ValidateBytes reads a GSDML document and validates it as CheckBytes
// does: nil, a *ValidationError with the errors, or the error of
// ParseBytes.
func ValidateBytes(data []byte) error {
	rep, err := CheckBytes(data)
	if err != nil {
		return err
	}
	return rep.Err()
}

func hasSchema(version string) bool {
	for _, v := range typed.Versions() {
		if v == version {
			return true
		}
	}
	return false
}
