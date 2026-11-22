# Releases

go-profinet follows semantic versioning from v1.0.0; before that, minor
versions may change the API. Releases are tagged by the Release workflow
(`.github/workflows/release.yml`).

The notes of a version are the body of its GitHub release: the Release
workflow (the shared `go-package-release.yml`) takes everything after the
heading `## vX.Y.Z` up to the next line that is exactly `---`. So: one
section per version, newest first, the heading exactly `## vX.Y.Z`, each
section ending with a `---` line and no `---` line inside a section.

## Release checklist

1. `make check` and `make test-e2e` pass; the Interop workflow is green; a
   Soak run of the release commit (25 runs) is entirely green.
2. `INTEROP_REF` (Makefile, `.github/workflows/interop.yml` and
   `.github/workflows/kernel-mrp.yml`) names the profinet-interop release
   the matrix in INTEROPERABILITY.md and docs/FEATURE-STATUS.md came from;
   the Kernel MRP workflow is green.
3. This file lists the changes under the new version, in a `## vX.Y.Z`
   section ending with `---` (`make release-notes-check`); API changes are
   called out.
4. Run the Release workflow with the version bump.
5. Update profinetctl: `go get github.com/otfabric/go-profinet@<tag>`.

---

## v0.13.0

**Date:** 2026-11-22
**Previous release:** v0.12.0

## Summary

Binary codecs of the data types with an embedded status, and GSDML DataType
legality by schema version and context, taken from an audit of five schema
versions. API: additive. `Check` reports more, but `Validate` fails on
nothing new.

### Added

- pnio: `ValueStatus` (B, S, U, G) and the binary codecs of Unsigned8_S,
  Unsigned16_S, Integer16_S and OctetString_S (`EncodeUnsigned8S`,
  `DecodeUnsigned8S`, `EncodeUnsigned16S`, `DecodeUnsigned16S`,
  `EncodeInteger16S`, `DecodeInteger16S`, `EncodeOctetStringS`,
  `DecodeOctetStringS`), from IEC 61158-5-10:2023 5.3.1.6.8–5.3.1.6.10 and
  5.3.2.2. A value out of range or a status above 3 is refused with
  `ErrInvalidConfig`, not masked; an OctetString_S that is no multiple of
  three octets is `ErrMalformed`, one of more than 65535 octets
  `ErrResourceExhausted`. `gsdml.ToModel` still refuses these types as Ref
  defaults: how GSDML writes their value and status is not established.
- **Legality.** `gsdml.DataTypeLegality(version, context, DataType)` answers
  allowed, forbidden or unverified, together with `DataTypeContext` (`io`,
  `parameter`, `unitDiag`, `extChannel`, `processAlarm`), `Legality` and
  `DataTypeLegalityVersions`.
  - Only V1.0, V2.0, V2.1, V2.2 and V2.35 are decided, each by its exact
    set. Every other version is unverified, and nothing is inferred
    between versions.
  - The sets come from third-party copies of the schemas
    (docs/research/GSDML-XSD-AUDIT.md).
- **New Check rule `datatype-context`.**
  - An IO DataItem or parameter Ref whose DataType the audited schema of
    the document's version forbids there is a warning.
  - A document of an unaudited version, or with no version, gets one
    unvalidated issue, so most current documents (V2.4x) now report one
    more unvalidated issue.
- **New DataType constants.** `DataTypeUnicodeString8`, `DataType61131STRING`
  and `DataType61131WSTRING` (V2.35).
  - `Validate` now accepts them; it used to refuse them as unknown values.
  - They have no size, so `ToModel` refuses a submodule or record that uses
    one with `ErrUnsupported`.
- **Schema audit tooling.** `gengsdml -audit` and
  `scripts/gsdml-xsd-audit.sh` audit locally supplied schemas, following
  includes, and write a reproducible JSON report (docs/CODEGEN.md).

### Changed

- `gsdml.CatalogSHA256` and `gsdml.DataTypes` change with the catalog.

---

## v0.12.0

**Date:** 2026-11-21
**Previous release:** v0.11.0

GSDML input hardening (work packages A1 to A7 of the interoperability
and compliance plan) and a system-redundancy ordering fix. `gsdml.ToModel` no longer turns a malformed, empty or
unsupported value into a default without an error. API: additive only
(`UnmarshalXML` on generated GSDML types); several conversions that used
to succeed now fail, listed under Breaking.

### Breaking

