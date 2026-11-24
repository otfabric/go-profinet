// SPDX-License-Identifier: MIT

package device

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/pnio"
)

// Alarm is an alarm the application raises.
type Alarm struct {
	// High sends the alarm with high priority.
	High    bool
	Type    pnio.AlarmType
	Slot    uint16
	Subslot uint16
	// USI and Data are the optional alarm item (for example the
	// manufacturer data of a process alarm); HasItem includes it.
	HasItem bool
	USI     uint16
	Data    []byte
	// Specifier bits other than the sequence number (pnio.AlarmSpec*).
	Specifier uint16
}

// ackKey names an alarm waiting for its AlarmAck: its priority and its
// sequence number (each priority counts its own).
type ackKey struct {
	high bool
	seq  uint16
}

// alarmData handles a DATA PDU from the controller: an AlarmAck.
func (d *Device) alarmData(a *ar, b []byte) {
	a.touch()
	blk, _, err := pnio.ReadBlock(b)
	if err != nil {
		d.log.Debugf("device: AR %s: alarm data: %v", a.uuid, err)
		return
	}
	switch blk.Header.Type {
	case pnio.BlockAlarmAckHigh, pnio.BlockAlarmAckLow:
		ack, err := pnio.DecodeAlarmAck(blk)
		if err != nil {
			d.log.Debugf("device: AR %s: alarm ack: %v", a.uuid, err)
			return
		}
		k := ackKey{high: ack.High, seq: ack.Specifier & pnio.AlarmSpecSeqMask}
		a.mu.Lock()
		ch := a.ackWait[k]
		delete(a.ackWait, k)
		a.mu.Unlock()
		if ch != nil {
			ch <- ack
		}
		d.handler.AlarmAck(a.info(), ack)
	default:
		d.log.Debugf("device: AR %s: unexpected %s on the alarm CR", a.uuid, blk.Header.Type)
	}
}

// SendAlarm sends an alarm to the controller of every AR in the Data
// state that owns the submodule, and waits for each acknowledgement. It
// returns an error matching profinet.ErrNotFound for an unknown
// submodule, profinet.ErrInvalidState when no AR can receive it,
// profinet.ErrTimeout when an acknowledgement did not arrive and a
// *pnio.StatusError when the controller acknowledged with an error.
func (d *Device) SendAlarm(ctx context.Context, al Alarm) error {
	m, ok := d.model.Module(al.Slot)
	if !ok {
		return fmt.Errorf("device: alarm: slot %d: %w", al.Slot, profinet.ErrNotFound)
	}
	sm, ok := m.Submodule(al.Subslot)
	if !ok {
		return fmt.Errorf("device: alarm: subslot %d/%d: %w", al.Slot, al.Subslot, profinet.ErrNotFound)
	}
	// The alarm goes to the AR that owns the submodule (several IO
	// controllers may share the device).
	// An AR owns its submodules from its Connect on, but its alarm
	// endpoints are only set up later: the state, read under the AR's lock,
	// orders the read of the endpoint after its write.
	var targets []*ar
	a := d.ownerOf(al.Slot, al.Subslot)
	if a != nil && a.sr != nil {
		// Of an AR set, the primary takes the alarms.
		a = d.srPrimaryOf(a)
	}
	if a != nil && a.currentState() == ARStateData && a.alarmLow != nil {
		targets = append(targets, a)
	}
	var sent int
	var errs []error
	for _, a := range targets {
		sent++
		if err := d.sendAlarm(ctx, a, al, m.IdentNumber, sm.IdentNumber); err != nil {
			errs = append(errs, err)
		}
	}
	if sent == 0 {
		return fmt.Errorf("device: alarm: %w: no AR in the Data state", profinet.ErrInvalidState)
	}
	return errors.Join(errs...)
}

func (d *Device) sendAlarm(ctx context.Context, a *ar, al Alarm, modIdent, subIdent uint32) error {
	_, err := d.sendAlarmSeq(ctx, a, al, modIdent, subIdent)
	return err
}

