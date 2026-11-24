# Diagnosis, identification and topology records

## Diagnosis

A device records diagnoses per submodule and channel and reports each with a
diagnosis alarm:

```go
err := dev.AddDiagnosis(ctx, 1, 1, pnio.DiagnosisItem{
	ChannelNumber: 3, ChannelProperties: pnio.ChanDirInput | pnio.ChanMaintRequired,
	ErrorType: pnio.ChannelErrLineBreak,
})
err = dev.RemoveDiagnosis(ctx, 1, 1, 3, pnio.ChannelErrLineBreak) // "disappears" alarm
```

`AddDiagnosis` records the diagnosis even when no AR can receive the alarm
(it then returns an error matching `profinet.ErrInvalidState`).

The active diagnoses of a submodule are a set (since v0.25.0; before,
every call added an entry and an alarm). A diagnosis is identified by its
channel number, channel group (Accumulative), direction, error type and
extended error type, as p-net v2.0.0 identifies it (`pf_diag_find_entry`):

| `AddDiagnosis` of a diagnosis that is | State | Alarms | Returns |
|---|---|---|---|
| not active | added | one: appears | nil, or `ErrInvalidState` without an AR |
| identical to an active one (also its other ChannelProperties bits, `ExtAddValue`, `Qualifier`) | unchanged | none | nil, with or without an AR |
| active with other values (for example another maintenance severity) | replaced | two: the new one appears, the old one disappears with others remaining | nil, or `ErrInvalidState` without an AR |

The check and the update are one step under the device's lock, so
concurrent identical calls record one diagnosis and send one alarm; the
device's lock is not held while an alarm waits for its acknowledgement.

Diagnosis operations (`AddDiagnosis`, `RemoveDiagnosis`) are reported in
the order they change the state: each waits, at most until its context
ends, for the previous operation's alarms to be acknowledged (before
v0.25.0 a removal's alarm could overtake the addition it undid). Alarms of
one priority go out one at a time, each after the controller's AlarmAck
of the previous, as p-net's alarm machine sends them. An alarm stays
outstanding until its AlarmAck, the end of the AR or the device's
AlarmAck timer (`AlarmAckTimeout` at least), also when its caller stops
waiting earlier: the next alarm of the priority waits, within its own
context, for one of those. The transport acknowledgement does not end
an alarm. After the timer the device takes the alarm as lost and the
next one may go; what a controller does with it then is not
established.

An alarm that is not delivered without the AR ending (no AlarmAck, a
refused AlarmAck, the context ending) is owed: the next diagnosis
operation of the submodule, an identical `AddDiagnosis` included, sends
the owed alarms first, so repeating the call retries the report; a
repeated `RemoveDiagnosis` whose diagnoses are already removed sends
what is owed and returns its result instead of `ErrNotFound`. The
alarms of an operation go to the AR that owned the submodule when the
operation changed the state, and are owed to that AR: when the
submodule moves to another AR (or another primary of an AR set), they
are dropped, as the new owner reads the diagnoses in its ModuleDiffBlock
and records. A transport timeout aborts the AR. At most 64 alarms are owed per
submodule; older ones are dropped and logged.

The AlarmSpecifier of a diagnosis alarm summarises the submodule's active
diagnoses after the change: ChannelDiagnosis, SubmoduleDiagnosisState and
ARDiagnosisState are set when one of them is a fault, none of them when
only maintenance diagnoses (or none) remain. A fault is a diagnosis with
Maintenance 0, or `ChanMaintQualified` with a bit of 0xF8000000 in its
`Qualifier`. This is p-net v2.0.0's summary (`pf_alarm_get_diag_summary`),
which fixed its issue 2727; before v0.25.0 go-profinet set
ChannelDiagnosis on every diagnosis alarm. Open points, not established
from a specification:

- ARDiagnosisState summarises the alarm's submodule only, as in p-net
  (whose comment names the standard's BuildSummarizedAlarmSpecifier,
  computed per subslot); the standard may mean every submodule of the AR.
  So when the fault of one submodule disappears while another submodule of
  the AR keeps one, the alarm carries no ARDiagnosisState
  (`TestARDiagnosisStateScope`). Wireshark names the bit but gives no
  rule.
- p-net puts a MaintenanceItem (USI 0x8100) before the diagnosis item of a
  maintenance alarm; go-profinet sends the diagnosis item alone.

`RemoveDiagnosis(ctx, slot, subslot, channel, errorType)` removes every
diagnosis of the channel with the error type (all directions and extended
error types) and sends one alarm for each: "disappears, others remain"
while another diagnosis remains, and "disappears" in a DiagnosisDisappears
alarm for the last one.

A diagnosis with a `Qualifier` or the qualified severity is sent and served
in the QualifiedChannelDiagnosis format (USI 0x8003; before v0.25.0 the
qualifier was dropped).

