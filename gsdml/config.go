// SPDX-License-Identifier: MIT

package gsdml

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/model"
)

// A configuration of a device access point: the modules plugged in its
// slots and the configurable submodules (UseableSubmodules) chosen for the
// subslots of the DAP and of each plugged module. The document describes
// what may be configured; Resolve turns the choices into the model.Device
// of what is plugged.
//
// The reading of the subslot attributes of a SubmoduleItemRef is the one
// ToModel applies to the slot attributes of a ModuleItemRef
// (configuredSlots), not checked against PI's specification or an
// engineering tool (LIMITATIONS.md):
//
//   - FixedInSubslots: the submodule is in these subslots in every
//     configuration; it cannot be removed, replaced or moved.
//   - UsedInSubslots (when there is no FixedInSubslots): the submodule is
//     in these subslots by default; another submodule allowed there may
//     replace it, and the subslot may be left empty.
//   - AllowedInSubslots: where the submodule may be chosen. Without it,
//     the subslots of FixedInSubslots and UsedInSubslots (a policy of this
//     package: the file states no other place).
//   - PhysicalSubslots of the DAP or module, when given, bound every
//     subslot a configurable submodule is placed in (the virtual and
//     built-in submodules are not checked against it, as most files of the
//     local corpus list only the configurable subslots there).

// SubmoduleOption is a configurable submodule a device access point or a
// module can use: a SubmoduleItemRef of its UseableSubmodules and the
// SubmoduleItem or PortSubmoduleItem of the SubmoduleList it names.
type SubmoduleOption struct {
	// ID is the reference's SubmoduleItemTarget: the ID of the item.
	ID string `json:"id"`
	// Port reports a PortSubmoduleItem: a port of the DAP's interface.
	// It has no IO data and no records here; its PDev records are the
	// controller's, as for a built-in port.
	Port bool `json:"port,omitempty"`
	// Submodule is the submodule as the model holds it, converted as
	// ToModel converts a VirtualSubmoduleItem (name, ident number, API, IO
	// data lengths, parameter records with their defaults), with Subslot
	// 0: the configuration places it.
	Submodule model.Submodule `json:"submodule"`
	// AllowedInSubslots, UsedInSubslots and FixedInSubslots are the
	// reference's attributes as the file gives them.
	AllowedInSubslots string `json:"allowed_in_subslots,omitempty"`
	UsedInSubslots    string `json:"used_in_subslots,omitempty"`
	FixedInSubslots   string `json:"fixed_in_subslots,omitempty"`
	// Defaults are the subslots the default configuration puts the
	// submodule in (FixedInSubslots, else UsedInSubslots); Fixed reports
	// that they are fixed.
	Defaults []uint16 `json:"defaults,omitempty"`
	Fixed    bool     `json:"fixed,omitempty"`
	// Err is why the submodule cannot be placed, nil when it can, and
	// Error its text: a reference to no item of the SubmoduleList, an item
	// that does not convert (ErrMalformed, ErrUnsupported,
	// ErrResourceExhausted, as ToModel classifies them), a DAP submodule
	// of an API other than 0 or a port of a module (ErrUnsupported).
	Err   error  `json:"-"`
	Error string `json:"error,omitempty"`

	allowed []listRange
}

// Allows reports whether the submodule may be chosen for a subslot:
// AllowedInSubslots, or without it FixedInSubslots and UsedInSubslots.
// The owner's PhysicalSubslots and the subslots of other submodules are
// Select's to check.
func (o *SubmoduleOption) Allows(subslot uint16) bool {
	return inList(o.allowed, uint32(subslot))
}

// clone is a copy of o that shares nothing a caller could change.
func (o *SubmoduleOption) clone() SubmoduleOption {
	c := *o
	c.Submodule = cloneSubmodule(o.Submodule)
	c.Defaults = slices.Clone(o.Defaults)
	return c
}

// ModuleOption is a module a device access point can use: a ModuleItemRef
// of its UseableModules and the ModuleItem it names.
type ModuleOption struct {
	// ID is the reference's ModuleItemTarget.
	ID string `json:"id"`
	// Module is the module with its virtual submodules, as ToModel
	// converts it; the configurable ones are in Submodules.
	Module model.Module `json:"module"`
	// AllowedInSlots, UsedInSlots and FixedInSlots are the reference's
	// attributes, PhysicalSubslots the module's, as the file gives them.
	AllowedInSlots   string `json:"allowed_in_slots,omitempty"`
	UsedInSlots      string `json:"used_in_slots,omitempty"`
	FixedInSlots     string `json:"fixed_in_slots,omitempty"`
	PhysicalSubslots string `json:"physical_subslots,omitempty"`
	// Submodules are the module's configurable submodules, in file order.
	Submodules []SubmoduleOption `json:"submodules,omitempty"`
	// Err is why the module cannot be plugged with its configurable
	// submodules, nil when it can, and Error its text: configurable
	// submodules whose default subslots collide with each other or with
	// the virtual submodules, or lie outside PhysicalSubslots
	// (ErrMalformed).
	Err   error  `json:"-"`
	Error string `json:"error,omitempty"`
}

