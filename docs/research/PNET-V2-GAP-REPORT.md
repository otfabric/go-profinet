# go-profinet against rt-labs p-net v2.0.0: gap report and audit

Baseline: go-profinet `main` at `d976ce9` (three documentation and test
commits after tag **v0.24.0**, `53334fa`; no library code changed between
them). Reference: rt-labs p-net **v2.0.0**, tag `public/v2.0.0`, commit
`67301c5`, its [release notes](https://github.com/rtlabs-com/p-net/releases/tag/public/v2.0.0)
and source. p-net is GPL-3.0: its source was read to learn its behaviour;
no code was taken. Its issue pages are not public (they return 404); for
each issue only the one-line summary in the release notes is known, and
the fix is inferred from the v2.0.0 source.

The interop harness, profinet-interop v0.15.0, still builds p-net
`ea5e554` (2023), not v2.0.0. Nothing in this report was tested against
p-net v2.0.0.

Pull requests (stacked in this order: #131 on #130, #132 on #131, this one on #132; none merged):

| Wave | Pull request | Content |
|---|---|---|
| 1 (P0) | otfabric/go-profinet#130 | LLDP TTL, diagnoses as a set, AlarmSpecifier, README; after review: diagnosis reports in order, undelivered ones owed and retried, alarms of one priority serialized to the AlarmAck |
| 2 (P1) | otfabric/go-profinet#131 | RTA sequence numbers, PDev reset at Connect, RPC fragment byte order; after review: a pending alarm PDU owns its payload, no DATA PDU after a timeout, a refused Connect undoes its PDev reset, DCP Set IP/Parameter of exactly 12 octets |
| 3 (MRP) | otfabric/go-profinet#132 | PDInterfaceMrpDataCheck domain check, written MRP timers, LLDP MRP port status ([PNET-V2-MRP.md](PNET-V2-MRP.md)); after review: a refused MRP adjustment changes nothing, the domain check follows the domain |
| docs | this pull request | this report and [PNET-V2-DESIGN.md](PNET-V2-DESIGN.md) |

Classes: **defect** (confirmed in go-profinet, with a test that fails
before the fix), **difference** (behaviour differs; neither is shown
wrong), **handled** (go-profinet already behaves as p-net v2.0.0 does),
**n/a** (does not apply), **needs specification** (the right behaviour
is not established from public evidence).

## Wave 0: the plan's items

| Item | Class | Code (baseline) | Expected | Observed (baseline) | Resolution |
|---|---|---|---|---|---|
| LLDP TTL of the IO-Device | defect | `device/ports.go` `startLLDP`; `lldp/agent.go` `announcedTTL`, `SendNow` | 21 s with the 5 s interval (p-net `PF_LLDP_TTL`; its release notes cite the PROFINET test specification) | 20 s (`TTLFactor` 4 × 5 s); no way to set a TTL | Wave 1: `lldp.AgentOptions.TTL`; device 21 s at 5 s, other intervals 4 × interval. `TestDeviceLLDPTTL`, `TestAgentExplicitTTL` |
| Repeated `AddDiagnosis` (p-net 2729) | defect | `device/alarms.go` `AddDiagnosis` | an identical active diagnosis adds nothing and sends no alarm; a changed one is updated (p-net `pf_diag_add`, `pf_diag_find_entry`) | every call appended an entry and sent an alarm; 32 concurrent identical adds made 32 entries | Wave 1: `upsertDiagnosis` under `d.mu`. `TestDiagnosisIdempotent`, `TestDiagnosisWithoutAR` |
| Diagnosis AlarmSpecifier (p-net 2727) | defect | `device/alarms.go` `diagAlarm` | ChannelDiagnosis, SubmoduleDiagnosisState, ARDiagnosisState only while a fault is active (p-net `pf_alarm_add_diag_item_to_summary`) | ChannelDiagnosis on every diagnosis alarm, all three on every appearing one: 0xA800 for a maintenance-required diagnosis | Wave 1: `diagSpecifier` over the active set. `TestDiagnosisAlarmSpecifier`, `TestDiagSpecifier` |
| Qualified diagnosis format | defect (found on the way) | `device/alarms.go` `diagAlarm`, `device/records.go` `diagnosis` | a `Qualifier` reaches the controller (USI 0x8003) | USI 0x8000/0x8002 only: the qualifier was dropped | Wave 1: `diagUSI`. `TestDiagnosisRecordQualified` |
| `RemoveDiagnosis` of several items | defect (found by the audit) | `device/alarms.go` `RemoveDiagnosis` | each removed diagnosis reported | all removed, one reported | Wave 1: one alarm per item. `TestDiagnosisIdempotent` |
| ARDiagnosisState scope | needs specification | `device/alarms.go` `diagSpecifier` | the standard may mean every submodule of the AR | the alarm's submodule only, as p-net (its comment cites the standard's summarized specifier, which is not read here; Wireshark names the bit only) | documented (docs/DIAGNOSTICS.md, LIMITATIONS.md); pinned by `TestARDiagnosisStateScope` |
| MaintenanceItem in a diagnosis alarm | difference | `device/alarms.go` `diagAlarm` | p-net puts a MaintenanceItem (USI 0x8100) before the item when its maintenance status is non-zero | the diagnosis item alone | documented; not changed (needs USI 0x8001 framing that is not verified) |
| Remote mismatch alarm type | difference | `device/pdev.go` `evalPort` | p-net sends PortDataChangeNotification for error type 0x8001 | Diagnosis alarm | documented; not changed |
| README comparison | stale | `README.md` | p-net v2.0.0 features | p-net column of v1.0.2 / `ea5e554` | Wave 1: column describes v2.0.0; the interop evidence is named as `ea5e554` |

## Wave 2: audit of the issues fixed in p-net v2.0.0

| Issue | Upstream failure (release notes; fix inferred from source) | go-profinet code path | Reproduces | Impact | Fix | Test |
|---|---|---|---|---|---|---|
| 2732 | non-zero padding after DeviceRole in the Identify response | `dcp/identity.go` `Identity.Blocks` (`{Role, 0}`); `dcp/hello.go` `HelloFrame` | no | — | — | Hello golden `TestHelloFrameGolden` pins it; Identify/Get have no golden |
| 2730 | a refused IODWrite answered with ErrorCode PNIO, not IODWriteRes | `device/serve.go` `serveWrite`, `asStatus`; `device/records.go` `writeRecordData`; `device/pdev.go`, `device/mrp.go` | no: every refusal after a decodable header is an IODWriteRes with ErrorCode 0xDF | residual: a handler's `StatusError` with another ErrorCode passes through; writes are not refused between PrmEnd and ApplicationReady (p-net: StateConflict) | not changed (low; the second needs the state rule established) | — |
| 2729, 2727 | see Wave 0 | | yes | maintenance shown as fault; duplicate alarms | Wave 1 | Wave 1 tests |
| 2728 | port diagnoses of every port reset at every Connect | `device/pdev.go` `pdevState`; `device/ar.go` `connect` | the inverse: never reset. A check, adjustment and remote mismatch diagnosis written by one AR outlived it, were reported in the next AR's ModuleDiffBlock, and a stale NoLLDP boundary kept LLDP off | wrong diagnosis and LLDP state for the next controller | Wave 2: `resetPDev` on the Connect of an IOCAR for the ports it takes (p-net `pf_pdport_reset_ar`); a Connect refused after it undoes it | `TestPDevResetOnConnect`, `TestPDevResetUndoneOnRefusedConnect` |
| 2701 | AlarmAck lost when the transport ACK cannot be sent; later alarms blocked | `alarm/endpoint.go` `handle` (receive), `Send` | not as stated: `handle` ignores the ACK send error and delivers. A related defect does: `Send` used its sequence number before transmitting, and a failed first transmission or a cancelled Send of a lost PDU skipped it; the peer then NACKed every later DATA | **the AR is lost**: the next alarm times out and the device aborts the AR (AbortAlarmSend), the controller fails it on the next AlarmAck | Wave 2: the number is given back when nothing reached the wire; a sent, cancelled PDU stays pending (with its own copy of the payload) and is retransmitted first; after a timeout the endpoint sends no DATA PDU | `TestSendAfterTransmitFailure`, `TestSendAfterCancelledSend`, `TestSendPendingTimeout`, `TestSendPendingOwnsPayload`, `TestSendAfterTimeoutIsTerminal` |
| 2537 | C build fails with `LOG_LEVEL_DEBUG` | `logger.go` | n/a: no conditional compilation; debug paths are compiled and tested | — | — | `logger_test.go` |
| 2503 | IODWriteMultipleReq mishandled | `device/serve.go` `serveWrite` (WriteMultiple) | no crash, no skipped item. Differences: the NDR status is the first failing item's (p-net keeps OK and reports per item); each item's ARUUID is replaced by the outer one (p-net answers ARUUIDUnknown) | a third-party controller might discard the per-item results on a non-OK NDR status | needs specification: no capture of a reference device's answer | existing `records_test.go`, `hostile_test.go` pin today's behaviour |
| 2501 | wrong block in a DCP Hello | `dcp/hello.go` `HelloFrame` | no: the same five blocks in p-net's order | difference: go-profinet sends the IP BlockInfo, p-net 0 (needs specification) | — | `TestHelloFrameGolden` |
| 2499 | wrong size checked for a Set with FullIPSuiteBlock | `dcp/responder.go` `apply` | n/a: Set FullIPSuite is refused (suboption unsupported). Related: Set IP/Parameter accepted more than 12 octets and used the first 12 (p-net requires exactly 12) | low: a Full IP Suite sent with the wrong suboption lost its DNS servers silently | after review: refused unless exactly 12 octets, the three addresses of Wireshark's dissector | `TestResponderSetIPLength` |
| 2275 | byte order confused in fragmented RPC responses | `rpc/frag.go` `packets` (send), `reassembly.add` (receive) | sending: no (one header, one byte order). Receiving: fragments in different byte orders were reassembled and the body read in the last one's order | garbled NDR data instead of an error against a peer with the upstream bug | Wave 2: `reassembly.add` refuses a fragment in another byte order (`ErrMalformed`; the server answers a REJECT) | `TestReassemblyMixedDRep` |

Another observation of the audit, changed after review: alarms of one
priority were not serialised up to the AlarmAck, so two concurrent
`SendAlarm` calls could both be on the wire before the first AlarmAck.
p-net queues them per priority (`pf_alarm.c`, ALPMI W_ALARM); go-profinet
now does the same (`TestAlarmsSerializedToAck`).

## Wave 3: MRP

| Item | Class | Resolution |
|---|---|---|
| PDInterfaceMrpDataCheck refused | gap; the MRP_DomainUUID check is established (Wireshark: bit, error code 0x8004, LLDP TLV; p-net: behaviour) | honoured for the domain check; the MediaRedundancyManager check refused (its diagnoses are not public) |
| MrpManagerParams/MrpClientParams timers ignored | gap; the units are in Wireshark's dissector, corroborated by p-net's default and microchip-ung/mrp's profiles | a written set selects the 200 ms or 500 ms profile; others refused; no recovery time claimed |
| A written manager priority did not reach the running entity | defect (found on the way) | the entity restarts on a priority change |
| LLDP MRP port status kept raw | gap | decoded and sent on the ring ports |

Details, sources and differences from p-net: [PNET-V2-MRP.md](PNET-V2-MRP.md).

## Review of the pull requests

The review of #130 to #133 before v0.25.0 raised the findings below.
Each was investigated against the code; each confirmed defect has a
regression test that fails on the previous implementation (shown by
running it against the files of the previous commit) and passes after
the fix.

| Finding | Class | Resolution | Test |
|---|---|---|---|
| 130-01 diagnosis notification order | defect | a ctx-aware sequencer holds each diagnosis change until its last alarm | `TestDiagnosisOrder`, `TestDiagnosisWaitCancelled` |
| 130-02 ARDiagnosisState aggregation | needs specification | per submodule, as p-net; not changed | `TestARDiagnosisStateScope` |
| 130-03 partial delivery of a replacement | defect | undelivered alarms are owed to the submodule's AR (at most 64) and sent first by its next diagnosis call; dropped when another AR owns it | `TestDiagnosisOwed` |
| alarm serialization (audit) | defect | one alarm per priority in flight up to its AlarmAck | `TestAlarmsSerializedToAck` |
| shared device, S2, API addresses | handled | the alarm owner is computed as for `SendAlarm`; diagnoses are keyed by slot and subslot, unique per device, and the API comes from the model | existing shared-device and S2 tests |
| 131-01 pending PDU aliases the caller's VarPart | defect | the endpoint keeps a copy | `TestSendPendingOwnsPayload` |
| 131-02 endpoint after retry exhaustion | defect | `Send` returns `ErrInvalidState`; the AR is aborted as before | `TestSendAfterTimeoutIsTerminal` |
| 131-03 PDev reset before the Connect completes | defect | a refused Connect undoes the reset | `TestPDevResetUndoneOnRefusedConnect` |
| DCP Set IP/Parameter length (audit) | defect (low) | exactly 12 octets | `TestResponderSetIPLength` |
| 132-01 TSTshortT accepted, not applied | fixed in the second review (R2-132-01) | applied as the manager's additional test | `TestAdditionalTest`, `TestWrittenProfile` |
| 132-02 MRP adjustment not transactional | defect | a write whose entity does not start restores the previous state | `TestMRPAdjustRollback` |
| 132-03 MRP check lifecycle | defect | a domain change drops the check; the check survives a Connect | `TestMRPCheckLifecycle` |
| 132-04 neighbor without the TLV | difference | documented, no change | `TestMRPDomainCheck` |
| 132-05 all-zero timers | difference | documented as an OT Fabric convention; the controller is unchanged | `TestWrittenProfile`, `TestMRPAdjustTimers` |

### Second review

| Finding | Class | Resolution | Test |
|---|---|---|---|
| R2-130-01 a repeated `RemoveDiagnosis` does not retry owed alarms | defect | the repeat sends what is owed and returns its result; `ErrNotFound` once nothing is owed | `TestRemoveDiagnosisRetry` |
| R2-130-02 owed alarms cross AR ownership | defect | the operation's owner, taken before the change, receives its alarms and is the AR they are owed to | `TestOwedDiagnosisStaysWithItsAR` |
| R2-130-03 alarm serialization after cancellation | defect | a delivered alarm stays outstanding until its AlarmAck, the end of the AR or the device's AlarmAck timer, whatever its caller does | `TestAlarmOutstandingAfterCancel` |
| R2-132-01 TSTshortT not applied | fixed | `mrp.Profile.TestShortInterval`: a client's LinkChange makes the manager test at once and again after MRP_TSTshortT, then at MRP_TSTdefaultT (IEC 62439-2:2016 Table 45, ADD_TEST); see [PNET-V2-MRP.md](PNET-V2-MRP.md). Interop: umrp MRP suite passed; the microchip-ung/mrp rings were not applicable on this Docker host | `TestAdditionalTest` (fails before), `TestNoAdditionalTestWithoutShortInterval`, `TestWrittenProfile` |
| R2-132-02 check write during an adjustment's rollback | defect | MRP record writes are serialized by `mrpMu` | `TestMRPCheckDuringAdjustRollback` |
| R2-132-03 failed restoration | defect | without a running entity nothing is advertised, reported as real or checked; the ring ports keep their state | `TestMRPRestoreFails` |

### Third review

| Finding | Class | Resolution | Test |
|---|---|---|---|
| R3-132-01 a LinkUp bypasses ADD_TEST | defect | with MRP_TSTshortT, ADD_TEST alone decides a LinkChange's test, for a LinkUp as for a LinkDown; without it a LinkUp still tests at once | `TestAdditionalTestEvents` (fails before for up-up, down-up, up-down), `TestLinkUpTestWithoutShortInterval` |
| R3-132-01 timer precision | defect | the tests are timed by a timer set to the next test; before, intervals were rounded up to the polling tick (Profile200 30 ms instead of 20, Profile500 60 ms instead of 50, MRP_TSTshortT 20 and 40 ms instead of 10 and 30). Medians within about 1 ms; no real-time bound claimed | `TestProfileTestTiming` (fails before) |
| R3-132-02 the same configuration does not restart a missing entity | defect | an adjustment equal to the stored configuration changes nothing only when its entity runs | `TestMRPRestoreFails` (fails before) |
| R3-132-03 blocked port without an entity | limitation, accepted | the forwarding state is kept, not repaired; configuration restored, entity restarted by a later write | `TestMRPRestoreFailsBlocked` (deterministic, controller outside the ring) |

## Residual observations and their disposition

| Observation | Disposition |
|---|---|
| 2730: a handler's `StatusError` with an ErrorCode other than IODWriteRes passes through | accepted: the handler chose the status; documented here |
| 2730: writes are not refused between PrmEnd and ApplicationReady (p-net answers StateConflict) | needs specification: the state rule is not established from public sources |
| 2503: the NDR status of a WriteMultiple is the first failing item's; each item's ARUUID is replaced by the outer one | needs specification: no capture of a reference device's answer |
| 2501: the Hello carries the IP BlockInfo where p-net sends 0 | needs specification |
| 2732: no golden vector for the Identify and Get responses | follow-up (tests only); the Hello golden pins the DeviceRole padding |
| MaintenanceItem (USI 0x8100) in diagnosis alarms | difference; not changed (the USI 0x8001 framing is not verified) |
| PortDataChangeNotification for remote mismatches | difference; documented in LIMITATIONS.md |
| ARDiagnosisState over the AR | needs specification (see above) |
| a neighbor without the MRP port status is no mismatch | difference; documented in PNET-V2-MRP.md |
| CHK_RC LinkChange rows of IEC 62439-2 Table 45 not read (outside the public preview) | limitation; the manager keeps its own reaction to MRP_LinkDown (ring opened at once), and without MRP_TSTshortT its test round on an MRP_LinkUp |
| the controller writes the manager priority alone | follow-up: writing complete profile timers changes the controller's output and needs interop evidence |
| owed diagnosis alarms are capped at 64 per submodule | limitation; the oldest are dropped and logged, the records stay authoritative |
| an alarm whose transport send was cancelled before its ACK: the next alarm of the priority retransmits it and then goes out without waiting for its AlarmAck | limitation; fixing it needs the outstanding alarm to push its pending PDU itself (an `alarm.Endpoint` addition); the delivered-alarm case is fixed |
| after the device's AlarmAck timer the next alarm of the priority may go before the late AlarmAck | needs specification: what the standard's alarm machine does on that timeout is not read |
| ring port forwarding when no MRP entity runs | accepted degraded mode (third review): kept as the last entity left it, a port held blocked in PT included; a later write restarts the entity |
| nothing here is tested against p-net v2.0.0 | the interop pin stays at profinet-interop v0.15.0 (p-net `ea5e554`) |

## Wave 4: design

See [PNET-V2-DESIGN.md](PNET-V2-DESIGN.md).
