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
| `pnio-rs/` | [core-engineering/pnio-rs](https://github.com/core-engineering/pnio-rs) `crates/pnio/testdata/` (cm, alarm, rt) and the two frames printed in `docs/dcp-golden-frames.md` | commit `1cbb7c4030e4d899f97ea494a2db119d052e4ba5` | MIT OR Apache-2.0 (`pnio-rs/LICENSE-MIT`, `pnio-rs/LICENSE-APACHE`) | yes, byte-identical |
| `wireshark/` | [Wireshark wiki, PROFINET/DCP](https://wiki.wireshark.org/PROFINET/DCP) example capture | downloaded 2026-11-08 | no licence statement; published as the PN-DCP dissector's example (as go-s7comm commits the wiki's S7comm samples) | yes, byte-identical |
| `ics-pcaps/` | [EmreEkin/ICS-Pcaps](https://github.com/EmreEkin/ICS-Pcaps) `Profinet/`, `LLDP/` | commit `0ffbf023afb4e39373228b54851bb4a1f33a3ab8` | **no licence** | **no**: single frames as excerpts only |

Redistribution: the raw ICS-Pcaps files are not committed because the
repository grants no licence. `ics-pcaps/*.hex` hold only the individual
frames a test needs, each labelled "excerpt; raw file not redistributed",
and `make captures-extended` fetches the raw files into the git-ignored
`.cache/`, checks their SHA256 and that every excerpt is the frame it names.
Committing those excerpts and the Wireshark sample is a judgement the owner
may revisit; removing them would leave the pnio-rs tests intact.

The pnio-rs project did not publish its original pcapng files (see its
`captures/README.md`): only the extracted frames exist, and their frame
numbers refer to recordings nobody else can read. `manifest.json` lists the
originals as not available.

## Evidence classes

| Class | Meaning | Here |
|---|---|---|
| A | independently verified physical device, model and firmware known | none |
| B | physical-device origin credibly documented, not verified by us | every frame **sent by the CPU 1515-2 PN** in `pnio-rs/` (pnio-rs documents the bench: 6ES7 515-2AM02-0AB0, HW 3, FW V2.9.4; the frames themselves show VendorID 0x002A, DeviceID 0x010E) |
| C | recorded traffic, origin unknown | `wireshark/`, `ics-pcaps/`: the frames name Siemens and GE hardware (OUIs, DCP identity, an S7-1200 6ES7 214-1AG40-0XB0 FW V4.2.1 and a KTP700 Basic in their own LLDP), but nobody documented how they were recorded |
| D | simulated or software-generated | frames **sent by p-net** v0.2.0 and **PLCSIM Advanced** in `pnio-rs/`: the IO-Device side of every pnio-rs bench is software, not pnio-rs and not hardware |

Only class B supports a statement about real hardware here: "go-profinet's
device answers the start-up, alarm and DCP requests a CPU 1515-2 PN sent, as
recorded". What the CPU would do with go-profinet's answers is not recorded:
where the device answers differently from p-net, the tests classify the
difference, and nothing shows that the CPU would accept it.

## Devices and peers

| Equipment | Identity | Seen in | Class |
|---|---|---|---|
| SIMATIC S7-1500 CPU 1515-2 PN | 6ES7 515-2AM02-0AB0 HW 3, FW V2.9 (bench 1, 2026-06-26), V2.9.4 (bench 2, 2026-08-27; alarm bench 2026-08-30, TIA V21); MAC ec:1c:5d:61:e7:3f (:3c in the alarm bench), 172.16.2.100 | `pnio-rs/` frames named `*_cpu*`, `connect_req`, `write_req`, `prmend_req`, `appready_res`, `dcp_set_req`, `im0_read_req*`, `identify_req_cpu` | B |
| rt-labs p-net v0.2.0 `pn_dev` (software IO-Device) | VendorID 0x0493 DeviceID 0x0002, "rt-labs-dev", 8c:f3:19:cd:19:f8, 172.16.2.10 | the other `pnio-rs/cm`, `alarm`, `rt` frames | D |
| PLCSIM Advanced "i-device" (software) | "S7-1500 (PLCSIM)", 0x002A/0x010E | `pnio-rs/dcp/identify_res_plcsim.hex` | D |
| SCALANCE X208 | 0x002A/0x0A01 "X208-BORD", 08:00:06:93:cf:32 | `wireshark/ChangeIPUsingDCP.pcap` | C |
| Siemens IO-Controller (2009) | CMInitiatorObjectUUID 0x002A/0x0101, 00:0e:8c:af:da:2c | `ics-pcaps/connect_req_siemens.hex` | C |
| S7-300EC | 0x002A/0x0101, role IO-Controller, 00:02:e3:17:c7:50 | `ics-pcaps/identify_res_s7300ec.hex`, `set_name_res.hex` | C |
| SCALANCE switches (2012) | 00:1b:1b:…, names switch1..7 | `ics-pcaps/identify_req_scalance.hex` | C |
| GE Intelligent Platforms device | 00:09:91:44:20:17, object UUID vendor 0x015A | `ics-pcaps/readimplicit_res.hex` | C |
| S7-1200 CPU 1214C | 6ES7 214-1AG40-0XB0 HW 7 FW V4.2.1 (its LLDP) | `ics-pcaps/lldp_s7-1200.hex` | C |
| KTP700 Basic | 6AV2 123-2GB03-0AX0 SW V13.0.1 (its LLDP) | `ics-pcaps/lldp_ktp700.hex` | C |
| SCALANCE X 200, OptiPlex 990 HMI PC | their LLDP | `ics-pcaps/lldp_scalance-x200.hex`, `lldp_pc-hmi.hex` | C |

pnio-rs's `cm-golden-frames.md` swaps the CPU's and p-net's MAC addresses;
the IP addresses in the frames (and the DCP golden frames) are
authoritative.

## Files

`manifest.json` lists every file with its source, original name, frame,
direction, SHA256, size, class and content, and the raw files that are only
referenced. [SHA256SUMS](SHA256SUMS) covers every committed file
(`shasum -a 256 -c SHA256SUMS` here). A hex fixture is one Ethernet frame;
`#` starts a comment, the first comment line names the frame.
`coverage.json` is the machine-readable form of the matrix below.

## What the tests check

| Recorded exchange | Source | Encode like the recording | Decode | go-profinet as client | go-profinet as server, against the recording | Known differences |
|---|---|---|---|---|---|---|
| DCP Identify by name (CPU), Identify All, Identify of a SCALANCE | B, C | byte-exact (`dcp.Frame.AppendTo`, `FilterName`, `FilterAll`) | ✅ | n/a | device answers the CPU's Identify with the blocks PLCSIM answered | — |
| DCP Identify responses (PLCSIM, p-net, S7-300EC, X208) | C, D | byte-exact re-encode | ✅ identity as tshark | `ParseIdentity` | n/a | the S7-300 pads NameOfStation with NULs to 108 octets (**fixed**: trimmed) |
| DCP Set IP (CPU, VLAN 0) and response | B, D | byte-exact | ✅ | n/a | device's response byte-exact as p-net's, IP applied | — |
| DCP Set NameOfStation "%n%n%n " + End-Transaction | C | byte-exact | ✅ | n/a | device refuses the invalid name | the S7-300 accepted it; intentional |
| LLDP of S7-1200, KTP700, SCALANCE X 200, HMI PC | C | same TLVs, other order | ✅ chassis, port, TTL, descriptions, PNO port status and chassis MAC, MAC/PHY, management address | n/a | n/a | the management address ifIndex and OID were dropped (**fixed**: `ManagementIfIndex`, `ManagementOID`); go-profinet's device sends ifIndex 1 and no OID, all four send the PNO OID 1.3.6.1.4.1.24686 |
| Connect (CPU; Siemens 2009) | B, C | byte-exact apart from block order | ✅ RPC header, NDR, every block | `rt.LayoutFromIOCR` takes the CPU's layout | device answers: ARBlockRes, IOCRBlockRes, AlarmCR, ARServer as p-net; NDR max_count echoes args_max | the CPUs send AlarmCRBlockReq last, go-profinet's encoder before the expected submodules; the device adds a ModuleDiffBlock (every submodule ApplicationReadyPending), p-net none; MaxAlarmDataLength 256 granted (p-net 200) |
| Write, MultipleWrite of PDInterfaceAdjust and 3 records | B, D | byte-exact (`AppendWriteMultiple`) | ✅ | n/a | five IODWriteResHeaders as p-net's | — |
| Control PrmEnd | B, D | byte-exact | ✅ | n/a | IODControlRes byte-exact as p-net's | — |
| Control ApplicationReady (device → CPU) and the CPU's answer | D, B | byte-exact | ✅ (CPU answers flags1 0x0A) | the device takes the CPU's recorded answer and enters Data | the device's IOXControlReq byte-exact as p-net's | — |
| Cyclic RT_CLASS_1 0x8000/0x8001, 40 octets, 32 ms | B, D | trailer byte-exact | ✅ cycle counter, data status (0x35; p-net once 0x36 "backup") | n/a | device consumes the CPU's frames (BOOL bit order, big-endian DWORD and REAL), its input frame has p-net's VLAN, status and IOxS positions | the CPU puts IOCS after each submodule's data, `rt.ComputeLayout` after all data; both valid |
| Process alarm (high) and its ACK-RTA / AlarmAck | D, B | byte-exact | ✅ | `pnio.AckFor` gives the CPU's AlarmAck byte for byte | device's AlarmNotification byte-exact as p-net's, sequence FFFF/FFFE; takes the CPU's acknowledgements | — |
| Diagnosis alarm (low) and its acknowledgements | D, B | byte-exact | ✅ | `AckFor` byte-exact | device's notification as p-net's, AlarmSpecifier 0xA800 | the AlarmSpecifier sequence number counted per AR (**fixed**: per priority, as p-net and the CPU); p-net sends ExtChannelDiagnosis (USI 0x8002) with zero extensions, go-profinet ChannelDiagnosis (0x8000) |
| ERR-RTA "AR removed" (CPU) and the answers | B, D | byte-exact | ✅ (the CPU puts 18 vendor octets after VarPartLen) | controller answers a device's ERR-RTA | device ends the AR | neither the device nor the controller answered an ERR-RTA (**fixed**: both answer "AR alarm.ind(err)" CF 81 FD 0B, as the CPU does); p-net echoes the peer's status instead |
| Read I&M0 (DAP, interface) | B, D | byte-exact | ✅ I&M0 fields | n/a | not replayed (another session's AR) | — |
| Read Implicit I&M0FilterData (GE device) | C | byte-exact | ✅ | n/a | n/a | — |

Every recorded frame is also a seed of the fuzz targets it reaches
(`testdata/fuzz/*/capture-*` in the packages), and
`internal/capture TestMalformedVariants` runs every truncation and
every 16- and 32-bit field forced to 0 / all ones through the decoders.

## Not covered by any recording

DCP Get, Hello, Signal, ResetToFactory and DHCP; Release; Read and Write of
I&M1–I&M4; plug and pull alarms; RT_CLASS_2/3, MRP, PTCP; device access
ARs; shared devices; system redundancy; SNMP. They stay verified by the
interop suite ([INTEROPERABILITY.md](../../INTEROPERABILITY.md)) or not at
all, as [docs/FEATURE-STATUS.md](../../docs/FEATURE-STATUS.md) states.

## Adding a recording

Commit a raw file only with an explicit licence or publisher statement that
permits it; otherwise reference it in `manifest.json` and
`scripts/fetch-captures.sh` and commit single-frame excerpts. Add each file
to `manifest.json`, `SHA256SUMS` and `coverage.json`, cross-check its
decoding with tshark, and name the device, the peer and the class. Never
change a recorded frame to make a test pass; normalise only the fields the
test names as dynamic (activity UUID, sequence and cycle counters, alarm
endpoint references, the alarm bench's CPU MAC).