// owner is the DAP or a module type with its configurable submodules.
type owner struct {
	// name names it in errors: "DAP X" or "module X".
	name string
	dap  bool
	// builtin are the subslots of the virtual (and, for the DAP,
	// system-defined) submodules, by subslot, with their names.
	builtin map[uint16]string
	// physical is PhysicalSubslots; none when hasPhysical is false.
	physical    []listRange
	hasPhysical bool
	opts        []*SubmoduleOption
	byID        map[string]*SubmoduleOption
	// fixed is the submodule fixed in each subslot; used the submodules
	// used in each subslot that is not fixed, in file order.
	fixed map[uint16]*SubmoduleOption
	used  map[uint16][]*SubmoduleOption
	// err is why the owner's configurable submodules cannot be resolved:
	// their default placements do not agree with the file.
	err error
}

// moduleOpt is a module reference of the DAP.
type moduleOpt struct {
	opt   ModuleOption
	owner *owner
	// fixedSlots are the slots the reference fixes the module in.
	fixedSlots []uint32
}

// instance is a module plugged in a slot, with the choices made for its
// subslots.
type instance struct {
	mod *moduleOpt
	sel map[uint16]*SubmoduleOption
}

// Configuration is a configuration of one device access point of a
// document: what NewConfiguration makes by default, changed by Plug,
// Unplug, Select and ClearSubslot, and turned into a model by Resolve.
// The default configuration plugs the modules ToModel plugs, in the slots
// it plugs them in, and places each configurable submodule in the
// subslots it is fixed or used in. Each plugged module has its own
// choices: two slots with the same module may hold different submodules.
//
// A Configuration reads the document it was made from at every call; the
// document must not change while the Configuration is used. It is not
// safe for concurrent use.
type Configuration struct {
	doc     *Document
	dap     *DeviceAccessPointItem
	base    *model.Device
	dapOpts *owner
	dapSel  map[uint16]*SubmoduleOption
	modules []*moduleOpt
	slots   map[uint16]*instance
}

// NewConfiguration makes the default configuration of the device access
// point dapID (the first one when empty) of a document.
//
// It returns the errors ToModel returns for the DAP and the modules it can
// use, except that configurable submodules are not refused:
// profinet.ErrNotFound for a DAP the document does not have,
// profinet.ErrMalformed for content that does not convert, for a
// reference whose subslot attributes are not ValueLists of subslot numbers
// (1 to 65535), and for configurable submodules of the DAP whose default
// subslots collide or lie outside its PhysicalSubslots, and
// profinet.ErrResourceExhausted beyond MaxModelRecordOctets. A configurable
// submodule or a module that cannot be placed is not an error here: its
// option carries why, and choosing it, or a default configuration that
// needs it, fails.
func NewConfiguration(doc *Document, dapID string) (*Configuration, error) {
	c := newConv(doc)
	base, dap, err := c.dapBase(dapID)
	if err != nil {
		return nil, err
	}
	cfg := &Configuration{doc: doc, dap: dap, base: base, slots: map[uint16]*instance{}}
	ap := doc.ProfileBody.ApplicationProcess
	// The modules first, as ToModel converts them, then the submodules of
	// the SubmoduleList: a file ToModel converts is not refused for the
	// record octets of a configurable submodule.
	var refs []*ModuleItemRef
	if dap.UseableModules != nil {
		refs = dap.UseableModules.ModuleItemRefs
	}
	modules := map[string]*ModuleItem{}
	if ap.ModuleList != nil {
		for _, mi := range ap.ModuleList.ModuleItems {
			modules[collapseSpace(mi.ID)] = mi
		}
	}
	converted := map[string]model.Module{}
	items := make([]*ModuleItem, len(refs))
	for i, r := range refs {
		mi := modules[collapseSpace(r.ModuleItemTarget)]
		if mi == nil {
			return nil, malformed("ModuleItemRef %q: no such module", r.ModuleItemTarget)
		}
		items[i] = mi
		if _, ok := converted[r.ModuleItemTarget]; !ok {
			mod, err := c.toModule(mi)
			if err != nil {
				return nil, err
			}
			converted[r.ModuleItemTarget] = mod
		}
	}
	subs := submoduleItems(ap)
	cache := map[string]*SubmoduleOption{}
	if cfg.dapOpts, err = c.newOwner("DAP "+clip(dap.ID), true, dap.UseableSubmodules, dap.PhysicalSubslots, emptyIn(dap.empty, "PhysicalSubslots"), base.DAP.Submodules, subs, cache); err != nil {
		return nil, err
	}
	if cfg.dapOpts.err != nil {
		return nil, cfg.dapOpts.err
	}
	owners := map[*ModuleItem]*owner{}
	used := map[uint16]bool{}
	for i, r := range refs {
		mi := items[i]
		ow := owners[mi]
		if ow == nil {
			if ow, err = c.newOwner("module "+clip(mi.ID), false, mi.UseableSubmodules, mi.PhysicalSubslots, emptyIn(mi.empty, "PhysicalSubslots"), converted[r.ModuleItemTarget].Submodules, subs, cache); err != nil {
				return nil, err
			}
			owners[mi] = ow
		}
		list, err := configuredSlots(r)
		if err != nil {
			return nil, err
		}
		mo := &moduleOpt{owner: ow, opt: ModuleOption{ID: r.ModuleItemTarget, Module: converted[r.ModuleItemTarget],
			AllowedInSlots: r.AllowedInSlots, UsedInSlots: r.UsedInSlots, FixedInSlots: r.FixedInSlots, PhysicalSubslots: mi.PhysicalSubslots, Err: ow.err, Error: errText(ow.err)}}
		if r.FixedInSlots != "" {
			// configuredSlots parsed it.
			mo.fixedSlots, _ = ParseValueList(r.FixedInSlots, 4096)
		}
		cfg.modules = append(cfg.modules, mo)
		// The slots as ToModel plugs them: the first reference to a slot
		// takes it.
		for _, s := range list {
			if s == 0 || s > 0x7FFF || used[uint16(s)] {
				continue
			}
			used[uint16(s)] = true
			cfg.slots[uint16(s)] = &instance{mod: mo}
		}
	}
	return cfg, nil
}

