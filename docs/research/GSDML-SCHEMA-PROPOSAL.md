# GSDML schemas as an engineering asset: assessment and proposal

Status: proposal, 2026-11-22. Nothing here is implemented beyond what
"Done" lists. Where it needs a maintainer decision, it says so.

## Done

- **Corpus.** The 40 schema files (GSDML V1.0 to V2.46, as TIA Portal V21
  installs them) are in the private repository
  otfabric/profinet-gsdml-schema, byte for byte, because their licence does
  not allow redistribution ([GSDML.md](GSDML.md)). `make gsdml-schemas`
  fetches them at a pinned commit; `make gsdml-schemas-verify` checks the
  commit, the SHA-256 of every file, well-formedness, and that all 18
  DeviceProfile schemas compile under `xmllint --nonet` with every import
  resolved locally.
- **Registry.** `gengsdml -registry` writes
  `gsdml/schema/xsd-registry.json`: per version its files, sizes,
  checksums, namespaces, revision dates and imports, ordered by GSDML
  number (2.35 < 2.4 < 2.41), and what the catalog claims for it.
- **DataType legality** for all 18 versions
  ([GSDML-XSD-AUDIT.md](GSDML-XSD-AUDIT.md)), with V2.46's four new
  composites in the vocabulary.

## Findings

The evidence: the catalog compared with each schema (`gengsdml -xsd`), and
a differential run over 130 public vendor GSDML files (fetched from GitHub
for the corpus scan of [GSDML-CORPUS-DEFAULTS.md](GSDML-CORPUS-DEFAULTS.md),
kept outside the repository). Each file was validated by `xmllint --nonet`
against the schema version its `xsi:schemaLocation` names, and by
`gsdml.Validate`, `gsdml.Check` and `gsdml.ToModel`. The corpus spans every
version from V2.0 to V2.46 (V2.3: 19 files, V2.33: 18, V2.46: 1); 3 files
name no version.

| Result | Files |
|---|---|
| xmllint valid, `Validate` passes | 7 |
| xmllint valid, `Validate` fails | **118** |
| xmllint invalid, `Validate` passes | 1 |
| xmllint invalid, `Validate` fails | 1 |
| no schema version named | 3 |
| `ToModel` succeeds | 129 of 130 |

`Validate` is therefore far stricter than the schemas, and its rejections
are false in almost every case. `ToModel` is not affected: it does not use
`Validate`. The causes, by the number of files each one hits:

1. **ID and IDRef are too narrow (97 files).** The catalog types `ID` and
   `IDRef` as `^[A-Za-z_][A-Za-z0-9_.\-]*$`. Every schema version types
   them as `base:IdT` and `base:RefIdT`, restrictions of `xsd:token`, which
   allow spaces: `"DAP 1"`, `"DIM IO"`, `"IRC5 Robot"` are legal. Relaxing
   both to `xsd:token` turns 4 more of the 125 files valid and removes the
   largest error class; the next causes then show.
2. **Uniqueness is global; the schemas scope it (75 files).** `Validate`
   reports "ID … already used" for any ID seen anywhere in the document. The
   schemas declare 70 to 80 identity constraints per version (`xsd:key`,
   `xsd:keyref`, `xsd:unique`: V2.2 6/19/29, V2.35 7/13/61, V2.46
   10/19/68), each with its own selector: a VirtualSubmoduleItem may reuse
   its DAP's or module's ID, a Ref ID is unique per Request.
   `internal/xsd` does not read identity constraints.
