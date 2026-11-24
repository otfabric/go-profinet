# Known limitations

What go-profinet does not do, or does with restrictions. Each item says why
and what would lift it.

## Protocol scope

| Not implemented | Notes |
|-----------------|-------|
| IRT (RT_CLASS_3), PTCP time synchronization and line delay measurement, isochronous mode | needs hardware timestamping and scheduling a general-purpose OS cannot give; out of scope for a pure Go library |
| MRP (media redundancy), MRPD | MRP client and manager (`mrp`, on a multiport device): state machines and default timers as microchip-ung/mrp and the Linux bridge implement them, not checked against IEC 62439-2 (not public); profiles 200 ms and 500 ms only (30 ms and 10 ms need timers a general-purpose OS does not keep); the automatic manager (MRA, `mrp.RoleAuto`) negotiates by priority with MRP_TestMgrNAck/TestPropagate as microchip-ung/mrp and umrp do, but it is a role of the device's configuration only (PROFINET's MRP_Role has no value for it) and its option frames carry the OUI Wireshark decodes (08-00-06), where the open implementations send 00-00-00 (both parse either); no interconnection (MIM/MIC), no MRPD; MrpManagerParams/MrpClientParams timers are accepted but not applied (their unit is not public); PDInterfaceMrpDataCheck is refused and no MRP diagnosis is reported (its codes are not verified); recovery times are measured, not claimed; tested against umrp and microchip-ung/mrp (manager, client and automatic manager; since profinet-interop v0.9.1 microchip-ung/mrp carries one fix, microchip-ung/mrp#4: an automatic manager that gave way kept its secondary port blocked) in rings of veth cables, the kernel one in micro-VMs with a kernel built without switchdev (the distribution kernels refuse MRP on a software bridge); tshark decodes its frames and records, scapy has no MRP dissector |
| System redundancy (IOCARSR, S2/R1/R2), shared input, MCR (multicast CRs) | S2 is an EXPERIMENT, off by default (`device.Config.SystemRedundancy`, `controller.DeviceConfig.SystemRedundancy`): an AR set of two IOCARSRs, outputs from the AR that requests primary, primary/backup acknowledge on the inputs, outputs held for RDHT (× 1 ms, the unit p-net's comment gives) and abort reason 38 when no AR becomes primary, writes and alarms through the primary only. It follows from the wire elements Wireshark dissects, not from the standard; it is not interoperable with PLCs (no open S2 implementation exists to test against) and its failure detection is the AR's data hold. R1/R2, MTOT, PrmBegin re-parameterization of a backup and the redundancy diagnoses are not implemented; MCR is refused. Their blocks are encoded and decoded (`pnio.SRInfoBlock`, `ARSetID`, `MCRBlockReq`, the `rt` redundancy DataStatus codes): no open implementation of their behaviour exists to test against, and the switchover sequence, MCR address resolution and the RedundancyDataHoldFactor unit are not public. Shared device (several controllers, one owner per submodule) is implemented; shared input is not: what a non-owner receives is not public |
| The DCP unicast server state machine (DCPUCS) of IEC 61158-6-10 | not checked against the standard's text (not public). The responder answers Get and Set of any requester; within a Control/Start … End Transaction of one requester it answers other requesters' Sets with "resource error", ending the transaction 3 s after the holder's last Set (the client hold time rt-labs p-net uses). p-net instead ignores every other requester for 3 s after any Get or Set; go-profinet's `dcp.Client` retransmits long enough for both |
| PROFINET security classes 1–3, PROFIsafe | no authentication, integrity protection or safety layer (see [SECURITY.md](SECURITY.md)) |
| Fast Startup beyond DCP Hello | the device sends DCP Hello and the controller connects through it; the rest of Fast Startup (fixed MAU type with autonegotiation off, stored parameters, ARFSUBlock) needs control over the link and persistence that a library on a general-purpose OS does not have |
| A DHCP client | DCP's DHCP blocks (option 3) are decoded and encoded, the device reports configured DHCP options and "IP set by DHCP", and passes a DCP Set of them to `Config.SetDHCP` (refused without it). Obtaining an address by DHCP stays with the operating system; no public PI guidance on DHCP use was found |
| Port checks and adjustments beyond peers and boundaries | the device evaluates CheckPeers and applies PeerToPeerBoundary, DCPBoundary and PDInterfaceAdjust; it refuses CheckMAUType, CheckLinkState, CheckLineDelay (the real MAU type is not known to a library, the line delay is not measured, the link-state diagnosis code is not public) and the domain, multicast, MAU, link-state and preamble adjustments (no frame forwarding with one port; the link belongs to the operating system), with PNIORW "feature not supported" (0xA9), a code PI may define differently |
| RT over UDP (RT_CLASS_UDP), RT_CLASS_STREAM (TSN) | RT_CLASS_UDP has codecs only (`rt.AppendUDPPayload`/`DecodeUDP` for the RT payload on UDP port 34962, the RTClass, LT and AlarmCR transport fields in `pnio`), checked by tshark and scapy; the device refuses an RT_CLASS_UDP Connect (PNIO status DB 81 02 07, IOCRProperties) and the controller refuses to build one. No open implementation has a UDP data path to test against (p-net validates the fields only) |
| SNMP beyond a read-only v1/v2c agent | the `snmp` agent answers Get, GetNext and GetBulk and refuses every Set; no SNMPv3 (USM, VACM: a v3 message is dropped and counted), no traps or informs, the community string is the only (clear-text) access control. MIB-II system, interfaces (one row per port, no traffic counters from the device) and snmp groups and the LLDP-MIB local data and lldpRemTable are served; lldpLocSysCap*, lldpLocManAddrTable, lldpRemManAddrTable, the LLDP statistics and configuration objects, ifXTable and the PNO's PROFINET LLDP extension MIB are not (the extension's text is not public). lldpRemIndex is given to a neighbor once and kept while it stays; lldpRemTimeMark is a TimeFilter whose walks list every row at filter 0 |
| GSDX rules and signing, MRCD | their formats are not public (PI members only): see [docs/research/GSDML.md](docs/research/GSDML.md). `gsdml/gsdx` inspects the public OPC layer of a GSDX (see below); MRCD is not implemented |

What of this list can be built without PI membership or PROFINET hardware,
with the protocol evidence for each feature and the order of work, is in
[docs/research/SOFTWARE-ONLY-ROADMAP.md](docs/research/SOFTWARE-ONLY-ROADMAP.md).

## APIs (application processes)

A submodule carries its API (`model.Submodule.API`, from GSDML
`VirtualSubmoduleItem` `API`); API 0 is the default. The controller and
the device address a submodule by (API, slot, subslot): the
ExpectedSubmoduleBlockReq has one API entry per API of a slot, the IOCRs
list each IO data object under its API, the read and write headers, the
identification, ModuleDiffBlock, DiagnosisData and alarms carry it.
Restrictions:

- The API is an address only. No profile is implemented: what IO-Link
  (0x4E01) or another profile defines for its submodules, records or
  diagnoses is the application's.
- A submodule's records may use the user range 0x0000–0x7FFF and the
  profile range of a subslot, 0xB000–0xBFFF (Wireshark's PNIO dissector:
  "Reserved for Profiles (subslot specific)"; IEC 61158-6-10 not read).
  A record there is a plain parameter record: the model's Length, Default
  and ReadOnly, written at Connect like a user record. No profile's
  protocol is implemented, PROFIdrive's parameter access (0xB02E, 0xB02F)
  included: a Handler implements it. The other ranges Wireshark reserves
  for profiles (slot 0xD000–0xDFFF, AR 0xEC00–0xEFFF, API 0xF400–0xF7FF,
  device 0xFC00–0xFFFF) are not a submodule's records and stay refused in
  a model. go-profinet's device serves them to an independent controller:
  profinet-py reads and writes 0xB600 in API 0x4E01, and the device
  refuses another API, a read-only record and a wrong length
  (`TestProfinetPyAPI`). No independent IO-Device in profinet-interop
  accepts a record of 0xB000–0xBFFF (p-net hands its application indexes
  up to 0x7FFF only), so the controller side's evidence is tshark's decoding
  of the wire format, go-profinet's own device and p-net extended by OT
  Fabric (profinet-interop's profile-records variant, not independent:
  `TestPnetProfile` writes 0xB600 at Connect and in Data in API 0x4E01 and
  reads it back).
- A subslot number is unique in its slot whatever the API
  (`model.Device.Validate`), so the device can find a submodule by slot and
  subslot; a device that numbers subslots per API cannot be modelled.
- The DAP's submodules (identity, interface, ports) are API 0: the device
  serves the I&M0 filter data and the PDEV records there. `ToModel` refuses
  a DAP submodule of another API (`ErrUnsupported`); no file of the local
  GSDML corpus has one.
- A module without submodules has no API of its own: it is in API 0 (the
  ExpectedSubmoduleBlockReq, the identification and the ModuleDiffBlock),
  as before APIs. That keeps the encoding of v0.19.0; it is not taken from
  IEC 61158-6-10 or a reference implementation.
- Which part of the identification and diagnosis a read returns follows
  the read header's address and the index range: a subslot-level index
  (0x8000) the header's API, slot and subslot; a slot-level one (0xC000)
  its API and slot; an AR-level one (0xE000) every API; an API-level one
  (0xF000) the header's API; 0xF80C everything. A subslot-level read whose
  API is not the submodule's, or another read in an API the device does not
  have, is refused with "invalid area/API". Not checked against IEC
  61158-6-10 (not read). Against independent peers with an API other than
  0: p-net's answers to 0xE001, 0xF000, 0xC001 and 0x8001 (`TestPnetAPI`),
  and profinet-py's reads of go-profinet's device, 0xF000, 0xC001 and
  0x8001 per API and record reads refused with invalid area/API
  (`TestProfinetPyAPI`). Where the address has no submodule in the read's
  API, p-net answers 0xC001 and 0x8001 with no record data; go-profinet
  answers 0xC001 with the API and no slot and refuses 0x8001. Neither is
  checked against the standard. tshark and scapy decode the
  frames without a malformed frame (the interop suite's codec scenario);
  the field values they decode are not compared.
- APIData (index 0xF821) lists the device's APIs, API 0 among them, in
  ascending order, inside an AR or by ReadImplicit;
  `RecordClient.ReadAPIData` reads it. The layout is the one Wireshark's
  PNIO dissector reads, and p-net answers the read with it; IEC 61158-6-10
  was not read. A device of two APIs is p-net built for two APIs
  (`TestPnetAPI`), whose sample application keeps records by slot and
  subslot: that a device scopes records by API is not tested against an
  independent device. go-profinet's device answering with two APIs is
  read by profinet-py (`TestProfinetPyAPI`).
- `controller.Parameter` has no API: a parameter is written at the API of
  the model's submodule in its slot and subslot (API 0 where the model has
  none). `RecordClient.ReadDiagnosis` reads a subslot at API 0;
  `ReadSubmoduleDiagnosis` takes the API. profinetctl discovers APIs in
  `device info` via APIData (0xF821), followed by RealIdentificationData
  (0xF000) per API (since v0.18.0), and reports incomplete discovery on
  failure; `record read`/`write` take `--api`, and since v0.20.0 so do
  `im read` and per-submodule `diagnostics`.

## Without PI membership

Several artefacts are published by PROFIBUS & PROFINET International to
members only: the GSDML XSDs, GSDX, MRCD, the PROFINET security and test
specifications. [docs/PI-MEMBERSHIP.md](docs/PI-MEMBERSHIP.md) lists, feature by
feature, what is missing or degraded as a result, and what membership would
change.

## GSDML

The official GSDML schemas and specification are licensed by PROFIBUS &
PROFINET International to its members and are not redistributable.

**The generated model** (`gsdml/typed`, `gsdml/versions/vNNN`) and the
schema validation of `gsdml.CheckBytes` and `ValidateBytes` are generated
from the reference schemas V1.0 to V2.46 that the maintainers keep in a
private repository (the copies TIA Portal V21 installs, reserialised; not
PI's specification package). The evidence that the generated validator
decides as the schemas do is agreement with libxml2's `xmllint`, one other
implementation and not PI's checker: the four committed fixtures against
all 18 versions, 127 public vendor files against their own version (three more name no version), 29
hand-written one-rule mutants, about 3 200 seeded mutants, and the
re-encoding of every valid input (`make gsdml-differential`, no
allowlist). Where `xmllint` deviates from XML Schema 1.0, the validator
would deviate with it unnoticed. Not implemented, and refused where a
document uses them: `xsi:type` and `xsi:nil`. Not implemented, so
generation fails if a later schema uses them: `xsd:all`, `xsd:include`,
`xsd:redefine`, attribute groups, `nillable`, identity-constraint XPath
beyond child steps, `.//`, `*`, `p:*`, `.` and `@attr`, and pattern
constructs without an exact RE2 translation (class subtraction, block
escapes `\p{Is…}`). After an element that does not fit its parent's
content model, the parent's later children are not assessed: the verdict
is the same, but fewer issues are listed than `xmllint` lists. A
`SchemaError` lists at most 1000 issues. There are no types for V2.5x:
its schema is not in the private repository. Encoding keeps the schema's
content, attribute values and element order, but not comments,
processing instructions, the declared encoding (it writes UTF-8) or the
namespace prefixes (it writes `xsi` and `ns1`…). `gsdml.Check` and
`Validate`, on a `*Document` in memory, still apply the catalog's rules
below: the catalog's encoding (`Marshal`) adds defaults and uses the
newest element order, which is not valid against every older version, so
only the original octets can be validated against their own version.
A program that imports `gsdml` but never calls `CheckBytes` links none of
the generated code; validation of all 18 versions adds about 1 MB to a
binary, the decoders of one version (a `versions/vNNN` package) about
1 MB more, those of all versions about 4 MB (measured on darwin/arm64,
docs/research/GSDML-CODEGEN.md).

**The catalog.**
go-profinet generates its GSDML types from an independently written catalog
of the schema (`gsdml/schema/catalog.json`) and validates against it: the
structure, attribute types and references that a device model relies on.
`gengsdml -xsd DIR` compares the catalog with a licensed local copy of the
XSDs; the maintainers keep theirs, V1.0 to V2.46, in a private repository,
used only locally. Against V2.35 the modelled elements and attributes
match in structure, but the catalog records none of the 56 schema defaults
of those attributes, and its requiredness is V2.35's for every version.
`Validate` is stricter than the schemas: of 125 public vendor files that
`xmllint` accepts against their own schema version, it rejects 114 (118
before IDs became `xsd:token`), for IDs it takes as unique across the
document (the schemas scope uniqueness), attributes and elements that
older versions do not require, a choice of `ChannelDiagList` items it
takes as one required item, and required attributes given empty, which
it reports missing. `make gsdml-differential` checks that these are the
only differences (docs/research/GSDML-SCHEMA-PROPOSAL.md). `ToModel`
converts 129 of the 130 files. `ToModel` is all or nothing per device
access point: one module it cannot convert fails the DAP. `AnalyzeModels`
reports, per DAP and module, why; it is a diagnostic and returns no model
for a DAP `ToModel` refuses. **`ToModel` does not convert configurable
submodules; `gsdml.Configuration` does.** `ToModel` builds submodules from
the `VirtualSubmoduleList` of the DAP and of each module only, and since
v0.23.0 refuses (`ErrUnsupported`) a DAP whose own references, or those of
a module it plugs, put a configurable submodule (`UseableSubmodules`,
`SubmoduleItemRef` to the `SubmoduleList`) in a subslot
(`FixedInSubslots` or `UsedInSubslots`); a module only in its catalog
that needs one is left out of `Catalog`, and optional references are left
out of the model. That is unchanged. Since v0.24.0
`gsdml.NewConfiguration` resolves them: its default configuration plugs
the modules `ToModel` plugs and puts each configurable submodule in the
subslots it is fixed or used in; `Plug`, `Select` and `ClearSubslot`
change it, and `Resolve` returns a validated `model.Device`. The reading
of the subslot attributes is this package's, not checked against PI's
specification or an engineering tool: fixed means present and not
removable, replaceable or movable; used means present by default,
replaceable by a submodule allowed there and removable; without
`AllowedInSubslots` a submodule is allowed only where it is fixed or used;
the `PhysicalSubslots` of a DAP or module bound the subslots of its
configurable submodules but not those of its virtual ones (most corpus
files do not list those there); a subslot several references use by
default needs a choice. A module or submodule referenced more than once
is placed by the one reference that applies to the slot or subslot (one
that fixes, else uses, it there, else the first that allows it); the
references' attributes are never combined, and a position none allows is
refused. The same reading for modules is `ToModel`'s
(`FixedInSlots`, else `UsedInSlots`, the first reference taking a slot);
`Configuration` adds that a module fixed in a slot cannot be unplugged or
replaced. A configurable `PortSubmoduleItem` is a port of the DAP only
(one in a module is refused, `ErrUnsupported`), with no records, like a
built-in port; the port's `RecordDataList`, a module's
`SystemDefinedSubmoduleList` ports and the `ApplicationProcess`-level
`RecordDataList` are not converted. On the local corpus (249 files, 779
DAPs) the default configuration resolves for 778 DAPs, among them all 68
`ToModel` refuses for a configurable submodule, and for 710 of the 711
DAPs `ToModel` converts it is `ToModel`'s plugged configuration (the
other's model fails `model.Device.Validate`, which `Resolve` applies and
`ToModel` does not); 238 of them gain catalog modules. A resolved model's
`Catalog` holds a module with its default submodules; `FromModel` and
`model.Device.ModuleTypes` take a module type by ident number, so two
slots of one module with different submodules are one module type there.
`profinetctl connect --plug` chooses modules, not submodules. `gsdml.Validate` is not PI's GSDML
checker, and a file it accepts may still fail PI's. Elements and attributes
outside the catalog are kept verbatim and reported as unvalidated by
`gsdml.Check`; its semantic rules are a small set, each with its basis
(docs/GSDML.md), several of them local heuristics. Its reference rule
uses one table of every ID in the document, which misses IDs in elements
the catalog does not model: one schema-valid V2.45 vendor file has 255
references `Check` reports as unresolved. `CheckBytes` uses the schema's
keyrefs, in their scopes, instead. A document may have at
most 1 048 576 elements and a module at most 1440 submodules; `Check` lists
at most 16 384 issues per severity (SECURITY.md lists every fixed bound). Schema versions after 2.45
(2.46, 2.50) are not in the catalog; `CheckBytes` validates V2.46
documents against the V2.46 schema.
The defaults of optional attributes come from public sources, not the
XSDs: Ref BitOffset 0 and BitLength 1 from ISO 15745-4:2003/Amd 1:2006
(docs/GSDML.md). The reference schemas V1.0 to V2.46 give a parameter
record's Ref the same two, and Changeable and Visible true, the defaults
`SubmoduleParameters` reports. An empty
optional attribute (`BitLength=""`) is told from an absent one only in a
parsed document, and only until it is encoded: encoding omits it. A BitArea Ref must lie in
one octet (BitOffset + BitLength ≤ 8), although the 2006 definition allows
BitLength up to 15. A Ref DefaultValue in 0x hexadecimal is accepted as a
compatibility tolerance, as every number of the package is, although the
2006 definition (ISO 15745-4:2003/Amd 1:2006, Table D.31, pattern
`\-?[\d+]{1,20}`) allows decimal digits only; whether the current GSDML
specification allows it is not known (its text is members-only), and how
often vendor files use it has not been measured. A string or OctetString
Ref default shorter than its Length is zero-padded: no public source states
a padding rule (OPC 30144 v1.00 Table 58 does not), so this is the
package's policy; the lexical form of an OctetString DefaultValue (read as
Const data, `"0x01,0x02"`) is likewise unverified, and a VisibleString
default is not checked for characters outside its character set.
The embedded-status codecs of `pnio` follow IEC 61158-5-10:2023, read in
its indexed text (the PDF was not available), and RIO for Process
Automation, PNO 3.132 V1.0 (2003); they encode the four status codes
without judging the context (IEC marks the output-channel codes reserved).
Integer32_S and Unsigned32_S have no codec: no source defines them.
A Ref DefaultValue of a `_S`, composite, date and time, PROFIdrive (`N2`,
`V2`, …) or F_MessageTrailer DataType is refused (`ErrUnsupported`): how a
DefaultValue writes the value and status of a `_S` type, and the lexical
forms of the others, are not in a public source (OPC 30144 v1.00 Table 58
says "plus status" without a grammar, and no corpus file uses one), so a
record with such a Ref does not convert. That a Boolean DefaultValue is
written true, false, 1 or 0 is the xsd:boolean form, not verified against
the GSDML specification.
**Parameter records** (`SubmoduleParameters`, `ParameterRecord.Set`,
`Encode`): a field is written with the encoder of `ToModel`'s defaults, so
the same DataTypes are refused and the same policies apply (zero padding,
the OctetString form, hexadecimal integers). The rules taken from ISO
15745-4:2003/Amd 1:2006, Annex D (D.4.7.1 to D.4.7.4, Tables D.28 and
D.31) are as the maintainer's review states them; the amendment is not in
this repository. `Set` writes a field that is not `Changeable` (only
`PlanParameters` refuses one), and `Visible` is not used. `AllowedValues`
is compared for numeric, Bit, BitArea and Boolean fields only; ValueItem
values of every DataType go through the field's encoder. A
ValueItemTarget that names no ValueItem is refused, not taken as
unrestricted. Fields that share bits with each other are reported by
`ParameterRecord.Check` as a warning, not refused: no source this package
has says whether that is allowed. Consts that share octets are an error
(D.4.7.3); a field over a Const is allowed, the Ref taking precedence
(D.4.7.4). `Encode` refuses two given values for such
fields; `Set` of one changes the bits of the other. A default outside its
own `AllowedValues` is a warning of `Check`; `ToModel` and `Encode` keep
it. `Field` falls back to a field's name, which is the text of the primary
language, when the file's Refs have no IDs (before V2.3). A float is
compared with AllowedValues at the precision of its DataType, value and
bounds rounded alike: a package policy, not taken from a source. The golden octets of
the tests are derived by hand from the encodings; no engineering tool or
captured parameter write has confirmed them.
**Parameterization plan** (`PlanParameters`): the TransferSequence rule
(1 to N within a submodule, ascending, 0 undefined) is ISO 15745-4:2003/Amd
1:2006, Table D.28, as the maintainer read it, not checked against PI's
current GSDML specification. Where records of 0 go among sequenced ones,
and the slot and subslot order across submodules, are policies of this
package. A model must match the file: `ToModel` refuses a file with a
record field without a verified encoding, so such a device's model is
plugged by hand. Only `PhysicalSlots` and `AllowedInSlots` are checked,
not `FixedInSlots`, `UsedInSlots` or module counts. A plan has been
written only to go-profinet's own device (e2e), not to an independent one.
DataType legality by context is known for the 18 versions from V1.0 to
V2.46. The evidence is the schemas as TIA Portal V21 installs them, a
reserialised copy without XML comments rather than PI's specification
package (docs/research/GSDML-XSD-AUDIT.md), so `Check` warns about a
forbidden value and `Validate` does not fail on it. A newer version, such
as V2.5x, is unverified, and nothing is inferred across versions. The unit diagnosis, channel diagnosis and process alarm
contexts are not modelled, so they are not checked.
Float64+Status8, Integer32_S, Unsigned32_S and UTF8String are in no
schema up to V2.46 and their source is not recorded. UnicodeString8,
61131_STRING, 61131_WSTRING and the four composites V2.46 adds
(Unsigned16+Unsigned8, Integer16+Unsigned8, Unsigned32+Unsigned8,
Integer32+Unsigned8) have no known size, so a submodule or record
that uses one does not convert.
A document type declaration with an internal subset is refused
(`ErrUnsupported`): its attribute defaults and entities are not applied, so
the document is not read at all rather than read differently.

