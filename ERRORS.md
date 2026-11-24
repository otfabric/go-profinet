# Errors

Every error go-profinet returns can be classified with `errors.Is` against
the sentinels of the root package. Typed errors carry the protocol's codes
and match the sentinels too, so that code can branch on the class and still
report the exact status.

## Sentinels

| Sentinel | Meaning |
|----------|---------|
| `profinet.ErrClosed` | the controller, device, client, port or mux was closed by the application |
| `profinet.ErrTimeout` | the peer did not answer in time: a DCP request, an RPC call, an alarm acknowledgement, a consumer watchdog |
| `profinet.ErrMalformed` | input that cannot be decoded: a truncated or inconsistent frame, block or document |
| `profinet.ErrUnsupported` | a service, option, block type or feature that is not provided (here or by the peer) |
| `profinet.ErrInvalidConfig` | a configuration value a component cannot be built or used with |
| `profinet.ErrInvalidState` | an operation the current state does not allow (a record write without an established AR, an alarm without an AR in Data) |
| `profinet.ErrRejected` | the peer answered with an error: a PNIO status other than OK, a DCP block error, an RPC fault or reject |
| `profinet.ErrAborted` | an AR ended without a release: watchdog, abort by the peer, CM activity timeout, link down |
| `profinet.ErrNotFound` | no device answered for a station name or MAC address, or a slot, subslot, index or AR does not exist |
| `profinet.ErrResourceExhausted` | a bounded resource is used up: ARs, queues, reassembly buffers, concurrent requests |
| `profinet.ErrLinkDown` | the Ethernet link of a port is down |

Errors returned by a context (`context.Canceled`, `context.DeadlineExceeded`)
are wrapped as they are; a call bounded by its own default timeout returns an
error that matches both `ErrTimeout` and `context.DeadlineExceeded`.

## Retries and backoff

What the library retries by itself, and what it leaves to the application:

| Where | Retry | Configured by |
|-------|-------|---------------|
| DCP Get and Set | retransmitted with the same transaction ID (default 3 times) | `dcp.ClientOptions.Retries` |
| DCE/RPC calls | retransmitted every second until the call's deadline (a fragment that is missing is asked for again); a device executes a call at most once and answers a retransmission from its response cache | `rpc.ClientOptions.Retransmit`, `controller.DeviceConfig.RPCTimeout`, the context |
| Alarms (RTA) | retransmitted until acknowledged, then the AR is aborted. A DATA PDU that never reached the wire gives its sequence number back; one whose `Send` was cancelled after it was sent stays pending and is retransmitted before the next one (since v0.25.0). After one is not acknowledged, `Send` returns `ErrInvalidState`: the AR is to be aborted | `RTATimeoutFactor`, `RTARetries` |
| Controller lifecycle | a failed AR or connection attempt is retried for ever with an exponential backoff and optional jitter; a DCP Hello of the device skips the wait | `ReconnectMin`, `ReconnectMax`, `ReconnectJitter`; `Metrics.OnRetry` |
| Record calls (`ReadRecord`, …) | not retried: a write must not be repeated behind the application's back | the application |

## Typed errors

| Type | Carries | Matches |
|------|---------|---------|
| `*pnio.StatusError` | `Op` and the PNIOStatus (`Code`, `Decode`, `Code1`, `Code2`), as a peer answered it or a decoder produced it | `ErrRejected`; by class `ErrNotFound` (invalid index, slot or area; unknown AR UUID), `ErrUnsupported` (feature not supported, unknown blocks), `ErrInvalidState` (state conflicts), `ErrResourceExhausted` (out of ARs, busy), `ErrAborted` (RTA aborts), `ErrMalformed` (a faulty block field in a Connect or Control) |
| `*dcp.SetError` | the MAC, the block and the `BlockError` of a DCP Set or Get the device refused | `ErrRejected`; `ErrUnsupported` for option and suboption unsupported |
| `*rpc.Fault` | a DCE/RPC FAULT or REJECT and its status | `ErrRejected`; `ErrUnsupported` (unknown interface, operation out of range, unsupported type), `ErrResourceExhausted` (server too busy) |
| `*snmp.VersionError` | the `Version` of an SNMP message that is neither v1 nor v2c (SNMPv3 is 3) | `ErrUnsupported` |
| `*gsdml.ValidationError` | the `Issues` of a document (path, message, rule, and for the schema rule the line) | `ErrMalformed` |
| `*typed.SchemaError` | the schema `Version` and the `Issues` (line, path, message; at most 1000, `Truncated` beyond) of a document that is not valid against the GSDML schema of that version (`gsdml/typed`) | `ErrMalformed` |
| `dcp.ErrInvalidName` | a station name that is not a valid NameOfStation | `ErrInvalidConfig` |