3. **Requiredness ignores the version (46 files and fewer).** The catalog
   has one requiredness per attribute, in effect V2.35's. The schemas add
   requirements over time, and every file that lacks one of these is of a
   version that does not require it:

   | Attribute | Required from |
   |---|---|
   | `DeviceAccessPointItem/@DeviceAccessSupported` | V2.25 |
   | `DeviceAccessPointItem/@PNIO_Version` | V2.3 |
   | `DeviceAccessPointItem/@CheckDeviceID_Allowed` | V2.31 |
   | `DeviceAccessPointItem/@NameOfStationNotTransferable` | V2.31 |
   | `VirtualSubmoduleItem/@MayIssueProcessAlarm` | V2.32 |

   The same applies to `ModuleInfo` in a VirtualSubmoduleItem (missing only
   in V2.0 and V2.1 files) and to `ID` on interface and port submodules
   (missing only in V2.0 to V2.2 files). In the other direction,
   `CertificationInfo/@ConformanceClass` and `@ApplicationClass` are
   required by the catalog but optional in V2.45 and V2.46.
4. **Defaults are not recorded (56 attributes).** Against V2.35 the
   modelled elements and attributes match in structure, but the catalog
   records none of the schema defaults of 56 of them (`Ref/@Changeable`
   and `@Visible` true, `Input/@Consistency` "Item consistency",
   `DeviceAccessPointItem/@MaxSupportedRecordSize` 4068,
   `ApplicationRelations/@NumberOfAR` 1, `TimingProperties/@SendClock` 32,
   `InterfaceSubmoduleItem/@SubslotNumber` 32768, …). The statement "0
   differences against v2.35" in LIMITATIONS.md and PI-MEMBERSHIP.md did
   not hold against this copy; both now say what `-xsd` reports.
5. **A confirmed defect from finding 4.** A document that omits the
   optional `InterfaceSubmoduleItem/@SubslotNumber` passes `Validate`, but
   `ToModel` fails with `malformed: SubslotNumber ""`; V2.35 says an absent
   value is 32768. The other defaults are read in the model as the
   schema's value or not at all (`DCP_HelloSupported` false,
   `NumberOfAR` 1); a generated defaults table would make that checkable
   rather than incidental.
6. **A choice is modelled as a requirement (12 files, V2.25 to V2.41).**
   `ChannelDiagList` is an `xsd:choice` of `ChannelDiagItem`,
   `SystemDefinedChannelDiagItem` and `ProfileChannelDiagItem`; the
   catalog requires `ChannelDiagItem`, so a list of profile items only is
   rejected. The catalog's child notation has no choice.
7. **Token values are not collapsed.** `SoftwareRelease/@Value` has the
   pattern `^\S+$` in the catalog; the schema types it `TokenParameterT`
   (`xsd:token`), so `"V 11.7.0"` and `"V  1  2  1"` are valid (4 files).
8. **One lenient case.** A V2.4 file with character content in
   `ModuleItemRef` (an element of empty content) passes `Validate`;
   xmllint rejects it.
9. **The catalog covers part of the schema.** Against V2.46, 164
   elements and attributes are in the schema and not in the catalog
   (kept verbatim, not validated): `ChannelProcessAlarmList`,
   `UnitDiagTypeList`, `LogBookEntryList`, `CommunicationInterfaceList`,
   `SystemRedundancy`, `AssetManagement`, … The number grows with every
   version (V2.25: 73, V2.35: 124, V2.46: 164).
10. **The TIA copy is reserialised.** It has no XML comments, so the
   schemas' "obsolete" annotations are not in it (finding of
   [GSDML-XSD-AUDIT.md](GSDML-XSD-AUDIT.md)). Any tool reading notes from
   it reads none.

Findings 1, 2, 3, 6 and 7 make `Validate` reject valid files. Finding 5 makes
`ToModel` reject a valid file. These are correctness defects, not
coverage gaps.

## Architecture recommendation

**Generated metadata, hand-written behaviour, in the existing pipeline.**
The catalog-and-generator design is right for this repository; its inputs
are what is missing. The schemas cannot be read in CI (they are private),
so the pipeline has to work from facts derived from them:

```text
private XSDs ──(maintainer, locally)──> derived facts, committed JSON
   internal/xsd: types, requiredness, defaults,        (no schema text)
   identity constraints, per version                         │
                                                             ▼
catalog.json (hand-written: names, docs, modelling) ──> gengsdml ──> zz_generated.go
                                                             │       (version-indexed tables)
                                                             ▼
                        hand-written Check, Validate, ToModel read the tables
```

