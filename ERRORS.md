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
| Alarms (RTA) | retransmitted until acknowledged, then the AR is aborted | `RTATimeoutFactor`, `RTARetries` |
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

## Network input

Malformed frames and datagrams never return an error to the application and
never panic: they are dropped and counted (`Malformed` in the stats of the
mux, the DCP client and responder, the LLDP agent, the alarm manager, the RPC
server, the RT consumers). A decoder used directly returns an error matching
`ErrMalformed`.

## Values that do not fit their field

A value that does not fit the wire field it goes into is an error, never a
wrapped or truncated field. Checked when a component is built
(`ErrInvalidConfig`): `Config.RPCPort` and `ControllerRPCPort` outside
0..65535, MRP ring ports outside the device's ports, a controller station
name or MRP domain name above 240 octets, an MRP `TopChangeInterval` or
`LinkInterval` that is negative or 65.535 s or more, a model too large for
its 16-bit blocks (`model.Validate`). Checked per call (`ErrInvalidConfig`):
an RPC body that needs more than 65 536 fragments, an alarm VarPart above
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
ByteOffset or Ref BitOffset that is no number, or a VisibleString,
UTF8String or OctetString Ref default longer than its Length
(`gsdml.ToModel`); a
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
(`gsdml.ToModel`; docs/GSDML.md).

The block encoders of `pnio` and `alarm.PDU.AppendPayload` return no error;
given a length that does not fit, they panic with an error matching
`ErrInvalidConfig`, as `strings.Repeat` does on misuse. controller and
device validate or recover at their boundary, so neither configuration nor
a peer reaches that panic through them.

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