// sendAlarmSeq sends an alarm and waits for its acknowledgement; it
// returns the alarm's sequence number.
func (d *Device) sendAlarmSeq(ctx context.Context, a *ar, al Alarm, modIdent, subIdent uint32) (uint16, error) {
	prio := 0
	if al.High {
		prio = 1
	}
	a.mu.Lock()
	if a.alarmSem[prio] == nil {
		a.alarmSem[prio] = make(chan struct{}, 1)
	}
	sem := a.alarmSem[prio]
	a.mu.Unlock()
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	defer func() { <-sem }()
	a.mu.Lock()
	seq := a.alarmSeq[prio]
	a.alarmSeq[prio] = (seq + 1) & pnio.AlarmSpecSeqMask
	k := ackKey{high: al.High, seq: seq}
	ch := make(chan pnio.AlarmAck, 1)
	a.ackWait[k] = ch
	a.mu.Unlock()
	n := pnio.AlarmNotification{
		High: al.High, Type: al.Type, API: d.apiOf(al.Slot, al.Subslot), Slot: al.Slot, Subslot: al.Subslot,
		ModuleIdentNumber: modIdent, SubmoduleIdentNumber: subIdent,
		Specifier: al.Specifier&^pnio.AlarmSpecSeqMask | seq,
		HasItem:   al.HasItem, USI: al.USI, Data: al.Data,
	}
	body, err := encodeSafely(func() ([]byte, error) { return n.Append(nil), nil })
	d.met.OnAlarm(a.ctrlName, al.Type.String(), al.High)
	if err != nil || len(body) > int(a.maxAlarmData) {
		a.mu.Lock()
		delete(a.ackWait, k)
		a.mu.Unlock()
		if err != nil {
			return seq, fmt.Errorf("device: alarm: %w", err)
		}
		return seq, fmt.Errorf("device: alarm: %w: %d octets exceed MaxAlarmDataLength %d", profinet.ErrInvalidConfig, len(body), a.maxAlarmData)
	}
	ep := a.alarmLow
	if al.High {
		ep = a.alarmHigh
	}
	if err := ep.Send(ctx, body); err != nil {
		a.mu.Lock()
		delete(a.ackWait, k)
		a.mu.Unlock()
		if errors.Is(err, profinet.ErrTimeout) {
			d.goAbort(a, pnio.AbortAlarmSend, true)
		}
		// The AR ended (release, abort) while the transport waited for its
		// acknowledgement: its endpoint was closed under it. Only a closed
		// device is "closed".
		if errors.Is(err, profinet.ErrClosed) && d.ctx.Err() == nil {
			return seq, fmt.Errorf("device: alarm to AR %s: %w: %w", a.uuid, profinet.ErrAborted, err)
		}
		return seq, fmt.Errorf("device: alarm to AR %s: %w", a.uuid, err)
	}
	// The application acknowledgement comes as a DATA PDU of the
	// controller; it has as long as the transport would retransmit, and at
	// least AlarmAckTimeout, to arrive.
	t := time.NewTimer(max(a.alarmAckTimeout, AlarmAckTimeout))
	defer t.Stop()
	select {
	case ack, ok := <-ch:
		if !ok {
			return seq, fmt.Errorf("device: alarm to AR %s: %w", a.uuid, profinet.ErrAborted)
		}
		if !ack.Status.OK() {
			return seq, &pnio.StatusError{Op: "alarm ack", Status: ack.Status}
		}
		return seq, nil
	case <-t.C:
		a.mu.Lock()
		delete(a.ackWait, k)
		a.mu.Unlock()
		return seq, fmt.Errorf("device: alarm to AR %s: %w: no AlarmAck", a.uuid, profinet.ErrTimeout)
	case <-ctx.Done():
		a.mu.Lock()
		delete(a.ackWait, k)
		a.mu.Unlock()
		return seq, ctx.Err()
	}
}

// AlarmAckTimeout is the shortest time the device waits for the
// controller's AlarmAck after the transport acknowledged the alarm.
const AlarmAckTimeout = 5 * time.Second

