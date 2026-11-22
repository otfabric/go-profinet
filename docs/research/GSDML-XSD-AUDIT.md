# GSDML DataType legality by schema version and context

Which `DataType` values each GSDML schema version allows in each element
context, for all 18 versions from V1.0 to V2.46, compared with
go-profinet's catalog (`gsdml/schema/catalog.json`). This is an audit of
evidence. The library applies it as described in "Legality in the catalog
and generator" below.

The machine-readable report is [gsdml-xsd-audit.json](gsdml-xsd-audit.json);
the registry of the schema files is
[gsdml/schema/xsd-registry.json](../../gsdml/schema/xsd-registry.json).
`scripts/gsdml-xsd-audit.sh` regenerates both from the reference schemas
(`-check` fails when a committed report differs). The tables below are
derived from the report.

## Sources

The schemas are part of the GSDML specification of PROFIBUS & PROFINET
International (PI), licensed to its members and not redistributable
([GSDML.md](GSDML.md)). The maintainers' copy is the set TIA Portal V21
installs: 18 DeviceProfile schemas with their Primitives schemas, the
schemas they import (`xml.xsd`, `xmldsig-core-schema.xsd` and, for V2.41
and V2.42, `XAdES01903v132-201601.xsd`) and `Common-Primitives-v1.0.xsd`,
which none of them imports. It is kept in the private repository
otfabric/profinet-gsdml-schema, byte for byte, and fetched at a pinned
commit ([CODEGEN.md](../CODEGEN.md#the-reference-schemas)). Only facts
derived from it (value names, contexts, file names, sizes, hashes, dates)
are in this repository.

Three properties of the sources matter for reading them:

- **A reserialised copy.** The TIA files are not PI's original bytes: they
  are indented with spaces, end lines with CRLF and contain no XML
  comments. Third-party copies of V1.0, V2.0, V2.1, V2.2 and V2.35 on
  GitHub (the evidence of the first version of this audit) are indented
  with tabs and keep their comments; their hashes differ, but every
  DataType enumeration of those five versions is the same in both. The
  comments are where the schemas mark a value obsolete: the third-party
  copies of V2.2 and V2.35 annotate `Float32+Status8` "obsolete,
  superseded by `Float32+Unsigned8`". The TIA copy cannot show that, so
  the report has no notes.
- **Version.** The root documents of V1.0 to V2.2 carry `version="1.0"` on
  `xsd:schema`; from V2.25 the attribute is the version. The audit takes
  the version from the DeviceProfile's file name and records the attribute
  as `schemaVersionAttribute`. The revision dates (`schemainfo/@versiondate`)
  start with V2.2 (20080603) and end with V2.46 (20250512); V2.45's
  Primitives schema is dated 20240329, its DeviceProfile 20240508.
- **White space.** V2.0's `DataItemTypeEnumT` has the value
  `"F_MessageTrailer5Byte "`, with a trailing space. On an `xsd:string` base
  the space is significant, so V2.0's schema does not validate a document
  that writes `F_MessageTrailer5Byte`. V2.1 drops the space. The report
  lists such values under `whitespace`.

## Method

`internal/xsd` reads the root document and every document its `include`,
`import` and `redefine` name (at most 64). Every import resolves in the
directory; none is missing. It resolves named and anonymous complex types,
extension bases and attribute groups to find every element occurrence that
has a `DataType` attribute, together with its parent. It then resolves the
attribute's simple type through restriction bases and unions to an
enumeration. The `Parent/Element` occurrences map to five contexts, the
same in every version:

| Context | Occurrences |
|---|---|
| `io` | `Input/DataItem`, `Output/DataItem` |
| `parameter` | `ParameterRecordDataItem/Ref`, `Request/Ref` (V2.3 and later) |
| `unitDiag` | `UnitDiagTypeItem/Ref`, `ProfileUnitDiagTypeItem/Ref` (V2.1 and later) |
| `extChannel` | `ExtChannelAddValue/DataItem` (V2.1 and later) |
| `processAlarm` | `ProcessAlarmReasonAddValue/DataItem` (V2.35 and later) |

Within each version, the occurrences of one context have identical
enumerations. The report keeps them separate.

## Findings

1. **There is no single DataType enumeration.** V1.0 and V2.0 have two
   enumerations, one for IO data and one for parameter and diagnosis Refs.
   V2.1 adds the channel diagnosis, V2.35 the process alarm reason. The
   catalog has one vocabulary, now of 59 values, for every context.
2. **The changes are few and large.** Of the 17 steps between versions,
   seven change a set:
   - V2.0: IO data gains `F_MessageTrailer4Byte`,
     `"F_MessageTrailer5Byte "` (with its space) and `Float32+Status8`.
   - V2.1: the parameter and unit diagnosis Refs gain the eleven date,
     time, float and string types; the channel diagnosis context appears;
     IO data's `F_MessageTrailer5Byte` loses its space.
   - V2.2: IO data gains `Float32+Unsigned8` and `Unsigned8+Unsigned8`.
   - V2.31: the channel diagnosis drops `OctetString`, `VisibleString`,
     `TimeOfDay without date indication` and `TimeDifference without date
     indication`.
   - V2.32: IO data gains 25 values and the parameter Ref 27: `Boolean`,
     `UnicodeString8`, the 61131 strings, the four `_S` types, the
     PROFIdrive types (`N2` … `X4`, `Unipolar2.16`), the TimeStamp types and
     `OctetString2+Unsigned8`; the parameter Ref also `Float32+Unsigned8`
     and `Unsigned8+Unsigned8`. The unit diagnosis Ref drops the seven
     date and time types.
   - V2.35: the process alarm reason context appears, with the ten
     integer and float types.
   - V2.46: IO data gains `Unsigned16+Unsigned8`, `Integer16+Unsigned8`,
     `Unsigned32+Unsigned8` and `Integer32+Unsigned8`, in no other context.

   V2.25, V2.3 (apart from the new `Request/Ref` occurrence, with the
   parameter Ref's set), V2.33, V2.34, V2.4 and V2.41 to V2.45 change
   nothing.
3. **Legality is not monotonic.** V2.31 and V2.32 remove values that
   earlier versions allowed (finding 2). A model in which a value only has
   an introduction version cannot represent this. Each version needs an
   exact set.
4. **Catalog values in no schema:** `Float64+Status8`, `Integer32_S`,
   `Unsigned32_S` and `UTF8String` are in no context of any version up to
   V2.46. The catalog has had them since its first commit (315320e,
   2026-10-26), which was "written independently from public descriptions
   of GSDML" and records no source per value: they are UNVERIFIED. No
   source the package has defines a layout for `Integer32_S` or
   `Unsigned32_S`; they are not Ref DataTypes in OPC 30144 v1.00 Table 58.
5. **Schema values the catalog lacked:** `UnicodeString8`, `61131_STRING`
   and `61131_WSTRING` (added in v0.13.0), and V2.46's four composites
   (added now). None has a size in a source the package has.
6. **Context rules that hold from V2.32 to V2.46:**
   - `Bit` and `BitArea` are allowed only in parameter and unit diagnosis
     Refs, never in IO data (in no version).
   - `Float32+Status8` is IO-only.
   - `Boolean` is not in the unit diagnosis Ref.
   - `F_MessageTrailer4Byte` and `F_MessageTrailer5Byte` are IO-only.
   - The four `_S` types with an established layout (`Unsigned8_S`,
     `Unsigned16_S`, `Integer16_S`, `OctetString_S`) are allowed in IO data
     and in parameter Refs.

## Unverified versions

Every version the catalog names (V2.25 to V2.45) is audited; the report's
`unverifiedCatalogVersions` is empty. Versions after V2.46, such as V2.5x,
are not: their legality must not be inferred from V2.46.

## Legality in the catalog and generator

1. **Catalog.** `gsdml/schema/catalog.json` has a `dataTypeLegality`
   section:
   - context names, each mapped to its `Parent/Element` occurrences;
   - one exact value set per audited version and context, 18 versions;
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
3. **Vocabulary.** The three V2.32 strings and the four V2.46 composites are
   constants with no size. `Float64+Status8`, `Integer32_S`, `Unsigned32_S`
   and `UTF8String` stay, documented as found in no schema.
4. **Check** (rule `datatype-context`) covers the IO DataItem and the
   parameter Ref, the contexts the catalog models:
   - a value forbidden by an audited version is a warning, because the
     evidence is a reserialised copy rather than PI's specification
     package, so `Validate`, which fails on errors, does not change;
   - a document of an unverified version, or with no version, gets one
     unvalidated issue.
5. **ToModel** is unchanged: it refuses by size and encoding, not by
   legality.

## Tables

Each row lists the versions whose schema allows the value in the context,
as ranges of consecutive versions in the order V1.0, V2.0, V2.1, V2.2,
V2.25, V2.3, V2.31 … V2.35, V2.4, V2.41 … V2.46.

### IO data (`Input`/`Output` `DataItem`)

The context exists in V1.0–V2.46.

| DataType | Allowed in |
|---|---|
| `61131_STRING` | V2.32–V2.46 |
| `61131_WSTRING` | V2.32–V2.46 |
| `Boolean` | V2.32–V2.46 |
| `C4` | V2.32–V2.46 |
| `D2` | V2.32–V2.46 |
| `Date` | V1.0–V2.46 |
| `E2` | V2.32–V2.46 |
| `F_MessageTrailer4Byte` | V2.0–V2.46 |
| `F_MessageTrailer5Byte` | V2.1–V2.46 (V2.0: `"F_MessageTrailer5Byte "`, unverified) |
| `Float32` | V1.0–V2.46 |
| `Float32+Status8` | V2.0–V2.46 |
| `Float32+Unsigned8` | V2.2–V2.46 |
| `Float64` | V1.0–V2.46 |
| `Integer16` | V1.0–V2.46 |
| `Integer16+Unsigned8` | V2.46 |
| `Integer16_S` | V2.32–V2.46 |
| `Integer32` | V1.0–V2.46 |
| `Integer32+Unsigned8` | V2.46 |
| `Integer64` | V1.0–V2.46 |
| `Integer8` | V1.0–V2.46 |
| `L2` | V2.32–V2.46 |
| `N2` | V2.32–V2.46 |
| `N4` | V2.32–V2.46 |
| `NetworkTime` | V1.0–V2.46 |
| `NetworkTimeDifference` | V1.0–V2.46 |
| `OctetString` | V1.0–V2.46 |
| `OctetString2+Unsigned8` | V2.32–V2.46 |
| `OctetString_S` | V2.32–V2.46 |
| `R2` | V2.32–V2.46 |
| `T2` | V2.32–V2.46 |
| `T4` | V2.32–V2.46 |
| `TimeDifference with date indication` | V1.0–V2.46 |
| `TimeDifference without date indication` | V1.0–V2.46 |
| `TimeOfDay with date indication` | V1.0–V2.46 |
| `TimeOfDay without date indication` | V1.0–V2.46 |
| `TimeStamp` | V2.32–V2.46 |
| `TimeStampDifference` | V2.32–V2.46 |
| `TimeStampDifferenceShort` | V2.32–V2.46 |
| `UnicodeString8` | V2.32–V2.46 |
| `Unipolar2.16` | V2.32–V2.46 |
| `Unsigned16` | V1.0–V2.46 |
| `Unsigned16+Unsigned8` | V2.46 |
| `Unsigned16_S` | V2.32–V2.46 |
| `Unsigned32` | V1.0–V2.46 |
| `Unsigned32+Unsigned8` | V2.46 |
| `Unsigned64` | V1.0–V2.46 |
| `Unsigned8` | V1.0–V2.46 |
| `Unsigned8+Unsigned8` | V2.2–V2.46 |
| `Unsigned8_S` | V2.32–V2.46 |
| `V2` | V2.32–V2.46 |
| `VisibleString` | V1.0–V2.46 |
| `X2` | V2.32–V2.46 |
| `X4` | V2.32–V2.46 |

### Parameter record (`ParameterRecordDataItem` and `Request` `Ref`)

The context exists in V1.0–V2.46.

| DataType | Allowed in |
|---|---|
| `61131_STRING` | V2.32–V2.46 |
| `61131_WSTRING` | V2.32–V2.46 |
| `Bit` | V1.0–V2.46 |
| `BitArea` | V1.0–V2.46 |
| `Boolean` | V2.32–V2.46 |
| `C4` | V2.32–V2.46 |
| `D2` | V2.32–V2.46 |
| `Date` | V2.1–V2.46 |
| `E2` | V2.32–V2.46 |
| `Float32` | V2.1–V2.46 |
| `Float32+Unsigned8` | V2.32–V2.46 |
| `Float64` | V2.1–V2.46 |
| `Integer16` | V1.0–V2.46 |
| `Integer16_S` | V2.32–V2.46 |
| `Integer32` | V1.0–V2.46 |
| `Integer64` | V1.0–V2.46 |
| `Integer8` | V1.0–V2.46 |
| `L2` | V2.32–V2.46 |
| `N2` | V2.32–V2.46 |
| `N4` | V2.32–V2.46 |
| `NetworkTime` | V2.1–V2.46 |
| `NetworkTimeDifference` | V2.1–V2.46 |
| `OctetString` | V2.1–V2.46 |
| `OctetString2+Unsigned8` | V2.32–V2.46 |
| `OctetString_S` | V2.32–V2.46 |
| `R2` | V2.32–V2.46 |
| `T2` | V2.32–V2.46 |
| `T4` | V2.32–V2.46 |
| `TimeDifference with date indication` | V2.1–V2.46 |
| `TimeDifference without date indication` | V2.1–V2.46 |
| `TimeOfDay with date indication` | V2.1–V2.46 |
| `TimeOfDay without date indication` | V2.1–V2.46 |
| `TimeStamp` | V2.32–V2.46 |
| `TimeStampDifference` | V2.32–V2.46 |
| `TimeStampDifferenceShort` | V2.32–V2.46 |
| `UnicodeString8` | V2.32–V2.46 |
| `Unipolar2.16` | V2.32–V2.46 |
| `Unsigned16` | V1.0–V2.46 |
| `Unsigned16_S` | V2.32–V2.46 |
| `Unsigned32` | V1.0–V2.46 |
| `Unsigned64` | V1.0–V2.46 |
| `Unsigned8` | V1.0–V2.46 |
| `Unsigned8+Unsigned8` | V2.32–V2.46 |
| `Unsigned8_S` | V2.32–V2.46 |
| `V2` | V2.32–V2.46 |
| `VisibleString` | V2.1–V2.46 |
| `X2` | V2.32–V2.46 |
| `X4` | V2.32–V2.46 |

### Unit diagnosis (`UnitDiagTypeItem` and `ProfileUnitDiagTypeItem` `Ref`)

The context exists in V1.0–V2.46.

| DataType | Allowed in |
|---|---|
| `Bit` | V1.0–V2.46 |
| `BitArea` | V1.0–V2.46 |
| `Date` | V2.1–V2.31 |
| `Float32` | V2.1–V2.46 |
| `Float64` | V2.1–V2.46 |
| `Integer16` | V1.0–V2.46 |
| `Integer32` | V1.0–V2.46 |
| `Integer64` | V1.0–V2.46 |
| `Integer8` | V1.0–V2.46 |
| `NetworkTime` | V2.1–V2.31 |
| `NetworkTimeDifference` | V2.1–V2.31 |
| `OctetString` | V2.1–V2.46 |
| `TimeDifference with date indication` | V2.1–V2.31 |
| `TimeDifference without date indication` | V2.1–V2.31 |
| `TimeOfDay with date indication` | V2.1–V2.31 |
| `TimeOfDay without date indication` | V2.1–V2.31 |
| `Unsigned16` | V1.0–V2.46 |
| `Unsigned32` | V1.0–V2.46 |
| `Unsigned64` | V1.0–V2.46 |
| `Unsigned8` | V1.0–V2.46 |
| `VisibleString` | V2.1–V2.46 |

### Extended channel diagnosis (`ExtChannelAddValue` `DataItem`)

The context exists in V2.1–V2.46.

| DataType | Allowed in |
|---|---|
| `Float32` | V2.1–V2.46 |
| `Integer16` | V2.1–V2.46 |
| `Integer32` | V2.1–V2.46 |
| `Integer8` | V2.1–V2.46 |
| `OctetString` | V2.1–V2.3 |
| `TimeDifference without date indication` | V2.1–V2.3 |
| `TimeOfDay without date indication` | V2.1–V2.3 |
| `Unsigned16` | V2.1–V2.46 |
| `Unsigned32` | V2.1–V2.46 |
| `Unsigned8` | V2.1–V2.46 |
| `VisibleString` | V2.1–V2.3 |

### Process alarm reason (`ProcessAlarmReasonAddValue` `DataItem`)

The context exists in V2.35–V2.46.

| DataType | Allowed in |
|---|---|
| `Float32` | V2.35–V2.46 |
| `Float64` | V2.35–V2.46 |
| `Integer16` | V2.35–V2.46 |
| `Integer32` | V2.35–V2.46 |
| `Integer64` | V2.35–V2.46 |
| `Integer8` | V2.35–V2.46 |
| `Unsigned16` | V2.35–V2.46 |
| `Unsigned32` | V2.35–V2.46 |
| `Unsigned64` | V2.35–V2.46 |
| `Unsigned8` | V2.35–V2.46 |
