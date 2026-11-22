# Ref DefaultValue forms in the vendor corpus

How vendor GSDML files write `Ref` `DefaultValue`s, measured on the
GitHub-hosted part of the corpus manifest
([gsdml-corpus/candidates.jsonl](gsdml-corpus/candidates.jsonl)). This
answers the corpus questions of the hexadecimal DefaultValue policy
(docs/GSDML.md, LIMITATIONS.md) and of the embedded-status and composite
types. It is evidence of observed usage only:

- a file shows how a vendor writes a value, not what PI's specification
  allows;
- a file does not show which octets a tool writes for it.

No vendor file is committed. The files were fetched at the manifest's
pinned commits and checked against its SHA-256.

## Population

Measured on 2026-11-22. The manifest has 473 entries. 217 have no
`duplicate_of`, and 130 of those are XML files on
`raw.githubusercontent.com`, which were scanned. The rest were not scanned:

- 60 zip archives from vendor sites (Beckhoff and others);
- entries behind JavaScript, forms or logins, or unreachable;
- entries with no artifact URL.

| | Count |
|---|---|
| Files scanned | 130 (all parsed) |
| Manufacturers (manifest `manufacturer`) | 47 |
| Schema versions (from `xsi:schemaLocation`) | 2.0 to 2.46 |
| Files with at least one `Ref` `DefaultValue` | 46, from 20 vendor names |
| `Ref` elements with a `DefaultValue` | 87 445 |
| `DataType` attribute occurrences | 153 989 |

The 46 files with Ref defaults are from the following vendors (as each file
names itself) and versions.

- **Vendors:**
  - Schneider Electric 9, Siemens 13, RT-Labs 4, CoreTigo 3, ABB 6;
  - Murrelektronik, ifm electronic and Balluff 2 each;
  - Festo, AVENTICS by Emerson, WAGO, SMC, Turck and one virtual device, 1
    each.
- **Versions:**
  - V2.0 1, V2.1 2, V2.25 4, V2.3 14;
  - V2.31 2, V2.32 1, V2.33 4, V2.34 5, V2.35 2;
  - V2.4 3, V2.41 4, V2.42 1, V2.43 1, V2.45 2.

The GitHub copies over-represent devices that open-source projects use, so
this is not a sample of the market.

## Findings

### Integers: decimal only

There are 85 576 integer and bit Ref defaults in 46 files. Their DataTypes
are Bit, BitArea, Integer8–32 and Unsigned8–64.

- Every value is plain decimal.
- None is hexadecimal (`0x`). None has a leading zero, a `+` sign or
  surrounding white space.
- 434 are negative (Integer8, Integer16 and Integer32).
- No `AllowedValues` of these Refs uses hexadecimal either.

So the hexadecimal tolerance of the package is used by **no** file in this
population: no DataType, manufacturer, version or source to record. This
matches the decimal-only pattern of ISO 15745-4:2003/Amd 1:2006
Table D.31. It does not show that hexadecimal is forbidden in any version,
and the tolerance stays as documented.

### Float32

There are 1 286 defaults, written as decimal integers (`0`, `25`) or with
one decimal (`1000.5`, `-999.9`). There is no exponent and no hexadecimal.

### OctetString

There are 300 defaults in 7 files (Balluff, Murrelektronik, ifm,
CoreTigo, Turck).

- Every one is the Const form: comma-separated `0xNN` octets, the form the
  package reads.
- Every one gives exactly `Length` octets, so the padding of a shorter
  OctetString default is not exercised by any file.

### VisibleString

There are 283 defaults in 4 files (Turck, Balluff, Murrelektronik,
Siemens).

- 253 are empty and 281 are shorter than `Length`.
- None has a character outside ASCII.

Shorter-than-Length strings are therefore the common case, and the
package's zero padding of them (LIMITATIONS.md) matters. Which octets
vendor tools write for the remainder is not something a file shows.

### Types absent from the corpus

No file in this population uses any of the following, in any context:

- the embedded-status types (`Unsigned8_S`, `Unsigned16_S`, `Integer16_S`,
  `OctetString_S`, `Integer32_S`, `Unsigned32_S`);
- the composites (`Float32+Unsigned8`, `Float32+Status8`,
  `Float64+Status8`, `Unsigned8+Unsigned8`, `OctetString2+Unsigned8`);
- `Boolean`, `UTF8String`, `UnicodeString8`, `61131_STRING` and
  `61131_WSTRING`;
- the date and time types, as Ref defaults.

The lexical form of an `_S` or composite DefaultValue therefore stays
unknown: there is no observed form to learn from. The four catalog values
with no audited schema (`Float64+Status8`, `Integer32_S`, `Unsigned32_S`,
`UTF8String`) get no support from usage either.

### DataType legality

Of the 153 989 DataType occurrences, 11 234 are in documents of an audited
schema version (V2.0, V2.1, V2.2 or V2.35; docs/research/GSDML-XSD-AUDIT.md).
**All 11 234 are allowed in their context** by that version's audited set.
The other 142 755 are in documents of versions no audited schema covers,
V2.3x and V2.4x mostly, so their legality is unverified.

## Not covered

- **Vendor zips.** The 60 vendor-site zip archives have not been scanned.
  They hold the larger catalogues (Beckhoff, Lenze, HMS and others).
- **Wire bytes.** No engineering tool was run, so the octets a tool writes
  for these defaults (padding, composite layout) are not observed. A
  startup capture of a tool parameterising one of these devices would
  establish them.

## Method

1. The candidates were selected from the manifest by these conditions: no
   `duplicate_of`, an `artifact_url` on `raw.githubusercontent.com` ending
   in `.xml`, and a SHA-256 match after download.
2. Each file was parsed with an XML parser. The script recorded every
   element with a `DataType` attribute, together with its parent, and every
   `Ref` with a `DefaultValue`: its DataType, value, `Length`, file, vendor
   name and schema version. The vendor name is the file's own `VendorName`;
   the manifest's `manufacturer` field is kept alongside it. The schema
   version is taken from `xsi:schemaLocation`.
3. The lexical classes are:
   - decimal: `^[-+]?\d+$`;
   - hexadecimal: `^[-+]?0[xX][0-9A-Fa-f]+$`;
   - leading zero: `^-?0\d+$`.

   Values are tested before trimming.
4. Each DataType occurrence was mapped to its context through
   `dataTypeLegality.contexts` of the catalog and checked against that
   version's set.

The scan scripts were one-off and are not in the repository. The manifest,
its hashes and this method reproduce the counts.
