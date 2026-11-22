# GSDML DataType legality by schema version and context

Which `DataType` values each GSDML schema version allows in each element
context, from five schema versions, compared with go-profinet's catalog
(`gsdml/schema/catalog.json`). This is an audit of evidence. The library applies it as described in
"Legality in the catalog and generator" below.

The machine-readable report is [gsdml-xsd-audit.json](gsdml-xsd-audit.json).
`scripts/gsdml-xsd-audit.sh` fetches the schemas, checks their SHA-256 and
regenerates it (`-check` fails when the committed report differs). The tables
below are derived from that report.

## Sources

The official schemas are distributed by PROFIBUS & PROFINET International
(PI) to members, under a licence that does not allow redistribution. The
copies audited here are third-party copies on GitHub. They are fetched into
the git-ignored `.xsd/` and never committed. Only facts derived from them
(value names, hashes, contexts) are in this repository.

| Version | Repository, commit | Licence of the repository | Root document |
|---|---|---|---|
| V1.0, V2.0, V2.1, V2.2 | `DaltonManning/lastCBOpenIF` b8251053e28a | none stated | `GSDML-DeviceProfile-V*.xsd` |
| V2.35 | `TINF17C/GSD2AML-Converter` d1a35b7c0d0d | AGPL-3.0 (applies to the repository's code, not to PI's schemas) | `GSDML-DeviceProfile-v2.35.xsd` |

The per-file SHA-256 values are in the report. These are copies, not PI's
distribution. Nothing here establishes that they are unmodified. A holder of
the official files can run the same audit on them:

```bash
go run ./internal/cmd/gengsdml -audit 2.35=DIR/GSDML-DeviceProfile-v2.35.xsd,...
```

Two properties of the sources matter for reading them:

- **Version.** The root documents of V2.0, V2.1 and V2.2 carry
  `version="1.0"` on `xsd:schema`. The version is therefore given to the tool
  (`2.0=PATH`); the attribute is recorded as `schemaVersionAttribute`.
- **White space.** V2.0's `DataItemTypeEnumT` has the value
  `"F_MessageTrailer5Byte "`, with a trailing space. On an `xsd:string` base
  the space is significant, so this copy does not validate a V2.0 document
  that writes `F_MessageTrailer5Byte`. V2.1 drops the space. The report lists
  such values under `whitespace`. The tables show the space as `␠`.

## Method

`internal/xsd` reads the root document and every document its `include`,
`import` and `redefine` name (at most 64; `xml.xsd` and
`xmldsig-core-schema.xsd` are not in the copies and are listed as `missing`).
It resolves named and anonymous complex types, extension bases and
attribute groups to find every element occurrence that has a `DataType`
attribute, together with its parent. It then resolves the attribute's
simple type through restriction bases and unions to an enumeration. An XML
comment that follows an enumeration value is recorded as a note: the
schemas mark obsolete values this way.

Within each version, the occurrences grouped in one table below have
identical enumerations. The report keeps them separate.

## Findings

1. **There is no single DataType enumeration.** V1.0 to V2.2 have two
   enumerations, one for IO data and one for parameter and diagnosis Refs.
   V2.1 adds a third, for extended channel diagnosis. V2.35 has five. The
   catalog has one, with 51 values, which it uses for every context.
2. **Legality is not monotonic.** The extended channel diagnosis context
   allowed `OctetString`, `VisibleString`, `TimeOfDay without date
   indication` and `TimeDifference without date indication` in V2.1 and
   V2.2, and V2.35 removed them. The V2.35 unit diagnosis Ref removed the
   seven date and time types that V2.1 and V2.2 allowed. A model in which a
   value only has an introduction version cannot represent this. Each
   version needs an exact set.
3. **Catalog values in no audited schema:**
   - `Float64+Status8`, `Integer32_S`, `Unsigned32_S` and `UTF8String` are in
     no context of any of the five versions.
   - The catalog has had them since its first commit (315320e, 2026-10-26).
     That catalog was "written independently from public descriptions of
     GSDML" and records no source per value, so their provenance is
     unknown: they are UNVERIFIED.
   - No source the package has defines a layout for `Integer32_S` or
     `Unsigned32_S`. They are not Ref DataTypes in OPC 30144 v1.00
     Table 58.