## GSDX containers

`gsdml/gsdx` is a generic Open Packaging Conventions inspector (ISO/IEC
29500-2 / Ecma-376 Part 2), not a GSDX implementation: PI's container
specification, signing policy and MRCD are members-only. It applies no
PI-specific rule and never says that a file is a valid GSDX. Digital
signatures are reported (references, signature time, signer certificate),
never verified: there is no XML canonicalization, no SignatureValue check
and no trust decision, so `Signature.Verified` is always false. Only the
digest of a part reference without transforms is compared with the part.
Which parts hold GSDML is guessed from the content type or the
`GSDML-*.xml` file name. Interleaved part pieces are kept, not reassembled.
`WriteTo` keeps every entry's name, method, time and content but compresses
again, so the compressed octets may differ.

## Timing

- Cyclic data runs on Go timers on a general-purpose OS. On Linux and macOS
  a 1 ms cycle is held with a mean lateness well below the cycle and
  occasional outliers; the watchdog and data hold factors absorb them. See
  [docs/CYCLIC.md](docs/CYCLIC.md) and [docs/BENCHMARKS.md](docs/BENCHMARKS.md)
  for measurements. In Docker Desktop's VM under the race detector, cycles
  below 4 ms are not reliable.
- There is no real-time guarantee: go-profinet is not for applications that
  need IRT, motion control or safety reaction times.