// submoduleItem is an item of the SubmoduleList.
type submoduleItem struct {
	sub  *SubmoduleItem
	port *PortSubmoduleItem
}

// submoduleItems indexes the SubmoduleList by ID (collapsed): the last
// item of an ID, as ToModel takes the last module of an ID.
func submoduleItems(ap *ApplicationProcess) map[string]submoduleItem {
	out := map[string]submoduleItem{}
	if sl := ap.SubmoduleList; sl != nil {
		for _, it := range sl.SubmoduleItems {
			out[collapseSpace(it.ID)] = submoduleItem{sub: it}
		}
		for _, it := range sl.PortSubmoduleItems {
			out[collapseSpace(it.ID)] = submoduleItem{port: it}
		}
	}
	return out
}

// asVirtual is a SubmoduleItem as the VirtualSubmoduleItem toSubmodule
// converts: the attributes and children they share. A SubmoduleItem has
// no FixedInSubslots; its subslot is the reference's.
func asVirtual(it *SubmoduleItem) *VirtualSubmoduleItem {
	return &VirtualSubmoduleItem{ID: it.ID, SubmoduleIdentNumber: it.SubmoduleIdentNumber, API: it.API,
		MayIssueProcessAlarm: it.MayIssueProcessAlarm, WriteableIMRecords: it.WriteableIMRecords,
		IOData: it.IOData, RecordDataList: it.RecordDataList, ModuleInfo: it.ModuleInfo, Graphics: it.Graphics, empty: it.empty}
}

// convertItem converts the item a reference names, once per ID.
func (c *conv) convertItem(target string, subs map[string]submoduleItem, cache map[string]*SubmoduleOption) *SubmoduleOption {
	k := collapseSpace(target)
	if o := cache[k]; o != nil {
		return o
	}
	o := &SubmoduleOption{ID: target}
	it, ok := subs[k]
	switch {
	case !ok:
		o.Err = malformed("SubmoduleItemRef %q: no SubmoduleItem or PortSubmoduleItem with this ID in the SubmoduleList", clip(target))
	case it.sub != nil:
		o.Submodule, o.Err = c.toSubmodule(asVirtual(it.sub), 0)
	default:
		o.Port = true
		o.Submodule, o.Err = c.systemSubmodule(it.port.TextID, "0", it.port.SubmoduleIdentNumber)
	}
	o.Submodule.Subslot = 0
	cache[k] = o
	return o
}