Three sentinels belong to a single package:

| Sentinel | Meaning | Matches |
|----------|---------|---------|
| `typed.ErrUnknownVersion` | a GSDML document names no schema version in `xsi:schemaLocation`, or one `gsdml/typed` has no schema for (V2.5x); or a version given to `ValidateVersion`, `ParseVersion` or `DecodeVersion` is not one of `typed.Versions()` | nothing else |
| `ethernet.ErrInvalidMAC` | `ethernet.ParseMAC` was given text that is not six octets separated by `:` or `-`, or twelve hexadecimal digits | nothing else: it is returned for an argument, not for a frame |
| `store.ErrCorrupt` | a persisted value failed its integrity check (short or bad header, wrong length, trailing data, checksum) and no intact previous version remains to fall back on | `ErrMalformed` |

```go
_, err := d.ReadRecord(ctx, controller.Record{Slot: 1, Subslot: 1, Index: 0x10}, 0)
var se *pnio.StatusError
switch {
case errors.As(err, &se) && errors.Is(err, profinet.ErrNotFound):
	log.Printf("no such record: %s", se.Status) // DE 80 B0 00 (read: access: invalid index)
case errors.Is(err, profinet.ErrTimeout):
	log.Print("no answer")
case errors.Is(err, profinet.ErrInvalidState):
	log.Print("the AR is not established")
}
```

## PNIO status

A `pnio.Status` prints as its four octets and their meaning, for example
`DE 80 B0 00 (read: access: invalid index)`. `Status.OK()` is true for
`00 00 00 00`. `pnio.RWStatus`, `pnio.ServiceStatus` and `pnio.AbortStatus`
build statuses; an IO-Device application answers a record request with
`device.RecordError(code1, code2)`.

The device answers a faulty Connect with the status of the faulty field:
ErrorCode 0xDB (connect), ErrorDecode 0x81 (PNIO), ErrorCode1 the block
(0x01 ARBlockReq, 0x02 IOCRBlockReq, 0x03 ExpectedSubmoduleBlockReq,
0x04 AlarmCRBlockReq, …) and ErrorCode2 the field, as IEC 61158-6-10 lists
them; a CMRPC error (ErrorCode1 0x40) for request-level faults (missing
IOCR, out of ARs, unknown AR UUID, state conflict).

A record request is addressed by (API, slot, subslot). The device answers
a slot or subslot it does not have with "invalid slot/subslot"
(`pnio.RWInvalidSlot`), and an API that is not the addressed submodule's
(index below 0xC000) or that no submodule of the device has (the other
indices) with "invalid area/API" (`pnio.RWInvalidArea`).

## Network input

Malformed frames and datagrams never return an error to the application and
never panic: they are dropped and counted (`Malformed` in the stats of the
mux, the DCP client and responder, the LLDP agent, the alarm manager, the RPC
server, the RT consumers). A decoder used directly returns an error matching
`ErrMalformed`; a block of a version it does not decode, `ErrUnsupported`
(`pnio.DecodeAPIData` takes APIData 1.0 only). A device that does not
support APIData answers `RecordClient.ReadAPIData` with a
`*pnio.StatusError`, which matches `ErrNotFound` for an invalid index.

## Values that do not fit their field

A value that does not fit the wire field it goes into is an error, never a
wrapped or truncated field. Checked when a component is built
(`ErrInvalidConfig`): `Config.RPCPort` and `ControllerRPCPort` outside
0..65535, MRP ring ports outside the device's ports, a controller station
name or MRP domain name above 240 octets, an MRP `TopChangeInterval` or
`LinkInterval` that is negative or 65.535 s or more, a model too large for
its 16-bit blocks (`model.Validate`). Checked per call (`ErrInvalidConfig`):
an RPC body that needs more than 65 536 fragments (and, received, a
fragment in another byte order than the first: `ErrMalformed`, answered
with a REJECT), an alarm VarPart above
65 535 octets, `device.SendAlarm` data that does not fit the notification,
a netstack `WriteTo` port outside 0..65535 or a datagram above 65 535
octets, a pcap frame time before 1970 or after 2106. A record the device
cannot encode (for example a diagnosis record grown past 64 KiB by
`AddDiagnosis`) is answered with an RW read error, not served truncated.

From a peer (`ErrMalformed` or a PNIO status): a record length above the
octets present, compared without narrowing so 32-bit platforms reject
0xFFFFFFFF too; a controller Read whose RecordDataLength exceeds `maxLen`;
a WriteMultiple answer with more results than items; a pcap record
whose sub-second field is a second or more; a Connect whose
ModuleDiffBlock could not be encoded in 16 bits (Connect, CMRPC out of
memory: `ErrResourceExhausted`).