- **Derived facts are committed; the schemas are not.** The legality sets
  are the precedent: values from the schemas, committed as facts, checked
  against the schemas by a test that runs where the schemas are. The same
  holds for requiredness, defaults, simple-type facets and identity
  constraints, all per version.
- **The library reads tables, not schemas.** No XSD processing at run
  time, no cgo, no file access; `gsdml` stays pure Go and deterministic.
  The conceptual APIs of the brief become lookups like
  `DataTypeLegality(version, ctx, dt)`, which exists: an attribute's
  requiredness and default for a version, its lexical type, the identity
  constraints of an element. Most should stay unexported until a caller
  outside the package needs them.
- **xmllint is an oracle, never a dependency.** It verifies the corpus and
  serves as the reference in differential tests that run locally (and in
  CI where the schemas are available); no library code calls it.

## Proposed changes

Each entry: the current state and its weakness, the mechanism, whether it
is generated, whether independent validation applies, benefit, cost,
compatibility, run time, security, tests, priority.

### Category A: immediate wins

**A1. ID, IDRef and token types follow the schemas.** Currently narrower
patterns than `xsd:token` (findings 1, 7). Mechanism: catalog simple types
`ID`, `IDRef`, `SoftwareRelease` and the other token-based types take the
schemas' lexical space, with `xsd:token` whitespace handling (collapse
before comparing). Generated: yes, from the catalog as today. Independent
validation: xmllint over the corpus. Benefit: removes the largest class of
false rejections. Cost: small. Compatibility: `Validate` accepts more;
nothing it accepted before is refused. Run time: none. Security: IDs may
now contain spaces; they are never used as paths or keys outside the
document. Tests: the corpus cases as fixtures (synthetic, since vendor
files cannot be committed), each failing before. Priority: 1.