4. **Schema values not in the catalog:** `UnicodeString8`, `61131_STRING`
   and `61131_WSTRING`. V2.35 allows all three in IO data and in parameter
   Refs.
5. **Context rules of V2.35** (verified in that copy only):
   - `Bit` and `BitArea` are allowed only in parameter and unit diagnosis
     Refs, not in IO data.
   - `Float32+Status8` is IO-only and annotated obsolete, superseded by
     `Float32+Unsigned8` (V2.2 already annotates it so).
   - `Boolean` is not in the unit diagnosis Ref enumeration.
   - `F_MessageTrailer4Byte` and `F_MessageTrailer5Byte` are IO-only.
   - The four `_S` types with an established layout (`Unsigned8_S`,
     `Unsigned16_S`, `Integer16_S`, `OctetString_S`) are allowed in IO data
     and in parameter Refs.
6. **Introductions between V2.2 and V2.35 are unknown.**
   - Absent in V2.2, present in V2.35: `Boolean`, `UnicodeString8`, the
     61131 strings, the four `_S` types, the PROFIdrive types (`N2` … `X4`,
     `Unipolar2.16`), the TimeStamp types and `OctetString2+Unsigned8`.
   - `Float32+Unsigned8` and `Unsigned8+Unsigned8` are in V2.2 IO data but
     not in its parameter Ref. Both are in the V2.35 parameter Ref.
   - Without V2.25 to V2.34 there is no telling which version introduced
     which of these.

## Unverified versions

No schema in this audit covers these versions, so nothing here is evidence
about them. The catalog's versions without an audited schema are listed in
the report as `unverifiedCatalogVersions`:

- V2.25, V2.3, V2.31, V2.32, V2.33 and V2.34;
- V2.4, V2.41, V2.42, V2.43, V2.44 and V2.45;
- every version after V2.45, such as V2.46 and V2.5x, which the catalog does
  not name.

The legality of a value in any of these versions must not be inferred from
V2.2 or V2.35. Finding 2 shows that a value present on both sides of a gap
may still have been removed and added again.

## Legality in the catalog and generator

Implemented after this audit, in the existing pipeline rather than as a
second source of truth:

1. **Catalog.** `gsdml/schema/catalog.json` has a `dataTypeLegality`
   section:
   - context names, each mapped to its `Parent/Element` occurrences (`io`,
     `parameter`, `unitDiag`, `extChannel`, `processAlarm`);
   - one exact value set per audited version and context;
   - the values a set cannot decide (`unverified`: V2.0's
     `"F_MessageTrailer5Byte "`).

   It has no `introducedIn` field. A gengsdml test checks every set against
   this report, so neither can change alone.
2. **Generated code.** The generator writes the tables, and
   `gsdml.DataTypeLegality` reads them. It is tri-state: allowed,
   forbidden or unverified. Any version without a set is unverified,
   including versions newer than the newest set.

   This keeps the following separate, each as it was: the vocabulary
   (`DataTypes`), sizing (`DataItemSize`), binary codecs (`pnio`), lexical
   parsing and DefaultValue capability (`ToModel`).
3. **Vocabulary.**
   - `UnicodeString8`, `61131_STRING` and `61131_WSTRING` are constants,
     with no size.
   - `Float64+Status8`, `Integer32_S`, `Unsigned32_S` and `UTF8String` stay,
     documented as found in no audited schema.
4. **Check** (rule `datatype-context`) covers the IO DataItem and the
   parameter Ref, the contexts the catalog models:
   - a value forbidden by an audited version is a warning, because the
     evidence is third-party copies, so `Validate`, which fails on errors,
     does not change;
   - a document of an unverified version, or with no version, gets one
     unvalidated issue.
5. **ToModel** is unchanged: it refuses by size and encoding, not by
   legality.
6. **CLI.** profinetctl's `gsdml check` lists the rule's issues like any
   other.

## Tables

`✓` allowed, `✗` not in the enumeration, `n/a` the context does not exist
in that version, `✓ obsolete` allowed with an obsolete annotation.
"catalog" is the current single enumeration.