A controller
reads the diagnosis with `Device.ReadDiagnosis` (index 0xF80C of the device)
or `RecordClient.ReadDiagnosis` for one submodule (0x800C), and receives the
alarms in `OnAlarm`.

| ChannelProperties | Values |
|-------------------|--------|
| Direction (bits 13–14) | `ChanDirManufacturer`, `ChanDirInput`, `ChanDirOutput`, `ChanDirInOut` |
| Maintenance (bits 9–10) | fault (0), `ChanMaintRequired`, `ChanMaintDemanded`, `ChanMaintQualified` |
| Specifier (bits 11–12) | appears, disappears (set by the device), all disappear, other remain |
| Accumulative (bit 8) | `ChanAccumulative` |

Error types 0x0001–0x0009 and 0x8000/0x8001 are the standard channel errors
(`pnio.ChannelErrShortCircuit` … `pnio.ChannelErrError`, `pnio.ChannelErrDataTransmiss`, `pnio.ChannelErrRemoteMismat`); 0x0100–0x7FFF are manufacturer specific.
The device serves every diagnosis index of the standard by scope (subslot,
slot, AR, API, device) and kind (channel, all, maintenance and qualified).

## Identification and maintenance (I&M)

| Record | Index | Contents |
|--------|-------|----------|
| I&M0 | 0xAFF0 | vendor, order ID, serial number, hardware and software revision, revision counter, profile, supported I&M records (from the model) |
| I&M1 | 0xAFF1 | tag function (32), tag location (22) |
| I&M2 | 0xAFF2 | installation date (`YYYY-MM-DD HH:MM`) |
| I&M3 | 0xAFF3 | descriptor (54) |
| I&M4 | 0xAFF4 | signature (54 octets) |
| I&M0FilterData | 0xF840 | the submodules that carry I&M data |

I&M1–4 of the DAP are writable; the device keeps them in memory
(`device.Config.IM` gives initial values). `controller.Device.ReadIM0` and
the `pnio.IM*` codecs read them.

## Identification of the configuration

RealIdentificationData (0xF000 for the API, 0xC001 per slot, 0x8001 per
subslot) lists the modules and submodules the device has;
ExpectedIdentificationData (0xE000…) what an AR expects; ModuleDiff (0xE002)
their differences.

## Topology

PDPortDataReal (0x802A of a port submodule) reports a port's own port ID,
its neighbors (port ID, chassis ID = station name, line delay, MAC), MAU
type, link state and media type; PDRealData (0xF841) the PDPortDataReal of
every port in MultipleBlockHeaders. go-profinet decodes both
(`pnio.DecodePDPortDataReal`, `pnio.DecodePDRealData`, verified against
p-net) and the IO-Device serves them from its LLDP agent, with its
interface's PDInterfaceDataReal (name of station, MAC, IP suite) in
PDRealData. profinetctl's `topology` reads PDRealData of every device and
derives the links; `Controller.CheckTopology` compares it with an expected
topology (links between station ports) and reports each link as ok,
missing, wrong peer, unknown port or unverified, plus the unexpected
links and the unreachable stations.

## Port checks and adjustments (PDev)

A controller writes what a port should see and how it should behave; the
device watches it.

| Record (index) | Device | Controller |
|----------------|--------|------------|
| PDPortDataCheck (0x802B) with CheckPeers | evaluated against the LLDP neighbor heard last, after the write and whenever the neighbors or the link change | `DeviceConfig.PeerChecks`, written before PrmEnd |
| PDPortDataAdjust (0x802F): PeerToPeerBoundary bit 0, DCPBoundary bit 1 | LLDP transmission off; DCP Hello off | encode with `pnio.PDPortDataAdjust` |
| PDInterfaceAdjust (0x8071) | MultipleInterfaceMode bit 0: LLDP port ID "port-001.<name>" (the default) or "port-001" | |
| PDExpectedData (0xF842) | the written checks and adjustments | |

A port whose neighbor differs from its CheckPeers reports channel diagnosis
0x8001 "remote mismatch" on channel 0x8000 of the port submodule, with the
extended type 0x8000 (another name of station), 0x8001 (another port) or
0x8005 (no neighbor), the codes p-net reports; each appears and disappears
with an alarm. A neighbor in the standard LLDP mode sends "port-001.<its
name>", which matches the expected port "port-001". Records the device does
not implement are refused, never stored: MAU type, link state and line
delay checks, domain, multicast, MAU, link-state and preamble adjustments,
with PNIORW 0xA9 "feature not supported" (p-net answers 0xB4; the code PI
specifies is not public). The checks and adjustments last as long as the
device runs (no persistence; a controller writes them at every Connect).
Evidence: [research/EVIDENCE-DCP-PDEV.md](research/EVIDENCE-DCP-PDEV.md).
