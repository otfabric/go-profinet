# GSDML

GSDML (General Station Description Markup Language) is the XML description
of a PROFINET IO device: its identity, its device access points (DAPs), the
modules and submodules it accepts with their IO data and parameter records,
its diagnosis texts and its capabilities. Engineering tools build a
controller's configuration from it.

## Reading

```go
f, _ := os.Open("GSDML-V2.35-Vendor-Device-20240101.xml")
doc, err := gsdml.Parse(f)          // UTF-8 or ISO-8859-1, at most 64 MiB
if err := gsdml.Validate(doc); err != nil {
	var ve *gsdml.ValidationError
	errors.As(err, &ve)             // ve.Issues: path and message of each
}
m, err := gsdml.ToModel(doc, "DAP 1") // "" for the first device access point
```

`ToModel` converts one DAP into a `model.Device`:

- the identity (vendor and device ID, vendor name, family, order number,
  hardware and software release, ObjectUUID_LocalIndex as instance) and the
  capabilities (MinDeviceInterval, conformance class, LLDP, ports);
- the DAP's submodules: the virtual submodules, the interface and the ports
  (system-defined submodules);
- the modules that the DAP fixes or uses in slots (`FixedInSlots`,
  `UsedInSlots`) as the plugged `Slots`, and the other usable modules as the
  `Catalog`;
- per submodule: the IO data lengths (`DataItemSize` of each DataItem) and
  the parameter records with their default values (Const and Ref).

A module several references name is converted once. The records of one
model hold at most `MaxModelRecordOctets` (16 MiB) of default values, counted
for the module types and again for the modules as plugged, once per slot; a
document asking for more fails with `ErrResourceExhausted`. A module or
device access point with more than 1440 submodules (more than a C_SDU
carries) and a Const ByteOffset or Ref BitOffset that is no number fail
with `ErrMalformed`.

An absent Ref `BitOffset` is 0 and an absent `BitLength` (of a
`DataType="BitArea"` Ref) is 1. Both defaults are those of ISO
15745-4:2003/Amd 1:2006 (PROFINET profiles), Annex D, Table D.31
"Attributes of element Ref", which declares BitLength optional, of type
Unsigned8, default 1 (range 1 to 15 in that edition) and BitOffset default
0; the catalog records them (`BitOffset:Unsigned8=0`,
`BitLength:Unsigned8=1`) and `gengsdml -xsd DIR` compares them with a
licensed copy of the XSDs. The declarations in the PI XSDs of the versions
in use (for example V2.35 and V2.42, which published files without a
BitLength carry) have not been checked: the XSDs are members-only and no
licensed copy was at hand. A BitLength that is present but no Unsigned8,
or 0, fails with `ErrMalformed`; so does a bit area that leaves its octet
(see LIMITATIONS.md). An attribute a parsed document gives as `""`
(`BitLength=""`) is present, not absent: it takes no default, and
`ToModel` refuses it (`ErrMalformed`) as it refuses any BitLength that is
no Unsigned8.

`Parse` records, per element, which typed optional attributes the
document gave as `""`; `Validate` reports those whose type does not admit
the empty string (`attribute BitLength: empty, not a Unsigned8`). An empty
attribute of type string or TokenList, or one marked always written (`~`,
such as `ApplicationClass`), is valid. A document built in Go cannot say
"present but empty": there an empty field is absent, as before. Encoding
writes no optional attribute that is empty, so a parsed `BitLength=""`
is absent once the document is written and read again.

The DefaultValue of an integer or bit Ref is decimal with an optional
sign, where leading zeros are decimal (`"010"` is ten), as in the lexical
space of xsd:integer, or 0x hexadecimal, as the package reads every other
number. A value outside the DataType's range, or in any other form (Go's
octal `0o`/`0` prefix, binary `0b`, digit separators `_`), fails with
`ErrMalformed`. That hexadecimal is allowed in a DefaultValue has not been
checked against the members-only GSDML specification (LIMITATIONS.md).