## Platforms

| Backend | State |
|---------|-------|
| Linux AF_PACKET | tested on a veth pair in Docker (`make l2-test`) and by the interop suite; a CI job runs the same tests on a veth pair |
| macOS / BSD BPF | macOS: a CI job tests it on a feth pair as root; locally, opening and reading an interface was verified (profinetctl on macOS). FreeBSD, NetBSD and DragonFly: cross-compiled and vetted only; the walk of a read buffer is unit-tested with the record layout of the 64-bit BSDs, no BSD kernel has run it |
| Windows Npcap | cross-compiled only: the package has no tests and was not run against Npcap (the hosted runners have none) |

32-bit: the tests run on linux/386 in CI (`make test-32bit`); linux/386
and linux/arm are cross-built. The e2e suite there runs without the race
detector, which 386 lacks.

Sizes: every 16-bit length, count and port is checked, never wrapped; the
limits and their errors are listed in ERRORS.md, "Values that do not fit
their field".

VLAN-tagged reception: the kernel strips the 802.1Q header before AF_PACKET
delivers a frame; the backend restores the tag from the packet's auxiliary
data (PACKET_AUXDATA).

## Devices

- One owner AR per submodule (shared device); several ARs per device up to
  `MaxARs`.
- One Ethernet port by default; with `Config.Bridge` a multiport device
  (`l2/bridge`): each port has its own MAC, LLDP agent, link state and port
  data. The bridge forwards in software (a goroutine hop and a copy per
  frame, no cut-through, no hardware queues): it suits tests and software
  devices, not deterministic forwarding; its ports must receive every frame
  (promiscuous). Its forwarding database is bounded (1024 stations by
  default; beyond that frames are flooded).