Fixed bounds (`ErrResourceExhausted` unless said): a GSDML document of
more than 1 048 576 elements (`gsdml.Parse`); parameter records of more than
16 MiB in the modules as plugged (`gsdml.ToModel`); a `[Content_Types].xml`
with more elements than the package model may have (`gsdx.Open`); a DCP
Identify without `Max` that more than 4096 stations answer (the 4096 are
returned with the error); an OctetString_S of more than 65535 octets
(`pnio.DecodeOctetStringS`; one that is no multiple of three is
`ErrMalformed`). As `ErrInvalidConfig`: a value or status out of range for
the embedded-status encoders of `pnio` (`EncodeUnsigned8S`, …); a model with more than
1440 plugged submodules or more than 16 MiB of parameter records in them
(`model.Validate`, and so `device.New` and `Controller.AddDevice`); an MRP
profile with a negative `TestInterval`. As `ErrMalformed`: a GSDML module
or device access point with more than 1440 submodules, or a Const
ByteOffset, Ref BitOffset or submodule `API` that is no number, or a VisibleString,
UTF8String or OctetString Ref default longer than its Length
(`gsdml.ToModel`, and `gsdml.SubmoduleParameters` for the same records),
a nil document or one without device access points (`gsdml.AnalyzeModels`; the
errors of its DAPs and modules are in its reports, as `ToModel` returns
them),
and any value for a parameter field whose ValueItemTarget names no
ValueItem of the file (`gsdml.ParameterRecord.Set`); a
value that does not fit its parameter field, is outside its AllowedValues or,
without them, is not a value of its ValueItem
(`gsdml.ParameterRecord.Set` and `Encode`, `ErrInvalidConfig`, the record
left as it was), as well as a field whose Length or bits do not agree with
its DataType, a Go value of another kind than the field's DataType (the
typed setters), two values for fields that share bits, a Const outside
the record or a field Default that does not encode (`Encode`), or a name
two fields share or an ID two fields have (`ParameterRecord.Field`); a
field without a verified encoding or an AllowedValues on a string field,
with more than 4096 values, a mantissa of more than 128 digits or an
exponent beyond ±400 (`ErrUnsupported`); an AllowedValues that is not a
SignedOrFloatValueListT (`ErrMalformed`); for `gsdml.PlanParameters`, a
model that does not match the file (another VendorID or DeviceID, another DAP, a module the DAP cannot
use or in a slot outside `PhysicalSlots` or its `AllowedInSlots`, a
submodule the file does not have), values for a record no plugged
submodule has, two sets for one record, a value for a field that is not
Changeable or two for one field (its ID and its name), a written record with a `Check`
error, or TransferSequence values of a submodule that are not 1 to N
(`ErrInvalidConfig`, with the plan's issues and no writes), a DAP the file
does not have (`ErrNotFound`), and values for a record with a field
without a verified encoding (`ErrUnsupported`); for a configurable
submodule, a subslot its reference does not allow or outside the
`PhysicalSubslots` of its DAP or module (`ErrInvalidConfig`); a
PDRealData record whose MultipleBlockHeaders nest more than four deep. The same element, size and depth bounds (256 levels) apply to
`typed.ParseNode` and everything in `gsdml/typed` that reads a document;
there, a document that is not namespace-well-formed (an undeclared
prefix, `xmlns:p=""`, a reserved prefix rebound, two attributes with one
expanded name) is `ErrMalformed`; `Decode` returns a decoding error that
matches `ErrMalformed` for an element or attribute the schema of the
version does not have, or a value that does not parse as its type. As
`ErrUnsupported`: a GSDML document whose document type declaration has an
internal subset (`gsdml.Parse`, `typed.ParseNode`; docs/GSDML.md); a Ref whose DataType has no
verified DefaultValue encoding, such as `Integer16_S`, `TimeStamp` or `N2`
(`gsdml.ToModel`; docs/GSDML.md); a DAP submodule of an API other than 0
(`gsdml.ToModel`; `model.Validate` refuses one as `ErrInvalidConfig`); a
configurable submodule (`UseableSubmodules`) the DAP or a module it plugs
puts in a subslot, `FixedInSubslots` or `UsedInSubslots`
(`gsdml.ToModel`; a module only in the catalog is left out of `Catalog`
instead, and `gsdml.AnalyzeModels` reports both; `gsdml.Configuration`
resolves them, below); a parameter record of no octets in a
model generated for PNIO_Version V2.31 or later, whose schemas require a
`Const` or a `Ref` in every `ParameterRecordDataItem` (`gsdml.FromModel`).

The block encoders of `pnio` and `alarm.PDU.AppendPayload` return no error;
given a length that does not fit, they panic with an error matching
`ErrInvalidConfig`, as `strings.Repeat` does on misuse. controller and
device validate or recover at their boundary, so neither configuration nor
a peer reaches that panic through them.

## GSDML configurations

`gsdml.NewConfiguration` returns what `ToModel` returns for the DAP and the
modules it can use (`ErrNotFound` for a DAP the document does not have,
`ErrMalformed`, `ErrResourceExhausted` beyond `MaxModelRecordOctets`),
except that configurable submodules are not refused; a reference whose
subslot attributes are not ValueLists of subslot numbers 1 to 65535, a
`PhysicalSubslots` that is not a ValueList, more than 1440 references in
one `UseableSubmodules`, and configurable submodules of the DAP whose
default subslots collide with each other or with its built-in submodules
or lie outside its `PhysicalSubslots` are `ErrMalformed`. A configurable
submodule that cannot be placed is not an error there: its
`SubmoduleOption.Err` says why (`ErrMalformed` for a reference to no item
of the `SubmoduleList` or an item that does not convert, `ErrUnsupported`
for a DAP submodule of an API other than 0 or a port of a module); so is a
module whose configurable submodules collide by default
(`ModuleOption.Err`, `ErrMalformed`; `ErrResourceExhausted` for more than
65536 default subslots).

Choices that do not fit the file are `ErrInvalidConfig` and leave the
configuration unchanged: `Plug` and `PlugIdent` for a module the DAP cannot
use, two modules of one ident (by ident), slot 0 or above 0x7FFF, a slot
outside `PhysicalSlots` or the reference's `AllowedInSlots`, a slot whose
module is fixed there, and a module with a `ModuleOption.Err` (which the
error also matches); `Unplug` for an empty slot or a fixed module; `Select`
and `ClearSubslot` for a slot without a module, a submodule the module's
`UseableSubmodules` do not name, subslot 0, the subslot of a virtual or
system-defined submodule, a subslot another submodule is fixed in, a
subslot the submodule is not allowed in or outside the `PhysicalSubslots`;
`Select` returns the option's `Err` for one that cannot be placed.
`Resolve` returns `ErrInvalidConfig` for a subslot several submodules are
used in by default that no `Select` or `ClearSubslot` decided (naming
them), for a module or DAP with more than 1440 submodules and for a model
`model.Device.Validate` refuses; the `Err` of a placed submodule or of a
plugged module; and `ErrResourceExhausted` for parameter records of more
than 16 MiB in the plugged submodules.

## Device diagnoses

`device.AddDiagnosis` and `RemoveDiagnosis` change the recorded state
first and report it second; an error describes the report, not the state:

| Call | Returns |
|---|---|
| `AddDiagnosis`, unknown slot or subslot | `ErrNotFound`; nothing recorded |
| `AddDiagnosis`, new or changed diagnosis, no AR in Data owns the submodule | `ErrInvalidState`; the diagnosis is recorded |
| `AddDiagnosis`, diagnosis identical to an active one | nil, with or without an AR; nothing sent (since v0.25.0) |
| `AddDiagnosis`, changed diagnosis whose first or second alarm fails | that error; the new value is recorded, and the alarms not delivered are owed: the next diagnosis call of the submodule (an identical `AddDiagnosis` included) sends them first |
| either, ctx ends while another diagnosis call reports | `ctx.Err()`; nothing recorded |
| `RemoveDiagnosis`, no diagnosis of that channel and error type | `ErrNotFound`; when alarms of the submodule are owed to its owner (a removal repeated after its alarm failed), they are sent and their result is returned instead |
| `RemoveDiagnosis`, an alarm fails | that error; the diagnoses are removed, the alarms not delivered are owed as above |
| either, alarm not acknowledged | `ErrTimeout`, `ErrAborted` or a `*pnio.StatusError` as for `SendAlarm` |
| `SendAlarm` or a diagnosis call, ctx ends after the alarm was delivered | `ctx.Err()`; the alarm stays outstanding: the next alarm of its priority waits for its AlarmAck, the end of the AR or the AlarmAck timer |
| `SendAlarm` or a diagnosis call, no AlarmAck within the AlarmAck timer | `ErrTimeout`; the alarm is taken as lost and the priority is free |

## Lifecycle errors

A netstack socket that is closed, by its `Close`, by `Stack.Close` or
because the stack stopped with its Mux, returns `net.ErrClosed` from
`ReadFrom` and an error matching both `net.ErrClosed` and
`profinet.ErrClosed` from `WriteTo`.

`controller.Device.State` returns the error of the last failure of the
lifecycle (resolve, Connect, parameterization, ApplicationReady, abort) next
to the state; `OnState(from, to, err)` reports it as it happens. The
controller retries after the backoff; the error is not fatal to the
controller.
