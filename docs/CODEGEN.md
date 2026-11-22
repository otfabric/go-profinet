# Code generation: the GSDML types

go-profinet has two generated GSDML layers:

- **The catalog model** (`gsdml`): the Go types of the content a device
  model needs (`gsdml/zz_generated.go`, about 2600 lines for 83 elements)
  and their reference (`gsdml/SCHEMA.md`), generated from one input,
  `gsdml/schema/catalog.json`, by `internal/cmd/gengsdml`. It is what
  `ToModel`, `FromModel`, `Check` and `Validate` use. The rest of this
  document up to [The generated schema model](#the-generated-schema-model)
  is about it.
- **The schema model** (`gsdml/typed`, `gsdml/versions/vNNN`): every type
  of every reference schema version V1.0 to V2.46, with a decoder, an
  encoder and a validator, generated from the schemas themselves by
  `internal/cmd/gsdmlc` ([The generated schema model](#the-generated-schema-model)).

## Why a catalog, not the XSDs

(What changes once OT Fabric is a PI member: [PI-MEMBERSHIP.md](PI-MEMBERSHIP.md).)

The GSDML XSDs are published by PROFIBUS & PROFINET International to its
members under a licence that does not allow redistributing them
([research/GSDML.md](research/GSDML.md)), so they are not in this
repository and nothing in the library or its tests needs them. The catalog is written
independently from public descriptions of GSDML and contains no text of the
schemas. It describes the elements and attributes go-profinet models; a
document's other content is preserved verbatim (`Attrs`, `Unknown`), so
reading and writing a file loses nothing.

## The catalog

```json
{"name": "SubslotItem", "attrs": ["SubslotNumber:Unsigned16!", "TextId:IDRef!"]},
{"name": "ApplicationProcess", "children": ["DeviceAccessPointList", "ModuleList?", ..., "ExternalTextList"]},
```

- `simpleTypes` define the attribute types: a pattern, an enumeration, an
  unsigned width (decimal or `0x` hexadecimal), and a doc string.
- `elements` list each element with its doc, its attributes
  (`name:type`, `!` required, `~` always written but may be empty) and its
  children (`?` optional, `*` repeated, `+` one or more) or its text type.
- `versions` are the schema versions the catalog was written for (2.25 to
  2.45).

## The generator

```bash
go generate ./gsdml                         # or: make generate
go run ./internal/cmd/gengsdml -check       # make generate-check: fail when out of date
go run ./internal/cmd/gengsdml -xsd DIR     # compare the catalog with licensed XSDs in DIR
go run ./internal/cmd/gengsdml -audit V=ROOT.xsd,...  # DataType enumerations per context, as JSON
```

For each element it generates a struct with `encoding/xml` tags (attributes
as strings, so that a document round-trips exactly as written, children as
pointers or slices), a `validate` method (required attributes and children,
lexical types, ID uniqueness, ID and text references) and its section in
SCHEMA.md. An attribute with a default (`=value`) gets an unexported
`<name>OrDefault` accessor. An element with optional typed attributes
(not `!`, not `~`, not string, not `xml:`) gets an `UnmarshalXML` that
records which of them the document gave as `""`, so that the accessors
and `validate` tell an empty attribute from an absent one. `gsdml.CatalogSHA256` records the hash of the catalog the code was
generated from; CI fails when they differ (`make generate-check`).

`-xsd DIR` reads the XSDs of a licensed local copy with the small XSD reader
of `internal/xsd` and reports elements and attributes the catalog lacks, has
in excess, or types and requiredness that differ. Against GSDML v2.35 it
reports no difference for the modelled elements.

`-audit V=ROOT.xsd,...` reads each schema set from its root document,
following its includes and imports (`xsd.LoadRoot`), and reports as JSON,
for every element occurrence with a `DataType` attribute, the enumeration it
takes (through restriction bases and unions), the comments that mark values
obsolete, the differences with the catalog's DataType enumeration and with
the previous set given, the files read with their SHA-256 and the catalog
versions no set covers. `V` is the GSDML version of the set: the schemas'
own `version` attribute is unreliable (V2.0 to V2.2 say `1.0`). The report
holds no timestamps or absolute paths, so it is reproducible.
A directory stands for every DeviceProfile in it, oldest version first.

`-registry DIR` reports, as JSON, every GSDML version in a directory of
schemas: its DeviceProfile and Primitives files, version attribute, target
namespace, revision date (`schemainfo/@versiondate`), the files it reads and
any it names that are missing, and whether the catalog was written for it
and has its DataType sets; then every file with its size, SHA-256, imports
and the versions that read it. Versions are ordered by GSDML number (2.4 is
2.40, after 2.35), never as decimals or semantic versions. When the
directory has a `SHA256SUMS`, every file must match it.

`scripts/gsdml-xsd-audit.sh` runs both on the reference schemas (see
below) and writes `gsdml/schema/xsd-registry.json` and
docs/research/gsdml-xsd-audit.json; docs/research/GSDML-XSD-AUDIT.md
explains the audit. Both reports hold facts about the schemas, never their
text.

## The reference schemas

The maintainers keep a licensed copy of the schemas, GSDML V1.0 to V2.46
(40 files from the TIA Portal V21 installation, byte for byte, with their
SHA-256 checksums), in the private repository
otfabric/profinet-gsdml-schema. go-profinet uses a checkout of it for
validation and code generation only, never commits or embeds it:

```bash
make gsdml-schemas          # clone it at the pinned commit into .gsdml-schemas/ (git-ignored)
make gsdml-schemas-verify   # pinned commit, no local changes, checksums, well-formed, every DeviceProfile compiles
```

```bash
make gsdml-differential     # gsdml.Validate against xmllint on the fixtures (GSDML_CORPUS=DIR adds files)
make gsdml-schemas-check    # verify, the differential test, and the committed registry and audit against the schemas
```

`scripts/gsdml-schemas.sh` holds the pinned commit (`GSDML_SCHEMAS_REF`
overrides it); `GSDML_SCHEMAS` points at another checkout. The checks need
libxml2's `xmllint` and run it with `--nonet`: every import resolves in
the checkout. Fetching needs read access to the private repository, so
these targets are not part of `make check`; the GSDML schemas workflow
(`.github/workflows/gsdml-schemas.yml`) runs `make gsdml-schemas
gsdml-schemas-check` on pull requests and weekly with
`OTFABRIC_READ_TOKEN`.

The differential test (`gsdml/xsd_differential_test.go`, build tag
`gsdmlxsd`) fails on any difference between xmllint and `Validate` that
`gsdml/testdata/xsd-differential.json` does not explain: `known` lists
the findings that explain an error on a schema-valid file (a message and
path pattern, and for a requirement a newer version added, the version
it starts with), `lenient` the schema-invalid files `Validate` accepts, by
SHA-256. With a corpus, a known finding that explains nothing any more
fails the test, so the list shrinks as the findings are fixed.

## The generated schema model

`internal/cmd/gsdmlc` reads the 18 DeviceProfile schemas of the checkout
with what they import (`internal/xsdc`: a loader for the XML Schema 1.0
constructs the GSDML schemas use, which fails on any other) and writes:

| File | What |
|---|---|
| `gsdml/typed/zz_types.go` | a struct per complex type and variant, typed fields (`string`, `bool`, sized integers, `Integer`, `Decimal`, `float64`, enumerations, lists), `…OrDefault` accessors for schema defaults, and straight-line decode and encode methods (no reflection) |
| `gsdml/typed/zz_tables.go` | the validation tables: simple types with every facet, patterns translated to RE2, complex types with their attribute uses and content automata (Glushkov, checked for Unique Particle Attribution at generation time), element declarations with substitution groups, wildcards and identity constraints, per version |
| `gsdml/typed/zz_versions.go` | the decoder of each version's root |
| `gsdml/versions/vNNN/zz_facade.go` | each version's names as aliases of the shared structs, its enumeration constants, `Parse`, `Decode`, `Validate` |
| `gsdml/typed/zz_samples_test.go`, `gsdml/versions/vNNN/zz_facade_test.go` | a sample document for every struct, decoded, encoded and decoded again; each facade against its version |
| `docs/GSDML-TYPED.md` | inventory, deduplication and evolution per version (names and counts, no schema text) |

Deduplication: `xsdc.Analyze` partitions the (component, version) pairs by
refinement (equal local signature, and the components they refer to in one
class, recursively) at three levels. A struct is shared by every version
whose type has the same Go representation (`ModuleItemT_V231` is the
`ModuleItemT` of V2.31 and of each later version until it changes); the
validation tables are shared where the full rules agree. The numbers are in
docs/GSDML-TYPED.md and docs/research/GSDML-CODEGEN.md;
`internal/xsdc`'s `TestReferenceSchemas` fails when they move.

The generated files are committed, so building and testing need no
schemas. With the checkout:

```bash
make gsdml-generate         # regenerate gsdml/typed, gsdml/versions and docs/GSDML-TYPED.md
make gsdml-generate-check   # fail when the committed files differ (part of make gsdml-schemas-check)
```

What is copied from the schemas into the generated code is what a
validator needs (names, types, facet values, enumeration values, patterns,
content models); no documentation text of the schemas is.

The schema model's differential test (`gsdml/typed/xsd_differential_test.go`,
build tag `gsdmlxsd`) has no allowlist: every fixture against every
version, every corpus file against its own, the hand-written mutants of
`TestMutants`, 24 seeded mutants of each input, and the encoding of every
valid input must get xmllint's verdict. The `gsdml` differential also
requires `gsdml.ValidateBytes` to agree with xmllint on every file.

## Changing the catalog

1. Edit `gsdml/schema/catalog.json` (keep it independent: describe, do not
   copy schema text).
2. `make generate`; review the diff of `zz_generated.go` and `SCHEMA.md`.
3. Add or adjust the conversion in `gsdml/model.go` if the model uses the
   new content, and a test with a fixture.
