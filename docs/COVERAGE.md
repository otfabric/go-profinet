# Coverage

`make coverage-check` measures the statement coverage of the library
packages across all their tests (the controller and device tests and the
end-to-end suite exercise each other's packages: `-coverpkg` is the list of
library packages) and fails below 90 %. The raw Ethernet backends need
privileges and are measured by the real Layer 2 jobs; the examples, the
generator command, the e2e package and the interop tool are not library
code.

Measured on 2026-11-22 on main at 6b313e7 (v0.12.0; the commit after it
changes documentation only) with `make coverage`, the race detector and Go
1.27.4 on macOS arm64; `scripts/coverage-by-package.sh coverage.out` prints
the table. The total was 97.2 % at v0.8.1 (2026-11-13) and 97.1 % at v0.6.0
(2026-10-29).

| Package | Coverage |
|---------|----------|
| profinet (root) | 100.0% |
| alarm | 100.0% |
| controller | 97.3% |
| dcp | 99.1% |
| device | 96.6% |
| ethernet | 100.0% |
| gsdml | 96.1% |
| gsdml/gsdx | 98.5% |
| internal/capture | 98.5% |
| internal/leakcheck | 97.8% |
| internal/xsd | 91.1% |
| l2 | 100.0% |
| l2/bridge | 93.2% |
| l2/osport | 93.8% |
| lldp | 99.3% |
| model | 100.0% |
| mrp | 94.3% |
| netstack | 99.5% |
| pcap | 100.0% |
| pnio | 98.6% |
| ptcp | 100.0% |
| rpc | 98.8% |
| rt | 98.9% |
| snmp | 99.9% |
| store | 97.6% |
| vnet | 100.0% |
| **total** | **97.7%** |

Targets: 90 % overall, 95 % for the codec packages (`ethernet`, `dcp`,
`lldp`, `rpc`, `pnio`, `rt`, `alarm`); every codec package is above 98 %.

What is not covered is mostly defensive (in `mrp` and `l2/bridge` also
timer races that the ring tests reach only some of the time): the generated GSDML validators'
nil-receiver guards, error paths of the operating system (`l2/osport` on
interfaces it cannot open), and errors the XSD reader of `internal/xsd`
only meets in malformed schemas.

Fuzzing complements coverage for the decoders: 40 fuzz targets run in CI
(`make fuzz FUZZTIME=10s`) and locally for longer.
