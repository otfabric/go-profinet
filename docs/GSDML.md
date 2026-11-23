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
space of xsd:integer. ISO 15745-4:2003/Amd 1:2006, Annex D, D.4.7.4,
Table D.31 (Attributes of element Ref; read in its adoption GOST R ISO
15745-4-2010) constrains DefaultValue by the pattern `\-?[\d+]{1,20}`:
decimal digits, no hexadecimal. The V2.35 XSD declares it xsd:string with
no pattern, so passing the schema says nothing about the form. As a
compatibility tolerance the package also reads `0x` (or `0X`) hexadecimal,
as it reads every other number: a magnitude with an optional sign before
the prefix (`"-0x80"` is −128), not a two's complement pattern (`"0xFF"`
does not fit an Integer8). Decimal and hexadecimal of one value write the
same octets. A value outside the DataType's range, or in any other form
(Go's octal `0o`/`0` prefix, binary `0b`, digit separators `_`, a sign after
`0x`), fails with `ErrMalformed`. Whether the current GSDML specification
allows hexadecimal there is not known: its text is members-only
(LIMITATIONS.md). `FromModel` writes no Ref (a record's default is Const
octets); a DefaultValue it wrote would be decimal. The Const Data and
OctetString DefaultValue form (`"0x01,0x02"`) is a separate matter, below.

The DefaultValue of a VisibleString, UTF8String or OctetString Ref fills
the Length octets of its field: a longer value fails with `ErrMalformed`,
naming the record, where it used to be cut short; a shorter one is padded
with zero octets, also over octets a Const wrote there. Length is in
octets, so a UTF-8 value counts its encoded octets (`"äö"` is four). A
string value is written as given, white space included, since DefaultValue
is an xsd:string in the V2.35 XSD, whose white space
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

- the `_S` types: their binary layout is established, and the `pnio`
  package encodes and decodes it (`pnio.EncodeUnsigned8S`,
  `EncodeUnsigned16S`, `EncodeInteger16S`, `EncodeOctetStringS` and their
  decoders; IEC 61158-5-10:2023 5.3.1.6.8–5.3.1.6.10 and 5.3.2.2, RIO for
  Process Automation 3.132 Tables 8 and 9): the value above two status
  bits (B 0, S 1, U 2, G 3), Integer16_S as 14-bit two's complement, and
  OctetString_S as m value octets followed by 2·m octets of channel
  statuses. How a DefaultValue writes the value and its status is not
  (OPC 30144 Table 58 says "plus status B/S/U/G" without a grammar), nor
  which status a parameter may carry, so a guess at either would write a
  wrong record; Unsigned32_S and Integer32_S are not Ref DataTypes in
  Table 58 and have no layout in any source the package has;
- the composite types (`Float32+Status8`, `Unsigned8+Unsigned8`, …), the
  date and time types (`Date`, `TimeStamp`, …) and the PROFIdrive types
  (`N2`, `V2`, `X4`, …) have no public lexical form for a DefaultValue;
- the types of more than 8 octets were written as a 64-bit number with the
  rest zero-filled.

The DataType vocabulary of the catalog (`DataTypes`) is one list for
every element. The schemas have one enumeration per context, which differ
by version and are not monotonic. `DataTypeLegality(version, context,
DataType)` answers from exact sets for all 18 versions from V1.0 to V2.46
(docs/research/GSDML-XSD-AUDIT.md). It returns:

- **allowed** or **forbidden** for an audited version;
- **unverified** for any other version (V2.5x, for example), and for
  V2.0's `F_MessageTrailer5Byte`, which that schema spells with a trailing
  space.

`Check` applies it, through the rule `datatype-context`, to every IO
DataItem and parameter Ref:

- A value forbidden by an audited version is a **warning**, not an error,
  because the sets come from the copy TIA Portal installs, not PI's
  specification package. `Validate` therefore does not fail on it.
- A document whose version was not audited, or that declares none, gets
  one **unvalidated** issue.
- The unit diagnosis, channel diagnosis and process alarm contexts are not
  modelled; their elements are reported as unknown.

`ToModel` refuses by size and encoding, not by legality. `UnicodeString8`,
`61131_STRING` and `61131_WSTRING` (V2.32), and `Unsigned16+Unsigned8`,
`Integer16+Unsigned8`, `Unsigned32+Unsigned8` and `Integer32+Unsigned8`
(V2.46, IO data only) are in the vocabulary, but have
no size: no source the package has gives their layout.
`Float64+Status8`, `Integer32_S`, `Unsigned32_S` and `UTF8String` are in no
audited schema. They stay in the vocabulary, unverified.

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