The DefaultValue of a VisibleString, UTF8String or OctetString Ref fills
the Length octets of its field: a longer value fails with `ErrMalformed`,
naming the record, where it used to be cut short; a shorter one is padded
with zero octets, also over octets a Const wrote there. Length is in
octets, so a UTF-8 value counts its encoded octets (`"äö"` is four). A
string value is written as given, white space included, since DefaultValue
is an xsd:string in a third-party copy of the V2.35 XSD, whose white space
is preserved; an OctetString value is octets as
in a Const, `"0x01,0x02"`. OPC 30144 v1.00 Table 58 (the Ref DataTypes,
after the GSDML specification's Table 18) gives no padding rule, and the
padding is this package's policy; that the OctetString DefaultValue takes the
Const form is inferred from Const.Data, as no public source states it.

`ToModel` writes the DefaultValue of a Ref only for the DataTypes whose
encoding a source this package has establishes: Bit and BitArea, Boolean,
Integer8–64, Unsigned8–64, Float32 and Float64, VisibleString, UTF8String
and OctetString. A Boolean is one octet, 0x00 for false and 0x01 for true
(IEC 61158-6-10 section 4.1.2: any nonzero octet reads as true, 0x01 is
canonical), from the xsd:boolean forms true, false, 1 and 0; any other
value fails with `ErrMalformed`. Every other DataType fails with
`ErrUnsupported` (`no verified DefaultValue encoding for DataType Integer16_S`):

- the `_S` types embed two status bits in the value (OPC 30144 v1.00
  Table 17 gives the codes B, S, U, G and Table 58 the value ranges, such as
  0–63 for Unsigned8_S, but neither the bit positions nor how the status is
  written in a DefaultValue); Unsigned32_S and Integer32_S are not Ref
  DataTypes in Table 58 at all;
- the composite types (`Float32+Status8`, `Unsigned8+Unsigned8`, …), the
  date and time types (`Date`, `TimeStamp`, …) and the PROFIdrive types
  (`N2`, `V2`, `X4`, …) have no public lexical form for a DefaultValue;
- the types of more than 8 octets were written as a 64-bit number with the
  rest zero-filled.

The DataType list of the catalog is one enumeration for every element; the
V2.35 XSD has one per context (DataItem, Ref, unit and channel diagnosis
items), which differ. Which DataType is legal where, by schema version, is
a separate audit (LIMITATIONS.md); `ToModel` refuses by encoding, not by
legality.

The Boolean attributes `ToModel` reads (`LLDP_NoD_Supported`,
`DCP_HelloSupported`) are true, false, 1 or 0, with white space around
them allowed (xsd:boolean); an absent one is false, and an empty one or any
other value fails with `ErrMalformed`. `ParseBool`, the exported helper, still
returns its default for a value that is no Boolean.

The DAP's `ObjectUUID_LocalIndex`, `MinDeviceInterval` and the
interface's `NumberOfAR` and `ReductionRatio`, a module reference's
`FixedInSlots`, `UsedInSlots` and `AllowedInSlots` and a submodule's
`FixedInSubslots` fail with `ErrMalformed` when they are present but no
Unsigned16 or ValueList (or empty); a list of more values than `ToModel`
expands (64 reduction ratios or fixed subslots) fails with
`ErrResourceExhausted`. An absent one keeps its default: 0 for the two
required DAP attributes (`Validate` reports them missing), one AR, no
reduction ratios, the default subslot. `NumberOfAR="0"` keeps the one AR.
More than one allowed slot, with no fixed or used slot, leaves a module
unplugged, as before. `HardwareRelease` and `SoftwareRelease` are text
(token): `ToModel` reads a number from them where there is one (`"3"`,
`"V2.1.0"`) and leaves 0 otherwise, which is not an error.

`Parse` refuses a document of more than 64 MiB or more than 1 048 576
elements (`ErrResourceExhausted`). It refuses a document type declaration
with an internal subset (`<!DOCTYPE ISO15745Profile [ … ]>`) with
`ErrUnsupported`: XML 1.0 (Fifth Edition) section 5.1 has even a
non-validating processor apply the attribute defaults (ATTLIST) and
entities declared there, and encoding/xml does not, so the document would
read differently here than in a conforming XML processor. A declaration
without an internal subset declares nothing a non-validating processor
must read; it is ignored, and the DTD it names is not fetched. An entity
reference other than the five predefined ones is malformed. Unknown
elements are kept as their inner XML, so nesting depth costs no recursion
(200 000 nested elements parse in under 0.1 s); the element bound limits
it.

A controller connects with `controller.DeviceConfig{Model: m}`; plug further
modules of the catalog into `m.Slots` first (profinetctl's `--plug`).

## Writing

```go
doc, err := gsdml.FromModel(m, gsdml.GenerateOptions{SupervisorAR: true})
b, err := gsdml.Marshal(doc)
name := gsdml.FileName(m, "", time.Now()) // GSDML-V2.35-<vendor>-<family>-<date>.xml
```

`FromModel` writes the identity, one DAP with the interface and port
submodules, every module type with its submodules, IO data (as
`OctetString` items of the submodule's lengths), parameter records, the
certification info and the external texts. profinet-py parses go-profinet's
generated files and connects with them (the interop suite).

## Validation

> Without PI membership the official XSDs are not available: see
> [PI-MEMBERSHIP.md](PI-MEMBERSHIP.md) for what this means.

`Check` grades every issue it finds as an **error**, a **warning** or
**unvalidated**, and names the rule; `Validate` returns the errors only (a
`*ValidationError`), so that warnings and unknown content do not fail a
file.

```go
r := gsdml.Check(doc)
for _, i := range r.Issues {
	fmt.Println(i.Severity, i.Rule, i) // warning slots ...: UsedInSlots: slot 7 is not in AllowedInSlots "1..4"
}
err := r.Err() // or gsdml.Validate(doc)
```

| Rule | Severity | What | Basis |
|------|----------|------|-------|
| `structure` | error | required elements and attributes, the lexical types of attributes (hexadecimal and decimal unsigned values, value lists, enumerations, IDs), unique IDs | go-profinet's catalog ([CODEGEN.md](CODEGEN.md)) |
| `reference` | error | every module, submodule, value item, graphic, category and text reference resolves | the catalog |
| `io-data-length` | error | a submodule's input or output data and its provider status fit the 1440 octets of an RT frame | a physical limit of the frame |
| `slots` | warning | modules allowed, used or fixed in slots the device access point has (`PhysicalSlots`), used or fixed only where allowed, no slot fixed by two modules | consistency of the file's own declarations |
| `record-index` | warning | parameter records in the user index range 0x0000–0x7FFF | the record index ranges of PROFINET IO |
| `ident-number` | warning | no two modules with one ModuleIdentNumber | local heuristic: a controller could not tell them apart |
| `subslot` | warning | interface and port submodules in 0x8000–0x8FFF | local heuristic: the numbering p-net and pnio-rs use |
| `reduction-ratio` | warning | reduction ratios are powers of two from 1 to 512 | local heuristic: the RT_CLASS_1 ratios |
| `unknown` | unvalidated | an element or attribute outside the catalog (a newer schema version, a vendor extension): kept, not checked | |
| `schema-version` | unvalidated | the file declares (in `xsi:schemaLocation`) a schema newer than the catalog's 2.45: what that version added is not checked | |

`Check` lists at most 16 384 issues of each severity; one more issue with
the rule `limit` then says how many it left out, and `Validate` still
fails when an error is among them. The slot rules look at slot numbers 0
to 65535 and warn once per list about values above; a ReductionRatio list
gets at most eight warnings and a count, and one warning when it has more
than 64 values. An ID longer than 256 octets is cut in the path of an
issue.

This is not PI's GSDML checker: it does not read the licensed schemas and
checks only the semantic rules above, not those of the GSDML
specification. GSDML 2.46 (I&M6 and I&M7 records) and 2.50 (the secure
application classes SecureAccess and SecureRealtime instead of
SecurityClass 2 and 3, conformance class E) are read like any newer
version: their additions are kept and reported as unvalidated. The catalog
is not extended with them: their schemas are PI members-only.

## Unknown content

Elements and attributes the catalog does not define are kept in the `Attrs`
and `Unknown` fields of the element they appear in and written back after
the known ones. Comments and the original order of unknown elements among
known ones are not preserved.

## Not supported

GSDX (the packaged GSDML with graphics) and MRCD: their formats are not
public. See [research/GSDML.md](research/GSDML.md). `gsdml/gsdx` inspects
the public OPC layer of a `.gsdx` (parts, content types, relationships,
signature metadata, the GSDML parts) without PI's rules and without
verifying signatures.