// AddDiagnosis records a diagnosis of a submodule and reports it to the
// controller of the AR that owns the submodule with a diagnosis alarm. The
// item's ChannelProperties specifier is set to "appears".
//
// The diagnoses of a submodule are a set. A diagnosis is identified by
// its channel number, channel group (ChannelProperties.Accumulative),
// direction, error type and extended error type:
//   - A diagnosis that is not active is recorded and reported.
//   - A diagnosis identical to an active one, also in its other
//     ChannelProperties bits, ExtAddValue and Qualifier, changes nothing:
//     no alarm is sent and AddDiagnosis returns nil, with or without an AR.
//   - A diagnosis that differs from an active one with its identity only
//     in those other fields (for example its maintenance severity)
//     replaces it: one alarm reports the new one appearing, a second the
//     old one disappearing while others remain.
//
// The diagnosis is recorded even when no AR can receive the alarm (it is
// then reported as an error matching profinet.ErrInvalidState); a
// controller that connects later reads it in the ModuleDiffBlock and the
// diagnosis records.
//
// Diagnosis operations are reported in the order they change the state:
// AddDiagnosis and RemoveDiagnosis wait, until ctx ends, for the alarms of
// the previous one (an error of ctx then, nothing recorded). An alarm that
// is not delivered while the AR lives (no or a refused AlarmAck, ctx
// ending) is owed: the next diagnosis operation of the submodule, an
// identical AddDiagnosis included, sends it first, so repeating the call
// retries the report.
func (d *Device) AddDiagnosis(ctx context.Context, slot, subslot uint16, item pnio.DiagnosisItem) error {
	if _, ok := d.model.Submodule(slot, subslot); !ok {
		return fmt.Errorf("device: diagnosis %d/%d: %w", slot, subslot, profinet.ErrNotFound)
	}
	item.ChannelProperties = item.ChannelProperties&^pnio.ChanSpecMask | pnio.ChanSpecAppears
	if err := d.lockDiag(ctx); err != nil {
		return err
	}
	defer d.unlockDiag()
	k := key{slot, subslot}
	owner := d.alarmOwner(k)
	d.mu.Lock()
	old, replaced, changed, spec := d.upsertDiagnosis(k, item)
	var notes []diagNote
	if changed {
		notes = append(notes, diagNote{pnio.AlarmDiagnosis, item, spec})
	}
	if replaced {
		old.ChannelProperties = old.ChannelProperties&^pnio.ChanSpecMask | pnio.ChanSpecOtherRemain
		notes = append(notes, diagNote{pnio.AlarmDiagnosis, old, spec})
	}
	notes = d.takeOwed(k, owner, notes)
	d.mu.Unlock()
	return d.reportDiag(ctx, k, notes)
}

// diagNote is a diagnosis alarm to send.
type diagNote struct {
	t    pnio.AlarmType
	item pnio.DiagnosisItem
	spec uint16
}

// owedDiag are the diagnosis alarms of a submodule a failed report left
// undelivered, for the AR that owned it then.
type owedDiag struct {
	ar    *ar
	notes []diagNote
}

// maxOwedDiag bounds the undelivered diagnosis alarms kept per submodule;
// beyond it the oldest are dropped (and logged).
const maxOwedDiag = 64

// lockDiag takes diagSem, waiting at most until ctx ends.
func (d *Device) lockDiag(ctx context.Context) error {
	select {
	case d.diagSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-d.ctx.Done():
		return fmt.Errorf("device: diagnosis: %w", profinet.ErrClosed)
	}
}

func (d *Device) unlockDiag() { <-d.diagSem }

// alarmOwner is the AR a diagnosis alarm of the submodule goes to (the
// primary of an AR set), nil when none.
func (d *Device) alarmOwner(k key) *ar {
	a := d.ownerOf(k.slot, k.subslot)
	if a != nil && a.sr != nil {
		a = d.srPrimaryOf(a)
	}
	return a
}

// takeOwed returns the submodule's undelivered alarms followed by notes,
// and forgets them. The alarms owed to an AR that no longer owns the
// submodule (owner, the alarmOwner taken before d.mu) are dropped: a new
// owner reads the diagnoses in its ModuleDiffBlock and records. d.mu held.
func (d *Device) takeOwed(k key, owner *ar, notes []diagNote) []diagNote {
	o, ok := d.diagOwed[k]
	if !ok {
		return notes
	}
	delete(d.diagOwed, k)
	if o.ar != owner {
		return notes
	}
	return append(o.notes, notes...)
}