### Why a device access point does not convert

`ToModel` is all or nothing: one module it cannot convert fails the DAP,
and it converts one DAP. `AnalyzeModels` is the diagnostic for a whole
document; it does not replace `ToModel` and is not a tolerant variant of
it:

```go
reps, err := gsdml.AnalyzeModels(doc) // one ModelReport per DAP, in file order
for _, r := range reps {
	fmt.Println(r.DAP, r.Outcome, r.Blocked, r.Error)
	for _, mr := range r.Modules {
		fmt.Println(" ", mr.Target, mr.ReferenceOutcome, mr.ModuleOutcome, mr.ConfiguredSlots)
	}
}
```

A report's `Outcome`, `Err` and `Model` are those of `ToModel(doc, dap)`,
whatever else the report finds: `Model` is set only for
`OutcomeConverted`, and is then the model `ToModel` returns (the tests
compare both, structurally and as JSON). No partial model is returned.

| Outcome | Meaning |
|---|---|
| `converted` | converts |
| `malformed` | an error of `ErrMalformed`, or of no other class |
| `unsupported` | `ErrUnsupported`: content go-profinet does not implement, such as a field without a verified encoding |
| `resource_exhausted` | `ErrResourceExhausted`, such as parameter records beyond `MaxModelRecordOctets` in the modules as plugged |

Malformed content is reported as malformed, never as unsupported: an error
that matches `ErrMalformed` is classified first. For a DAP that does not
convert, the rest of the report says why:

- `DAPErr` is the error of the DAP's own content (identity, capabilities,
  its submodules), without its modules.
- `Blocked` is set when a module reference or the module it names does not
  convert: every one `ToModel` would refuse the DAP for, not only the first
  it reaches. A blocked DAP keeps `ToModel`'s outcome: one whose plugged
  records exceed the limit before `ToModel` reaches a malformed module is
  `resource_exhausted` and blocked.
- Every `UseableModules` reference gets a `ModuleReport`, in file order,
  with two statuses. `ReferenceOutcome` is the reference's own (`malformed`
  when it names no module or its slot attributes do not parse);
  `ModuleOutcome` is the module's, converted on its own with the whole
  `MaxModelRecordOctets`, once however many references name it (empty when
  the reference names no module). So a valid module referenced with a bad
  slot list and a bad module referenced validly are told apart.
- `ConfiguredSlots` are the slots the reference asks for, as `ToModel`
  assigns them (slot 0, slots above 0x7FFF and slots an earlier reference
  took left out). They are requested positions, not a model: only for a
  converted DAP are they its model's slots.
- `RecordOctets` are what one slot of the module adds. A DAP whose modules
  each convert but together exceed the limit is `resource_exhausted` and
  not blocked, and the modules' octets show where the size goes.

Each error is kept (`Err`, `DAPErr`, `ReferenceErr`, `ModuleErr`, for
`errors.Is`); the JSON form carries their texts and the element paths.
`AnalyzeModels` returns `ErrMalformed` for a nil document and for one
without device access points.

### Parameter records

`ToModel` keeps a record's index, length and default octets
(`model.Record`). `SubmoduleParameters` describes the records of one
submodule with what a value of each field must be, so that a record can be
built from chosen values:

```go
recs, err := gsdml.SubmoduleParameters(doc, "DAP_1", 0x50, 0x51) // DAP, module and submodule ident numbers
r := recs[0]
for i, f := range r.Fields {
	fmt.Println(i, f.Name, f.DataType, f.ByteOffset, f.BitOffset, f.BitLength, f.Default, f.AllowedValues, f.Values)
}
rec, err := r.Encode(map[int]string{0: "250", 2: "2"}) // the default, with fields 0 and 2 set

i, err := r.Field("R_Interval")      // by ID (V2.3 and later), else by a name only one field has
err = r.SetUint(rec, i, 500)          // or SetInt, SetFloat, SetBool, SetString, SetBytes
for _, is := range r.Check() {        // overlaps, AllowedValues, encodings: see below
	fmt.Println(is.Severity, is.Rule, is.Path, is.Message)
}
```

