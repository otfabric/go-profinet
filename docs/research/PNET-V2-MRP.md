# MRP records against rt-labs p-net v2.0.0

Wave 3 of the comparison of go-profinet with rt-labs p-net v2.0.0:
what go-profinet's device does with PDInterfaceMrpDataCheck (0x8051) and
with the timers of MrpManagerParams and MrpClientParams, and where each
rule comes from. IEC 62439-2 and IEC 61158-6-10 were not read; p-net
(GPL-3.0) was read for behaviour only.

## Sources

| Fact | Source |
|---|---|
| MRP_Check bit 0x01 "MediaRedundancyManager", 0x02 "MRP_DomainUUID", each "Enable … diagnosis / the check of the MRP_DomainUUID" | Wireshark `plugins/epan/profinet/packet-dcerpc-pn-io.c` (`hf_pn_io_mrp_check_mrm`, `hf_pn_io_mrp_check_mrpdomain`, `pn_io_mrp_mrm_on`, `pn_io_mrp_checkUUID`) |
| PDInterfaceMrpDataCheck v1.0: padding, MRP_DomainUUID, MRP_Check (32 bits); v1.1 several instances | Wireshark `dissect_PDInterfaceMrpDataCheck_block` |
| Extended channel error type 0x8004 of ChannelErrorType 0x8001 (remote mismatch): "Peer MRP domain mismatch"; 0x8005 "No peer detected" | Wireshark `pn_io_ext_channel_error_type0x8001` |
| LLDP PROFINET TLV subtype 4: MRP_DomainUUID (16 octets), MRRT_PortStatus (2 octets: 0 OFF, 1 MRRT_CONFIGURED, 2 MRRT_UP) | Wireshark `epan/dissectors/packet-lldp.c` |
| The domain check compares the port's MRP domain with the one its LLDP neighbor announces, and reports 0x8004 with severity fault | p-net `pf_pdport_check_mrp_domain_mismatch` (`src/device/pf_pdport.c`) |
| Units: MRP_TOPchgT "time base 10ms", MRP_TSTshortT and MRP_TSTdefaultT "time base 1 ms", MRP_LNKdownT and MRP_LNKupT "Interval in ms"; the NRmax fields are counts | Wireshark `packet-dcerpc-pn-io.c` field blurbs |
| The 200 ms and 500 ms profiles: TOPchgT 10/20 ms, TOPNRmax 3/3, TSTshortT 10/30 ms, TSTdefaultT 20/50 ms, TSTNRmax 3/5, LNKdownT = LNKupT 20/100 ms, LNKNRmax 4/4 | microchip-ung/mrp `state_machine.c` (`MRP_RING_RECOVERY_200`, `_500`); go-profinet's `mrp.Profile200` and `Profile500` already used these |
| Corroboration of the units: p-net's default MRP_LNKdownT is 0x14 (20 ms, the 200 ms profile's link interval), and p-net logs the written link_down_T and link_up_T "ms" | p-net `include/pf_mrp.h` (`MRP_CLIENT_PARAM_LINK_DOWN_T_DEFAULT`), `src/device/pf_pdport.c` |

## PDInterfaceMrpDataCheck (0x8051)

Before: every write refused with "feature not supported"; a read
returned nothing.

Now:

- **MRP_DomainUUID check (bit 0x02)**: honoured. A ring port whose LLDP
  neighbor announces another MRP domain reports a remote mismatch
  diagnosis (ChannelErrorType 0x8001, extended 0x8004, channel 0x8000,
  fault) on the port submodule; it disappears when the domains agree, the
  neighbor stops announcing one, the check is written without the bit, or
  MRP is switched off. The device announces its own domain on its ring
  ports in the MRP port status TLV (MRRT off: MRRT is not implemented).