### IO data (`Input`/`Output` `DataItem`)

| DataType | V1.0 | V2.0 | V2.1 | V2.2 | V2.35 | catalog |
|---|---|---|---|---|---|---|
| `61131_STRING` | ✗ | ✗ | ✗ | ✗ | ✓ | — |
| `61131_WSTRING` | ✗ | ✗ | ✗ | ✗ | ✓ | — |
| `Bit` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `BitArea` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Boolean` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `C4` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `D2` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `Date` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `E2` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `F_MessageTrailer4Byte` | ✗ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `F_MessageTrailer5Byte` | ✗ | ✗ | ✓ | ✓ | ✓ | ✓ |
| `F_MessageTrailer5Byte␠` | ✗ | ✓ | ✗ | ✗ | ✗ | — |
| `Float32` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Float32+Status8` | ✗ | ✓ | ✓ | ✓ obsolete | ✓ obsolete | ✓ |
| `Float32+Unsigned8` | ✗ | ✗ | ✗ | ✓ | ✓ | ✓ |
| `Float64` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Float64+Status8` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Integer16` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Integer16_S` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `Integer32` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Integer32_S` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Integer64` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Integer8` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `L2` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `N2` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `N4` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `NetworkTime` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `NetworkTimeDifference` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `OctetString` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `OctetString2+Unsigned8` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `OctetString_S` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `R2` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `T2` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `T4` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `TimeDifference with date indication` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `TimeDifference without date indication` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `TimeOfDay with date indication` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `TimeOfDay without date indication` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `TimeStamp` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `TimeStampDifference` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `TimeStampDifferenceShort` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `UnicodeString8` | ✗ | ✗ | ✗ | ✗ | ✓ | — |
| `Unipolar2.16` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `Unsigned16` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Unsigned16_S` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `Unsigned32` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Unsigned32_S` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Unsigned64` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Unsigned8` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Unsigned8+Unsigned8` | ✗ | ✗ | ✗ | ✓ | ✓ | ✓ |
| `Unsigned8_S` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `UTF8String` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `V2` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `VisibleString` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `X2` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `X4` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |

Types: V1.0 `DataItemTypeEnumT`, V2.0 `DataItemTypeEnumT`, V2.1 `DataItemTypeEnumT`, V2.2 `DataItemTypeEnumT`, V2.35 `DataItemTypeEnumT`.

### Parameter record (`ParameterRecordDataItem` and `Request` `Ref`)

| DataType | V1.0 | V2.0 | V2.1 | V2.2 | V2.35 | catalog |
|---|---|---|---|---|---|---|
| `61131_STRING` | ✗ | ✗ | ✗ | ✗ | ✓ | — |
| `61131_WSTRING` | ✗ | ✗ | ✗ | ✗ | ✓ | — |
| `Bit` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `BitArea` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Boolean` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `C4` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `D2` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `Date` | ✗ | ✗ | ✓ | ✓ | ✓ | ✓ |
| `E2` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `F_MessageTrailer4Byte` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `F_MessageTrailer5Byte` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Float32` | ✗ | ✗ | ✓ | ✓ | ✓ | ✓ |
| `Float32+Status8` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Float32+Unsigned8` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `Float64` | ✗ | ✗ | ✓ | ✓ | ✓ | ✓ |
| `Float64+Status8` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Integer16` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Integer16_S` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `Integer32` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Integer32_S` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Integer64` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Integer8` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `L2` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `N2` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `N4` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `NetworkTime` | ✗ | ✗ | ✓ | ✓ | ✓ | ✓ |
| `NetworkTimeDifference` | ✗ | ✗ | ✓ | ✓ | ✓ | ✓ |
| `OctetString` | ✗ | ✗ | ✓ | ✓ | ✓ | ✓ |
| `OctetString2+Unsigned8` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `OctetString_S` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `R2` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `T2` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `T4` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `TimeDifference with date indication` | ✗ | ✗ | ✓ | ✓ | ✓ | ✓ |
| `TimeDifference without date indication` | ✗ | ✗ | ✓ | ✓ | ✓ | ✓ |
| `TimeOfDay with date indication` | ✗ | ✗ | ✓ | ✓ | ✓ | ✓ |
| `TimeOfDay without date indication` | ✗ | ✗ | ✓ | ✓ | ✓ | ✓ |
| `TimeStamp` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `TimeStampDifference` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `TimeStampDifferenceShort` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `UnicodeString8` | ✗ | ✗ | ✗ | ✗ | ✓ | — |
| `Unipolar2.16` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `Unsigned16` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Unsigned16_S` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `Unsigned32` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Unsigned32_S` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Unsigned64` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Unsigned8` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Unsigned8+Unsigned8` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `Unsigned8_S` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `UTF8String` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `V2` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `VisibleString` | ✗ | ✗ | ✓ | ✓ | ✓ | ✓ |
| `X2` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| `X4` | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |

Types: V1.0 `DataTypeEnumT`, V2.0 `DataTypeEnumT`, V2.1 `DataTypeEnumT`, V2.2 `DataTypeEnumT`, V2.35 `RecordDataRefTypeEnumT`.

### Unit diagnosis (`UnitDiagTypeItem` and `ProfileUnitDiagTypeItem` `Ref`)

| DataType | V1.0 | V2.0 | V2.1 | V2.2 | V2.35 | catalog |
|---|---|---|---|---|---|---|
| `Bit` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `BitArea` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Boolean` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `C4` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `D2` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Date` | ✗ | ✗ | ✓ | ✓ | ✗ | ✓ |
| `E2` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `F_MessageTrailer4Byte` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `F_MessageTrailer5Byte` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Float32` | ✗ | ✗ | ✓ | ✓ | ✓ | ✓ |
| `Float32+Status8` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Float32+Unsigned8` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Float64` | ✗ | ✗ | ✓ | ✓ | ✓ | ✓ |
| `Float64+Status8` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Integer16` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Integer16_S` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Integer32` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Integer32_S` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Integer64` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Integer8` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `L2` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `N2` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `N4` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `NetworkTime` | ✗ | ✗ | ✓ | ✓ | ✗ | ✓ |
| `NetworkTimeDifference` | ✗ | ✗ | ✓ | ✓ | ✗ | ✓ |
| `OctetString` | ✗ | ✗ | ✓ | ✓ | ✓ | ✓ |
| `OctetString2+Unsigned8` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `OctetString_S` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `R2` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `T2` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `T4` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `TimeDifference with date indication` | ✗ | ✗ | ✓ | ✓ | ✗ | ✓ |
| `TimeDifference without date indication` | ✗ | ✗ | ✓ | ✓ | ✗ | ✓ |
| `TimeOfDay with date indication` | ✗ | ✗ | ✓ | ✓ | ✗ | ✓ |
| `TimeOfDay without date indication` | ✗ | ✗ | ✓ | ✓ | ✗ | ✓ |
| `TimeStamp` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `TimeStampDifference` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `TimeStampDifferenceShort` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Unipolar2.16` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Unsigned16` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Unsigned16_S` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Unsigned32` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Unsigned32_S` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Unsigned64` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Unsigned8` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `Unsigned8+Unsigned8` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `Unsigned8_S` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `UTF8String` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `V2` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `VisibleString` | ✗ | ✗ | ✓ | ✓ | ✓ | ✓ |
| `X2` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| `X4` | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |

Types: V1.0 `DataTypeEnumT`, V2.0 `DataTypeEnumT`, V2.1 `DataTypeEnumT`, V2.2 `DataTypeEnumT`, V2.35 `UnitDiagRefTypeEnumT`.

### Extended channel diagnosis (`ExtChannelAddValue` `DataItem`)