- The submodules are those `ToModel` converts: the `VirtualSubmoduleList`
  of the device access point (when the module ident number is the DAP's)
  or of a module the DAP can use (`UseableModules`).
- `Default` is the record `ToModel` built when the record was described,
  a snapshot. It is nil when a field has a DataType without a verified
  encoding (the `_S`, composite, time and PROFIdrive types above): the
  field is described, but not `Encodable`, and `Encode` refuses the record
  rather than build it without that field.
- A field's `BitOffset`, `BitLength`, `Changeable` and `Visible` take the
  defaults of PI's schemas when the file does not give them: 0, 1, true
  and true. These are the defaults of a parameter record's Ref in every
  schema V1.0 to V2.46 (`ValueItemReferenceT` in V1.0 to V2.1,
  `ParameterRecordDataT/Ref` in V2.2 and V2.25, `RecordDataRefT` from
  V2.3).
- `Values` are the texts of the field's `ValueItem` (`ValueItemTarget`,
  kept in `ValueItem`): the `Assign` of each value, for a tool to show and
  to offer. A ValueItemTarget that names no ValueItem of the file sets
  `ValueItemMissing`: the field's permitted values are then not known, so
  `Set` refuses every value with `ErrMalformed` and `Check` reports an
  error, rather than taking the field as unrestricted. Siemens, "Creating a
  Device Description (GSD) file for PROFINET devices — Getting Started",
  02/2024, A5E51228963-AC, §15.2: without ValueItemTarget and
  AllowedValues "the value range is limited only by the data type".
- `Set` writes one value into a record, in the lexical form of a
  DefaultValue, with the encoder `ToModel` uses for defaults. The other
  octets, and the other bits of a Bit or BitArea octet, are not changed. A
  value that does not fit the field, is outside its `AllowedValues` or,
  when the field has none, is not one of the values its `ValueItem`
  assigns, fails with `ErrInvalidConfig` and leaves the record as it was.
  Table D.31 of ISO 15745-4:2003/Amd 1:2006, Annex D makes the ValueItem's
  values the permitted ones when AllowedValues is absent, and requires
  AllowedValues to be a subset of them when both are given.
  `AllowedValues` (values and `lo..hi` ranges, `SignedOrFloatValueListT`)
  is compared exactly, in rational numbers, for the integer, float, Bit,
  BitArea and Boolean (0, 1) fields; on a string or OctetString field it is
  not interpreted, and `Set` fails with `ErrUnsupported`.
  The whole list is parsed first, in the form of the schemas' pattern
  (numbers `-?[0-9]+(\.[0-9]+)?([eE]-?[0-9]+)?`, single spaces), so a
  malformed list is `ErrMalformed` whatever the value. A mantissa of more
  than 128 digits, an exponent beyond ±400 or more than 4096 values are not
  compared (`ErrUnsupported`), so the cost of a comparison does not grow
  with the file.
- A float is compared at the precision of its DataType, the only one a
  device receives: the value as written, rounded to a Float32 or a Float64,
  against the bounds rounded the same way. A bound such as 1.1, which no
  float holds exactly, so admits the float nearest to it, and a Float32
  value that rounds onto a bound (1.50000001 onto 1.5) is that bound. This
  is a policy of this package; no source it has says how a device or a
  tool compares.
- A field is plain data. `Set` writes it where its exported fields say
  (`ByteOffset`, `BitOffset`, `BitLength`, `Length`, `DataType`), so a
  record read back from its JSON, or changed by its caller, encodes as it
  reads. A field whose `Length` or bits do not agree with its DataType is
  refused with `ErrInvalidConfig`. `Encodable` is reported; `Set` decides
  from the DataType.
- `Field(key)` finds a field by its `ID`, which a Ref has from schema V2.3,
  or else by its `Name` when only one field has it; a name two fields
  share, or an ID two fields have (a record edited as JSON), is
  `ErrInvalidConfig`. The typed setters (`SetUint`, `SetInt`,
  `SetFloat`, `SetBool`, `SetString`, `SetBytes`) write a Go value as `Set`
  writes its lexical form, and refuse a field of another DataType.