Every item is a behaviour change of `gsdml`. Consumer assessment for all
of them: profinet-interop ships no GSDML file and sets no DefaultValue;
`gsdml.Generate` (profinetctl's `gsdml generate`) writes no Ref, no DOCTYPE
and no empty attribute; profinetctl's `--gsdml` loading and `gsdml
inspect` report the new errors (its invalid-GSDML error) where they used
to go on with a model missing the value; `Validate` already reported
every lexically malformed value except where noted.

- Integer and bit Ref DefaultValues are decimal, with an optional sign,
  or 0x hexadecimal (A1). Go's literal syntax was used, so `"010"` was
  octal 8 and `"0b101"`, `"0o7"` and `"1_0"` were accepted. Now `"010"`
  is ten, as in xsd:integer, and the other forms fail with
  `ErrMalformed`. Decimal values without leading zeros and 0x values give
  the same records as before. `Validate` does not check DefaultValue
  (type string).
- `LLDP_NoD_Supported` and `DCP_HelloSupported` that are present but not
  true, false, 1 or 0 fail with `ErrMalformed` (A3); they became false.
- An optional attribute of a type other than string that a document
  gives as `""` is no longer taken for an absent one (A4). `Validate`
  reports it when its type does not admit the empty string (`attribute
  BitLength: empty, not a Unsigned8`; newly reported), and `ToModel`
  gives a `BitLength=""` or `BitOffset=""` no default and refuses an
  empty Boolean capability. Documents built in Go are unaffected: an
  empty field there is absent.
- `ObjectUUID_LocalIndex`, `MinDeviceInterval`, `NumberOfAR`,
  `ReductionRatio`, `FixedInSlots`, `UsedInSlots`, `AllowedInSlots` and
  `FixedInSubslots` that are present but no number or ValueList, or
  empty, fail with `ErrMalformed`; a list of more than 64 reduction ratios
  or fixed subslots fails with `ErrResourceExhausted`, which `Validate`
  does not report (A5). Absent attributes keep their defaults;
  `NumberOfAR="0"` keeps one AR; more than one allowed slot still leaves a
  module unplugged.
- `Parse` refuses a document type declaration with an internal subset
  (`<!DOCTYPE x [ … ]>`) with `ErrUnsupported` (A7): XML 1.0 section 5.1
  has even a non-validating processor apply the attribute defaults and
  entities it declares, and encoding/xml ignores them, so such a document
  read differently here. A declaration without one is still ignored, and
  no DTD is fetched. `Validate` cannot report this (the document does not
  parse).
- A VisibleString, UTF8String or OctetString Ref DefaultValue longer than
  its Length fails with `ErrMalformed` (A2); it was cut to Length. A
  shorter one now zeroes the rest of its field, also over octets a Const
  wrote there, which it used to leave; and a string default keeps its
  leading and trailing white space, which was trimmed. A UTF-8 value
  counts its encoded octets. `Validate` does not check DefaultValue.
- A Boolean Ref DefaultValue is true, false, 1 or 0 and writes 0x00 or
  0x01 (A6). It was read as an Unsigned8: `"true"` failed, and now
  converts; `"2"` wrote 0x02, and now fails with `ErrMalformed`. A Ref of a DataType whose
  DefaultValue encoding is not verified fails with `ErrUnsupported`: the
  `_S` types, the composite, date and time types, the PROFIdrive types
  (`N2`, `V2`, …) and F_MessageTrailer4Byte/5Byte. They were written as an
  unsigned number. No GSDML file of profinet-interop or profinetctl has
  such a Ref.

### Added

- gsdml: the generated types of the 29 elements with optional typed
  attributes implement `xml.Unmarshaler`, recording which of those
  attributes a document gave as `""` (docs/CODEGEN.md).

### Fixed

- gsdml: a leading-zero integer DefaultValue (`"010"`) wrote the octal
  value into the parameter record the controller writes to the device at
  start-up, without an error.
- gsdml: an over-long string or octet Ref default was cut short without
  an error, so the device started with a value other than the one the
  document gives; a short one left a Const's octets after it.
- gsdml: an `Integer16_S` Ref default could not be negative and ignored
  the type's status bits, and a `TimeStamp` or `Float64+Status8` default
  was written as a 64-bit number with the remaining octets zero; such a
  record now fails rather than reaching the device wrong.
- gsdml: `ToModel` discarded the parse errors of the optional numbers and
  value lists above, so a `MinDeviceInterval="32ms"` gave a model with a
  MinDeviceInterval of 0, and a `device` run from it applied no lower
  bound to the IOCR cycle.
- gsdml: an attribute given as `""` took the default of an absent one
  (Ref `BitLength=""` became 1, an empty Boolean capability false) and
  passed `Validate`, which skipped every empty attribute.
- gsdml: `ToModel` turned an invalid `LLDP_NoD_Supported` or
  `DCP_HelloSupported` (`"yes"`, `"TRUE"`) into false, so a caller that
  skipped `Validate` got a model without LLDP or DCP Hello.
- device: a closing IOCARSR left the AR table (`ARs`) before its AR set
  (`SystemRedundancy`), in two separate steps, so a caller could see the
  AR gone from `ARs` and still in its set. Both now change in one step
  under the device lock (`TestSRSetLeavesWithAR`).
- Tests: the GSDML allocation budgets take the fewest octets of five runs
  (under `-race`, `sync.Pool` drops items at random); `TestSRPrimaryLeaves`
  waits for the no-primary time to grow (a coarse monotonic clock on
  Windows read 0); `TestCaptureReplayAlarms` reads the AR table once per
  round (an index panic when the AR left between two reads).

---

## v0.11.0

**Date:** 2026-11-21
**Previous release:** v0.10.0

Fixes for three reports by @mprotogerakis. API: four fields added to
`device.ARInfo`; the device's ModuleDiffBlock changes behaviour.

### Breaking

- `gsdml.CatalogSHA256` has a new value, because the catalog now records
  the Ref BitOffset and BitLength defaults. gorelease reports a changed
  constant as incompatible. Consumer assessment: no dependant
  (profinetctl, profinet-interop's own/go-profinet adapter) refers to the
  constant; code that compares it with a stored value must store the new
  one.
- Behaviour: an IO controller connecting to `device` gets no
  ModuleDiffBlock for a matching configuration, where it got every
  submodule as ApplicationReadyPending (see Changed). Consumer assessment:
  go-profinet's own controller treats both alike (`ModuleDiff()` is now
  nil for a matching device, as against p-net); profinetctl's mismatch
  warning ignores ARInfo and is unchanged, and the `module_diff` of its
  `ar` output no longer lists those submodules of a go-profinet device;
  pnio-rs and profinet-py as controllers against the device pass in the
  interop suite.

### Changed

- device: the ModuleDiffBlock of the Connect response and of the ModuleDiff
  record (0xE002) no longer lists every matching submodule as
  ApplicationReadyPending. A matching submodule the AR owns is Own and is
  listed only when it has a diagnosis, with the Fault, MaintenanceRequired
  or MaintenanceDemanded bit of its SubmoduleState, as p-net reports it
  (`pf_cmdev_generate_module_diff`); a matching configuration has no
  ModuleDiffBlock. A soft-PLC IO controller skipped every parameter write
  between Connect and PrmEnd on the ApplicationReadyPending submodules, so
  the parameters of its project never reached `Handler.WriteRecord`
  ([#72](https://github.com/otfabric/go-profinet/issues/72)). Wrong,
  missing and locked submodules are reported as before.

### Added

- device: `ARInfo.SendClockFactor`, `ReductionRatio`, `WatchdogFactor` and
  `DataHoldFactor`, from the output IOCR of the Connect (zero for an
  IOSAR), so an application can say how long a gap the controller asked
  the device to accept when an AR ends with "consumer data hold timer
  expired" ([#73](https://github.com/otfabric/go-profinet/issues/73)).
  `CycleNanos` is documented as the input IOCR's cycle.
- gsdml: optional attributes can carry a default in the catalog
  (`name:type=value`); the generated reference (SCHEMA.md) shows it and
  `gengsdml -xsd DIR` compares it with the XSDs' `default`.

### Fixed

- gsdml: a `DataType="BitArea"` Ref without `BitLength` made `ToModel`
  fail for every submodule with such a record ("malformed: bit Ref at
  5.0"), so no module of the file could be used. An absent BitLength is
  now 1 and an absent BitOffset 0, the defaults of ISO 15745-4:2003/Amd
  1:2006, Annex D, Table D.31 (not yet compared with the V2.3x/V2.4x PI
  XSDs). A BitLength that is present but no Unsigned8, or 0, is refused
  with `ErrMalformed` and named in the error; before, its parse error was
  discarded ([#74](https://github.com/otfabric/go-profinet/issues/74)).

---

## v0.10.0

**Date:** 2026-11-15
**Previous release:** v0.9.1

The DCP responder answers every requester again. No change to the API.

### Changed

- dcp: the responder no longer ignores the Get and Set requests of other
  requesters for 3 s after a Get or Set (v0.9.0). A tool that sends its
  Get and Set from different source addresses, or a Set right after
  another tool's Get, timed out; pnio-dcp, which uses a new source address
  per request in profinet-interop, failed `dcp.set-name`. Get is now
  always answered. Sets are serialized only within an explicit Set
  transaction: after a Set with Control/Start Transaction, the Sets of
  other requesters are answered with BlockError 4 (resource error) for
  every block and change nothing, until the holder's End Transaction or
  3 s after its last Set. `Stats().Ignored` no longer counts such
  requests; refused blocks count in `Refused` and are reported as
  `AuditRefused` with `BlockErrorResource`. The per-source rate limits
  (`Policy.SetLimit`, `Policy.IdentifyLimit`) apply as before, also to
  refused Sets.

---

## v0.9.1

**Date:** 2026-11-15
**Previous release:** v0.9.0

Fixed bounds on the work and memory a file or the network can ask for,
lifecycle fixes in the controller, device, MRP, netstack and store, and
contract tests for every long-lived type. No change to the API. The first
three items change what a caller can observe.

- dcp: an Identify without `IdentifyOptions.Max` collects at most 4096
  stations; when one more answers it returns the 4096 at once with an
  error matching `profinet.ErrResourceExhausted`. A `Max` replaces the
  bound.
- netstack: the ARP table holds at most 1024 addresses; when it is full, a
  new address is learned only from traffic for the stack itself, in place
  of the entry used longest ago. A stack that stops with its Mux closes its
  sockets: `ReadFrom` returns `net.ErrClosed`, and `WriteTo` on a closed
  socket returns an error matching `net.ErrClosed` and
  `profinet.ErrClosed`. A resolution removes its waiter however it ends.
- gsdml: `Parse` refuses a document of more than 1 048 576 elements
  (`ErrResourceExhausted`). `Check` lists at most 16 384 issues per
  severity and one that counts the rest (rule `limit`); slot lists are
  compared as sets of slot numbers, with one warning per list for values
  above 65535 and the warnings in the order allowed, used, fixed; a
  ReductionRatio list gets at most eight warnings and a count. `ToModel`
  counts `MaxModelRecordOctets` also per plugged slot, refuses a module or
  device access point with more than 1440 submodules and a Const
  ByteOffset or Ref BitOffset that is no number, compares offsets in 64
  bits and looks its texts up in a table.
- model: `Validate` refuses more than 1440 plugged submodules and more
  than 16 MiB of parameter records in them before its work per submodule
  (`ErrInvalidConfig`), which bounds `device.New` and
  `Controller.AddDevice` for any model; `Each` sorts positions instead of
  copies.
- gsdml/gsdx: `Open` decodes at most 65 536 XML elements into the package
  model and lists at most 16 384 issues plus a count; a certificate part
  is parsed once and listed once per signature.
- pnio: `DecodePDRealData` and `DecodePDRealDataRecord` take
  MultipleBlockHeaders nested up to four deep and refuse deeper ones with
  `ErrMalformed`; `DecodePDRealData` allocates in proportion to the record.
- snmp: a request reads the LLDP neighbors once, whatever its kind and
  number of variable bindings; all bindings of a response come from that
  reading.
- controller: an AR becomes the device's current one when its alarm
  endpoints, provider, consumer and tasks exist; until then `Stats`, `AR`
  and `ARUUID` report no AR.
- device: an abort that reaches an AR while its Connect runs ends the AR
  with the Connect, which is refused (CMRPC state conflict); the AR keeps
  no entry, FrameID, endpoint or submodule.
- store: `OpenDir` removes only the temporary files of its own directory
  (a valid key, `.tmp`, digits), whatever the keys and the path contain;
  `Delete` removes the previous version first; `OnRecover` runs with no
  lock held and may use the `Dir`.
- mrp: the manager counts a missed test per test interval; the rounds it
  sends at once for MRP_LinkUp frames do not count. After `Close` no timer
  changes a port state, flushes or calls `OnChange`, and `Close` waits for
  a timer callback in progress. The fields a manager swaps with its ports
  are read under the lock. A negative `TestInterval` is refused
  (`ErrInvalidConfig`).
- l2/bpf: the walk of a read buffer uses the header layout and
  `BPF_ALIGNMENT` of the OS it is built for and compares the lengths
  unsigned (FreeBSD, NetBSD, DragonFly: compiled, not run); macOS
  unchanged.
- Documentation: every callback states its goroutine, what it may call,
  that it must not call `Close` and that a run-time failure in it is not
  recovered; every `Close` states that it is safe to call more than once
  and from multiple goroutines; corrected statements on `lldp.TLV.Value`,
  the SNMP length encoder, the BSD and Npcap backends; SECURITY.md lists
  the fixed bounds.
- Tests: the lifecycle contract matrix (L1 to L16) per type in
  docs/TESTING.md with tests for concurrent `Close`, callbacks that use
  their type and construction failures; five more fuzz targets (39); the
  packages that start goroutines fail when their tests leave one behind
  (`internal/leakcheck`).

---

## v0.9.0

**Date:** 2026-11-14
**Previous release:** v0.8.1

Tighter limits and lifecycle handling for peers and files. One addition to
the API: `rpc.Client.Foreign`, `gsdml.MaxModelRecordOctets`.

- rpc: the server refuses a newer call on an activity whose call still
  runs (`Busy`), so waiting handlers stay bounded by the activities. The
  client takes the packets of a call only from the host it called and
  counts the others (`Foreign`).
- device: a Connect for an ARUUID whose Connect still runs is refused; a
  Connect that ends after `Close` began fails instead of adding its AR; the
  CM activity timer and submodule handovers no longer start work after
  `Close`. Each AR uses its own RPC activity for ApplicationReady.
- device: a DCP ResetToFactory refuses Connects while it runs, so no AR
  outlives it, and reports a refused IP reset instead of success.
- device: MRP role changes are serialized; one MRP entity runs, and none
  after `Close`.
- device: a permanent DCP Set of the IP suite or a DHCP option that the
  application refuses leaves the stored value as it was.
- dcp: the responder ignores the Get and Set requests of other requesters
  for 3 s after a Get or Set (counted in `Ignored`), as documented.
- mrp: TopologyChanges with an interval share one pending FDB flush,
  stopped by `Close`.
- controller: the handover of a submodule after a Released alarm runs once
  at a time per submodule and ends with the AR.
- snmp: long-form lengths of four octets are checked unsigned (32-bit
  platforms).
- l2/afpacket: a received frame leaves room for the VLAN tag the kernel
  stripped.
- gsdml: slot warnings are bounded per module reference and quote at most
  64 octets; `ToModel` converts a module once and bounds the octets of all
  parameter records (`MaxModelRecordOctets`, 16 MiB).
- gsdml/gsdx: a signature part is read once, a part's digest is computed
  once per method, and core property text is built in linear time.

---

## v0.8.1

**Date:** 2026-11-12
**Previous release:** v0.8.0

The interop suite moves to profinet-interop v0.8.0 (contract 1.3, a
compatible addition). No code change.

- `INTEROP_REF` is v0.8.0 in the Makefile and the Interop and Kernel MRP
  workflows.
- The matrix in INTEROPERABILITY.md and docs/FEATURE-STATUS.md is that of
  the v0.8.0 run (2026-11-12, Interop and Kernel MRP workflows): 39 test
  runs, 248 pass, 0 fail, 6 n/a, 14 attributed peer deviations, the same
  per-peer counts as the v0.6.0 run it replaces. profinet-interop's
  MATRIX.md is regenerated from it.

---

## v0.8.0

**Date:** 2026-11-08
**Previous release:** v0.7.0

go-profinet is now tested against recorded traffic of real equipment
([testdata/captures](testdata/captures/README.md)): above all the frames a
SIMATIC S7-1500 CPU 1515-2 PN (firmware V2.9.4) sent as IO-Controller,
recorded by pnio-rs, which the device tests send to go-profinet's device
unchanged; and DCP, LLDP and Connect frames of older Siemens and GE
equipment. Every recorded frame decodes as Wireshark decodes it and
re-encodes to its bytes; the device answers the CPU's start-up (Connect,
MultipleWrite, PrmEnd, ApplicationReady), cyclic, alarm and DCP requests
as the recorded IO-Device (p-net, software) did, apart from classified
differences. The recordings showed four defects, fixed here. This is
recorded evidence, not a test against a live PLC and not a conformance
claim.

### Fixed

- `device`: the AlarmSpecifier sequence number is counted per priority, not
  per AR. The recorded device numbered its high and low priority alarms
  separately (the first diagnosis alarm after a process alarm has
  sequence 0) and the CPU acknowledged them so; go-profinet gave it 1.
- `device`, `controller`: an ERR-RTA of the peer is answered with an
  ERR-RTA "AR alarm.ind(err)" (`CF 81 FD 0B`), as the CPU answers a
  device's ERR-RTA. Both ended the AR silently before.
- `dcp.ParseIdentity`: trailing NUL padding of NameOfStation and
  TypeOfStation is dropped. A recorded S7-300 answers its name in a
  108-octet field padded with NULs, which made the name unusable for
  matching and display.
- `lldp`: the IPv4 Management Address TLV's interface number and OID were
  dropped on decode and always sent as 1 and none; they are now
  `Frame.ManagementIfIndex` and `Frame.ManagementOID` (an interface number
  0 is sent as 0). A management address that is not IPv4 (an Extreme
  switch's MAC) is kept in `Other` instead of dropped. A second Chassis ID,
  Port ID or TTL TLV makes `Decode` fail with `ErrMalformed` (IEEE 802.1AB:
  exactly one each; a recorded LLDPDU repeats its TTL TLV). The device's
  LLDP is unchanged (ifIndex 1, no OID).

### Added

- `lldp.Frame.ManagementIfIndex`, `lldp.Frame.ManagementOID`.
- testdata/captures: 37 frames from pnio-rs (MIT OR Apache-2.0), the
  Wireshark wiki's PN-DCP sample and ten EmreEkin/ICS-Pcaps PROFINET and
  LLDP recordings (974 frames), byte-identical under clean names, with
  `manifest.json` (source, original name, device, direction, evidence class
  A–D, SHA256), `SHA256SUMS` and `coverage.json`; `internal/capture` reads them.
- Tests: decoding, byte-exact re-encoding and encoders against every
  recorded frame (`dcp`, `lldp`, `pnio`, `rt`, `alarm`); the device against
  the CPU's start-up, cyclic, alarm, ERR-RTA and DCP frames
  (`device/capture_test.go`), every recorded DCP Identify request and the
  Connect of a 2009 Siemens controller; the record client against a GE
  device's and p-net's recorded answers; every LLDP, DCP, PNIO-CM, RT and
  RTA frame of every file decoded and re-encoded; every truncation and
  forced length field of every frame; 180 fuzz seeds from the recordings.

### Changed

- The first low priority alarm of an AR has sequence number 0 even after
  high priority alarms (see Fixed). An application that matched
  acknowledgements by its own count across priorities must count per
  priority.
- `lldp.Decode` refuses an LLDPDU with a second Chassis ID, Port ID or TTL
  TLV; the agent's neighbor table ignores such a frame.

### Known differences (recorded, classified, not changed)

The device sends a ModuleDiffBlock marking every submodule
ApplicationReadyPending where p-net sends none; it grants
MaxAlarmDataLength 256 where p-net answers 200; it reports a channel
diagnosis as USI 0x8000 where p-net uses 0x8002; it refuses an invalid
NameOfStation an S7-300 accepted; it answers I&M0FilterData without the
optional module block a GE device sends. The encoder puts the AlarmCRBlockReq
before the expected submodules, where both Siemens controllers put it last,
and `rt.ComputeLayout` puts the IOCS after all data, where the CPU puts
them after each submodule. None of these is known to matter to a CPU: the
recordings show what the CPU accepted from p-net, not what it would do
with go-profinet's answers.

---

## v0.7.0

**Date:** 2026-11-05
**Previous release:** v0.6.2

Security hardening from a gosec/opengrep review (153 findings at low/low:
146 fixed in code, 7 accepted with a recorded justification), new hostile-
peer, fuzz and chaos tests, and Go 1.26. Values that used to wrap silently
are now errors; see ERRORS.md, "Values that do not fit their field".

### Breaking

- **Go 1.26 or later** (was 1.23); CI runs 1.26 and 1.27. `go fix` applied.
- Out-of-range configuration is refused with `ErrInvalidConfig` where it
  was truncated before: `RPCPort`/`ControllerRPCPort`, MRP ring ports, MRP
  profile intervals, controller station and MRP domain names above 240
  octets, `rpc` `MaxFragmentBody` above 65 427 (capped), `store.OpenDir`
  `MaxValueSize` above 2^32-1 (capped).
- `controller.RecordClient.Read` refuses a RecordDataLength above `maxLen`
  (its documented bound; it returned the data before).
- `gsdml`: `ParseValueList` with a negative limit allows no values (was:
  unbounded); `ToModel` refuses `FixedInSubslots` above 0xFFFF and a bit
  DefaultValue wider than its BitLength or a wrapping BitOffset.

### Fixed

- **32-bit platforms:** a record length of 0xFFFFFFFF in a Write, a
  WriteMultiple or a Read answer passed the length check (int -1) and
  panicked the device or controller. Lengths are compared as uint64. CI now
  runs the tests on linux/386 (`make test-32bit`).
- Device: a Connect with ~4 100 expected modules got a ModuleDiffBlock with
  a wrapped length; it is refused (CMRPC out of memory). A diagnosis record
  grown past 64 KiB panicked the RPC server; it is answered with a read
  error. The diagnosis reader no longer encodes under its lock. Abort
  goroutines are tied to `Close`.
- RPC: fragment numbers wrapped above 65 536 fragments; such a call or
  response is refused (a FAULT for a response). Alarm: a VarPart above
  65 535 octets is refused before it takes a sequence number.
- Controller: a WriteMultiple answer with more results than items is
  refused; reconnect jitter comes from crypto/rand.
- LLDP TTL saturates to 1..65535 (a receive-only agent announced 65532);
  netstack refuses ports outside 0..65535 and IPv4 datagrams above 65 535
  octets; pcap refuses times outside 1970..2106 and reads a record whose
  sub-second field is a second or more as malformed; the store opens values
  through `os.OpenInRoot`, so a symlink cannot lead outside its directory.
- `l2/afpacket` decodes the kernel structures with `binary.NativeEndian`
  instead of pointer casts (two `unsafe` calls remain, for recvmsg and the
  flags ioctl); otfnode, l2bench and examples/controller refuse flag values
  out of range.

### Tests

- Hostile peers: `device/hostile_test.go`, `controller/hostile_test.go`
  (malicious Connects, AR floods, lying record lengths, 4 GiB claims),
  DCP responder floods, rpc client and alarm endpoint fuzzing.
- 8 new fuzz targets (34 in all), including the device and controller RPC
  servers end to end.
- e2e: chaos on startup frames only, RTA ACK loss, Close in each phase.
- profinet-interop v0.7.1 (base images pinned by digest; adapters
  unchanged), shared workflows v3.3.1.

---

## v0.6.2

**Date:** 2026-10-31
**Previous release:** v0.6.1

### Changed

- Policy 1.2: build output in `bin/` and `release/`, a per-package coverage floor, and more tests and CI. No change in behaviour.

---

## v0.6.1

**Date:** 2026-10-30
**Previous release:** v0.6.0

Device behaviour change; no API change.

- **`MinDeviceInterval` bounds the cycle, not the send clock.** The device
  compares it with SendClockFactor × ReductionRatio, so a device can declare
  up to 4096 (128 ms); before, one that declared more than 128 refused every
  Connect. Contributed by Giebisch (#52).
  - A device that declares 32 now accepts a send clock below 1 ms when the
    cycle reaches 1 ms.
  - A cycle below `MinDeviceInterval` is refused with IOCRBlockReq
    ErrorCode2 11 (ReductionRatio), as p-net does; it was 10
    (SendClockFactor) when the send clock alone was below it.
- **`model.Device.Validate`** refuses a `MinDeviceInterval` above 4096, so
  `device.New` and `gsdml.FromModel` do too. Such a device refused every
  Connect in v0.6.0. Zero stays valid and sets no lower bound.

---

## v0.6.0

**Date:** 2026-10-29
**Previous release:** v0.5.1

Released together with profinet-interop v0.6.0 and profinetctl v0.6.0:
from this version the three share one version number. No incompatible API
change (apidiff against v0.5.1: additions only).

- **Metrics:** `profinet.Metrics` with `NopMetrics`, on
  `controller.Config.Metrics` and `device.Config.Metrics`: state changes,
  connects, aborts, record accesses with their durations, alarms and the
  controller's reconnection attempts.
- **Backoff:** `profinet.Backoff` (exponential, with jitter); the
  controller reconnects with it, and `DeviceConfig.ReconnectJitter` (0 to
  1) spreads the waits of devices that failed together. Without it the
  waits are as before.
- The retry warning names the device once; `pnio.Status` names the CMRPC
  error codes ("AR UUID unknown", "state conflict", …).
- **Examples:** `observability` (log/slog, Prometheus-style counters, a
  pulled cable and the jittered backoff) and `mrp` (a ring of three
  bridged stations, a cut cable); both run in `make examples`, their output
  is in examples/README.md.
- **Docs:** the support, feature and interop matrix
  (docs/FEATURE-STATUS.md) gains the release compatibility of the three
  repositories and an API and engineering section; OBSERVABILITY.md
  (Metrics), ERRORS.md (retries and backoff), API.md, docs/CONTROLLER.md.
- **Tests:** the MRA interop tests run the 500 ms profile (a stalled
  micro-VM made a kernel MRA client take over in the 200 ms profile).
  The MRP ring test resends its loop check when the manager opened the
  ring during it (Windows' timers delayed its tests); the release passed
  the 25-run soak (351 jobs).
- Interop: profinet-interop v0.6.0 (contract 1.2), pinned in the Makefile
  and both interop workflows.

---

## v0.5.1

**Date:** 2026-10-28
**Previous release:** v0.5.0

Documentation and pins; no library change (apidiff against v0.5.0: none).

- docs/FEATURE-STATUS.md is the support, feature and interop matrix:
  versions, platforms, features by role (device, controller,
  profinetctl), and each independent implementation against each feature
  area with profinet-interop's totals.
- README: the automatic manager and the kernel MRP peer; the comparison
  with other stacks gains re-parameterization, MRP and SNMP rows.
- Corrected or completed: SECURITY.md (0.5.x supported), PROGRESS.md,
  API.md (the MRA symbols), OBSERVABILITY.md (bridge, MRP and SNMP
  counters, the DCP audit), TESTING.md and COVERAGE.md (26 fuzz targets,
  coverage 97.1 % per package as of v0.5.0, the Kernel MRP tier),
  LIMITATIONS.md, DEVICE.md and CONTROLLER.md (multiport, MRP, SNMP, DCP
  policy, re-parameterization, S2), dated notes on the research reports.
- Interop: profinet-interop v0.4.1 (contract 1.2; 248 pass, 0 fail, 6 n/a,
  14 attributed deviations), pinned in the Makefile and both interop
  workflows.
- Tests: three timing assumptions that a loaded runner broke (the
  automatic manager tests run the 500 ms profile; the DCP Hello spacing and
  an RPC counter are checked without a receiver's scheduling delay).

---

## v0.5.0

**Date:** 2026-10-28
**Previous release:** v0.4.0

The MRP automatic manager (MRA), and MRP verified against the Linux
kernel's bridge MRP: microchip-ung/mrp runs as a peer in micro-VMs booted
with a kernel built without switchdev. Feature coverage is not
conformance: go-profinet is not certified, and no measured time is a
real-time claim.

No incompatible API change (apidiff against v0.4.0: additions only).

- `mrp.RoleAuto` (MRA): starts as a manager; the better of two managers
  (lower priority, then lower address: `mrp.Better`) answers the other's
  tests with MRP_TestMgrNAck, the other gives way (MRP_TestPropagate) and
  monitors the better managers' tests, managing again after TSTNRmax
  intervals without one. `Status.Acting`, `Manager`, `ManagerPrio`;
  `ManagerOption`, `NewManagerOption` (Siemens OUI, which Wireshark
  decodes; any OUI read). `device.MRPConfig.Role` may be `mrp.RoleAuto`
  (configuration only: PROFINET's MRP_Role has no value for it);
  PDInterfaceMrpDataReal reports the acting role.
- `mrp.Decode` accepts the Linux bridge's MRA test frames, whose last
  MRP_Option claims five octets more than the frame has.
- Verified against umrp and microchip-ung/mrp (kernel): manager, client,
  seeded cable faults, MRA with go-profinet and with the peer the best
  (failure and return of the manager): no loop or hole after any change;
  measured outages 10–20 ms on the kernel ring, 200 ms profile. umrp's MRA
  as a client keeps reclaiming the manager role (its monitor ignores the
  manager it gave way to): recorded as its deviation.
- Kernel MRP workflow: `scripts/mrp-kernel.sh` builds Linux 6.12 with
  `BRIDGE_MRP` and without `NET_SWITCHDEV`; `scripts/microvm-interop.sh`
  runs each interop test in its own virtme-ng micro-VM with a Docker daemon
  inside (amd64, KVM). Interop harness: results written before teardown,
  ring nodes stopped before removal, bounded removal.
- `device`: a state that did not change is not saved again (a DCP Set
  retransmitted by a tool), so a store keeps the previous version it
  recovers from after a torn write.
- Interop: profinet-interop v0.4.0, contract 1.2 (248 pass, 0 fail, 6 n/a,
  14 attributed deviations).

---

## v0.4.0

**Date:** 2026-10-28
**Previous release:** v0.3.0

Independent interoperability and production readiness: MRP and the SNMP
agent tested against independent implementations, persistence,
re-parameterization of a running AR, an allocation-free Linux transport, and
synthetic GSDML fixtures. What each feature covers, how it is tested and
what limits it is in [docs/FEATURE-STATUS.md](docs/FEATURE-STATUS.md); the
program's outcome, blockers and roadmap in
[docs/research/NEXT-PROGRAM-REPORT.md](docs/research/NEXT-PROGRAM-REPORT.md).
Feature coverage is not conformance: go-profinet is not certified, and no
measured time is a real-time claim.

No incompatible API change (apidiff against v0.3.0). One behaviour change:
`snmp.LLDPRemote.TimeMark` is the time of a row's last change; walks list
every row at TimeFilter 0 and a Get answers any filter value up to it
(before, a row appeared at its TimeMark only).

- Persistence: package `store` (`Store`; `Memory`; `Dir`: CRC-32C-checked
  files replaced atomically, the previous version kept and used after a
  crash or corruption) and `device.Config.Store`/`StoreKey`: the permanent
  station name and IP suite, DHCP options, I&M1–4 and resets survive a
  restart; a temporary DCP Set clears the stored value; a failing store
  refuses the change; `Device.StoreError`.
- `controller.Device.Reparameterize`: PrmBegin, writes, PrmEnd and the
  device's ApplicationReady on a running AR; the parameters are kept for
  reconnects. Verified with profinet-py re-parameterizing go-profinet's
  device; p-net and pnio-rs refuse PrmBegin in Data.
- SNMP: lldpRemIndex is kept per neighbor (`lldp.Neighbor.Index`,
  `Changed`); lldpRemTimeMark is a TimeFilter. Verified with Net-SNMP:
  walks, errors, limits, malformed datagrams, a flood, changing neighbors.
- MRP verified against umrp as manager and as client (profinet-interop
  v0.3.0); microchip-ung/mrp could not run (the kernels available refuse
  MRP on a software bridge); the interop suite builds MRP rings of veth
  cables (`otfnode mrp`).
- `l2/afpacket`: reads, writes and `LinkUp` allocate nothing; flood
  throughput ×11, losses gone, 35 → 1 garbage collection per 30 s of a 1 ms
  exchange (docs/BENCHMARKS.md, `scripts/l2-bench.sh`); the cyclic intervals
  in a VM are unchanged.
- GSDML: synthetic MIT fixtures (`gsdml/testdata/fixtures`: two device
  access points with one and two ports, shared device, a compact device),
  run as devices and controllers and parsed by profinet-py; an evidence file
  for engineering-tool imports (none run yet).
- Fixes: `device`: a Connect counted twice against MaxARs while its AR was
  being established, so a second IO controller connecting at that moment was
  refused "out of AR resources"; `l2/bridge`: statistics counted a frame
  after it could be read.
- Docs: `docs/research/INDEPENDENT-CONTROLLER.md` (CODESYS manual recipe),
  `docs/research/GSDML-TOOLS.md`; a correction of v0.3.0's report: scapy has
  no MRP dissector.
- Interop: profinet-interop v0.3.0 (193 pass, 0 fail, 8 n/a, 8 attributed
  deviations).

---

## v0.3.0

**Date:** 2026-10-27
**Previous release:** v0.2.0

The rest of the software-only roadmap
([docs/research/SOFTWARE-ONLY-ROADMAP.md](docs/research/SOFTWARE-ONLY-ROADMAP.md)):
multiport devices, MRP, system redundancy as an experiment, SNMP, DCP
hardening, DHCP blocks, RT_CLASS_UDP codecs and a GSDX inspector. What each
feature covers, how it was verified and what it does not do is in
[docs/research/FEATURE-COMPLETION-REPORT.md](docs/research/FEATURE-COMPLETION-REPORT.md).
Feature coverage is not conformance: go-profinet is not certified, and no
time measured on the virtual network is a real-time claim.

One incompatible API change (apidiff against v0.2.0): `dcp.ResponderOptions`
is no longer comparable with `==`, since its `Policy` holds an audit
callback. Behaviour changes: a
Connect that has both a bad LT and a bad RT class is refused naming the RT
class (IOCRProperties) instead of the LT; an AlarmCR over UDP is refused
naming its properties.

- Multiport devices (roadmap 4A): `l2/bridge`, the learning switch of a
  multiport device (CPU port, per-port local ports for LLDP and MRP,
  forwarding/blocked states, DCP boundaries, a bounded FDB), and
  `device.Config.Bridge`: each port has its own MAC address, LLDP agent,
  link state, port data and peer check.
- MRP (roadmap 4B): `mrp.New` runs a ring client or manager on a bridge
  (200 ms and 500 ms profiles); the PROFINET MRP records 0x8050–0x8054
  (`pnio`), `device.Config.MRP`, `Device.MRPStatus`,
  `controller.DeviceConfig.MRP`.
- System redundancy S2 (roadmap 3): an experiment, off by default and not
  interoperable: `device.Config.SystemRedundancy`,
  `controller.DeviceConfig.SystemRedundancy`, `Device.SetPrimary`,
  `Device.RedundancyAck`; abort reasons 38–43 and the SRInfoBlock error
  code in `pnio`.
- SNMP (roadmap 5, for CC-B features): `snmp`, a read-only SNMPv1/v2c agent
  with MIB-II and the LLDP-MIB; `device.Config.SNMP` (opt-in).
- DCP: DHCP blocks of option 3 (`dcp.DHCPParameter`, `Identity.DHCP`,
  `device.Config.DHCP`, `SetDHCP`); an opt-in policy (`dcp.Policy`,
  `device.Config.DCPPolicy`): read-only DCP, per-source rate limits, audit
  events. Never called a security class.
- RT_CLASS_UDP: codecs (`rt.AppendUDPPayload`, `rt.DecodeUDP`, the UDP
  fields of `pnio`); the device and the controller refuse it.
- GSDX: `gsdml/gsdx`, a safe inspector of the OPC container (no PI rules,
  signatures not verified); `gsdml.Severity` decodes from its JSON.
- Fixes, found by soaking: `controller`: no deadlock between an AR's
  teardown and a frame being handled (since v0.1.0); `device`: an AR being
  replaced (a rerun) counts once against MaxARs; a shared-device handover to
  an AR completing its startup is announced once it runs (v0.2.0);
  `gsdml.Severity` decodes from its JSON (v0.2.0).
- Interop: pinned to profinet-interop v0.2.1; new decoder checks for SNMP,
  UDP-RT, the DCP DHCP blocks and the MRP records.
- 25 fuzz targets (was 17); coverage 97.1 %.

---

## v0.2.0

**Date:** 2026-10-27
**Previous release:** v0.1.2

The first features of the software-only roadmap
([docs/research/SOFTWARE-ONLY-ROADMAP.md](docs/research/SOFTWARE-ONLY-ROADMAP.md)):
DCP Hello, port checks and adjustments, GSDML validation, shared device,
and codecs of MRP, PTCP and system redundancy. No incompatible API change
(apidiff against v0.1.2); two behaviour changes: the IO-Device now
accepts the IOCARs of several IO controllers (shared device) instead of
refusing a second one, and generated GSDML no longer declares SNMP.
Feature coverage is not conformance: go-profinet is not certified.

- DCP Hello (roadmap 1A): `dcp.HelloFrame`, `Responder.StartHello` and
  `StopHello` (bounded sequences, rate-limited across restarts, ended by a
  DCP Set); the IO-Device sends Hello at start and on link up while no IO
  controller is connected (`device.Config.Hello`, `HelloOptions`) and
  reports DeviceInitiative; the IO-Controller connects through a Hello
  without Identify (`controller.Config.IgnoreHello`, `DeviceStats.Hellos`).
  `model.Capabilities.DCPHello` and GSDML `DCP_HelloSupported`.
- Port checks and adjustments (roadmap 1B): `pnio` codecs of
  PDPortDataCheck, PDPortDataAdjust, PDInterfaceAdjust, PDInterfaceDataReal
  and the PDRealData record with the interface (`DecodePDRealDataRecord`);
  the IO-Device evaluates CheckPeers against its LLDP neighbor (remote
  mismatch diagnoses with alarms), applies PeerToPeerBoundary,
  DCPBoundary and PDInterfaceAdjust, serves PDExpectedData and
  PDInterfaceDataReal, reports the real link state, and refuses the
  checks and adjustments it cannot honour; the IO-Controller writes
  `DeviceConfig.PeerChecks` and verifies a topology (`CheckTopology`).
  The LLDP agent reports changes (`AgentOptions.OnChange`), can stop
  sending (`SetTransmit`) and forget its neighbors (`Clear`).
- Shared device (roadmap 2): the IO-Device accepts IOCARs of several IO
  controllers; each submodule belongs to the first AR that expects it,
  the others see it locked (ModuleDiff ARInfo 3), get no data of it,
  cannot write it; alarms go to the owner; when the owner leaves, the
  submodule is handed over with a Released alarm, PrmEnd (plug) and
  ApplicationReady (plug). The IO-Controller leaves out locked submodules,
  takes over released ones, and `model.Device.WithSlots` builds the
  model of a controller that owns only some modules. A closed AR now
  invalidates only its own outputs.
- Codecs of features without an open implementation to test behaviour
  against (roadmap 3, 4B, 5): `mrp` (MRP frames), `ptcp` (PTCP delay
  frames), `pnio.SRInfoBlock`, `pnio.ARSetID`, `pnio.MCRBlockReq`, the
  IOCARSR DataStatus codes in `rt`; golden vectors, fuzz targets, tshark
  and scapy decode them without a warning.
- Generated GSDML no longer declares SNMP in `SupportedProtocols`: the
  device has no SNMP agent.
- `docs/research/SOFTWARE-ONLY-ROADMAP.md` and its evidence files.
- GSDML validation (roadmap 1C): `gsdml.Check` grades every issue as
  error, warning or unvalidated and names its rule; semantic rules for IO
  data that does not fit an RT frame (error), slots against PhysicalSlots
  and AllowedInSlots, record indexes, module ident numbers, interface and
  port subslots and reduction ratios (warnings, each with its basis);
  elements and attributes outside the catalog and newer schema versions
  are reported as unvalidated. `Validate` still fails on errors only.
- `controller`: `Device.WaitState` returns only after the `OnState`
  callback of the state it waited for has run, as documented; before, it
  could return in between.
- `device`: a controller that connects anew before the device saw its old
  AR end replaces that AR, which hands its submodules to the new one
  (before, they were locked until the old AR timed out, or went to
  another controller); `SendAlarm` no longer reads the alarm endpoint of
  an AR that is still being set up (data race).
- Tests: a failed e2e test prints the logs of its devices and
  controllers; tests that are not about the watchdog ride out CI runner
  stalls of seconds. Soaked: every CI job 25 times in parallel, green.
- Interop: pinned to profinet-interop v0.1.3, whose matrix covers the new
  features (p-net 55 checks pass, 0 fail).

---

## v0.1.2

**Date:** 2026-10-26
**Previous release:** v0.1.1

Fixes found by soaking every CI job and the interop suite 25 times in
parallel. No API change.

- `device`: `Handler.StateChanged` reports the transitions of an AR in
  order. A transition caused while the previous one was being reported
  (PrmBegin right after Data) could be reported first.
- `rpc`: the client no longer drops response fragments when they arrive
  faster than the call takes them; a large fragmented response could time
  out on a loaded host because every retransmission lost the same
  fragments.
- `l2/bpf`: a closed port returns `ErrClosed` instead of frames left over
  from its last read.
- Tests: mux tests wait until a frame is fully handled; the real Layer 2
  test tolerates scheduler stalls of a CI runner; the AF_PACKET close test
  reads past frames the kernel sends.
- Interop: pinned to profinet-interop v0.1.1 (tshark decodes PNIO RPC on
  any ephemeral port); otfnode asks for a 32-cycle data hold; a failing
  test logs otfnode's report and keeps its captures.
- CI: the Soak workflow (every job N times in parallel, own chaos seeds);
  Intel macOS on `macos-15-intel` (GitHub retired `macos-13`).

---

## v0.1.1

**Date:** 2026-10-26
**Previous release:** v0.1.0

No code change.

- The interop suite (`make interop-images`, the Interop workflow) builds the
  reference images from profinet-interop v0.1.0 instead of its `main`
  branch; the Interop workflow's `interop_ref` input still overrides it.
- Documentation names the released versions (SECURITY.md, this file) and
  links profinet-interop's matrix at v0.1.0.

---

## v0.1.0

**Date:** 2026-10-26

First version.

- IO-Controller (`controller`): DCP resolution and IP assignment, legacy
  (RT_CLASS_1) and advanced (RT_CLASS_2) startup, parameterization, cyclic
  data, alarms, records, reconnect with backoff; `RecordClient` with
  ReadImplicit; device access ARs (`OpenSupervisor`); `Device.AR`.
- IO-Device (`device`): DCP responder, LLDP, Connect/Read/Write/Control/
  Release, IOCAR and IOSAR, several ARs, ModuleDiff, cyclic data with
  watchdogs, alarms and diagnosis, I&M0–4, RealIdentificationData,
  PDPortDataReal and PDRealData.
- DCP, LLDP, CL DCE/RPC, PNIO blocks, RT and RTA codecs and machines.
- GSDML: generated types from an independent catalog, parse, validate,
  encode, model conversion.
- Raw Ethernet backends for Linux, macOS/BSD and Windows; virtual Ethernet
  with fault injection; ARP/IPv4/UDP stack for it.
- Verified against p-net, pnio-rs, profinet-py and pnio-dcp
  ([INTEROPERABILITY.md](INTEROPERABILITY.md)).

---
