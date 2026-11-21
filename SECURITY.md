# Security Policy

## Reporting a Vulnerability

Please report vulnerabilities privately: email **security@otfabric.io**, or
open a private advisory on GitHub (the
[Security](https://github.com/otfabric/go-profinet/security) tab →
**Advisories** → **Report a vulnerability**), with a description, steps to
reproduce, the affected version and the impact you identified. Do not open a
public issue.

We acknowledge reports within 48 hours and aim to fix or mitigate critical
issues within 7 days.

## Supported Versions

| Version | Supported |
|---------|-----------|
| Latest release | Yes |
| Older releases | No |

## Security considerations

### PROFINET has no security of its own

PROFINET IO without the security classes (which go-profinet does not
implement) has no authentication, integrity protection or encryption.
Anyone with Layer 2 access to a device can rename it, readdress it, reset it,
write its records and take control of its outputs; anyone on the path can
read and alter the cyclic data. go-profinet does not change that.

- Run PROFINET on isolated networks; separate them from office networks and
  the Internet with the zones and conduits of IEC 62443.
- Treat every host that runs go-profinet with raw Ethernet access as part of
  the control network.

### Privileges

Raw Ethernet needs CAP_NET_RAW (Linux), access to `/dev/bpf*` (macOS) or
Npcap (Windows). Grant the capability to the program, not root to the user;
do not grant it on shared hosts. The virtual network (`vnet`) needs none.

### Hardening of the implementation

- Every decoder of network input bounds counts and lengths before it
  allocates, rejects trailing or inconsistent data and is fuzzed
  (`make fuzz`); malformed input is dropped and counted, never a panic.
- Resources are bounded: RPC messages (64 KiB, 256 fragments), RPC
  activities and handler workers, ARs per device, subscription queues, alarm
  windows, GSDML documents (64 MiB and 1 048 576 elements; `encoding/xml`
  fetches no DTD and expands no entity beyond the predefined five, and
  `gsdml.Parse` refuses an internal DTD subset, whose declarations it
  cannot apply) and the parameter records a model gets from one
  (16 MiB, `gsdml.MaxModelRecordOctets`). A Connect for an ARUUID whose
  Connect still runs is refused; the RPC client takes an answer only from
  the host it called.
- Fixed bounds on what files and the network can make the library keep or
  compute (each is a constant in the package named; none is configurable
  unless said):
  - `gsdml`: a document has at most 1 048 576 elements (start tags,
    counted before decoding; `profinet.ErrResourceExhausted` beyond). A
    parsed document of that size occupies up to about 300 MiB. `Check`
    lists at most 16 384 issues per severity plus one that counts the rest
    (rule `limit`); its work follows the size of the document, not the
    ranges in its slot and reduction ratio lists. `ToModel` charges
    `MaxModelRecordOctets` for the module types and again for the modules
    as plugged, once per slot, and refuses a module or device access point
    with more than 1440 submodules.
  - `model`: `Validate` refuses more than 1440 plugged submodules and more
    than 16 MiB of parameter records in them (`profinet.ErrInvalidConfig`),
    before any work per submodule, so `device.New` and
    `Controller.AddDevice` never clone or write more.
  - `gsdml/gsdx`: `Open` decodes at most 65 536 XML elements into the
    package model (16 per ZIP entry where `MaxParts` allows more entries
    than the default), lists at most 16 384 issues plus a count, and
    parses a certificate part once.
  - `dcp`: an Identify without `IdentifyOptions.Max` collects at most 4096
    stations and ends with `profinet.ErrResourceExhausted` when one more
    answers; `Max` replaces the bound.
  - `netstack`: the ARP table holds at most 1024 addresses; a full table
    learns a new address only from traffic for the stack itself, in place
    of the entry used longest ago.
  - `pnio`: the MultipleBlockHeaders of a PDRealData record nest at most
    four deep; deeper records are refused as malformed.
  - `snmp`: a request reads the LLDP neighbors once, whatever its kind and
    number of variable bindings.
- Concurrency is race-tested (`go test -race`) and chaos-tested with seeded
  loss, duplication, corruption, reordering and delay.
- The DCP responder answers every requester. While one requester holds an
  open Set transaction (Control/Start Transaction without End Transaction,
  at most 3 s after its last Set), the Sets of other requesters are
  answered with BlockError "resource error" and change nothing. It
  validates station names.
- Opt-in DCP restrictions (`dcp.Policy`, `device.Config.DCPPolicy`): a
  read-only mode that refuses every Set that changes the device, rate
  limits per source MAC address for Identify responses and Set requests in
  a fixed-size table, and audit events (source, block, outcome) for every
  Set, ResetToFactory and refused request. They limit accidents and floods;
  they authenticate nobody (a source address can be forged) and are not a
  PROFINET security class.

### Changing devices

`dcp.Client.Set`, `SetName`, `SetIP`, `ResetToFactory`, `RecordClient.Write`
and the outputs of a controller change real equipment; their documentation
says so. Only call them on an explicit request of the user.

### Not certified

go-profinet is not certified by PROFIBUS & PROFINET International and is not
a safety component.

### Reference implementations

The interop suite runs GPL-licensed reference implementations in containers.
The p-net tree used is older than its v1.0.2 security fixes: run the interop
suite only on isolated Docker networks.