// newOwner makes the configurable submodules of the DAP or a module:
// builtin are its virtual and system-defined submodules. A reference
// whose subslot attributes do not parse, or a PhysicalSubslots that does
// not, is an error; default placements that do not agree are the owner's
// err.
func (c *conv) newOwner(name string, dap bool, us *UseableSubmodules, physical string, physicalEmpty bool, builtin []model.Submodule,
	subs map[string]submoduleItem, cache map[string]*SubmoduleOption) (*owner, error) {
	ow := &owner{name: name, dap: dap, builtin: map[uint16]string{}, byID: map[string]*SubmoduleOption{},
		fixed: map[uint16]*SubmoduleOption{}, used: map[uint16][]*SubmoduleOption{}}
	for _, s := range builtin {
		ow.builtin[s.Subslot] = s.Name
	}
	if physical != "" || physicalEmpty {
		r, err := parseRanges(physical)
		if err != nil || physicalEmpty {
			return nil, malformed("%s PhysicalSubslots %q", name, clip(physical))
		}
		ow.physical, ow.hasPhysical = r, true
	}
	if us == nil {
		return ow, nil
	}
	if len(us.SubmoduleItemRefs) > maxModuleSubmodules {
		return nil, malformed("%s has %d configurable submodules, more than the %d a C_SDU carries", name, len(us.SubmoduleItemRefs), maxModuleSubmodules)
	}
	for _, ref := range us.SubmoduleItemRefs {
		// The checks of ToModel: no empty attribute, ValueLists.
		if _, err := configuredSubslots(ref); err != nil {
			return nil, fmt.Errorf("gsdml: %s: %w", name, err)
		}
		fixed, err := subslotList(ref.FixedInSubslots, ref.SubmoduleItemTarget, "FixedInSubslots")
		if err != nil {
			return nil, fmt.Errorf("gsdml: %s: %w", name, err)
		}
		usedIn, err := subslotList(ref.UsedInSubslots, ref.SubmoduleItemTarget, "UsedInSubslots")
		if err != nil {
			return nil, fmt.Errorf("gsdml: %s: %w", name, err)
		}
		o := c.convertItem(ref.SubmoduleItemTarget, subs, cache).clone()
		o.AllowedInSubslots, o.UsedInSubslots, o.FixedInSubslots = ref.AllowedInSubslots, ref.UsedInSubslots, ref.FixedInSubslots
		switch {
		case o.Err != nil:
		case dap && o.Submodule.API != model.API:
			o.Err = fmt.Errorf("gsdml: %w: %s submodule %s of API %#x: the model's DAP submodules are API 0", profinet.ErrUnsupported, name, clip(ref.SubmoduleItemTarget), o.Submodule.API)
		case !dap && o.Port:
			o.Err = fmt.Errorf("gsdml: %w: %s: port submodule %s: the model has ports in the DAP only", profinet.ErrUnsupported, name, clip(ref.SubmoduleItemTarget))
		}
		o.Error = errText(o.Err)
		if ref.AllowedInSubslots != "" {
			// configuredSubslots parsed it as a ValueList.
			o.allowed, _ = parseRanges(ref.AllowedInSubslots)
		} else {
			for _, s := range append(slices.Clone(fixed), usedIn...) {
				o.allowed = append(o.allowed, listRange{uint32(s), uint32(s)})
			}
		}
		o.Defaults, o.Fixed = fixed, len(fixed) > 0
		if !o.Fixed {
			o.Defaults = usedIn
		}
		k := collapseSpace(ref.SubmoduleItemTarget)
		if _, dup := ow.byID[k]; !dup {
			ow.byID[k] = &o
		}
		ow.opts = append(ow.opts, &o)
	}
	n := 0
	for _, o := range ow.opts {
		n += len(o.Defaults)
	}
	if n > maxDefaultPlacements {
		ow.err = fmt.Errorf("gsdml: %w: %s: %d default subslots of configurable submodules, more than %d", profinet.ErrResourceExhausted, name, n, maxDefaultPlacements)
		return ow, nil
	}
	ow.err = ow.place()
	return ow, nil
}

// maxDefaultPlacements bounds the default subslots (FixedInSubslots,
// UsedInSubslots) of the configurable submodules of one DAP or module
// that NewConfiguration places: the number of subslot numbers. Beyond it
// the owner cannot be resolved (ErrResourceExhausted).
const maxDefaultPlacements = 1 << 16

