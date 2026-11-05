// SPDX-License-Identifier: MIT

package controller

import (
	"fmt"
	"math"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/model"
	"github.com/otfabric/go-profinet/mrp"
	"github.com/otfabric/go-profinet/pnio"
)

// MRPSetting is the media redundancy role a controller gives a device: it
// writes PDInterfaceMrpDataAdjust to the interface and PDPortMrpDataAdjust
// to the two ring ports before Parameters.
type MRPSetting struct {
	// Role is mrp.RoleClient or mrp.RoleManager (mrp.RoleDisabled turns
	// MRP off).
	Role mrp.Role `json:"role"`
	// Domain is the domain UUID (zero: mrp.DefaultDomain) and DomainName
	// its name (empty: "mrpdomain-1").
	Domain     [16]byte `json:"domain"`
	DomainName string   `json:"domainName,omitempty"`
	// Prio is a manager's priority; zero means mrp.DefaultManagerPrio.
	Prio uint16 `json:"prio,omitempty"`
	// RingPorts are the ring ports, 1-based; zero means 1 and 2.
	RingPorts [2]int `json:"ringPorts"`
}

// mrpParameters returns the parameters that write an MRP setting.
func mrpParameters(m *model.Device, s *MRPSetting) ([]Parameter, error) {
	if s == nil {
		return nil, nil
	}
	if s.Role < mrp.RoleDisabled || s.Role > mrp.RoleManager {
		return nil, fmt.Errorf("controller: %w: MRP role %d", profinet.ErrInvalidConfig, s.Role)
	}
	ports := s.RingPorts
	if ports == [2]int{} {
		ports = [2]int{1, 2}
	}
	if ports[0] == ports[1] {
		return nil, fmt.Errorf("controller: %w: MRP ring ports %v", profinet.ErrInvalidConfig, ports)
	}
	domain := s.Domain
	if domain == ([16]byte{}) {
		domain = mrp.DefaultDomain
	}
	name := s.DomainName
	if name == "" {
		name = "mrpdomain-1"
	}
	// The device side (device.Device) refuses a MRP_DomainName longer
	// than 240 octets, and the encoder would truncate it at 255.
	if len(name) > maxNameLen {
		return nil, fmt.Errorf("controller: %w: MRP domain name of %d octets (at most %d)", profinet.ErrInvalidConfig, len(name), maxNameLen)
	}
	if _, ok := m.Submodule(0, model.SubslotInterface); !ok {
		return nil, fmt.Errorf("controller: %w: MRP needs the interface submodule %#x", profinet.ErrInvalidConfig, model.SubslotInterface)
	}
	a := pnio.PDInterfaceMrpDataAdjust{Domain: domain, Role: uint16(s.Role), DomainName: name}
	if s.Role == mrp.RoleManager {
		prio := s.Prio
		if prio == 0 {
			prio = mrp.DefaultManagerPrio
		}
		a.Manager = &pnio.MrpManagerParams{Prio: prio}
	}
	out := []Parameter{{Slot: 0, Subslot: model.SubslotInterface, Index: pnio.IndexPDInterfaceMrpDataAdjust, Data: a.Append(nil)}}
	if s.Role == mrp.RoleDisabled {
		return out, nil
	}
	for _, p := range ports {
		// Ports beyond the subslot range would wrap onto a port of the
		// model (0x8001 + p-1 modulo 2^16).
		if p < 1 || p > math.MaxUint16-int(model.SubslotPort1)+1 {
			return nil, fmt.Errorf("controller: %w: MRP ring port %d is not a port of the model", profinet.ErrInvalidConfig, p)
		}
		sub := model.SubslotPort1 + uint16(p-1)
		if _, ok := m.Submodule(0, sub); !ok {
			return nil, fmt.Errorf("controller: %w: MRP ring port %d is not a port of the model", profinet.ErrInvalidConfig, p)
		}
		pa := pnio.PDPortMrpData{Domain: domain}
		out = append(out, Parameter{Slot: 0, Subslot: sub, Index: pnio.IndexPDPortMrpDataAdjust, Data: pa.Append(nil)})
	}
	return out, nil
}