- Persistence is opt-in (`Config.Store`, package `store`): the permanent
  station name and IP suite, permanent DHCP options and I&M1–4 survive a
  restart; port, interface and MRP adjustments and user records are not
  stored (the IO controller writes them at every startup), so the
  boundaries and MRP role they set apply again only after the next
  connect. The semantics of a temporary Set follow p-net's reading of the
  CMINA state table, not the (non-public) text itself.
- Diagnoses (`AddDiagnosis`): the identity of a diagnosis, the update of
  a changed one and the AlarmSpecifier summary follow p-net v2.0.0
  (`pf_diag.c`, `pf_alarm.c`), not the standard's text. Not established:
  whether ARDiagnosisState covers the other submodules of the AR (it
  summarises the alarm's submodule, as p-net does). Not implemented: the
  MaintenanceItem (USI 0x8100) p-net puts before a maintenance alarm's
  diagnosis item, manufacturer-specific diagnoses (USI below 0x8000), and
  the PortDataChangeNotification alarm type p-net uses for remote mismatch
  diagnoses (go-profinet sends a Diagnosis alarm). Alarms of one priority are sent one at a time, each after
  the previous one's AlarmAck (p-net's behaviour; the standard's alarm
  machine text is not read). See
  [docs/DIAGNOSTICS.md](docs/DIAGNOSTICS.md).