**A2. Absent SubslotNumber uses the schema default.** Finding 5.
Mechanism: `systemSubmodule` takes 32768 for an absent interface
`SubslotNumber` (V2.35's default; check every version first). Hand-written.
Benefit: a valid document converts. Cost: trivial. Compatibility: a
document refused before converts. Tests: a fixture without the attribute.
Priority: 1.

**A3. Differential test against xmllint.** Mechanism: a test behind a
build tag (`gsdmlxsd`), or a script, that validates every fixture and a
local corpus with xmllint and with `Validate`, and fails on an unexplained
disagreement (an allowlist names each known one with its finding).
Independent validation: yes, by definition. Benefit: every later change
is measured against the schemas. Cost: small (the harness used for this
assessment). Run time: test only. Security: xmllint runs with `--nonet`
on local files. Priority: 1.

**A4. Committed derived facts are checked.** `scripts/gsdml-xsd-audit.sh
-check` already compares the registry and the audit with the schemas.
Add it, with `gsdml-schemas-verify`, to a Make target the maintainer runs
before a release (`make gsdml-schemas-check`). Priority: 2.

### Category B: strategic improvements

**B1. Version-aware requiredness, defaults and choices.** Findings 3, 4
and 6 (the catalog's child notation needs a choice group).
Mechanism: `internal/xsd` reports, per version, each modelled attribute's
`use` and `default` and each child's `minOccurs`; a committed facts file
holds them; gengsdml generates version-indexed tables; `Validate` applies
the version of the document (as `datatype-context` does), falling back to
the catalog's current rule for an unknown version. Generated: yes.
Independent validation: xmllint differential. Benefit: no false
rejections of older documents; defaults stop being incidental.
Cost: medium (the reader resolves attribute uses today; defaults are read
already by `-xsd`). Compatibility: `Validate` accepts older documents it
refused; a V2.45 document without `CertificationInfo/@ConformanceClass`
is accepted. Whether a document without a version is checked by the
newest or the oldest rules is a **maintainer decision**. Run time: table
lookups. Tests: generated positive and negative cases per version (one
omission per required attribute). Priority: 2.

**B2. Identity constraints from the schemas.** Finding 2. Mechanism:
`internal/xsd` reads `xsd:key`, `xsd:keyref` and `xsd:unique` (name,
selector and field XPaths, which in these schemas are simple relative
paths); facts are committed per version; the generator emits them; a
hand-written evaluator in `gsdml` checks uniqueness within each
constraint's scope and resolves each keyref, replacing the global ID map.
Generated: the constraints yes, the evaluator no. Independent validation:
xmllint enforces identity constraints, so the differential test covers it.
Benefit: correct duplicate and reference checks; today's global check is
both too strict (scopes) and incomplete (keyrefs to ValueItems, texts).
Cost: medium to high: a restricted XPath subset (`.//a:B`, `a:B/a:C`,
`@ID`) must be implemented and its limits enforced at generation time (a
constraint outside the subset fails generation, not run time).
Compatibility: fewer false errors, possibly new true ones (a keyref that
resolves nowhere). Run time: one walk per constraint; bounded by the
document size limits that exist. Security: the XPath subset is fixed at
generation time; documents cannot inject expressions. Priority: 3.

**B3. Schema evolution report.** Mechanism: a generated diff per adjacent
version pair (elements, attributes, requiredness, defaults, enumerations,
identity constraints), committed as JSON and Markdown like the audit.
Generated: yes. Benefit: a new schema version (V2.50) becomes a reviewed
diff rather than a reading exercise; the catalog's `versions` and
FEATURE-STATUS follow from it. Cost: medium; most comparisons exist in
`-xsd` and `-audit`. Priority: 3.

**B4. Coverage of the unmodelled elements.** Finding 9. Mechanism: extend
the catalog element by element where a consumer needs the content
(`ChannelProcessAlarmList` and `UnitDiagTypeList` for diagnosis decoding
in profinetctl, for example), each with its version facts from B1.
Hand-written catalog entries, generated types. Benefit: real features.
Cost: per element. Which elements matter is a **maintainer decision**.
Priority: by need.

**B5. profinet-interop and profinetctl.** profinetctl's `gsdml check`
prints the new findings without changes. A `profinetctl gsdml validate
--xsd DIR` that shells out to xmllint would put an external dependency in
the CLI: offer it only if the maintainer wants it, and never by default.
profinet-interop can run the differential test in its containers, where
installing libxml2 is free, against fixtures generated by go-profinet;
the schemas would have to be mounted, not baked into an image (they are
private). Priority: 4.

### Category C: experimental

**C1. Schema-driven negative tests.** Generate, per version, documents
that violate one rule each (a missing required attribute, a value outside
an enumeration, a duplicate key, a dangling keyref), validate them with
xmllint and `Validate`, and require both to reject. Promising, but the
generator of valid base documents is the hard part; prototype on the
fixtures first.

**C2. Schema-aware fuzzing.** Seed the `gsdml` fuzz targets with
mutations that keep the document schema-valid (from the facts tables), to
reach deeper than byte-level mutation does. Measure coverage gain against
the existing corpus before keeping it.

**C3. Round-trip fidelity against the schemas.** The fixtures round-trip
through `Marshal` and `ParseBytes` today; checking that every vendor file that
is schema-valid stays schema-valid after a round trip (xmllint before and
after) is cheap with A3 in place and should run with it.

### Category D: avoid

**D1. Full XSD-to-Go model generation.** 252 element and 438 attribute
declarations in the V2.46 DeviceProfile alone, 18 versions, deep type derivation and
substitution: the result would be a large, version-forked API, and it
would replace a hand-shaped model that preserves unknown content
verbatim. No finding above needs it.

**D2. libxml2 in the library.** cgo, a C dependency and an XSD processor
on untrusted input, against rules 4 and 6 of AGENTS.md.

**D3. A Go XSD validator.** A general XML Schema 1.0 validator is a
project of its own; B1 and B2 extract the parts of the schemas this
library needs, and xmllint covers the rest as an oracle.

**D4. Reading schemaLocation from documents.** Tools resolve schemas
only in the pinned local corpus, never from a path or URL a document
names. This is how `internal/xsd` (os.Root) and `scripts/gsdml-schemas.sh`
(`--nonet`) work today; it must stay so.

**D5. Notes from the TIA copy.** It has no comments (finding 10);
obsolescence must come from another source or not at all.

## Decision matrix

| Approach | Fit with go-profinet | Benefit | Cost | Risk | Verdict |
|---|---|---|---|---|---|
| Full XSD-to-Go model generation | poor: replaces the hand-shaped, verbatim-preserving model | low: no finding needs it | very high | API churn per version | avoid (D1) |
| Selective Go type generation | already the design (catalog → types) | medium: new elements as needed | low per element | low | continue (B4) |
| Generated validation metadata | good: extends the legality precedent | high: fixes findings 1–4, 6 and 7 | medium | low; facts are checked against the schemas | **adopt (A1, B1, B2)** |
| Generated native validators | partial: generate the rules, keep the evaluator hand-written | high for identity constraints | medium | XPath subset must be closed | adopt as B2, evaluator hand-written |
| Independent libxml2 validation | as an oracle only | high: ground truth for every change | low | none, if never in the library | **adopt as oracle (A3)**, avoid in the library (D2) |
| Schema-driven test generation | good, after A3 | medium to high | medium | base-document generation | experiment (C1) |
| Hybrid: facts generated, behaviour hand-written, xmllint as oracle | matches the existing pipeline | highest | medium overall | low | **recommended** |

## What can be done now, and what needs a decision

Can be done now, each as its own PR with a RELEASE.md entry:

1. A1 (ID, IDRef and token types) with synthetic regression fixtures.
2. A2 (SubslotNumber default).
3. A3 (differential test behind a build tag, with an allowlist that names
   each remaining disagreement and its finding).
4. A4 (a Make target that checks the committed facts against the schemas).

Needs a maintainer decision:

- **CI access to the private schemas.** A read-only token or deploy key as
  a repository secret would let a scheduled or manually started job run
  `gsdml-schemas-verify`, the audit check and the differential test. It
  must never run on pull requests from forks. Without it, these checks
  stay local, and the committed facts are checked only when the
  maintainer runs them.
- **The vendor corpus.** The 130 files are vendors' copyright and cannot
  be committed. A private corpus repository (as for SCL,
  otfabric/iec61850-scl-corpus) would make the differential test
  reproducible; otherwise it runs on whatever the developer has.
- **Validate's strictness policy.** Whether `Validate` should aim to
  accept exactly what the schema of the document's version accepts (the
  recommendation), and how to treat a document with no version.
- **Forbidden DataTypes as errors.** The legality evidence is now every
  version, from a reserialised but complete copy. Promoting
  `datatype-context` from warning to error would make `Validate` fail on
  documents it accepts today: a behavioural break for v0.x consumers.
  Recommendation: keep the warning until B1 makes the rest of `Validate`
  as faithful, then decide.
- **Which unmodelled elements to model** (B4).
- **An xmllint-backed CLI command** (B5).

Remaining phases of the brief, mapped: schema analysis engine (phase 3) is
`internal/xsd` extended by B1 and B2; code generation (4) is the
recommendation above; layered validation (5) is parse, `Validate` with
generated facts, `Check` rules, `ToModel`, and xmllint outside the
library; the implementation audit (6) is the Findings; datatype and
DefaultValue questions (7) are answered in GSDML-XSD-AUDIT.md and
GSDML-CORPUS-DEFAULTS.md; evolution (8) is B3; testing (9) is A3, C1 to C3;
interop and CLI (10, 11) are B5; security (12) is D4 and the generation-time
XPath subset of B2; CI (13) is A4 and the CI decision; documentation (14)
is this file, CODEGEN.md and the audit.
