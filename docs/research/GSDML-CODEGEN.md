# GSDML schema compiler: design, measurements and status

Status: **implemented** (go-profinet v0.14.0). This document replaces the
architecture section of [GSDML-SCHEMA-PROPOSAL.md](GSDML-SCHEMA-PROPOSAL.md),
which the maintainer superseded on 2026-11-22: complete generation from
every schema version is the objective, and the volume of generated code is
not an argument against it. Its items A1 to A4 were done before; B1 to B5
and C1 to C3 are done here (status table at the end); D1 ("avoid full
generation") is overruled.

Every number below was measured on the code of this change. Sizes and
times: darwin/arm64, Apple M1, Go 1.27.4.

## 1. Inventory

The private repository otfabric/profinet-gsdml-schema holds 40 files: 18
DeviceProfile schemas (V1.0, 2.0, 2.1, 2.2, 2.25, 2.3, 2.31 to 2.35, 2.4,
2.41 to 2.46), 18 Primitives schemas, `xml.xsd`, xmldsig (imported from
V2.0) and XAdES (V2.41 and V2.42 only). Common-Primitives-v1.0.xsd is
imported by none. All versions share one target namespace, so a document's
version comes only from its `xsi:schemaLocation`.

| Measure | Count |
|---|---|
| (complex type, version) pairs, anonymous types included | 2921 |
| distinct complex type identities (name or path) | 354 |
| (simple type, version) pairs / identities | 1915 / 199 |
| (global element, version) pairs / identities | 556 / 93 |
| constructs used | sequence, choice, element refs, substitution groups (abstract heads), `xsd:any` and `anyAttribute` (`##other`, `##any`, lists; strict, lax, skip), extension and restriction of simple and complex content, named groups, list and union types, every facet but `totalDigits`/`fractionDigits`, key, keyref, unique |
| constructs not used (refused by the compiler) | `xsd:all`, `include`, `redefine`, attribute groups, `nillable`, occurrence bounds other than 0, 1 and unbounded (handled anyway by expansion) |
| content models that let two element names interleave | 24 types (their structs keep the document order) |

The per-version table is generated: [GSDML-TYPED.md](../GSDML-TYPED.md).

## 2. Deduplication

`internal/xsdc.Analyze` computes, by partition refinement over all
(component, version) pairs, the coarsest classes in which two pairs agree
on their local signature and the components they refer to are pairwise in
one class. Three levels, each a refinement of the previous one:

| Level | What must agree | Complex: 2921 pairs → | Simple: 1915 → | Saved (complex) |
|---|---|---|---|---|
| representation | the Go struct: field names, Go kinds, multiplicity | 828 | 204 | 71.7 % |
| codec | plus element order and content model | 836 | 221 | 71.4 % |
| full | plus every facet, default, fixed value, identity constraint | 917 | 253 | 68.6 % |

Global elements: 556 pairs → 110 classes. The generator uses the
representation level for structs (828 structs; a type that changes in a
version gets a new struct named after the first version of the variant,
`ModuleItemT_V231`) and interns the validation tables by content (881
complex specs, 264 simple specs, 1030 element declarations, 159 identity
constraints, 46 patterns, 9 wildcards for all 18 versions). The test
`TestReferenceSchemas` (internal/xsdc, build tag `gsdmlxsd`) pins these
counts.

## 3. Package layout

```text
internal/xsdc            generation time only: schema loader, resolver, Glushkov automata,
                         UPA check, shapes, deduplication (no library package imports it)
internal/cmd/gsdmlc      the generator (make gsdml-generate)
gsdml/typed              runtime: Node reader, encoder, validator, builders (hand-written)
                         + zz_types.go, zz_tables.go, zz_versions.go (generated)
gsdml/versions/v100 …    one package per version: aliases, enum constants, Parse/Decode/Validate
  v246
gsdml                    the catalog model (unchanged) + CheckBytes/ValidateBytes (schema-backed)
```

No library package reads a schema at run time; the generated code has no
reflection; everything is standard library.

## 4. APIs

Unified (`gsdml/typed`): `DetectVersion`, `Versions`, `Validate`,
`ValidateVersion`, `Parse`, `ParseVersion`, `Decode`, `DecodeVersion`
returning a `Versioned` (`*Document[T]` with `Version`, `Root`,
`SchemaLocation`, `Marshal`, `Encode`, `Validate`), `ParseNode` for the
generic tree. Errors: `*SchemaError` (line, path, message per issue;
matches `ErrMalformed`), `ErrUnknownVersion`.

Version-specific (`gsdml/versions/v246`): `v246.ISO15745Profile`,
`v246.ModuleItemT`, … as aliases of the shared structs, so a value of a
type that did not change between versions is the same Go type in both;
`v246.Parse`, `Decode`, `Validate`, and the enumeration constants.

`gsdml`: `CheckBytes` and `ValidateBytes` validate the original octets
against the version the document names (structure, facets, identity
constraints, in their scopes) and add `Check`'s semantic and
physical-limit rules; the schema's issues carry `RuleSchema` and a line.
`Check` and `Validate` on a `*Document` keep the catalog's rules: a
re-encoding by `gsdml.Marshal` writes defaults and the newest element
order, which older versions do not accept (measured: of the 128 corpus
and fixture files the schema of their version accepts, 58 are not valid
any more after `ParseBytes` and `Marshal`).

## 5. Codec and validation architecture

- **Reading.** `ParseNode` tokenises with `encoding/xml` without namespace
  translation and resolves prefixes itself (an undeclared prefix,
  `xmlns:p=""` or a rebound reserved prefix is malformed), with the
  package's limits (64 MiB, 2^20 elements, depth 256), BOM, ISO-8859-1 and
  windows-1252, and refuses an internal DTD subset.
- **Validation** walks the tree once. Each element's content runs its
  deterministic automaton (state 0 to final), element transitions match a
  declaration or a member of its substitution group, wildcard transitions
  apply strict, lax or skip processing. Attributes are matched against the
  type's uses, then its attribute wildcard. Simple values are normalised
  by the type's white-space rule and checked step by step up the
  derivation chain (union members, list items, every facet in the value
  space: big.Rat for decimals, float64 for floats). Identity constraints
  evaluate the restricted XPath on the subtree, compare values in their
  value space, and resolve each keyref in the key tables of its element's
  subtree.
- **Decoding** is generated straight-line code per struct: a switch over
  the attribute and element names, typed parsers, the document order kept
  for the 24 interleaving types, open content (`xsd:any`, mixed,
  `anyType`) kept as `*Node`. `Decode` fails only on what the struct
  cannot hold; `Parse` validates first.
- **Encoding** writes the struct back in content-model order (or the kept
  order), with namespace declarations it chooses itself.

## 6. Measurements

**Generated code.** 61 388 lines in `gsdml/typed` (zz_types.go 54 065,
zz_tables.go 2 429, zz_samples_test.go 4 828), 13 270 in the 18 facades,
3.9 MB of source; 2 625 hand-written runtime lines, 5 210 in the compiler.
Generation (`make gsdml-generate-check`, compiler built) takes 0.9 s.

**Build.** Cold `go build ./gsdml/typed`: 5.6 s wall (18.9 s CPU); the 18
facades 0.2 s; `go vet ./gsdml/typed` 3.9 s.

**Binary size** (a program that reads a file and calls one function):

| Program | Size |
|---|---|
| imports `gsdml`, does not call `CheckBytes` (examples/gsdml): before / after | 4.35 MB / 4.38 MB |
| `typed.Validate` (all 18 versions) | 4.43 MB |
| `gsdml.CheckBytes` | 5.47 MB |
| `v246.Parse` (one version's decoders and the validator) | 5.43 MB |
| `typed.Parse` (every version's decoders) | 8.43 MB |

The decoders are reached through a generated switch, not a table, and a
facade decodes through its own root type, so the linker keeps only what a
program calls. The first measurement with a table of decoders built at
init time was 8.40 MB for a program that never decodes.

**Throughput** (`go test -bench . ./gsdml/typed`):

| Operation | 16 KB fixture (V2.43) | 31.6 MB vendor file (V2.45) |
|---|---|---|
| `ParseNode` | 279 µs, 60 MB/s | 0.42 s, 76 MB/s |
| `Validate` | 507 µs, 33 MB/s | 1.74 s, 18 MB/s, 802 MB allocated |
| `Decode` | 292 µs, 57 MB/s | 0.48 s, 66 MB/s |
| `Parse` (validate and decode) | 534 µs, 31 MB/s | 1.82 s |
| `Encode` | 34 µs, 492 MB/s | — |

`xmllint --noout --schema` takes 1.34 s on the same 31.6 MB file.

## 7. Testing

Without the schemas (CI's `make check`): a sample document for every one
of the 828 structs, decoded, encoded, decoded again; unknown attributes
and elements refused unless a wildcard allows them; every facade against
its version; golden encodings of the fixtures and a golden issue list;
29 hand-written one-rule mutants with the expected issue; the simple-type
checker on tables written by hand; `ParseNode`'s namespace and limit
errors; `internal/xsdc` on a synthetic two-version schema set with every
construct the compiler implements and each error it reports; fuzzing of
`ParseNode`, `Validate` and `Decode` (an accepted document re-encodes to
a valid, stable document).

With the schemas (the GSDML schemas workflow, `make gsdml-schemas-check`):
the generated files must equal what the schemas generate; the
deduplication counts; and the differential test against xmllint, with no
allowlist, on the fixtures against all 18 versions, the hand-written
mutants, 24 seeded mutants per input and the re-encoding of every valid
input. With the local corpus of 130 public vendor files (not committed):
134 inputs, 3 404 xmllint runs, no disagreement; `gsdml.ValidateBytes`
agrees with xmllint on all 131 files that name a version. The fuzzers and
the tests found and fixed: an undeclared prefix accepted, an empty CDATA
section changing the encoding, a crash on a restricted list type in the
compiler, and follow-on keyref issues after a content error.

## 8. Migration

Nothing that worked changes: the catalog model, `Check`, `Validate`,
`ToModel` and `FromModel` behave as in v0.13. New: `gsdml.CheckBytes` and
`ValidateBytes` for files read from disk (recommended for validation),
`Issue.Line`, `RuleSchema`, and the `gsdml/typed` and `gsdml/versions`
packages. profinetctl's `gsdml check` and `gsdml validate` move to
`CheckBytes`; profinet-interop's fixtures generated by go-profinet are
checked with it.

## 9. Risks

- **One oracle.** Agreement is with libxml2 only; a shared misreading of
  XML Schema would pass. Mitigation: hand-written mutants with stated
  expectations, and the rules each implement the XML Schema 1.0 text.
- **Schema provenance.** The schemas are TIA Portal V21's reserialised
  copies, not PI's distribution; a difference would not be noticed.
- **Size of generated code.** 3.9 MB of source; acceptable by the
  maintainer's decision; the linker keeps only what a program calls.
- **A new schema version** (V2.5x) needs its schema in the private
  repository and `make gsdml-generate`; the compiler fails on any construct
  it does not implement rather than approximating it.
- **API surface.** 828 structs and 18 facades are public; a schema
  correction can rename a variant (a new `_Vnnn` struct). Version-specific
  names in the facades are the stable way to refer to a type.

## 10. Status of the plan

| Item | Status |
|---|---|
| A1–A4 | done before this change (v0.13.x) |
| B1 version-aware requiredness, defaults and choices | done: the validation tables of each version; `CheckBytes`; `…OrDefault` accessors |
| B2 identity constraints from the schemas | done: generated constraints, hand-written evaluator, scoped keys and keyrefs; `CheckBytes` uses them instead of the global ID table |
| B3 schema evolution report | done: [GSDML-TYPED.md](../GSDML-TYPED.md), generated and checked |
| B4 coverage of the unmodelled elements | done for the schema model: every element of every version has a type; the catalog model is unchanged |
| B5 profinet-interop and profinetctl | profinetctl and profinet-interop move to this release (their own pull requests) |
| C1 schema-driven negative tests | done: hand-written and seeded mutants, verdicts checked against xmllint |
| C2 schema-aware fuzzing | partly: the fuzz targets start from schema-valid fixtures and check that what validates re-encodes valid; no mutator keeps documents valid by construction, and the coverage gain was not measured |
| C3 round-trip fidelity | done: every valid input's encoding is valid (xmllint) and stable |
| D1 avoid full generation | overruled by the maintainer; this document |
