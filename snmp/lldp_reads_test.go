// SPDX-License-Identifier: MIT

package snmp

import (
	"fmt"
	"testing"
)

// TestLLDPRemoteReadOncePerRequest: a request reads the neighbors once,
// whatever its kind and however many of its variable bindings name
// lldpRemTable, and every binding is answered from that reading.
func TestLLDPRemoteReadOncePerRequest(t *testing.T) {
	calls := 0
	m := NewMIB()
	if err := m.AddLLDP(func() LLDPLocal { return LLDPLocal{} }, func() []LLDPRemote {
		calls++
		// The second reading of a request would be told apart by its name.
		name := fmt.Sprintf("plc-%d", calls)
		return []LLDPRemote{
			{TimeMark: 500, LocalPortNum: 1, Index: 1, ChassisIDSubtype: LLDPChassisIDLocal, ChassisID: []byte("plc"), PortIDSubtype: LLDPPortIDLocal, PortID: []byte("port-002.plc"), SysName: name},
			{LocalPortNum: 1, Index: 2, ChassisIDSubtype: LLDPChassisIDLocal, ChassisID: []byte("drive"), PortIDSubtype: LLDPPortIDLocal, PortID: []byte("port-001.drive"), SysName: name},
		}
	}); err != nil {
		t.Fatal(err)
	}
	// Responses of the largest size, so that every binding is answered.
	a := newTestAgent(t, Config{MIB: m, MaxMessageSize: MaxMessageSize})
	sysName := func(filter, port, idx uint32) OID { return OIDLLDPRemTable.Append(1, 9, filter, port, idx) }
	// The kinds of name a Get may carry, and whether the row is there.
	kinds := []struct {
		name  OID
		found bool
	}{
		{sysName(0, 1, 1), true},    // a listed row
		{sysName(1, 1, 1), true},    // the row at a filter value up to its TimeMark
		{sysName(500, 1, 1), true},  // at its TimeMark
		{sysName(501, 1, 1), false}, // after it
		{sysName(0, 1, 2), true},
		{sysName(1, 1, 2), false},                       // a row without a TimeMark shows at filter 0 only
		{sysName(7, 2, 1), false},                       // another port
		{sysName(7, 1, 3), false},                       // another index
		{OIDLLDPRemTable.Append(1, 11, 7, 1, 1), false}, // not a column
		{OIDLLDPRemTable.Append(1, 9, 7, 1), false},     // not a whole instance name
	}
	names := func(n int) []OID {
		out := make([]OID, n)
		for i := range out {
			out[i] = kinds[i%len(kinds)].name
		}
		return out
	}
	// present are names of rows that are there (SNMPv1 ends a Get at the
	// first name that is not).
	present := func(n int) []OID {
		out := make([]OID, n)
		for i := range out {
			out[i] = kinds[i%3].name
		}
		return out
	}
	for _, c := range []struct {
		what    string
		msg     Message
		calls   int
		dropped bool
		// values: the response has a value for every name of the request.
		values bool
	}{
		{"Get without bindings", req(Version2c, PDUGetRequest), 0, false, false},
		{"Get of a name outside the table", req(Version2c, PDUGetRequest, oidSysName), 0, false, false},
		{"Get of one name", req(Version2c, PDUGetRequest, names(1)...), 1, false, true},
		{"Get of one name of a filter value", req(Version2c, PDUGetRequest, sysName(1, 1, 1)), 1, false, true},
		{"Get of one name that is not there", req(Version2c, PDUGetRequest, sysName(501, 1, 1)), 1, false, true},
		{"Get of two names", req(Version2c, PDUGetRequest, names(2)...), 1, false, true},
		{"Get of every kind of name", req(Version2c, PDUGetRequest, names(len(kinds))...), 1, false, true},
		{"Get of MaxVarBinds-1 names", req(Version2c, PDUGetRequest, names(MaxVarBinds-1)...), 1, false, true},
		{"Get of MaxVarBinds names", req(Version2c, PDUGetRequest, names(MaxVarBinds)...), 1, false, true},
		{"Get of MaxVarBinds+1 names", req(Version2c, PDUGetRequest, names(MaxVarBinds+1)...), 0, true, false},
		{"SNMPv1 Get of MaxVarBinds names", req(Version1, PDUGetRequest, present(MaxVarBinds)...), 1, false, true},
		{"SNMPv1 Get that ends at a name that is not there", req(Version1, PDUGetRequest, names(MaxVarBinds)...), 1, false, false},
		{"GetNext of one name", req(Version2c, PDUGetNextRequest, names(1)...), 1, false, false},
		{"GetNext of MaxVarBinds names", req(Version2c, PDUGetNextRequest, names(MaxVarBinds)...), 1, false, false},
		{"GetBulk of MaxVarBinds names", bulk(names(MaxVarBinds), 0, 4), 1, false, false},
	} {
		t.Run(c.what, func(t *testing.T) {
			calls = 0
			r, ok := exchange(t, a, c.msg)
			if calls != c.calls {
				t.Errorf("the neighbors were read %d times, want %d", calls, c.calls)
			}
			if ok == c.dropped {
				t.Fatalf("answered %v, want dropped %v", ok, c.dropped)
			}
			if !c.values {
				return
			}
			in := c.msg.PDU.VarBinds
			if r.PDU.ErrorStatus != NoError || len(r.PDU.VarBinds) != len(in) {
				t.Fatalf("status %v, %d bindings for %d", r.PDU.ErrorStatus, len(r.PDU.VarBinds), len(in))
			}
			for i, vb := range r.PDU.VarBinds {
				found := false
				for _, k := range kinds {
					if k.name.Equal(in[i].Name) {
						found = k.found
					}
				}
				switch {
				case !vb.Name.Equal(in[i].Name):
					t.Fatalf("binding %d is %s, want %s", i, vb.Name, in[i].Name)
				case found && (vb.Value.Type != TypeOctetString || string(vb.Value.Bytes) != "plc-1"):
					t.Fatalf("binding %d (%s): %+v, want the name of the request's one reading", i, vb.Name, vb.Value)
				case !found && vb.Value.Type != TypeNoSuchInstance:
					t.Fatalf("binding %d (%s): %+v, want noSuchInstance", i, vb.Name, vb.Value)
				}
			}
		})
	}
}
