# LLDP

PROFINET devices send LLDP (IEEE 802.1AB, EtherType 0x88CC, to
01:80:C2:00:00:0E) every 5 s with PROFINET TLVs, so that each port knows its
neighbor: the basis of the topology and of the AliasName of a device without
a station name.

```go
a, err := lldp.NewAgent(mux, lldp.AgentOptions{
	Local: func() lldp.LocalInfo {
		return lldp.LocalInfo{StationName: "io-rack-1", PortName: "port-001", Description: "IO rack", IP: ip}
	},
})
neighbors := a.Neighbors() // MAC, decoded frame, expiry
alias := a.Alias()         // "port-002.switch-1": what DCP Identify by alias matches
```

- **Sent**: chassis ID = station name (or the MAC address without one),
  port ID = `port-001.<station>` (the "legacy" form without the station
  with `LocalInfo.Legacy`), TTL, system description, management address,
  and the PROFINET port status and chassis MAC TLVs. The TTL is
  `TTLFactor` × `Interval` (20 s by default: 4 × 5 s), or `TTL` when set.
  A negative `Interval` only listens; `SendNow` sends at once.
- **Received**: the IEEE 802.1AB TLVs, the PROFINET TLVs (OUI 00-0E-CF)
  delay, port status and chassis MAC, and the IEEE 802.3 MAC/PHY TLV are
  decoded; the others (PROFINET alias, MRP port status, PTCP status, other
  organizations) are kept as raw TLVs. A neighbor
  expires after its TTL; a TTL of 0 withdraws it at once.
- The IO-Device runs an agent (`device.Config.DisableLLDP`,
  `LLDPInterval`) that announces a TTL of 21 s with the PROFINET interval
  of 5 s, the default (since v0.25.0; 20 s before); another interval keeps
  the generic four intervals. 21 s is p-net v2.0.0's value, whose release notes give the
  PROFINET test specification as the reason; go-profinet has not read that
  specification. The device reports its neighbors as the peers of its first port
  in PDPortDataReal and PDRealData ([DIAGNOSTICS.md](DIAGNOSTICS.md)).

A Linux bridge does not forward 01:80:C2:00:00:0E by default: set bit 14 of
its `group_fwd_mask` (the interop harness does) or connect the interfaces
directly.