// reportDiag sends the diagnosis alarms in order, while holding diagSem,
// and calls afterDiagChange first. On the first failure the alarms not
// delivered, that one included, are owed to the submodule's AR: the next
// diagnosis operation of the submodule, an identical AddDiagnosis
// included, sends them first. Nothing is owed when no AR can receive the
// alarm (ErrInvalidState) or it cannot be encoded (ErrInvalidConfig).
func (d *Device) reportDiag(ctx context.Context, k key, notes []diagNote) error {
	if d.afterDiagChange != nil {
		d.afterDiagChange()
	}
	for i, n := range notes {
		err := d.diagAlarm(ctx, n.t, k.slot, k.subslot, n.item, n.spec)
		if err == nil {
			continue
		}
		if errors.Is(err, profinet.ErrInvalidState) || errors.Is(err, profinet.ErrInvalidConfig) {
			return err
		}
		rest := append([]diagNote(nil), notes[i:]...)
		if len(rest) > maxOwedDiag {
			d.log.Infof("device: diagnosis %d/%d: %d undelivered alarms dropped", k.slot, k.subslot, len(rest)-maxOwedDiag)
			rest = rest[len(rest)-maxOwedDiag:]
		}
		owner := d.alarmOwner(k)
		d.mu.Lock()
		d.diagOwed[k] = owedDiag{ar: owner, notes: rest}
		d.mu.Unlock()
		return err
	}
	return nil
}

// upsertDiagnosis makes item an active diagnosis of the submodule, d.mu
// held. changed is false when an identical diagnosis is already active;
// replaced reports that item took the place of old, the active diagnosis
// with the same identity. spec is the AlarmSpecifier of the submodule's
// active diagnoses afterwards.
func (d *Device) upsertDiagnosis(k key, item pnio.DiagnosisItem) (old pnio.DiagnosisItem, replaced, changed bool, spec uint16) {
	items := d.diags[k]
	for i, it := range items {
		if !sameDiagnosis(it, item) {
			continue
		}
		if it == item {
			return it, false, false, diagSpecifier(items)
		}
		items[i] = item
		return it, true, true, diagSpecifier(items)
	}
	items = append(items, item)
	d.diags[k] = items
	return pnio.DiagnosisItem{}, false, true, diagSpecifier(items)
}

// diagIdentityProps are the ChannelProperties bits in a diagnosis'
// identity: the channel group and the direction (pnio.ChanDirInOut has
// both direction bits set).
const diagIdentityProps = pnio.ChanAccumulative | pnio.ChanDirInOut

// sameDiagnosis reports whether two diagnoses of a submodule are the same
// diagnosis: the same channel number, channel group, direction, error
// type and extended error type. The maintenance severity, the channel
// type bits, ExtAddValue and Qualifier are its value, which may change.
// This is the identity of p-net's pf_diag_find_entry (src/device/pf_diag.c,
// rtlabs-com/p-net v2.0.0), which updates an active diagnosis instead of
// adding a second one (p-net issue 2729).
func sameDiagnosis(a, b pnio.DiagnosisItem) bool {
	return a.ChannelNumber == b.ChannelNumber && a.ErrorType == b.ErrorType && a.ExtErrorType == b.ExtErrorType &&
		a.ChannelProperties&diagIdentityProps == b.ChannelProperties&diagIdentityProps
}

// RemoveDiagnosis removes the diagnoses of a submodule channel with the
// error type (every direction and extended error type) and reports the
// disappearance of each with an alarm: "disappears, others remain" while
// another diagnosis of the submodule remains, and for the last one, when
// none remains, "disappears" in a DiagnosisDisappears alarm. It returns an
// error matching profinet.ErrNotFound when there is none, and the first
// alarm error otherwise; the diagnoses are removed either way, and the
// alarms not delivered are owed as for AddDiagnosis.
func (d *Device) RemoveDiagnosis(ctx context.Context, slot, subslot, channel, errorType uint16) error {
	if err := d.lockDiag(ctx); err != nil {
		return err
	}
	defer d.unlockDiag()
	k := key{slot, subslot}
	owner := d.alarmOwner(k)
	d.mu.Lock()
	var kept, removed []pnio.DiagnosisItem
	for _, it := range d.diags[k] {
		if it.ChannelNumber == channel && it.ErrorType == errorType {
			removed = append(removed, it)
			continue
		}
		kept = append(kept, it)
	}
	if len(removed) == 0 {
		d.mu.Unlock()
		return fmt.Errorf("device: diagnosis %d/%d channel %d type %d: %w", slot, subslot, channel, errorType, profinet.ErrNotFound)
	}
	d.diags[k] = kept
	spec := diagSpecifier(kept)
	var notes []diagNote
	for i, it := range removed {
		chSpec, t := pnio.ChanSpecOtherRemain, pnio.AlarmDiagnosis
		if len(kept) == 0 && i == len(removed)-1 {
			chSpec, t = pnio.ChanSpecDisappears, pnio.AlarmDiagnosisDisappears
		}
		it.ChannelProperties = it.ChannelProperties&^pnio.ChanSpecMask | chSpec
		notes = append(notes, diagNote{t, it, spec})
	}
	notes = d.takeOwed(k, owner, notes)
	d.mu.Unlock()
	return d.reportDiag(ctx, k, notes)
}

