# Code generation: the GSDML types

The Go types of GSDML documents (`gsdml/zz_generated.go`, about 2600 lines
for 83 elements) and their reference (`gsdml/SCHEMA.md`) are generated from
one input, `gsdml/schema/catalog.json`, by `internal/cmd/gengsdml`.

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
`scripts/gsdml-xsd-audit.sh` runs it on five versions;
docs/research/GSDML-XSD-AUDIT.md is the result.

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

`scripts/gsdml-schemas.sh` holds the pinned commit (`GSDML_SCHEMAS_REF`
overrides it); `GSDML_SCHEMAS` points at another checkout. The verify step
needs libxml2's `xmllint` and runs it with `--nonet`: every import resolves
in the checkout. Fetching needs read access to the private repository, so
these targets are not part of `make check` or of CI.

## Changing the catalog

1. Edit `gsdml/schema/catalog.json` (keep it independent: describe, do not
   copy schema text).
2. `make generate`; review the diff of `zz_generated.go` and `SCHEMA.md`.
3. Add or adjust the conversion in `gsdml/model.go` if the model uses the
   new content, and a test with a fixture.