- Port checks and adjustments (PDPortDataCheck, PDPortDataAdjust) and the
  remote mismatch diagnoses they cause belong to the AR that wrote them:
  the Connect of an IOCAR resets them for the DAP ports it takes, as
  p-net v2.0.0's `pf_pdport_reset_ar`; an IOSAR and S2 ARs keep them, and
  a port another AR holds keeps its state. A Connect refused after the
  reset gives the ports their state back (go-profinet's rule; p-net's
  source does not show it). The standard's text on this is not read.
- An alarm endpoint whose DATA PDU was not acknowledged within its
  retries sends no further DATA PDU: the AR is expected to be aborted, as
  the device does. Whether the standard's alarm machine allows anything
  else is not read.
- The LLDP TTL of 21 s is p-net v2.0.0's (its release notes cite the
  PROFINET test specification, which go-profinet has not read).

## Verification

- Interoperability is verified against four open PROFINET implementations
  (p-net, pnio-rs, profinet-py, pnio-dcp), two MRP implementations (umrp;
  microchip-ung/mrp on the Linux kernel's bridge MRP, in micro-VMs), an
  SNMP manager (Net-SNMP) and two decoders (scapy, Wireshark). There is no
  second open IO-Controller (CODESYS by hand: not run); no commercial PLC,
  device or engineering tool has been tested.
- Recorded traffic of real equipment ([testdata/captures](testdata/captures/README.md))
  is replayed offline. Only the frames a CPU 1515-2 PN sent (recorded by
  pnio-rs, firmware V2.9.4) are documented hardware traffic (class B); the
  IO-Device in those recordings was p-net, software. What the CPU would do
  with go-profinet's answers is not recorded, so the device is not
  "tested with an S7-1500": it answers the requests an S7-1500 sent. The
  other recordings (S7-300, S7-1200, KTP700, SCALANCE, a GE device) have no
  documented origin (class C).
- No PI certification test has been run. go-profinet claims no conformance.
