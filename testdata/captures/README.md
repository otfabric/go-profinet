# Recorded PROFINET traffic

Frames recorded from real equipment and from other PROFINET stacks, and the
tests that check go-profinet against them. The most valuable part is the
traffic a **SIMATIC S7-1500 CPU 1515-2 PN** (firmware V2.9 / V2.9.4)
sent as IO-Controller, recorded by the
[pnio-rs](https://github.com/core-engineering/pnio-rs) project: the
device tests send the CPU's frames to go-profinet's device, unchanged, and
compare its answers with those of the recorded IO-Device.

This is evidence recorded by others. It is not a test against a live PLC and
not a conformance claim; see the evidence classes below for what each file
can support.

## Sources

| Directory | Source | Revision | Terms | Committed |
|---|---|---|---|---|
| `pnio-rs/` | [core-engineering/pnio-rs](https://github.com/core-engineering/pnio-rs) `crates/pnio/testdata/` (cm, alarm, rt) and the two frames printed in `docs/dcp-golden-frames.md` | commit `1cbb7c4030e4d899f97ea494a2db119d052e4ba5` | MIT OR Apache-2.0 (`pnio-rs/LICENSE-MIT`, `pnio-rs/LICENSE-APACHE`) | yes, byte-identical, renamed (see Names; original names in Files) |
| `wireshark/` | [Wireshark wiki, PROFINET/DCP](https://wiki.wireshark.org/PROFINET/DCP) example capture | downloaded 2026-11-08 | no licence statement; published as the PN-DCP dissector's example (as go-s7comm commits the wiki's S7comm samples) | yes, byte-identical, renamed |
| `ics-pcaps/` | [EmreEkin/ICS-Pcaps](https://github.com/EmreEkin/ICS-Pcaps) `Profinet/`, `LLDP/` | commit `0ffbf023afb4e39373228b54851bb4a1f33a3ab8` | no licence stated | yes, byte-identical, renamed |

Every recording is committed byte-identical under the names below; the
Files table gives each one's original name. The licence column states what
the source says, nothing more. Not committed: ICS-Pcaps `Profinet/pro1.pcap`
(the Wireshark sample again) and `Profinet/pro2.pcap` (S7comm, the subject
of go-s7comm, not PROFINET). Every file is far below the size at which a
test would move to an extended target, so all of them run in `make test`.

The pnio-rs project did not publish its original pcapng files (see its
`captures/README.md`): only the extracted frames exist, and their frame
numbers refer to recordings nobody else can read. `manifest.json` lists the
originals as not available.

## Evidence classes

| Class | Meaning | Here |
|---|---|---|
| A | independently verified physical device, model and firmware known | none |
| B | physical-device origin credibly documented, not verified by us | every frame **sent by the CPU 1515-2 PN** in `pnio-rs/` (pnio-rs documents the bench: 6ES7 515-2AM02-0AB0, HW 3, FW V2.9.4; the frames themselves show VendorID 0x002A, DeviceID 0x010E) |
| C | recorded traffic, origin unknown | `wireshark/`, `ics-pcaps/`: the frames name Siemens, GE, Cisco, Extreme and HP hardware (OUIs, DCP identity, an S7-1200 6ES7 214-1AG40-0XB0 FW V4.2.1 and a KTP700 Basic in their own LLDP), but nobody documented how they were recorded |
| D | simulated or software-generated | frames **sent by p-net** v0.2.0 and **PLCSIM Advanced** in `pnio-rs/`: the IO-Device side of every pnio-rs bench is software, not pnio-rs and not hardware |

Only class B supports a statement about real hardware here: "go-profinet's
device answers the start-up, alarm and DCP requests a CPU 1515-2 PN sent, as
recorded". What the CPU would do with go-profinet's answers is not recorded:
where the device answers differently from p-net, the tests classify the
difference, and nothing shows that the CPU would accept it.

## Devices and peers

| Equipment | Identity | Seen in | Class |
|---|---|---|---|
| SIMATIC S7-1500 CPU 1515-2 PN | 6ES7 515-2AM02-0AB0 HW 3, FW V2.9 (bench 1, 2026-06-26), V2.9.4 (bench 2, 2026-08-27; alarm bench 2026-08-30, TIA V21); MAC ec:1c:5d:61:e7:3f (:3c in the alarm bench), 172.16.2.100 | `pnio-rs/s7-1500_*`: the frames it sent (class B in the Files table) | B |
| rt-labs p-net v0.2.0 `pn_dev` (software IO-Device) | VendorID 0x0493 DeviceID 0x0002, "rt-labs-dev", 8c:f3:19:cd:19:f8, 172.16.2.10 | `pnio-rs/s7-1500_p-net_*`: the frames it sent (class D) | D |
| PLCSIM Advanced "i-device" (software) | "S7-1500 (PLCSIM)", 0x002A/0x010E | `pnio-rs/s7-1500_plcsim_dcp-identify-res.hex` | D |
| SCALANCE X208 | 0x002A/0x0A01 "X208-BORD", 08:00:06:93:cf:32 | `wireshark/unknown_scalance-x208_dcp-set-ip.pcap` | C |
| Siemens IO-Controller of the S7-300 family (2009) | CMInitiatorObjectUUID 0x002A/0x0101, 00:0e:8c:af:da:2c | `ics-pcaps/s7-300_unknown_connect-req.pcap` | C |
| S7-300EC | 0x002A/0x0101, role IO-Controller, 00:02:e3:17:c7:50 | `ics-pcaps/unknown_s7-300ec_dcp-set-name.pcap` | C |
| SCALANCE switches (2012) | 00:1b:1b:…, names switch1..7 | `ics-pcaps/scalance_unknown_dcp-identify.pcap` | C |
| GE Intelligent Platforms device | 00:09:91:44:20:17, object UUID vendor 0x015A | `ics-pcaps/unknown_ge-ip-device_read-implicit.pcap` | C |
| S7-1200 CPU 1214C | 6ES7 214-1AG40-0XB0 HW 7 FW V4.2.1 (its LLDP) | `ics-pcaps/unknown_s7-1200_lldp.pcapng` | C |
| KTP700 Basic | 6AV2 123-2GB03-0AX0 SW V13.0.1 (its LLDP) | `ics-pcaps/unknown_s7-1200_lldp.pcapng` | C |
| SCALANCE X 200, OptiPlex 990 HMI PC | their LLDP | `ics-pcaps/unknown_scalance-x200_lldp.pcap` | C |
| Cisco Catalyst 3560 (IOS 12.2(44)SE), Extreme Summit300-48 (7.4e.1), an Extreme device (00:04:96), HP ProCurve 2600-8-PWR (H.08.89) | their LLDP | `ics-pcaps/unknown_cisco-c3560_lldp.pcap`, `unknown_summit300-48_lldp.pcap`, `unknown_extreme_lldp-minimal.pcap`, `unknown_procurve-2600_lldp-med.pcap` | C |

pnio-rs's `cm-golden-frames.md` swaps the CPU's and p-net's MAC addresses;
the IP addresses in the frames (and the DCP golden frames) are
authoritative.

## Names

`<client-or-peer>_<device>_<operation>.<ext>`, lower case, words joined by
hyphens; the directory is the source and the extension keeps the original
format (hex for the frames pnio-rs published, pcap or pcapng for recordings). The client
or peer is the side that is not the device the file is about: the IO
controller or tool. `unknown` means the traffic does not show it. Requests
and responses end in `-req` and `-res`; alarm files name the channel
(`alarm-high`, `alarm-low`, `alarm-err-rta`) and, for RTA acknowledgements,
their sender (`-cpu`, `-device`).

| Client or peer | What |
|---|---|
| `s7-1500` | SIMATIC S7-1500 CPU 1515-2 PN as IO-Controller (pnio-rs benches) |
| `s7-300` | a Siemens IO-Controller of the S7-300 family (CMInitiatorObjectUUID DeviceID 0x0101) |
| `scalance` | a SCALANCE switch asking for its own name (DCP name check) |
| `unknown` | not shown by the traffic: an engineering tool, a DCP tool, or (LLDP) any neighbour |

| Device | What |
|---|---|
| `p-net` | rt-labs p-net v0.2.0 `pn_dev`, software IO-Device |
| `plcsim` | PLCSIM Advanced "i-device", software |
| `scalance-x208` | SCALANCE X208 |
| `s7-300ec` | S7-300EC (DeviceID 0x0101, role IO-Controller) |
| `s7-1200`, `scalance-x200` | the S7-1200 file also holds the KTP700's LLDP, the SCALANCE X 200 file an HMI PC's |
| `cisco-c3560`, `summit300-48`, `extreme`, `procurve-2600` | LLDP of non-PROFINET switches |
| `ge-ip-device` | a GE Intelligent Platforms device |
| `unknown` | not shown by the traffic |

## Files

| File | Original name | Direction | Class |
|---|---|---|---|
| `pnio-rs/s7-1500_p-net_alarm-high-process-ack.hex` | `crates/pnio/testdata/alarm/alarm_ack_high_cpu.hex` | CPU -> device | B |
| `pnio-rs/s7-1500_p-net_alarm-high-ack-rta-cpu.hex` | `crates/pnio/testdata/alarm/alarm_ack_rta_high_cpu.hex` | CPU -> device | B |
| `pnio-rs/s7-1500_p-net_alarm-high-ack-rta-device.hex` | `crates/pnio/testdata/alarm/alarm_ack_rta_high_dev.hex` | device -> CPU | D |
| `pnio-rs/s7-1500_p-net_alarm-low-ack-rta-cpu.hex` | `crates/pnio/testdata/alarm/alarm_ack_rta_low_cpu.hex` | CPU -> device | B |
| `pnio-rs/s7-1500_p-net_alarm-low-ack-rta-device.hex` | `crates/pnio/testdata/alarm/alarm_ack_rta_low_dev.hex` | device -> CPU | D |
| `pnio-rs/s7-1500_p-net_alarm-low-diagnosis-ack.hex` | `crates/pnio/testdata/alarm/alarm_diag_ack_cpu.hex` | CPU -> device | B |
| `pnio-rs/s7-1500_p-net_alarm-low-diagnosis-appears.hex` | `crates/pnio/testdata/alarm/alarm_diag_notif.hex` | device -> CPU | D |
| `pnio-rs/s7-1500_p-net_alarm-low-diagnosis-all-disappear.hex` | `crates/pnio/testdata/alarm/alarm_diag_std_remove.hex` | device -> CPU | D |
| `pnio-rs/s7-1500_p-net_alarm-low-diagnosis-update-appears.hex` | `crates/pnio/testdata/alarm/alarm_diag_update_appears.hex` | device -> CPU | D |
| `pnio-rs/s7-1500_p-net_alarm-low-diagnosis-update-others-remain.hex` | `crates/pnio/testdata/alarm/alarm_diag_update_others_remain.hex` | device -> CPU | D |
| `pnio-rs/s7-1500_p-net_alarm-low-diagnosis-manufacturer-disappears.hex` | `crates/pnio/testdata/alarm/alarm_diag_usi_disappears.hex` | device -> CPU | D |
| `pnio-rs/s7-1500_p-net_alarm-err-rta-cpu-reply.hex` | `crates/pnio/testdata/alarm/alarm_err_rta_cpu.hex` | CPU -> device | B |
| `pnio-rs/s7-1500_p-net_alarm-err-rta-cpu-ar-removed.hex` | `crates/pnio/testdata/alarm/alarm_err_rta_cpu_removed.hex` | CPU -> device | B |
| `pnio-rs/s7-1500_p-net_alarm-err-rta-device-abort.hex` | `crates/pnio/testdata/alarm/alarm_err_rta_dev.hex` | device -> CPU | D |
| `pnio-rs/s7-1500_p-net_alarm-err-rta-device-reply.hex` | `crates/pnio/testdata/alarm/alarm_err_rta_dev_removed_reply.hex` | device -> CPU | D |
| `pnio-rs/s7-1500_p-net_alarm-high-process.hex` | `crates/pnio/testdata/alarm/alarm_process_notif.hex` | device -> CPU | D |
| `pnio-rs/s7-1500_p-net_read-im0-dap-req.hex` | `crates/pnio/testdata/alarm/im0_read_req.hex` | CPU -> device | B |
| `pnio-rs/s7-1500_p-net_read-im0-interface-req.hex` | `crates/pnio/testdata/alarm/im0_read_req_if.hex` | CPU -> device | B |
| `pnio-rs/s7-1500_p-net_read-im0-dap-res.hex` | `crates/pnio/testdata/alarm/im0_read_res.hex` | device -> CPU | D |
| `pnio-rs/s7-1500_p-net_read-im0-interface-res.hex` | `crates/pnio/testdata/alarm/im0_read_res_if.hex` | device -> CPU | D |
| `pnio-rs/s7-1500_p-net_appready-req.hex` | `crates/pnio/testdata/cm/appready_req.hex` | device -> CPU | D |
| `pnio-rs/s7-1500_p-net_appready-res.hex` | `crates/pnio/testdata/cm/appready_res.hex` | CPU -> device | B |
| `pnio-rs/s7-1500_p-net_connect-req.hex` | `crates/pnio/testdata/cm/connect_req.hex` | CPU -> device | B |
| `pnio-rs/s7-1500_p-net_connect-res.hex` | `crates/pnio/testdata/cm/connect_res.hex` | device -> CPU | D |
| `pnio-rs/s7-1500_p-net_dcp-set-ip-req.hex` | `crates/pnio/testdata/cm/dcp_set_req.hex` | CPU -> device | B |
| `pnio-rs/s7-1500_p-net_dcp-set-ip-res.hex` | `crates/pnio/testdata/cm/dcp_set_res.hex` | device -> CPU | D |
| `pnio-rs/s7-1500_p-net_dcp-identify-res.hex` | `crates/pnio/testdata/cm/ident_ok_pnet.hex` | device -> CPU | D |
| `pnio-rs/s7-1500_p-net_prmend-req.hex` | `crates/pnio/testdata/cm/prmend_req.hex` | CPU -> device | B |
| `pnio-rs/s7-1500_p-net_prmend-res.hex` | `crates/pnio/testdata/cm/prmend_res.hex` | device -> CPU | D |
| `pnio-rs/s7-1500_p-net_write-multiple-req.hex` | `crates/pnio/testdata/cm/write_req.hex` | CPU -> device | B |
| `pnio-rs/s7-1500_p-net_write-multiple-res.hex` | `crates/pnio/testdata/cm/write_res.hex` | device -> CPU | D |
| `pnio-rs/s7-1500_plcsim_dcp-identify-req.hex` | `docs/dcp-golden-frames.md (section 1)` | CPU -> device | B |
| `pnio-rs/s7-1500_plcsim_dcp-identify-res.hex` | `docs/dcp-golden-frames.md (section 2)` | device -> CPU | D |
| `pnio-rs/s7-1500_p-net_rt-output-8001-echo.hex` | `crates/pnio/testdata/rt/echo_cpu_8001.hex` | CPU -> device | B |
| `pnio-rs/s7-1500_p-net_rt-input-8000-echo.hex` | `crates/pnio/testdata/rt/echo_dev_8000.hex` | device -> CPU | D |
| `pnio-rs/s7-1500_p-net_rt-output-8001.hex` | `crates/pnio/testdata/rt/rtc_cpu_8001.hex` | CPU -> device | B |
| `pnio-rs/s7-1500_p-net_rt-input-8000.hex` | `crates/pnio/testdata/rt/rtc_dev_8000.hex` | device -> CPU | D |
| `wireshark/unknown_scalance-x208_dcp-set-ip.pcap` | `ChangeIPUsingDCP.pcap` | tool <-> device | C |
| `ics-pcaps/s7-300_unknown_connect-req.pcap` | `Profinet/pro.pcap` | S7-300 class controller -> Siemens device | C |
| `ics-pcaps/unknown_ge-ip-device_read-implicit.pcap` | `Profinet/pro3.pcap` | tool <-> GE device | C |
| `ics-pcaps/unknown_scalance-x200_lldp.pcap` | `Profinet/pro4.pcap` | neighbours -> LLDP multicast | C |
| `ics-pcaps/scalance_unknown_dcp-identify.pcap` | `Profinet/pro5.pcap` | SCALANCE switches -> multicast | C |
| `ics-pcaps/unknown_s7-300ec_dcp-set-name.pcap` | `Profinet/pro6.pcap` | tool <-> S7-300EC | C |
| `ics-pcaps/unknown_s7-1200_lldp.pcapng` | `LLDP/simens.pcap` | neighbours -> LLDP multicast | C |
| `ics-pcaps/unknown_cisco-c3560_lldp.pcap` | `LLDP/LLDP.pcap` | switches -> LLDP multicast | C |
| `ics-pcaps/unknown_summit300-48_lldp.pcap` | `LLDP/lldp.detailed.pcap` | switch -> LLDP multicast | C |
| `ics-pcaps/unknown_extreme_lldp-minimal.pcap` | `LLDP/lldp.minimal.pcap` | device -> LLDP multicast | C |
| `ics-pcaps/unknown_procurve-2600_lldp-med.pcap` | `LLDP/lldpmed_civicloc.pcap` | switch -> LLDP multicast | C |

`manifest.json` lists every file with its source, original name, frame,
direction, SHA256, size, class and content, and the raw files that are only
referenced. [SHA256SUMS](SHA256SUMS) covers every committed file
(`shasum -a 256 -c SHA256SUMS` here). A hex fixture is one Ethernet frame;
`#` starts a comment, the first comment line names the frame.
`coverage.json` is the machine-readable form of the matrix below.

## What the tests check

| Recorded exchange | Source | Encode like the recording | Decode | go-profinet as client | go-profinet as server, against the recording | Known differences |
|---|---|---|---|---|---|---|
| DCP Identify by name (CPU), Identify All, the 14 name checks of SCALANCE switches | B, C | byte-exact (`dcp.Frame.AppendTo`, `FilterName`, `FilterAll`) | ✅ | n/a | device answers the CPU's Identify with the blocks PLCSIM answered; of all 16 recorded Identify requests it answers exactly those that name it or all | — |
| DCP Identify responses (PLCSIM, p-net, S7-300EC, X208) | C, D | byte-exact re-encode | ✅ identity as tshark | `ParseIdentity` | n/a | the S7-300 pads NameOfStation with NULs to 108 octets (**fixed**: trimmed) |
| DCP Set IP (CPU, VLAN 0) and response | B, D | byte-exact | ✅ | n/a | device's response byte-exact as p-net's, IP applied | — |
| DCP Set NameOfStation "%n%n%n " + End-Transaction | C | byte-exact | ✅ | n/a | device refuses the invalid name | the S7-300 accepted it; intentional |
| LLDP of S7-1200, KTP700, SCALANCE X 200, HMI PC (all 710 frames) and of Cisco, Extreme and HP switches | C | same TLVs, other order | ✅ chassis, port, TTL, descriptions, PNO port status and chassis MAC, MAC/PHY, management address; the LLDPDU with four TTL TLVs refused | n/a | n/a | the management address ifIndex and OID were dropped, ifIndex 0 sent as 1, a non-IPv4 management address dropped and a repeated TTL TLV accepted (**fixed**); go-profinet's device sends ifIndex 1 and no OID, all four send the PNO OID 1.3.6.1.4.1.24686 |
| Connect (CPU; Siemens 2009) | B, C | byte-exact apart from block order | ✅ RPC header, NDR, every block | `rt.LayoutFromIOCR` takes the CPU's layout | device answers: ARBlockRes, IOCRBlockRes, AlarmCR, ARServer as p-net; NDR max_count echoes args_max | the CPUs send AlarmCRBlockReq last, go-profinet's encoder before the expected submodules; the device adds a ModuleDiffBlock (every submodule ApplicationReadyPending), p-net none; MaxAlarmDataLength 256 granted (p-net 200) |
| Write, MultipleWrite of PDInterfaceAdjust and 3 records | B, D | byte-exact (`AppendWriteMultiple`) | ✅ | n/a | five IODWriteResHeaders as p-net's | — |
| Control PrmEnd | B, D | byte-exact | ✅ | n/a | IODControlRes byte-exact as p-net's | — |
| Control ApplicationReady (device → CPU) and the CPU's answer | D, B | byte-exact | ✅ (CPU answers flags1 0x0A) | the device takes the CPU's recorded answer and enters Data | the device's IOXControlReq byte-exact as p-net's | — |
| Cyclic RT_CLASS_1 0x8000/0x8001, 40 octets, 32 ms | B, D | trailer byte-exact | ✅ cycle counter, data status (0x35; p-net once 0x36 "backup") | n/a | device consumes the CPU's frames (BOOL bit order, big-endian DWORD and REAL), its input frame has p-net's VLAN, status and IOxS positions | the CPU puts IOCS after each submodule's data, `rt.ComputeLayout` after all data; both valid |
| Process alarm (high) and its ACK-RTA / AlarmAck | D, B | byte-exact | ✅ | `pnio.AckFor` gives the CPU's AlarmAck byte for byte | device's AlarmNotification byte-exact as p-net's, sequence FFFF/FFFE; takes the CPU's acknowledgements | — |
| Diagnosis alarm (low) and its acknowledgements | D, B | byte-exact | ✅ | `AckFor` byte-exact | device's notification as p-net's, AlarmSpecifier 0xA800 | the AlarmSpecifier sequence number counted per AR (**fixed**: per priority, as p-net and the CPU); p-net sends ExtChannelDiagnosis (USI 0x8002) with zero extensions, go-profinet ChannelDiagnosis (0x8000) |
| ERR-RTA "AR removed" (CPU) and the answers | B, D | byte-exact | ✅ (the CPU puts 18 vendor octets after VarPartLen) | controller answers a device's ERR-RTA | device ends the AR | neither the device nor the controller answered an ERR-RTA (**fixed**: both answer "AR alarm.ind(err)" CF 81 FD 0B, as the CPU does); p-net echoes the peer's status instead |
| Read I&M0 (DAP, interface) | B, D | byte-exact | ✅ I&M0 fields | `ReadIM0` decodes p-net's answers | not replayed (another session's AR) | — |
| Read Implicit I&M0FilterData (GE device) | C | byte-exact | ✅ | the record client reads the GE answer | device answers the submodule and device blocks | no I&M0FilterDataModule: the device's only I&M0 carrier is the DAP (optional block) |

`internal/capture TestEveryRecordedFrame` decodes and re-encodes every LLDP, DCP, PNIO-CM, RT and RTA frame of every file and checks tshark's count per file; the 2009 Siemens Connect is replayed into the device (accepted with a ModuleDiffBlock: the DAP and the API 0x3A00 modules are another device's). Every recorded frame is also a seed of the fuzz targets it reaches
(`testdata/fuzz/*/capture-*` in the packages, identical frames once), and
`internal/capture TestMalformedVariants` runs every truncation and
every 16- and 32-bit field forced to 0 / all ones through the decoders.

## Not covered by any recording

DCP Get, Hello, Signal, ResetToFactory and DHCP; Release; Read and Write of
I&M1–I&M4; plug and pull alarms; RT_CLASS_2/3, MRP, PTCP; device access
ARs; shared devices; system redundancy; SNMP. They stay verified by the
interop suite ([INTEROPERABILITY.md](../../INTEROPERABILITY.md)) or not at
all, as [docs/FEATURE-STATUS.md](../../docs/FEATURE-STATUS.md) states.

## Adding a recording

Commit the file byte-identical under a clean name, add it to
`manifest.json` (source URL and revision, original name, the licence the
source states), `SHA256SUMS`, `coverage.json` and the frame counts of
`internal/capture TestEveryRecordedFrame` (cross-check them with tshark),
and name the device, the peer and the class. Never change a recorded frame
to make a test pass; normalise only the fields a test names as dynamic
(activity UUID, sequence and cycle counters, alarm endpoint references,
addresses moved to the test network).
