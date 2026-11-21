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
(see LIMITATIONS.md). An attribute written as `BitLength=""` cannot be told
from an absent one (the generated types hold attributes as strings) and
takes the default.

The DefaultValue of an integer or bit Ref is decimal with an optional
sign, where leading zeros are decimal (`"010"` is ten), as in the lexical
space of xsd:integer, or 0x hexadecimal, as the package reads every other
number. A value outside the DataType's range, or in any other form (Go's
octal `0o`/`0` prefix, binary `0b`, digit separators `_`), fails with
`ErrMalformed`. That hexadecimal is allowed in a DefaultValue has not been
checked against the members-only GSDML specification (LIMITATIONS.md).

`Parse` refuses a document of more than 64 MiB or more than 1 048 576
elements (`ErrResourceExhausted`).

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