// Diagnoses returns the recorded diagnoses of a submodule.
func (d *Device) Diagnoses(slot, subslot uint16) []pnio.DiagnosisItem {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]pnio.DiagnosisItem(nil), d.diags[key{slot, subslot}]...)
}

// qualifierFault are the bits of a QualifiedChannelQualifier that qualify
// a diagnosis as a fault: p-net's PF_DIAG_QUALIFIER_MASK_FAULT
// (src/pf_types.h, rtlabs-com/p-net v2.0.0).
const qualifierFault uint32 = 0xF8000000

// isFault reports whether an active diagnosis is a fault: its
// ChannelProperties.Maintenance is "diagnosis" (0), or "qualified" with a
// fault bit in its qualifier. A maintenance-required or -demanded
// diagnosis is not a fault.
func isFault(it pnio.DiagnosisItem) bool {
	switch it.ChannelProperties & pnio.ChanMaintQualified {
	case 0:
		return true
	case pnio.ChanMaintQualified:
		return it.Qualifier&qualifierFault != 0
	default:
		return false
	}
}

// diagSpecifier is the AlarmSpecifier, without the sequence number, of a
// diagnosis alarm of a submodule whose active diagnoses are items:
// ChannelDiagnosis, SubmoduleDiagnosisState and ARDiagnosisState are set
// when one of them is a fault; none is set when only maintenance
// diagnoses, or none, remain. The alarm goes to the AR that owns the
// submodule, so the diagnosis is always one of that AR.
//
// This is the summary of p-net's pf_alarm_get_diag_summary and
// pf_alarm_add_diag_item_to_summary (src/common/pf_alarm.c,
// rtlabs-com/p-net v2.0.0), which fixed p-net issue 2727: the
// ChannelDiagnosis bit was set for a diagnosis that is not a fault. Like
// p-net it summarises the alarm's submodule only; whether
// ARDiagnosisState should also reflect the other submodules of the AR is
// not established (docs/DIAGNOSTICS.md). The device keeps no
// manufacturer-specific diagnoses, so ManufacturerSpecificDiagnosis is
// never set.
func diagSpecifier(items []pnio.DiagnosisItem) uint16 {
	for _, it := range items {
		if isFault(it) {
			return pnio.AlarmSpecChannelDiag | pnio.AlarmSpecSubmoduleDiag | pnio.AlarmSpecARDiag
		}
	}
	return 0
}

// diagUSI is the format that carries every field of the items: the
// qualified channel diagnosis when one has a qualifier or a qualified
// severity, the extended one when one has an extended error type or
// value, the channel diagnosis otherwise.
func diagUSI(items ...pnio.DiagnosisItem) uint16 {
	usi := pnio.USIChannelDiagnosis
	for _, it := range items {
		switch {
		case it.Qualifier != 0 || it.ChannelProperties&pnio.ChanMaintQualified == pnio.ChanMaintQualified:
			return pnio.USIQualifiedChannel
		case it.ExtErrorType != 0 || it.ExtAddValue != 0:
			usi = pnio.USIExtChannelDiagnosis
		}
	}
	return usi
}

func (d *Device) diagAlarm(ctx context.Context, t pnio.AlarmType, slot, subslot uint16, item pnio.DiagnosisItem, spec uint16) error {
	usi := diagUSI(item)
	return d.SendAlarm(ctx, Alarm{
		Type: t, Slot: slot, Subslot: subslot,
		HasItem: true, USI: usi, Data: pnio.AppendDiagnosisItems(nil, usi, []pnio.DiagnosisItem{item}),
		Specifier: spec,
	})
}
