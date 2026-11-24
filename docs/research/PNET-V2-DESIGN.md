# Features p-net v2.0.0 has and go-profinet does not: ranked design

This is a design note. It adds no code; the Go API sketches below are
proposals, written as text, not code that exists. It ranks five features by their
value to integrators of go-profinet's IO-Device, from p-net v2.0.0's release
notes and source (`public/v2.0.0`, `67301c5`; GPL-3.0, read for behaviour
only). None of these was tested against p-net v2.0.0: the interop harness
builds p-net `ea5e554`.

The rank orders what to do next. It weighs value and readiness
together: a feature of high strategic value that cannot be built on
evidence ranks low.

| Rank | Feature | Strategic value | Readiness (evidence available) | Size |
|---|---|---|---|---|
| 1 | Application-owned I&M providers | high: every product needs its own I&M0 and persistent I&M1–4 | ready: records already implemented and verified | small |
| 2 | Runtime plug and pull | high for modular and remote-IO devices | partial: alarm types and ModuleDiff from Wireshark and p-net; the sequence after a plug from p-net only | large |
| 3 | Combined Object Container | medium: controllers that use it for parameter sets | partial: the block layout from p-net and Wireshark; error semantics from p-net only | medium |
| 4 | Multicast pruning (filterable Identify) | low to medium: large networks, Identify by name | partial: address formula from p-net (citing the standard's table and formula), the rest not public | small |
| 5 | Production S2 (system redundancy) | high: process plants require it | not ready: no open implementation in the harness to test against, the switchover sequence is not public | large, blocked |

## 1. Application-owned I&M providers

- **Today**: I&M0 is built from the model (vendor, order ID, serial
  number, revisions); I&M1–4 of the DAP are writable, kept in memory,
  given initial values by `device.Config.IM` and persisted when
  `Config.Store` is set (docs/DIAGNOSTICS.md, LIMITATIONS.md "Devices").
  The application cannot answer a read itself, refuse a write, or serve
  I&M for a submodule other than the DAP.
- **p-net v2.0.0 adds**: the application owns I&M. Read callbacks for
  I&M0–4 and write callbacks for I&M1–4, `pnet_load_im`/`pnet_save_im`,
  and per-submodule flags marking I&M carriers (`PNET_SMF_IM_CARRIER`,
  `PNET_SMF_IM_DEVICE_REP`).
- **Gap**: no hook between the record and the application; I&M only on
  the DAP; I&M0FilterData lists what the model says, not what the
  application carries.
- **Why integrators care**: serial numbers and hardware revisions come
  from the product at run time, not from a static model; tag and
  location must survive firmware updates in the product's own storage;
  modules with their own I&M (a field-replaceable module) need I&M0 per
  submodule.
- **Go API (proposal)**:

  ```text
  // IMAddr is the address of a submodule as a record request carries it.
  type IMAddr struct {
      API           uint32
      Slot, Subslot uint16
  }
  // IMProvider serves the I&M records of the submodules that carry them.
  type IMProvider interface {
      // ReadIM returns I&M n (0..4) of the submodule; ErrNotFound when it
      // carries none.
      ReadIM(ctx context.Context, at IMAddr, n int) (pnio.IMData, error)
      // WriteIM stores I&M n (1..4); a *pnio.StatusError refuses it.
      WriteIM(ctx context.Context, at IMAddr, n int, d pnio.IMData) error
  }
  // Config.IMProvider: nil keeps today's built-in provider (model + Config.IM
  // + Config.Store), which becomes the default implementation.
  // model.Submodule.IMCarrier marks the carriers for I&M0FilterData.
  ```

  Calls run outside the device's locks with the record's deadline, like
  `Handler.WriteRecord`. The address carries the API: today a slot
  belongs to one API of the model, so (slot, subslot) is unique, but the
  request names the API, and a request whose API is not the
  submodule's is refused before the provider is called. Carrying it
  keeps the interface unchanged if a model ever places the same slot in
  several APIs, and lets a provider serve I&M of profile APIs.
- **Standards evidence needed**: none beyond what the I&M records already
  have (Wireshark and the reference stacks); I&M0FilterData's carrier
  semantics are in Wireshark's dissector.
- **Compatibility**: additive. Without a provider the behaviour is
  today's.
- **Recommendation**: do it next. Small, fully evidenced, and it removes
  a reason to fork the device.

## 2. Runtime plug and pull

- **Today**: the model is fixed when the device is built
  (`device.New`); ModuleDiff reports what is missing at Connect; there is
  no Plug or Pull alarm and no way to change the real configuration while
  an AR runs.
- **p-net v2.0.0 adds**: `pnet_plug_module`, `pnet_plug_submodule`
  (with flags) and `pnet_pull_submodule` at any time; "dynamic
  reconfiguration"; Plug, Pull and PlugWrongSubmodule alarms; the
  `sm_activated_cb` callback when a submodule plugged during a connection
  becomes active after the controller's parameterization of it.
- **Gap**: a mutable real configuration, the Pull/Plug alarm sequence,
  the parameterization of a plugged submodule within a running AR
  (PrmBegin/PrmEnd per submodule, ApplicationReady for it), the IO data of
  a pulled submodule (IOPS bad), and ModuleDiff/identification reads that
  follow the change.
- **Why integrators care**: modular IO (backplane slices), hot swap of a
  failed module, devices whose options are detected at start.
- **Go API (proposal)**:

  ```text
  // Plug makes a submodule real at (api, slot, subslot); an AR that
  // expects it gets a Plug alarm (PlugWrongSubmodule for another ident)
  // and parameterizes it.
  func (d *Device) Plug(ctx context.Context, api uint32, slot, subslot uint16, sm model.Submodule) error
  // Pull removes it: its AR gets a Pull alarm, its inputs IOPS bad, its
  // outputs are no longer applied.
  func (d *Device) Pull(ctx context.Context, api uint32, slot, subslot uint16) error
  // Handler gains an optional SubmoduleActivated(info ARInfo, api uint32, slot, subslot uint16).
  ```

  The API is part of the address because the alarm (its API field), the
  ModuleDiffBlock and the expected configuration all name it; a Plug
  into a slot of another API than the model's is refused.

  The model stays immutable; the device keeps a "real" overlay under its
  lock, read by ModuleDiff, identification and the IO layout.
- **Standards evidence needed**: the alarm types (Pull 0x0003, Plug 0x0004,
  PlugWrongSubmodule 0x000A) and the submodule-level PrmBegin are in
  Wireshark and already in `pnio`; the order of events after a Plug
  (alarm, AlarmAck, PrmBegin, writes, PrmEnd, ApplicationReady for the
  submodule) is from p-net only and must be checked against a PLC or a
  public capture before it is claimed.
- **Compatibility**: additive API; behaviour of devices that never plug
  or pull is unchanged. The IO layout of an AR must not change shape
  (the IOCR length is fixed at Connect): a pulled submodule keeps its
  slots in the frame with IOPS bad.
- **Recommendation**: worth doing after I&M, in two steps: Pull/Plug of
  the same ident (no layout change) first, then wrong-submodule handling.

## 3. Combined Object Container

- **Today**: index 0x80B0 is unknown and refused; the ARProperties bit
  `CombinedObjectContainer` (`pnio.ARPropCombinedObjectCont`) is decoded
  only.
- **p-net v2.0.0 adds**: a write to 0x80B0 whose data is a sequence of
  COContainerContent blocks (0x0401), each carrying an IODWriteReq for a
  submodule; the device applies them in order; a nested 0x80B0 is
  refused (p-net `pf_cmwrr_write_coc`).
- **Gap**: the container decode, the dispatch of each inner write through
  the normal write path, and the answer when an inner write fails.
- **Why integrators care**: controllers that set the ARProperties bit
  expect to write parameter sets of several submodules atomically per
  container; without it such a project fails parameterization.
- **Go API**: none for the application: inner writes reach
  `Handler.WriteRecord` as ordinary writes. Internally a COContainer
  codec (with a fuzz target and golden vectors) and a dispatcher next to
  WriteMultiple.
- **Standards evidence needed**: the block layout (Wireshark dissects
  0x0401); whether a failing inner write stops the container and which
  status the outer write returns (p-net's answer is one implementation's;
  no capture is public); whether the inner writes must be atomic.
- **Compatibility**: additive: a write that is refused today is served.
- **Recommendation**: do it once a capture or a second implementation
  settles the error semantics; implement it without claiming atomicity.

## 4. Multicast pruning

- **Today**: the DCP responder accepts Identify on the Identify multicast
  address; `PDPortDataAdjust` DCPBoundary filters DCP across ports of a
  multiport device. No filterable Identify addresses.
- **p-net v2.0.0 adds** ("filtering only"): with `PNET_OPTION_MC_PRUNING`
  the device accepts an Identify only on 01-0E-CF-00-00-00 or on
  01-0E-CF-00-00-xx with xx = 0x20 + (MD5(name of station)[15] & 0x1F),
  citing the standard's table and formula (`pf_dcp_filter_destination_address`).
- **Gap**: computing the address and joining that group; a controller
  that sends Identify by name to it; changing the group when the name
  changes.
- **Why integrators care**: in large flat networks a filterable Identify
  reaches about 1/32 of the devices' CPUs.
- **Go API**: in package dcp, a responder option FilterableIdentify
  (bool) and a function FilterableIdentifyMAC(name) returning the
  group address; the controller
  side would send Identify by name to that address when asked.
- **Standards evidence needed**: the formula comes from p-net's comment
  (it cites the standard). Before claiming it, a second source (a
  Wireshark display or a capture of an engineering tool) must confirm
  the selector, and whether the device must still answer on
  01-0E-CF-00-00-00.
- **Compatibility**: opt-in; off by default as in p-net.
- **Recommendation**: low priority; do it as a small opt-in when a
  second source confirms the address.

## 5. Production S2

- **Today**: S2 is an experiment, off by default, built from the wire
  elements Wireshark dissects (LIMITATIONS.md); not interoperable with a
  PLC; no R1/R2, no redundancy diagnoses.
- **p-net v2.0.0 adds**: AR sets with primary/backup, behind
  `PNET_OPTION_SR`, and conformance (its release notes report a pass of
  PROFINET Test Bundle 2.46.0.205).
- **Gap**: the switchover sequence, RedundancyDataHoldFactor semantics,
  backup behaviour (data invalid on the backup AR), the redundancy
  diagnoses, and above all evidence from a real S2 controller.
- **Why integrators care**: process plants require S2 for redundant
  controllers.
- **Go API**: the current `device.Config.SystemRedundancy` and
  `controller.DeviceConfig.SystemRedundancy` would stay; what changes is
  behaviour and its claim level.
- **Standards evidence needed**: p-net v2.0.0's S2 is open source now and
  could become the oracle the experiment lacks, but only if
  profinet-interop builds p-net v2.0.0 with `PNET_OPTION_SR`; the public
  v2.0.0 tree has no ports and no build files, so that needs a port of
  its own (not in scope here: the harness's p-net pin stays).
- **Compatibility**: none until the behaviour changes; a change of the
  switchover would be a behaviour break of an experimental feature.
- **Value and readiness**: the strategic value is high (redundant
  controllers in process plants are a buying criterion); the readiness
  is the lowest of the five. The rank reflects readiness, not value.
- **Recommendation**: keep it experimental. Revisit when an S2 oracle
  can be built; until then, adding behaviour would be invention.