- **MediaRedundancyManager check (bit 0x01)**: refused (0xA9 "feature not
  supported"). Its diagnoses (which manager conditions, which codes) are
  not in a public source.
- **Reserved bits**: refused (0xB8 "invalid parameter"): nothing is known
  that they would check, and accepting them would claim a check that does
  not happen.
- **Another MRP_DomainUUID than the device's**: refused (0xB8), as
  PDPortMrpDataAdjust of another domain is.
- **Block version 1.1** (several MRP instances): refused (0xA9); the
  device runs one instance.
- The written check is stored and read back. It is not reset by a new AR
  (p-net's `pf_pdport_reset_ar` resets the port checks only): the PDev
  reset of an IOCAR's Connect removes the mismatch diagnosis with the
  port checks' ones, and the evaluator reports it again for the new AR
  (`TestMRPCheckLifecycle`).
- The check names the domain it checks, which must be the device's when
  it is written. A PDInterfaceMrpDataAdjust that changes the domain
  drops it (go-profinet's rule: a check of a domain the device is not in
  could not be written); the neighbor is compared with the check's
  domain. A refused adjustment keeps the check.
- MRP off (role disabled): the check does not apply, no diagnosis.

Differences from p-net:

- A neighbor that sends no MRP port status is not a mismatch. p-net
  compares with the stored peer domain, which is empty then, so it
  reports a mismatch against a non-MRP neighbor; go-profinet reports
  nothing, as no domain is known to differ. This covers a neighbor that
  is an MRP ring port of a device that does not send the TLV: it is not
  reported, so a domain difference with such a device goes unnoticed.
  `TestMRPDomainCheck` pins it; whether the standard wants a mismatch
  here is not read, and no change is made without that.
- p-net sends the MRP port status TLV on the ports of an MRP client, and
  once after a role change; go-profinet sends it on the ring ports while
  the role is client or manager.
- p-net reports the diagnosis with a PortDataChangeNotification alarm;
  go-profinet with a Diagnosis alarm (as for its other remote mismatch
  diagnoses, LIMITATIONS.md).

## MrpManagerParams and MrpClientParams

Before: accepted, not applied ("unit not public"); the entity ran
`Config.MRP.Profile`. A changed manager priority without a role change
was stored but did not reach the running entity.

Now: the timers of a written PDInterfaceMrpDataAdjust select a timer
profile.

- They must be, field for field in the units above, those of
  `mrp.Profile200` or `mrp.Profile500`; when both blocks are present,
  both must name the same profile. The entity restarts with that profile.
- Any other set of timers is refused (0xA9 "feature not supported"),
  including the 30 ms and 10 ms profiles: the ring of a general-purpose
  OS does not keep their timers, and no recovery time is claimed for any
  profile.
- A block whose timers are all zero selects nothing and keeps the
  profile: go-profinet's controller writes MrpManagerParams with the
  priority alone (`controller/mrp.go`). This is an OT Fabric convention,
  not a meaning of zero in the standard or in another implementation
  known here. Having the controller write the complete timers of a
  profile was considered and not done in v0.25.0: it changes what the
  controller sends to every device, which needs interop evidence against
  a device that reads them (none in the matrix runs MRP records).
- MRP_TSTshortT is matched as part of the profile and not applied: the
  entity has no short test phase after a topology change, so implementing
  it is a change to the MRP state machine, not to the record. A write
  whose TSTshortT is not the profile's is refused, so no value is
  accepted that differs from what the profile table says.
- Ranges: no range check beyond the exact match; the comparison is done
  on `time.Duration`, which holds 65535 × 10 ms without overflow.
- Atomicity: the whole record is checked before anything changes. A
  refused write leaves the role, domain, priority, timers, the stored
  adjustment and check as they were. An accepted write stores the record
  and the configuration, then restarts the entity when the role, domain,
  name, priority or profile changed. When the new entity does not start,
  the write is refused and the previous records, configuration and
  entity are restored (`TestMRPAdjustRollback`, with an injected start
  failure); if the previous entity does not start either, the error is
  logged and the device runs no entity. The restart is not atomic with
  the write: the ring is briefly without an entity, as for a role change
  before; no recovery time is claimed.
- The manager priority (`MRP_Prio`) of a write now restarts the entity
  when it alone changed.

## Not done

- The MediaRedundancyManager check and the manager diagnoses.
- The short test interval after a topology change (MRP_TSTshortT): the
  entity has no such phase; the value is only matched (see above).
- The controller writing the complete timers of a profile (see above).
- MRRT, MRP interconnection (LLDP subtype 8), several MRP instances.
- Interop: none of this is tested against p-net or a PLC. profinet-interop
  v0.15.0 builds p-net `ea5e554`, without `PNET_OPTION_MRP`.