// place computes the default placements: every fixed subslot, then the
// used ones. A subslot two references fix, a default subslot of a virtual
// or system-defined submodule, a used subslot that is also fixed, and a
// default subslot outside PhysicalSubslots are errors of the file.
// Several references used in one subslot are not: the configuration must
// choose one (Resolve).
func (ow *owner) place() error {
	for _, o := range ow.opts {
		if !o.Fixed {
			continue
		}
		for _, s := range o.Defaults {
			if err := ow.defaultSubslot(o, s, "fixed"); err != nil {
				return err
			}
			if prev := ow.fixed[s]; prev != nil && prev != o {
				return malformed("%s: subslot %d: submodules %s and %s are both fixed there", ow.name, s, clip(prev.ID), clip(o.ID))
			}
			ow.fixed[s] = o
		}
	}
	for _, o := range ow.opts {
		if o.Fixed {
			continue
		}
		for _, s := range o.Defaults {
			if err := ow.defaultSubslot(o, s, "used"); err != nil {
				return err
			}
			if f := ow.fixed[s]; f != nil {
				return malformed("%s: subslot %d: submodule %s is used where submodule %s is fixed", ow.name, s, clip(o.ID), clip(f.ID))
			}
			if !slices.Contains(ow.used[s], o) {
				ow.used[s] = append(ow.used[s], o)
			}
		}
	}
	return nil
}

func (ow *owner) defaultSubslot(o *SubmoduleOption, s uint16, how string) error {
	if name, ok := ow.builtin[s]; ok {
		return malformed("%s: subslot %d: submodule %s is %s where the built-in submodule %q is", ow.name, s, clip(o.ID), how, clip(name))
	}
	if ow.hasPhysical && !inList(ow.physical, uint32(s)) {
		return malformed("%s: subslot %d: submodule %s is %s outside the PhysicalSubslots", ow.name, s, clip(o.ID), how)
	}
	return nil
}

// subslotList parses FixedInSubslots or UsedInSubslots: subslot numbers 1
// to 65535.
func subslotList(list, target, attr string) ([]uint16, error) {
	if list == "" {
		return nil, nil
	}
	l, err := ParseValueList(list, 4096)
	if err != nil {
		return nil, fmt.Errorf("gsdml: submodule %s %s: %w", clip(target), attr, err)
	}
	out := make([]uint16, 0, len(l))
	for _, v := range l {
		if v == 0 || v > 0xFFFF {
			return nil, malformed("submodule %s %s: %d is no subslot number (1 to 65535)", clip(target), attr, v)
		}
		if !slices.Contains(out, uint16(v)) {
			out = append(out, uint16(v))
		}
	}
	return out, nil
}

// DAP returns the configurable submodules of the device access point, in
// file order.
func (c *Configuration) DAP() []SubmoduleOption { return c.dapOpts.options() }

func (ow *owner) options() []SubmoduleOption {
	out := make([]SubmoduleOption, 0, len(ow.opts))
	for _, o := range ow.opts {
		out = append(out, o.clone())
	}
	return out
}

// Modules returns the modules the device access point can use, one per
// ModuleItemRef, in file order.
func (c *Configuration) Modules() []ModuleOption {
	out := make([]ModuleOption, 0, len(c.modules))
	for _, mo := range c.modules {
		o := mo.opt
		o.Module = cloneModule(mo.opt.Module)
		o.Submodules = mo.owner.options()
		out = append(out, o)
	}
	return out
}

// Module returns the ID of the module plugged in a slot.
func (c *Configuration) Module(slot uint16) (string, bool) {
	if in := c.slots[slot]; in != nil {
		return in.mod.opt.ID, true
	}
	return "", false
}

