# Testing

go-profinet is tested in tiers, from codecs to independent implementations
on real Layer 2 networks.

| Tier | What | How to run |
|------|------|------------|
| Unit | every codec with golden vectors written from the standard, every state machine, every option and error path | `make test` (race detector) |
| Fuzz | every decoder of network input and of documents (45 targets, among them the receive path of `netstack`, the walk of a BPF read buffer, the MRP manager option and the readers of `internal/capture`): every input is handled without a run-time failure, and what decodes re-encodes to the same value | `make fuzz` (`FUZZTIME`, 30 s each; 10 s in CI; 5 min each in the Extended workflow, every night) |
| End to end | controller and devices on the virtual network: startup modes, records, alarms, diagnosis, ModuleDiff, IP assignment and rename, many devices, recovery, activity timeouts, a second controller, reset, topology | `make test-e2e` |
| Chaos | the same with seeded loss, duplication, corruption, reordering, delay and jitter, a reconnect storm, malformed input of every kind | part of `make test-e2e` (seeds are logged; `vnet.New(seed)` reproduces) |
| Chaos monkey | a controller with three devices under a seeded random sequence of disruptions (fault profiles, link flaps, device restarts, renames, devices removed and re-added, diagnosis storms, record and output traffic); afterwards every AR must be Running with fresh data both ways and working records, and no goroutine may be left (`TestChaosMonkey`; 40 steps, 400 in the extended workflow; `PROFINET_CHAOS_SEED` and `PROFINET_MONKEY_STEPS` reproduce a run). Verified with seeds 7, 42, 99 (200 steps), 1000–1002 | part of `make test-e2e` |
| Lifecycle contracts | rows L1 to L16 of the organisation's contract test matrix, per long-lived type: see [Lifecycle contract tests](#lifecycle-contract-tests) | `make test` |
| Goroutines left behind | the packages that start goroutines (`l2`, `dcp`, `rpc`, `alarm`, `rt`, `lldp`, `mrp`, `snmp`, `netstack`, `device`, `controller`) end their tests through `internal/leakcheck`: tests that pass and leave a goroutine fail, and the report names the goroutine by its function and by what started it (a comparison of stacks, not of counts) | `make test` (`TestMain` of each package) |
| Leaks | three full sessions (an AR with cyclic data, records, an alarm, a device access AR, DCP, LLDP), then everything closed: the goroutine count returns to its baseline (`TestNoLeaks`; it fails with 11 goroutines left when the controller is not closed) | part of `make test-e2e` |
| Real Layer 2 | the AF_PACKET (Linux) and BPF (macOS) backends on a veth or feth pair, and an AR over them | `make l2-test` (Docker), CI jobs `l2-linux`, `l2-macos` |
| Interop | p-net, pnio-rs, profinet-py, pnio-dcp, umrp and Net-SNMP in containers (MRP rings of veth cables), captures decoded by scapy and tshark | `make interop-images && make interop` ([INTEROPERABILITY.md](../INTEROPERABILITY.md)) |
| Kernel MRP | microchip-ung/mrp on the Linux kernel's bridge MRP: a Linux 6.12 built without switchdev (`scripts/mrp-kernel.sh`), one virtme-ng micro-VM with its own Docker daemon per test (`scripts/microvm-interop.sh`), in CI each test on a runner of its own (a matrix, in parallel); amd64 with KVM | the Kernel MRP workflow (`.github/workflows/kernel-mrp.yml`: pull requests, main, weekly) |
| Soak | every CI job (unit with and without the race detector on the oldest and newest Go, coverage, e2e and chaos, every native platform, real Layer 2 on Linux and macOS) and the interop suite, each run N times in parallel (default 25), run N with chaos seed 1000+N; required to be all green before a release | the Soak workflow: Actions tab, or a push to a `soak/…` branch |
| Benchmarks | codecs, cyclic path, mux, vnet | `make bench` ([BENCHMARKS.md](BENCHMARKS.md)) |
| Recorded traffic | frames recorded from a SIMATIC S7-1500 CPU 1515-2 PN (FW V2.9.4, by pnio-rs) and from older Siemens, GE, Cisco, Extreme and HP equipment: every frame decodes as tshark decodes it and re-encodes to its bytes; the device answers the CPU's Connect, MultipleWrite, PrmEnd, ApplicationReady answer, cyclic frames, alarm acknowledgements, ERR-RTA and DCP requests, sent on vnet unchanged, as the recorded IO-Device did, apart from classified differences; every truncation and forced length field of every frame through the decoders; the frames seed the fuzz targets | `make test` (offline, every recording committed; [testdata/captures](../testdata/captures/README.md)) |
| GSDML reference schemas | the licensed copy of the GSDML schemas V1.0 to V2.46: pinned commit, checksums, well-formedness, every DeviceProfile compiles under `xmllint --nonet`; the committed registry and DataType audit match them ([CODEGEN.md](CODEGEN.md#the-reference-schemas)) | `make gsdml-schemas gsdml-schemas-check`; the GSDML schemas workflow (pull requests, weekly; needs `OTFABRIC_READ_TOKEN`) |
| GSDML differential | every fixture, and a local corpus of vendor files (`GSDML_CORPUS`), validated by xmllint against its own schema version, by `gsdml.ValidateBytes`, which must agree on every file, and by `gsdml.Validate` (the catalog's rules), where a difference fails unless `gsdml/testdata/xsd-differential.json` explains it by a known finding (130 vendor files: 114 differ by known findings; `ValidateBytes` agrees on all) | `make gsdml-differential` (build tag `gsdmlxsd`) |
| GSDML corpus run | a local corpus (profinet-interop's `make corpus-fetch` cache, or a directory): every file in a child process with a timeout and a garbage-collector memory target (`GOMEMLIMIT`, not a hard limit), its octets hashed and compared with the index (a changed file is `integrity`, not analysed); per file ParseBytes, CheckBytes (issues by severity and rule), sizes, and `gsdml.AnalyzeModels` per device access point; the files' stages (ok, parse, model, crash), the DAP outcomes, the files by schema version, and the failures ranked by error signature (files, DAPs); and fidelity: the files and DAPs with a submodule of an API other than 0 or a record above 0x7FFF, which a converted model does not carry. Its outcomes are ToModel conversion outcomes, not device support. Results stay local: they name files and quote errors. Not run in CI; no corpus result is claimed yet | `make gsdml-corpus GSDML_CORPUS_CACHE=dir` (or `GSDML_CORPUS=dir`): `gsdml-corpus.json` (`gsdml-corpus-result-v1`), `gsdml-corpus.md` |
| GSDML schema model | the generated validator of `gsdml/typed` against xmllint with no allowlist: the fixtures against all 18 versions, the corpus against its own, 29 hand-written one-rule mutants (`TestMutants`, which also runs without the schemas), 24 seeded mutants per input, the encoding of every valid input; the measured deduplication (`internal/xsdc` `TestReferenceSchemas`); without the schemas, a sample of every generated struct, golden encodings and issue lists, and fuzzing of `ParseNode`, `Validate` and `Decode` | `make gsdml-differential`; `make test`, `make fuzz` |
| Hardware | commercial PLCs, devices and engineering tools | not done: no hardware available; the recorded traffic above is the nearest evidence ([docs/research/INDEPENDENT-CONTROLLER.md](research/INDEPENDENT-CONTROLLER.md) is the manual recipe for CODESYS) |

Coverage is gated at 90 % of the statements of the library packages, and at
85 % of each package (`make coverage-pkg-check`, `COVER_PKG_MIN`)
([COVERAGE.md](COVERAGE.md)).

## Lifecycle contract tests

The long-lived types and the rows of the contract test matrix
(otfabric/engineering, standards/lifecycle.md) that apply to them. A row
names the tests that assert it; "open" is a row that applies and has no
test yet; "n/a" is a row that cannot apply, with the reason.

Long-lived types: `l2.Mux` and `Subscription`, `dcp.Client`,
`dcp.Responder`, `rpc.Client`, `rpc.Server`, `alarm.Manager` and
`Endpoint`, `rt.Dispatcher`, `rt.Scheduler`, `lldp.Agent`, `mrp.Entity`,
`snmp.Agent`, `netstack.Stack` and `PacketConn`, `bridge.Bridge`,
`vnet.Network` and `Endpoint`, the raw ports (`l2/afpacket`, `l2/bpf`,
`l2/npcap`), `device.Device`, `controller.Controller` and `Device`.
`store.Dir` has no `Close`; its callback is in rows L9 and L10.

| Row | Tests | Open | n/a |
|-----|-------|------|-----|
| L1 Close twice | l2 `TestMuxClose`; alarm `TestSendTimeoutCancelClose`, `TestManagerRefsAndRouting`; rt `TestSchedulerClose`, `TestCloseConcurrent`; device `TestConnectResources`, `TestCloseConcurrent`; controller `TestAddDeviceValidation`, `TestCloseConcurrent`; snmp `TestServe`, `TestCloseConcurrent`; lldp `TestAgentsExchange`; netstack `TestStackClose`; vnet `TestEndpointClose`, `TestNetworkClose`; rpc `TestCloseConcurrent`; mrp `TestTimersAfterClose` | bridge (`TestQueueOverflowAndClose` closes twice without asserting the result); afpacket and bpf (implicit in their tests) | npcap: the package has no tests (LIMITATIONS.md) |
| L2 Two goroutines call Close | `TestCloseConcurrent` in l2 (Mux and Subscription), dcp (Client and Responder), rpc (Client and Server), alarm (Manager and Endpoint), rt (Dispatcher and Scheduler), lldp, snmp, l2/bridge, vnet (Network and Endpoint), device, controller (Controller and Device); netstack `TestStackCloseConcurrent` (Stack and PacketConn); mrp `TestTimersAfterClose` | afpacket, bpf (need a privileged interface: `make l2-test`) | npcap: no tests |
| L3 Close during a blocked read | l2 `TestMuxClose`; dcp `TestIdentifyContext`, `TestClientAgainstFakeDevice`; rpc `TestTimeoutAndCancel`; netstack `TestReadDeadlineAndClose`, `TestStackClose`, `TestMuxStopClosesSockets` (the stack stops with its Mux); vnet `TestEndpointClose`; afpacket `TestOwnFramesAndClose`; snmp `TestServe` | `rpc.Server` on a socket it does not own (the deadline path); bpf (a read in progress ends by the kernel's read timeout) | bridge ports: `Close` of a local port is a no-op by design |
| L4 Close during a write | alarm `TestSendTimeoutCancelClose`; rpc `TestClientCloseDuringRetransmit`; netstack `TestStackClose`, `TestResolveLeavesNoWaiter`; vnet `TestCloseWithPendingFaults` | lldp and mrp (their sends take a context; no test closes during one) | types that never block in a write: `rt.Scheduler`, `rt.Dispatcher` |
| L5 Close during handshake | device `TestCloseDuringConnect`, `TestConnectDuringResetToFactory`, `TestAbortDuringConnect`, `TestPeerAbortDuringConnect`; controller `TestStatsWhileConnecting`; e2e `TestCloseDuringPhase` | | types without a handshake: every type but `device.Device` and `controller.Device` |
| L6 Cancel a pending request | rpc `TestTimeoutAndCancel`; dcp `TestIdentifyContext`; alarm `TestSendTimeoutCancelClose`; netstack `TestWriteDeadline`, `TestResolveLeavesNoWaiter` (the waiter of a cancelled resolution is gone and the next one works) | "the slot is free and the next request succeeds" for `rpc.Client` and `dcp.Client` | types without requests |
| L7 Response after the request timed out | rpc `TestClientFack` (packets of another sequence number are ignored); dcp `TestClientQueueFull` | a counter of late responses in `rpc.Client` and `dcp.Client` (they are dropped, not counted) | types without requests |
| L8 Response after reconnect | | controller: no test that an ApplicationReady or alarm of the previous AR cannot complete the new one (the code compares ARUUID and session key) | `rpc` and `dcp`: datagrams, no connection |
| L9 Callback calls a public method | controller `TestCallbacksMayUseTheDevice` (OnState, OnAlarm); device `TestHandlerMayUseTheDevice`; alarm `TestOnDataMaySend`; lldp `TestOnChangeMayUseTheAgent`; mrp `TestOnChangeMayReadStatus`; store `TestOnRecoverMayUseTheDir`; snmp (the provider of the test MIB reads the agent's statistics in every agent test, `newTestAgent`) | `l2.Matcher`, `rt.Consumer.OnUpdate`, `alarm.EndpointConfig.OnErr` | types without callbacks |
| L10 Callback calls Close | store `TestOnRecoverMayUseTheDir` (`Dir` has no Close; the callback saves and deletes) | | every other callback: its documentation forbids calling `Close` from it, because `Close` waits for the goroutine the callback runs on (`controller.DeviceConfig`, `device.Handler`, `alarm.EndpointConfig`, `lldp.AgentOptions`, `mrp.Config`) |
| L11 Slow callback | alarm `TestSlowApplication` | `l2.Matcher`, `rt.Consumer.OnUpdate`, lldp and mrp `OnChange` (documented "must not block", run on the receiving goroutine) | types without callbacks |
| L12 Queue at capacity | l2 `TestOverflow`; dcp `TestClientQueueFull`, `TestLimiter`, `TestRateLimits`, `TestIdentifyBound`; rpc `TestNewerCallOnExecutingActivityRefused`, `TestServerBusy`; snmp `TestRateLimit`; netstack `TestReceiveFiltering`, `TestARPTableBound`; bridge `TestFDBBoundAndFlush`; vnet `TestQueueOverflow`; lldp `TestAgentTable` | the subscription queue of `mrp.Entity` (counted in the Mux only); a counter for a full LLDP neighbor table and for a full ARP table | |
| L13 Concurrent sessions | device `TestConcurrentConnects`, `TestTwoConnectsBothAccepted`, `TestConcurrentConnectsOneARUUID`, `TestApplicationReadyFaultFromOtherController`; rpc `TestConcurrentCalls`; e2e (shared device) | | single-session types |
| L14 Failure during construction | device `TestNewFailureReleases` and controller `TestNewFailureReleases` (each socket failing in turn: the sockets opened before are closed, no goroutine left); dcp `TestNewClientClosedMux`; alarm `TestNewManagerClosedMux`; rt `TestDispatcherClosedMux`; lldp `TestNewAgentErrors`; store `TestDirOpen`, `TestDirFailures`; mrp `TestTestIntervalValidated` | afpacket and bpf: the descriptor after a failed bind; `mrp.New` when the second Subscribe fails | |
| L15 Shutdown(ctx) | | | no type has a graceful shutdown; `controller.Close` releases every AR first, bounded by `RPCTimeout` |
| L16 Close racing reconnect | e2e `TestCloseDuringPhase` (Close in every phase of the controller's lifecycle); mrp `TestTimersAfterClose`, `TestCloseWaitsForTimerCallback` (no timer acts after Close) | controller: Close during the backoff wait with a pending Hello wake-up | types without reconnect or timers |

## The virtual network

`vnet` is an in-memory Ethernet switch: endpoints are `l2.Port`s, frames are
delivered to the destination (or flooded), and each endpoint can inject
faults with a seeded random source:

```go
n := vnet.New(42)
ep, _ := n.NewEndpoint(vnet.EndpointConfig{MAC: mac})
ep.SetFaults(vnet.Faults{Loss: 0.05, Duplicate: 0.01, Corrupt: 0.01, Reorder: 0.02, Delay: time.Millisecond, Jitter: 2 * time.Millisecond,
	Match: func(f []byte) bool { return true }})
ep.SetLinkUp(false)
n.AddTap(func(from *vnet.Endpoint, frame []byte) { /* observe */ })
```

`netstack` gives an endpoint ARP, IPv4 and UDP, so that DCE/RPC runs in
Ethernet frames on the same switch, and a test exercises the whole stack in
one process without privileges.

## Writing tests

- Prefer a test against a go-profinet peer on vnet over a mock: the e2e and
  the controller and device harnesses show how.
- A protocol fact learned from a reference implementation gets an interop
  check and a comment naming the implementation.
- Tests that depend on timing wait with deadlines (`eventually`), not
  sleeps, and stay correct under the race detector on a loaded machine.

## When CI runs

| Event | Workflow | What runs |
|---|---|---|
| Pull request | `pr.yml` | `docs.yml` (docsync, otfabric-policy) always; `ci.yml`, `interop.yml`, `kernel-mrp.yml` unless every changed file is documentation; then `OT Fabric Required Checks` |
| Push to a branch | none | nothing; dispatch `ci.yml`, `interop.yml`, `kernel-mrp.yml` to test a branch without a pull request |
| Push to `main` | `docs.yml` | the light checks; the merged commit was tested as the pull request's merge commit |
| Schedule | `extended.yml`, `interop.yml`, `kernel-mrp.yml` | the scheduled runs, on `main` |
| Release | `release.yml` | the release workflow tests the release commit again |

Documentation means an added or modified `*.md` file outside `.github/`, `testdata/` and `vendor/`, other than `AGENTS.md` and `CLAUDE.md`, and other than `gsdml/SCHEMA.md` and `docs/GSDML-TYPED.md`, which code reads or generates. A deleted or renamed document, or any other file, runs everything. `OT Fabric Required Checks` fails unless every job that applies succeeded; its summary lists them.