- `Encode` builds the record from `Consts` and `Fields` as they are, as
  `ToModel` does (zeros, the Const octets, every field's `Default`), and
  then sets the given values; `Default`, the snapshot, is not read. So an
  edited Default, or a moved field, is encoded as it reads. `Encode`
  refuses two given fields that share bits: which one would be written
  last is not its choice.
- `Check` reports; `Encode` encodes. A field's Default is written as the
  file gives it, also when `Check` warns that it is outside the field's
  AllowedValues; a value given to `Set` or `Encode` must be within them.
  Which issues allow a record to be written to a device is the caller's
  decision: an unvalidated issue is not evidence that the record is right.
- `Check` reports, as `Issue`s with a severity:
  - `parameter-layout` (error): a field or a Const outside the record, a
    field whose Length or bits do not agree with its DataType, a Default
    not of the record's length, an ID two fields have;
  - `parameter-overlap`: two Consts that share octets (an error: D.4.7.3
    does not allow it), or two fields that share bits (a warning: no source
    this package has says whether that is allowed; the one written last
    decides the bits). A field over a Const is not reported: D.4.7.4
    allows it and gives the Ref precedence, as `ToModel` and `Encode`
    write the Consts first;
  - `parameter-default`: a DefaultValue that does not encode, or a
    `Default` snapshot that is not the record `Encode` builds (errors); a
    DefaultValue outside the field's own AllowedValues or, without them,
    not a value of its ValueItem (warnings);
  - `parameter-allowed-values`: an AllowedValues not of the schemas' form,
    a ValueItemTarget that names no ValueItem, a ValueItem value that the
    field's encoder refuses (its DataType and width), an AllowedValues
    that is not a subset of the ValueItem's values (errors), or one that is
    not compared: on a string field, with more than
    4096 values, a mantissa of more than 128 digits or an exponent beyond
    ±400 (unvalidated);
  - `parameter-encoding` (unvalidated): a DataType without a verified
    encoding.

  `SubmoduleParameters` refuses a malformed layout or default already, so
  for its records `Check` reports overlaps, AllowedValues and encodings.
  `gsdml.Check` of a document runs it on the records of every submodule of
  every DAP and module, with `parameter-record` for a record
  `SubmoduleParameters` (and so `ToModel`) refuses and the
  `transfer-sequence` rule of the plan; `Validate` fails on their errors
  (see the rule table below).
- `Changeable` and `Visible` are reported. `Set` is a low-level encoder and
  writes a field that is not Changeable; `PlanParameters` refuses a value
  for one (Table D.31). `Visible` is not used.
- The clauses of ISO 15745-4:2003/Amd 1:2006, Annex D cited here (D.4.7.1
  to D.4.7.4, Tables D.28 and D.31) are as the maintainer's review states
  them: the amendment is not in this repository.

Nothing writes these records on its own: a controller writes the records of
`controller.DeviceConfig.Parameters`, as before.

### Parameterization plan

`PlanParameters` turns a model of the plugged modules, the file it was
built from and chosen values into the records to write, in the order to
write them. It writes nothing:

```go
m, err := gsdml.ToModel(doc, "DAP_1")        // or a model plugged by hand
plan, err := gsdml.PlanParameters(doc, m, gsdml.PlanOptions{
	DAP:      "DAP_1",
	Defaults: false, // true writes every record, with its defaults where no value is given
	Values: []gsdml.ParameterValues{
		{Slot: 1, Subslot: 1, Index: 2, Values: map[string]string{"R_Interval": "500"}},
	},
})
for _, is := range plan.Issues {
	fmt.Println(is.Severity, is.Rule, is.Path, is.Message) // review before writing
}
for _, w := range plan.Writes {
	cfg.Parameters = append(cfg.Parameters, controller.Parameter(w.Write))
}
```

- The model must be of the file's device: its VendorID and DeviceID, and
  the DAP's ModuleIdentNumber. Module ident numbers alone do not tell two
  devices apart.
- `Values` may set only fields that are `Changeable` (Table D.31), each
  once: an ID and a name of one field in one set are refused, rather than
  one of them winning by map order. A field that is not Changeable is still
  written at its default when its record is written.
- The topology is checked against the file: every plugged module must be
  one the DAP can use (`UseableModules`), in a slot of the DAP's
  `PhysicalSlots` and of the module's `AllowedInSlots`, and every plugged
  submodule must be in it. The DAP's interface and port submodules have no
  parameter records in the file; their PDev records are the controller's
  (`PeerChecks`, `MRP`).
- A record is written when `Values` sets a field of it; with `Defaults`,
  every record is, so nothing the device already has is written unless
  asked. A record with values must build; one written with its defaults
  only that has a field without a verified encoding is left out and
  reported (`parameter-plan`, unvalidated): the device keeps its own state
  of it.
- Every written record is checked (`ParameterRecord.Check`): an error
  refuses the plan, warnings and unvalidated issues are reported. Which of
  them allow the plan to be written is the caller's decision; an
  unvalidated issue is not evidence that a record is right.
- **Order.** TransferSequence orders the records of one submodule:
  positive values must be 1 to N, each once, and are written in ascending
  order; 0, the default, is an undefined order (ISO 15745-4:2003/Amd
  1:2006, Annex D, D.4.7.1, Table D.28, as the maintainer read it: the
  amendment is not in this repository). Values that are not 1 to N refuse
  the plan (`transfer-sequence`, an error) unless `AllowSequenceErrors`.
  The records of 0 among sequenced ones are written after them, in file
  order, with a warning: where they go is a policy of this package, not of
  Table D.28. Submodules are written in slot and subslot order, the DAP
  first, also a policy of this package. Each write keeps its
  `TransferSequence` and `FilePosition` for review.
- `RecordWrite` has the fields of `controller.Parameter`, so
  `controller.Parameter(w.Write)` converts it; `gsdml` does not import the
  controller.

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

### Against the file's own schema version

For a file read from disk, `CheckBytes` and `ValidateBytes` validate the
original octets against the schema of the version the file names in
`xsi:schemaLocation` (V1.0 to V2.46), with the validator generated from the
reference schemas ([CODEGEN.md](CODEGEN.md#the-generated-schema-model)):
the elements, their order and number, attributes, values with every
facet, defaults and fixed values, and the key, keyref and unique
constraints in their scopes. These issues have the rule `schema` and a
line. The `io-data-length` and warning rules below apply as well; the
`structure`, `reference`, `unknown` and `schema-version` rules of the
catalog do not, since the schema decides them.

```go
data, _ := os.ReadFile("GSDML-V2.35-Vendor-Device-20240101.xml")
r, err := gsdml.CheckBytes(data) // err: not well-formed, or a limit
for _, i := range r.Issues {
	fmt.Println(i.Severity, i.Rule, i.Line, i) // error schema 42 ...: attribute DeviceID: "66" does not match the pattern ...
}
```

A file that names no version, or one without a schema here (V2.5x), is
checked against the catalog as `Check` checks it, with one unvalidated
`schema` issue that says so. This agrees with libxml2's `xmllint` on every
file of the differential test, and it is not PI's checker either.

The typed model of a version is in `gsdml/typed` and `gsdml/versions/vNNN`:

```go
d, err := v246.Parse(data)             // validate against V2.46, then decode
for _, dap := range d.Root.ProfileBody.ApplicationProcess.DeviceAccessPointList.DeviceAccessPointItem {
	fmt.Println(dap.ID, dap.ModuleIdentNumber)
}
v, err := typed.Parse(data)            // the version the file names; v.SchemaVersion()
```

### Against the catalog

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
| `parameter-record` | error | a parameter record `ToModel` can convert: its DefaultValues encode, its fields and Consts lie in the record, its attributes parse; a record outside the user index range is left to `record-index` | the encoders `ToModel` uses ([Parameter records](#parameter-records)) |
| `parameter-layout`, `parameter-overlap`, `parameter-default`, `parameter-allowed-values`, `parameter-encoding` | as `ParameterRecord.Check` grades them | the record rules of [Parameter records](#parameter-records), for every record of the document | ISO 15745-4:2003/Amd 1:2006, Annex D as the maintainer's review states it; Siemens GSDML Getting Started 02/2024 §15 |
| `transfer-sequence` | error, warning | a submodule's TransferSequence values are 1 to N (error), and records without one are not mixed with sequenced ones (warning) | Table D.28; the warning is go-profinet's plan policy |
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

This is not PI's GSDML checker: it checks the catalog's rules and the
semantic rules above, not those of the GSDML specification. GSDML 2.46 (I&M6 and I&M7 records) and 2.50 (the secure
application classes SecureAccess and SecureRealtime instead of
SecurityClass 2 and 3, conformance class E) are read like any newer
version: their additions are kept and reported as unvalidated by `Check`
(`CheckBytes` validates V2.46 files against the V2.46 schema). The catalog
is not extended with them.

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