// Slots returns the slots that hold a module, in ascending order.
func (c *Configuration) Slots() []uint16 {
	out := make([]uint16, 0, len(c.slots))
	for s := range c.slots {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// invalid is an error matching profinet.ErrInvalidConfig.
func invalid(format string, a ...any) error {
	return fmt.Errorf("gsdml: %w: "+format, append([]any{profinet.ErrInvalidConfig}, a...)...)
}

// moduleByID returns the first reference to the module id.
func (c *Configuration) moduleByID(id string) *moduleOpt {
	k := collapseSpace(id)
	for _, mo := range c.modules {
		if collapseSpace(mo.opt.ID) == k {
			return mo
		}
	}
	return nil
}

// Plug plugs the module the DAP can use with ID moduleID into a slot, with
// its default submodules; a module the slot held is replaced, with the
// choices made for it. The slot must be 1 to 0x7FFF, in the DAP's
// PhysicalSlots and in the reference's AllowedInSlots (when it gives them),
// and must not hold a module fixed there (FixedInSlots) unless it is the
// same module. A module the DAP cannot use, a slot outside those, and a
// module that cannot be plugged (ModuleOption.Err) are errors matching
// profinet.ErrInvalidConfig; the configuration is then unchanged.
func (c *Configuration) Plug(slot uint16, moduleID string) error {
	mo := c.moduleByID(moduleID)
	if mo == nil {
		return invalid("device access point %s cannot use module %q (UseableModules)", clip(c.dap.ID), clip(moduleID))
	}
	return c.plug(slot, mo)
}

// PlugIdent is Plug for the module with ModuleIdentNumber ident. Two
// modules of the ident are an error matching profinet.ErrInvalidConfig
// that names them: plug one by ID.
func (c *Configuration) PlugIdent(slot uint16, ident uint32) error {
	var found *moduleOpt
	for _, mo := range c.modules {
		if mo.opt.Module.IdentNumber != ident {
			continue
		}
		if found != nil && collapseSpace(found.opt.ID) != collapseSpace(mo.opt.ID) {
			return invalid("modules %s and %s have ModuleIdentNumber 0x%08X: plug one by ID", clip(found.opt.ID), clip(mo.opt.ID), ident)
		}
		if found == nil {
			found = mo
		}
	}
	if found == nil {
		return invalid("device access point %s can use no module 0x%08X (UseableModules)", clip(c.dap.ID), ident)
	}
	return c.plug(slot, found)
}

func (c *Configuration) plug(slot uint16, mo *moduleOpt) error {
	if slot == 0 || slot > 0x7FFF {
		return invalid("slot %d: modules go in slots 1 to 0x7FFF (slot 0 is the DAP's)", slot)
	}
	var set slotSet
	if set.parse(c.dap.PhysicalSlots) && !set.has(slot) {
		return invalid("slot %d is not in the PhysicalSlots %q of device access point %s", slot, clip(c.dap.PhysicalSlots), clip(c.dap.ID))
	}
	if a := mo.opt.AllowedInSlots; a != "" && set.parse(a) && !set.has(slot) {
		return invalid("module %s is not allowed in slot %d (AllowedInSlots %q)", clip(mo.opt.ID), slot, clip(a))
	}
	if err := c.notFixed(slot, mo); err != nil {
		return err
	}
	if mo.owner.err != nil {
		return fmt.Errorf("gsdml: %w: slot %d: module %s cannot be plugged: %w", profinet.ErrInvalidConfig, slot, clip(mo.opt.ID), mo.owner.err)
	}
	c.slots[slot] = &instance{mod: mo}
	return nil
}

// notFixed refuses to replace (with next, or to empty with nil) the module
// of a slot it is fixed in.
func (c *Configuration) notFixed(slot uint16, next *moduleOpt) error {
	in := c.slots[slot]
	if in == nil || !slices.Contains(in.mod.fixedSlots, uint32(slot)) {
		return nil
	}
	if next != nil && collapseSpace(next.opt.ID) == collapseSpace(in.mod.opt.ID) {
		return nil
	}
	return invalid("slot %d: module %s is fixed there (FixedInSlots %q)", slot, clip(in.mod.opt.ID), clip(in.mod.opt.FixedInSlots))
}

// Unplug empties a slot. A slot without a module, and one whose module is
// fixed there, are errors matching profinet.ErrInvalidConfig.
func (c *Configuration) Unplug(slot uint16) error {
	if c.slots[slot] == nil {
		return invalid("slot %d holds no module", slot)
	}
	if err := c.notFixed(slot, nil); err != nil {
		return err
	}
	delete(c.slots, slot)
	return nil
}

// target returns the owner and the choices of a slot: the DAP for slot 0.
func (c *Configuration) target(slot uint16) (*owner, *map[uint16]*SubmoduleOption, error) {
	if slot == 0 {
		return c.dapOpts, &c.dapSel, nil
	}
	in := c.slots[slot]
	if in == nil {
		return nil, nil, invalid("slot %d holds no module", slot)
	}
	return in.mod.owner, &in.sel, nil
}

// Select puts the configurable submodule submoduleID of the module in a
// slot (of the DAP for slot 0) in a subslot, in place of the submodule
// the subslot holds by default (UsedInSubslots). It returns an error
// matching profinet.ErrInvalidConfig, and leaves the configuration
// unchanged, for a slot without a module, a submodule the module's
// UseableSubmodules do not name, subslot 0, the subslot of a virtual or
// system-defined submodule, a subslot another submodule is fixed in, a
// subslot the submodule is not allowed in (SubmoduleOption.Allows) or
// outside the PhysicalSubslots; and the option's Err for a submodule that
// cannot be placed.
//
// The choices belong to the module plugged in the slot: plugging another
// module, or the same one again, starts from its defaults.
func (c *Configuration) Select(slot, subslot uint16, submoduleID string) error {
	ow, sel, err := c.target(slot)
	if err != nil {
		return err
	}
	o := ow.byID[collapseSpace(submoduleID)]
	if o == nil {
		return invalid("slot %d: %s has no configurable submodule %q (UseableSubmodules)", slot, ow.name, clip(submoduleID))
	}
	if o.Err != nil {
		return fmt.Errorf("gsdml: slot %d subslot %d: %s submodule %s: %w", slot, subslot, ow.name, clip(o.ID), o.Err)
	}
	if err := ow.free(slot, subslot, o); err != nil {
		return err
	}
	if ow.fixed[subslot] == o {
		return nil // fixed there already
	}
	if !o.Allows(subslot) {
		allowed := o.AllowedInSubslots
		if allowed == "" {
			allowed = o.FixedInSubslots + " " + o.UsedInSubslots
		}
		return invalid("slot %d: %s submodule %s is not allowed in subslot %d (allowed: %q)", slot, ow.name, clip(o.ID), subslot, clip(strings.TrimSpace(allowed)))
	}
	if ow.hasPhysical && !inList(ow.physical, uint32(subslot)) {
		return invalid("slot %d: subslot %d is not among the PhysicalSubslots of %s", slot, subslot, ow.name)
	}
	if *sel == nil {
		*sel = map[uint16]*SubmoduleOption{}
	}
	(*sel)[subslot] = o
	return nil
}

// free checks that a subslot can take a chosen submodule o (nil to empty
// it): not 0, not built in, not fixed to another submodule.
func (ow *owner) free(slot, subslot uint16, o *SubmoduleOption) error {
	if subslot == 0 {
		return invalid("slot %d: subslot 0 holds no submodule", slot)
	}
	if name, ok := ow.builtin[subslot]; ok {
		return invalid("slot %d subslot %d holds the built-in submodule %q of %s", slot, subslot, clip(name), ow.name)
	}
	if f := ow.fixed[subslot]; f != nil && f != o {
		return invalid("slot %d subslot %d: submodule %s of %s is fixed there (FixedInSubslots %q)", slot, subslot, clip(f.ID), ow.name, clip(f.FixedInSubslots))
	}
	return nil
}

// ClearSubslot leaves a subslot of the module in a slot (of the DAP for
// slot 0) empty, removing the submodule it holds by default
// (UsedInSubslots) or by a Select. A slot without a module, subslot 0,
// the subslot of a virtual or system-defined submodule and a subslot a
// submodule is fixed in are errors matching profinet.ErrInvalidConfig.
func (c *Configuration) ClearSubslot(slot, subslot uint16) error {
	ow, sel, err := c.target(slot)
	if err != nil {
		return err
	}
	if err := ow.free(slot, subslot, nil); err != nil {
		return err
	}
	if *sel == nil {
		*sel = map[uint16]*SubmoduleOption{}
	}
	(*sel)[subslot] = nil
	return nil
}

// Resolve returns the model of the configuration: the DAP's identity,
// capabilities and submodules as ToModel builds them, with the chosen
// configurable submodules after the DAP's (a configurable port counts in
// Capabilities.Ports); the plugged modules with their virtual submodules
// and then their configurable ones in subslot order; and as the Catalog,
// per module reference in file order, the module with its default
// submodules, less those whose default does not resolve. Without
// configurable submodules it is ToModel's model.
//
// It returns an error matching profinet.ErrInvalidConfig for a subslot
// that several submodules are used in by default and that no Select or
// ClearSubslot decided (the error names them), and for a model that
// model.Device.Validate refuses; a placed submodule's
// SubmoduleOption.Err; a module's ModuleOption.Err; and
// profinet.ErrResourceExhausted for parameter records of more than
// MaxModelRecordOctets in the plugged submodules.
func (c *Configuration) Resolve() (*model.Device, error) {
	m := *c.base
	m.Capabilities.SupportedRTClasses = slices.Clone(c.base.Capabilities.SupportedRTClasses)
	m.Capabilities.ReductionRatios = slices.Clone(c.base.Capabilities.ReductionRatios)
	m.DAP = cloneModule(c.base.DAP)
	m.Slots, m.Catalog = nil, nil
	extra, ports, err := c.dapOpts.resolve(0, c.dapSel)
	if err != nil {
		return nil, err
	}
	m.DAP.Submodules = append(m.DAP.Submodules, extra...)
	m.Capabilities.Ports += ports
	if n := len(m.DAP.Submodules); n > maxModuleSubmodules {
		return nil, invalid("the DAP has %d submodules, more than the %d a C_SDU carries", n, maxModuleSubmodules)
	}
	plugged := recordOctets(m.DAP.Submodules)
	for _, s := range c.Slots() {
		in := c.slots[s]
		mod, err := in.mod.resolve(s, in.sel)
		if err != nil {
			return nil, err
		}
		if plugged += recordOctets(mod.Submodules); plugged > MaxModelRecordOctets {
			return nil, fmt.Errorf("gsdml: %w: parameter records of more than %d octets in the plugged modules", profinet.ErrResourceExhausted, MaxModelRecordOctets)
		}
		m.Slots = append(m.Slots, model.Slot{Number: s, Module: mod})
	}
	for _, mo := range c.modules {
		if mod, err := mo.resolve(0, nil); err == nil {
			m.Catalog = append(m.Catalog, mod)
		}
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// resolve is the module of a reference with the choices sel, for slot.
func (mo *moduleOpt) resolve(slot uint16, sel map[uint16]*SubmoduleOption) (model.Module, error) {
	mod := cloneModule(mo.opt.Module)
	extra, _, err := mo.owner.resolve(slot, sel)
	if err != nil {
		return model.Module{}, err
	}
	mod.Submodules = append(mod.Submodules, extra...)
	if n := len(mod.Submodules); n > maxModuleSubmodules {
		return model.Module{}, invalid("slot %d: module %s has %d submodules, more than the %d a C_SDU carries", slot, clip(mo.opt.ID), n, maxModuleSubmodules)
	}
	return mod, nil
}

// resolve places the configurable submodules of an owner with the choices
// sel: the fixed ones, the chosen ones, and in the other subslots the
// used ones. It returns them in subslot order and the number of ports
// among them.
func (ow *owner) resolve(slot uint16, sel map[uint16]*SubmoduleOption) ([]model.Submodule, int, error) {
	if ow.err != nil {
		return nil, 0, ow.err
	}
	placed := map[uint16]*SubmoduleOption{}
	for s, o := range ow.fixed {
		placed[s] = o
	}
	for s, o := range sel {
		if ow.fixed[s] == nil && o != nil {
			placed[s] = o
		}
	}
	used := make([]uint16, 0, len(ow.used))
	for s := range ow.used {
		used = append(used, s)
	}
	slices.Sort(used)
	for _, s := range used {
		if _, chosen := sel[s]; chosen {
			continue
		}
		if os := ow.used[s]; len(os) > 1 {
			ids := make([]string, 0, len(os))
			for _, o := range os {
				ids = append(ids, clip(o.ID))
			}
			return nil, 0, invalid("slot %d subslot %d: submodules %s of %s are all used there by default: Select one or ClearSubslot", slot, s, strings.Join(ids, ", "), ow.name)
		}
		placed[s] = ow.used[s][0]
	}
	subslots := make([]uint16, 0, len(placed))
	for s := range placed {
		subslots = append(subslots, s)
	}
	slices.Sort(subslots)
	out := make([]model.Submodule, 0, len(subslots))
	ports := 0
	for _, s := range subslots {
		o := placed[s]
		if o.Err != nil {
			return nil, 0, fmt.Errorf("gsdml: slot %d subslot %d: %s submodule %s: %w", slot, s, ow.name, clip(o.ID), o.Err)
		}
		sm := cloneSubmodule(o.Submodule)
		sm.Subslot = s
		out = append(out, sm)
		if o.Port {
			ports++
		}
	}
	return out, ports, nil
}

func cloneSubmodule(s model.Submodule) model.Submodule {
	s.Records = slices.Clone(s.Records)
	for i := range s.Records {
		s.Records[i].Default = slices.Clone(s.Records[i].Default)
	}
	return s
}

func cloneModule(m model.Module) model.Module {
	subs := make([]model.Submodule, len(m.Submodules))
	for i := range m.Submodules {
		subs[i] = cloneSubmodule(m.Submodules[i])
	}
	m.Submodules = subs
	return m
}

// listRange is a value or a range lo..hi of a ValueList.
type listRange struct{ lo, hi uint32 }

// parseRanges parses a ValueList as its ranges, without expanding them:
// what ParseValueList accepts, whatever the number of values.
func parseRanges(s string) ([]listRange, error) {
	var out []listRange
	for tok := range strings.FieldsSeq(s) {
		lo, hi := tok, tok
		if before, after, ok := strings.Cut(tok, ".."); ok {
			lo, hi = before, after
		}
		a, err := strconv.ParseUint(lo, 10, 32)
		if err != nil {
			return nil, malformed("value list %q", clip(s))
		}
		b, err := strconv.ParseUint(hi, 10, 32)
		if err != nil || b < a {
			return nil, malformed("value list %q", clip(s))
		}
		out = append(out, listRange{uint32(a), uint32(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].lo < out[j].lo })
	return out, nil
}

func inList(r []listRange, v uint32) bool {
	for _, x := range r {
		if x.lo > v {
			return false
		}
		if v <= x.hi {
			return true
		}
	}
	return false
}