| DataType | V1.0 | V2.0 | V2.1 | V2.2 | V2.35 | catalog |
|---|---|---|---|---|---|---|
| `Bit` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `BitArea` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `Boolean` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `C4` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `D2` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `Date` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `E2` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `F_MessageTrailer4Byte` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `F_MessageTrailer5Byte` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `Float32` | n/a | n/a | ✓ | ✓ | ✓ | ✓ |
| `Float32+Status8` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `Float32+Unsigned8` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `Float64` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `Float64+Status8` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `Integer16` | n/a | n/a | ✓ | ✓ | ✓ | ✓ |
| `Integer16_S` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `Integer32` | n/a | n/a | ✓ | ✓ | ✓ | ✓ |
| `Integer32_S` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `Integer64` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `Integer8` | n/a | n/a | ✓ | ✓ | ✓ | ✓ |
| `L2` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `N2` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `N4` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `NetworkTime` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `NetworkTimeDifference` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `OctetString` | n/a | n/a | ✓ | ✓ | ✗ | ✓ |
| `OctetString2+Unsigned8` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `OctetString_S` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `R2` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `T2` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `T4` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `TimeDifference with date indication` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `TimeDifference without date indication` | n/a | n/a | ✓ | ✓ | ✗ | ✓ |
| `TimeOfDay with date indication` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `TimeOfDay without date indication` | n/a | n/a | ✓ | ✓ | ✗ | ✓ |
| `TimeStamp` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `TimeStampDifference` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `TimeStampDifferenceShort` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `Unipolar2.16` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `Unsigned16` | n/a | n/a | ✓ | ✓ | ✓ | ✓ |
| `Unsigned16_S` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `Unsigned32` | n/a | n/a | ✓ | ✓ | ✓ | ✓ |
| `Unsigned32_S` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `Unsigned64` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `Unsigned8` | n/a | n/a | ✓ | ✓ | ✓ | ✓ |
| `Unsigned8+Unsigned8` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `Unsigned8_S` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `UTF8String` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `V2` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `VisibleString` | n/a | n/a | ✓ | ✓ | ✗ | ✓ |
| `X2` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |
| `X4` | n/a | n/a | ✗ | ✗ | ✗ | ✓ |

Types: V2.1 `ExtChannelAddValueDataItemTypeEnumT`, V2.2 `ExtChannelAddValueDataItemTypeEnumT`, V2.35 `ExtChannelAddValueDataItemTypeEnumT`.

### Process alarm reason (`ProcessAlarmReasonAddValue` `DataItem`)

| DataType | V1.0 | V2.0 | V2.1 | V2.2 | V2.35 | catalog |
|---|---|---|---|---|---|---|
| `Bit` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `BitArea` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `Boolean` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `C4` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `D2` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `Date` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `E2` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `F_MessageTrailer4Byte` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `F_MessageTrailer5Byte` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `Float32` | n/a | n/a | n/a | n/a | ✓ | ✓ |
| `Float32+Status8` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `Float32+Unsigned8` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `Float64` | n/a | n/a | n/a | n/a | ✓ | ✓ |
| `Float64+Status8` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `Integer16` | n/a | n/a | n/a | n/a | ✓ | ✓ |
| `Integer16_S` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `Integer32` | n/a | n/a | n/a | n/a | ✓ | ✓ |
| `Integer32_S` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `Integer64` | n/a | n/a | n/a | n/a | ✓ | ✓ |
| `Integer8` | n/a | n/a | n/a | n/a | ✓ | ✓ |
| `L2` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `N2` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `N4` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `NetworkTime` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `NetworkTimeDifference` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `OctetString` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `OctetString2+Unsigned8` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `OctetString_S` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `R2` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `T2` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `T4` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `TimeDifference with date indication` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `TimeDifference without date indication` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `TimeOfDay with date indication` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `TimeOfDay without date indication` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `TimeStamp` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `TimeStampDifference` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `TimeStampDifferenceShort` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `Unipolar2.16` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `Unsigned16` | n/a | n/a | n/a | n/a | ✓ | ✓ |
| `Unsigned16_S` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `Unsigned32` | n/a | n/a | n/a | n/a | ✓ | ✓ |
| `Unsigned32_S` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `Unsigned64` | n/a | n/a | n/a | n/a | ✓ | ✓ |
| `Unsigned8` | n/a | n/a | n/a | n/a | ✓ | ✓ |
| `Unsigned8+Unsigned8` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `Unsigned8_S` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `UTF8String` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `V2` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `VisibleString` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `X2` | n/a | n/a | n/a | n/a | ✗ | ✓ |
| `X4` | n/a | n/a | n/a | n/a | ✗ | ✓ |

Types: V2.35 `ProcessAlarmReasonAddValueDataItemTypeEnumT`.

