// SPDX-License-Identifier: MIT

package alarm

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/l2"
	"github.com/otfabric/go-profinet/pnio"
)

// EndpointConfig configures one priority of the alarm transport of an AR.
type EndpointConfig struct {
	// High selects the high priority (FrameID 0xFC01).
	High bool
	// LocalRef is this side's alarm reference, RemoteRef the peer's.
	LocalRef, RemoteRef uint16
	// RemoteMAC is the peer's address.
	RemoteMAC ethernet.MAC
	// Timeout is the retransmission timeout (RTATimeoutFactor × 100 ms);
	// Retries the number of retransmissions (RTARetries).
	Timeout time.Duration
	Retries int
	// OnData receives the VarPart of every DATA PDU accepted in sequence,
	// on the endpoint's own goroutine (it may call Send). The slice is a
	// copy. DATA PDUs that arrive while it runs wait in a queue of 32;
	// when that is full they are left to the peer's retransmission
	// (EndpointStats.Busy). It must not call the endpoint's Close,
	// which waits for that goroutine. A run-time failure in it is not
	// recovered.
	OnData func(varPart []byte)
	// OnErr is called when the peer sends an ERR PDU (an abort), on the
	// manager's receiving goroutine: it must not block, and it must not
	// call Manager.Close or the Close of an endpoint, which wait for a
	// goroutine it may hold up. To end the AR, hand the work to another
	// goroutine. A run-time failure in it is not recovered.
	OnErr func(status pnio.Status)
}

// EndpointStats are the counters of an [Endpoint].
type EndpointStats struct {
	Sent, Received, Retransmits, Duplicates, OutOfSequence, Acks, Nacks uint64
	// Busy counts DATA PDUs left unacknowledged because the application
	// had not taken the previous ones yet; the peer retransmits them.
	Busy uint64
}

// Endpoint is the alarm sender and receiver of one priority of one AR:
// window size 1, one DATA PDU outstanding at a time.
type Endpoint struct {
	cfg     EndpointConfig
	frameID uint16
	mgr     *Manager

	// slot is held by the DATA PDU being sent, from its Send or Start to
	// its transport acknowledgement or terminal failure: window size 1.
	slot chan struct{}

	mu       sync.Mutex
	closed   bool   // done is closed; under mu, which orders it with wg.Add
	nextSend uint16 // the sequence number of the next DATA PDU
	sendSeq  uint16 // the last DATA PDU sent, InitialAckSeq before any
	lastRecv uint16
	expect   uint16
	waiting  bool   // a DATA PDU awaits its ACK
	waitSeq  uint16 // the sequence number of that DATA PDU
	acked    chan struct{}
	nacked   chan struct{}
	// pending is a DATA PDU that was transmitted and is not acknowledged:
	// its Send was cancelled. The next Send retransmits it first, so the
	// peer, which may have accepted it, and this side agree on the
	// sequence numbers.
	pending *PDU
	// failed is the error of a DATA PDU that was never acknowledged: the
	// peer may or may not have accepted it, so the sequence numbers of the
	// two sides are not known to agree and no DATA PDU is sent again.
	failed error

	deliver chan []byte
	done    chan struct{}
	// life ends at Close; it bounds the retransmissions of a Transfer.
	life     context.Context
	stopLife context.CancelFunc
	once     sync.Once
	wg       sync.WaitGroup

	sent, received, retrans, dups, oos, acks, nacks, busy atomic.Uint64
}

func newEndpoint(m *Manager, cfg EndpointConfig) *Endpoint {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 100 * time.Millisecond
	}
	if cfg.Retries <= 0 {
		cfg.Retries = 3
	}
	e := &Endpoint{
		cfg:      cfg,
		frameID:  pnio.FrameIDAlarmLow,
		mgr:      m,
		nextSend: InitialSendSeq,
		sendSeq:  InitialAckSeq,
		lastRecv: InitialAckSeq,
		expect:   InitialSendSeq,
		deliver:  make(chan []byte, 32),
		done:     make(chan struct{}),
		slot:     make(chan struct{}, 1),
	}
	e.life, e.stopLife = context.WithCancel(context.Background())
	if cfg.High {
		e.frameID = pnio.FrameIDAlarmHigh
	}
	e.wg.Add(1)
	go e.run()
	return e
}

// LocalRef returns the endpoint's alarm reference.
func (e *Endpoint) LocalRef() uint16 { return e.cfg.LocalRef }

// Stats returns a snapshot of the counters.
func (e *Endpoint) Stats() EndpointStats {
	return EndpointStats{
		Sent: e.sent.Load(), Received: e.received.Load(), Retransmits: e.retrans.Load(),
		Duplicates: e.dups.Load(), OutOfSequence: e.oos.Load(), Acks: e.acks.Load(), Nacks: e.nacks.Load(), Busy: e.busy.Load(),
	}
}

func (e *Endpoint) run() {
	defer e.wg.Done()
	for {
		select {
		case d := <-e.deliver:
			if e.cfg.OnData != nil {
				e.cfg.OnData(d)
			}
		case <-e.done:
			return
		}
	}
}

func (e *Endpoint) transmit(ctx context.Context, p *PDU) error {
	p.FrameID = e.frameID
	p.Dst = e.cfg.RemoteRef
	p.Src = e.cfg.LocalRef
	p.Version = Version
	b := p.AppendEthernet(make([]byte, 0, 64+len(p.VarPart)), e.cfg.RemoteMAC, e.mgr.mux.HardwareAddr())
	return e.mgr.mux.Send(ctx, b)
}

// Send transmits a DATA PDU carrying varPart and waits for its transport
// acknowledgement, retransmitting it every Timeout up to Retries times.
// It returns an error matching profinet.ErrTimeout when the peer never
// acknowledged (the owner then aborts the AR), and one matching
// profinet.ErrInvalidConfig, before any sequence number is used, for a
// varPart longer than 65535 octets.
//
// A PDU that never left (its first transmission failed, or ctx ended
// before it) gives its sequence number back: the error is returned and
// the next Send uses the number again. A transmission that fails after
// the PDU was on the wire counts as a lost frame and is retried. When ctx
// ends while a transmitted PDU awaits its acknowledgement, the PDU stays
// pending and the next Send retransmits it, with its own Retries, before
// its own PDU: the peer may have accepted it, and skipping its number
// would make the peer refuse every later PDU as out of sequence.
//
// After a timeout the endpoint sends no DATA PDU again: whether the peer
// accepted the unacknowledged one is unknown, so the sequence numbers may
// disagree. Every later Send fails at once with an error matching
// profinet.ErrInvalidState; the owner aborts the AR (SendErr still works)
// and a new AR gets new endpoints. Receiving is not affected.
func (e *Endpoint) Send(ctx context.Context, varPart []byte) error {
	if len(varPart) > math.MaxUint16 {
		return varPartTooLong(len(varPart))
	}
	if err := e.acquire(ctx); err != nil {
		return err
	}
	defer e.release()
	p, prevSend, err := e.prepare(ctx, varPart)
	if err != nil {
		return err
	}
	sent, err := e.await(ctx, p, false)
	if !sent {
		e.giveBack(p.SendSeq, prevSend)
	}
	return err
}

// acquire takes the send slot, waiting for the DATA PDU before to end.
func (e *Endpoint) acquire(ctx context.Context) error {
	select {
	case e.slot <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-e.done:
		return fmt.Errorf("alarm: %w", profinet.ErrClosed)
	}
	select {
	case <-e.done:
		<-e.slot
		return fmt.Errorf("alarm: %w", profinet.ErrClosed)
	default:
		return nil
	}
}

func (e *Endpoint) release() { <-e.slot }

// prepare, holding the slot, retransmits a pending DATA PDU and numbers
// the new one; prevSend is the sendSeq to restore when it never leaves.
func (e *Endpoint) prepare(ctx context.Context, varPart []byte) (p *PDU, prevSend uint16, err error) {
	e.mu.Lock()
	pending, failed := e.pending, e.failed
	e.mu.Unlock()
	if failed != nil {
		return nil, 0, fmt.Errorf("alarm: %w: an earlier DATA PDU was never acknowledged (%v); abort the AR", profinet.ErrInvalidState, failed)
	}
	if pending != nil {
		if _, err := e.await(ctx, pending, true); err != nil {
			return nil, 0, err
		}
	}
	e.mu.Lock()
	seq, prevSend := e.nextSend, e.sendSeq
	e.nextSend = nextSeq(seq)
	e.sendSeq = seq
	ack := e.lastRecv
	e.mu.Unlock()
	// The PDU owns a copy: it may stay pending after Send returns, and the
	// caller may reuse varPart.
	return &PDU{Type: PDUData, AddFlags: WindowSize | FlagTACK, SendSeq: seq, AckSeq: ack, VarPart: bytes.Clone(varPart)}, prevSend, nil
}

// giveBack returns the sequence number of a DATA PDU that never reached
// the wire.
func (e *Endpoint) giveBack(seq, prevSend uint16) {
	e.mu.Lock()
	e.nextSend, e.sendSeq = seq, prevSend
	e.mu.Unlock()
}

// Transfer is a DATA PDU that [Endpoint.Start] transmitted. The endpoint
// owns it until its transport acknowledgement or its terminal failure,
// whatever happens to the context of the Start.
type Transfer struct {
	done chan struct{}
	err  error
}

// Done is closed when the transfer ended: the peer acknowledged the PDU,
// or it failed for good.
func (t *Transfer) Done() <-chan struct{} { return t.done }

// Err returns nil once the peer acknowledged the PDU and, once it failed,
// an error matching profinet.ErrTimeout (never acknowledged: the endpoint
// sends no DATA PDU again, as after a timed-out Send) or
// profinet.ErrClosed (the endpoint closed first). Before Done it returns
// nil.
func (t *Transfer) Err() error {
	select {
	case <-t.done:
		return t.err
	default:
		return nil
	}
}

// Start transmits a DATA PDU carrying varPart, like Send, but ctx bounds
// only what happens before the PDU is on the wire: waiting for the DATA
// PDU before it to end, retransmitting one an earlier Send left pending,
// and the first transmission. When Start returns an error the PDU never
// left: its sequence number is given back and nothing of it reaches the
// peer. Otherwise the returned Transfer owns the PDU: the endpoint
// retransmits it every Timeout up to Retries times, until the peer
// acknowledges it, it fails for good or the endpoint closes, and the next
// Send or Start waits for that. A caller whose context ends after Start
// returned does not change the outcome: the PDU was transmitted and the
// peer may have accepted it, so it is never left pending.
//
// The errors before the transmission are those of Send: one matching
// profinet.ErrInvalidConfig for a varPart longer than 65535 octets,
// profinet.ErrInvalidState after an earlier PDU timed out,
// profinet.ErrClosed, the error of ctx, or that of the transmission.
func (e *Endpoint) Start(ctx context.Context, varPart []byte) (*Transfer, error) {
	if len(varPart) > math.MaxUint16 {
		return nil, varPartTooLong(len(varPart))
	}
	if err := e.acquire(ctx); err != nil {
		return nil, err
	}
	p, prevSend, err := e.prepare(ctx, varPart)
	if err != nil {
		e.release()
		return nil, err
	}
	acked, nacked := e.arm(p.SendSeq)
	if err := e.transmit(ctx, p); err != nil {
		e.mu.Lock()
		e.waiting = false
		e.mu.Unlock()
		e.giveBack(p.SendSeq, prevSend)
		e.release()
		return nil, fmt.Errorf("alarm: send: %w", err)
	}
	e.sent.Add(1)
	tr := &Transfer{done: make(chan struct{})}
	e.mu.Lock()
	if e.closed {
		e.waiting = false
		e.mu.Unlock()
		e.release()
		tr.err = fmt.Errorf("alarm: %w", profinet.ErrClosed)
		close(tr.done)
		return tr, nil
	}
	e.wg.Add(1)
	e.mu.Unlock()
	go func() {
		defer e.wg.Done()
		defer e.release()
		_, err := e.wait(e.life, p, acked, nacked, false, true)
		if e.life.Err() != nil && !errors.Is(err, profinet.ErrTimeout) {
			err = fmt.Errorf("alarm: %w", profinet.ErrClosed)
		}
		e.mu.Lock()
		e.waiting = false
		if errors.Is(err, profinet.ErrTimeout) {
			e.failed = err
		}
		e.mu.Unlock()
		tr.err = err
		close(tr.done)
	}()
	return tr, nil
}

// arm records that the DATA PDU seq awaits its acknowledgement; the
// channels report its ACK and the NACKs.
func (e *Endpoint) arm(seq uint16) (acked chan struct{}, nacked chan struct{}) {
	acked = make(chan struct{})
	nacked = make(chan struct{}, 1)
	e.mu.Lock()
	e.waiting, e.waitSeq = true, seq
	e.acked, e.nacked = acked, nacked
	e.mu.Unlock()
	return acked, nacked
}

// await transmits the DATA PDU p (retransmitted reports that it was sent
// before) until its acknowledgement, Retries times after the first. sent
// reports whether p reached the wire. On a cancelled ctx a sent p is left
// in e.pending; otherwise e.pending is cleared.
func (e *Endpoint) await(ctx context.Context, p *PDU, retransmitted bool) (sent bool, err error) {
	acked, nacked := e.arm(p.SendSeq)
	defer func() {
		e.mu.Lock()
		e.waiting = false
		e.pending = nil
		if sent && err != nil && ctx.Err() != nil && !errors.Is(err, profinet.ErrClosed) {
			e.pending = p
		}
		if errors.Is(err, profinet.ErrTimeout) {
			e.failed = err
		}
		e.mu.Unlock()
	}()
	return e.wait(ctx, p, acked, nacked, retransmitted, false)
}

// wait transmits p until its acknowledgement, Retries times after the
// first; transmitted reports that the first transmission was made, so
// the first attempt only waits for the ACK.
func (e *Endpoint) wait(ctx context.Context, p *PDU, acked, nacked chan struct{}, retransmitted, transmitted bool) (sent bool, err error) {
	sent = retransmitted || transmitted
	var lost error
	for attempt := 0; attempt <= e.cfg.Retries; attempt++ {
		// Start made the first transmission itself.
		if attempt > 0 || !transmitted {
			if attempt > 0 || retransmitted {
				e.retrans.Add(1)
				e.mu.Lock()
				p.AckSeq = e.lastRecv
				e.mu.Unlock()
			}
			if err := e.transmit(ctx, p); err != nil {
				if !sent || ctx.Err() != nil {
					return sent, fmt.Errorf("alarm: send: %w", err)
				}
				// The PDU was on the wire before: this copy is lost like a
				// dropped frame; the timer retransmits it.
				lost = err
			} else {
				sent = true
				e.sent.Add(1)
			}
		}
		t := time.NewTimer(e.cfg.Timeout)
		select {
		case <-acked:
			t.Stop()
			return sent, nil
		case <-nacked:
			t.Stop()
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return sent, ctx.Err()
		case <-e.done:
			t.Stop()
			return sent, fmt.Errorf("alarm: %w", profinet.ErrClosed)
		}
	}
	if lost != nil {
		return sent, fmt.Errorf("alarm: DATA %d not acknowledged after %d retransmissions (last send: %v): %w", p.SendSeq, e.cfg.Retries, lost, profinet.ErrTimeout)
	}
	return sent, fmt.Errorf("alarm: DATA %d not acknowledged after %d retransmissions: %w", p.SendSeq, e.cfg.Retries, profinet.ErrTimeout)
}

// SendErr sends an ERR PDU with status, the abort of the AR.
func (e *Endpoint) SendErr(ctx context.Context, status pnio.Status) error {
	e.mu.Lock()
	p := PDU{Type: PDUErr, AddFlags: WindowSize, SendSeq: e.sendSeq, AckSeq: e.lastRecv, VarPart: status.Append(nil)}
	e.mu.Unlock()
	return e.transmit(ctx, &p)
}

func (e *Endpoint) ackLocked(seq uint16) {
	if e.waiting && e.waitSeq == seq {
		e.waiting = false
		close(e.acked)
	}
}

// handle processes a PDU addressed to this endpoint, on the manager's
// goroutine.
func (e *Endpoint) handle(p *PDU) {
	switch p.Type {
	case PDUErr:
		var st pnio.Status
		if len(p.VarPart) >= 4 {
			st = pnio.StatusFromUint32(binary.BigEndian.Uint32(p.VarPart))
		}
		if e.cfg.OnErr != nil {
			e.cfg.OnErr(st)
		}
	case PDUAck:
		e.acks.Add(1)
		e.mu.Lock()
		e.ackLocked(p.AckSeq)
		e.mu.Unlock()
	case PDUNack:
		e.nacks.Add(1)
		e.mu.Lock()
		if e.waiting {
			select {
			case e.nacked <- struct{}{}:
			default:
			}
		}
		e.mu.Unlock()
	case PDUData:
		e.mu.Lock()
		e.ackLocked(p.AckSeq) // a DATA PDU may acknowledge ours
		var reply *PDU
		var data []byte
		switch {
		case p.SendSeq == e.expect && len(e.deliver) == cap(e.deliver):
			// The application is behind: leave the PDU unacknowledged
			// so that the peer retransmits it, instead of blocking the
			// manager (which would also hold up our own ACKs).
			e.busy.Add(1)
		case p.SendSeq == e.expect:
			e.lastRecv = p.SendSeq
			e.expect = nextSeq(p.SendSeq)
			data = append([]byte(nil), p.VarPart...)
			e.received.Add(1)
			reply = &PDU{Type: PDUAck, AddFlags: WindowSize, SendSeq: e.sendSeq, AckSeq: p.SendSeq}
		case p.SendSeq == e.lastRecv:
			e.dups.Add(1)
			reply = &PDU{Type: PDUAck, AddFlags: WindowSize, SendSeq: e.sendSeq, AckSeq: p.SendSeq}
		default:
			e.oos.Add(1)
			reply = &PDU{Type: PDUNack, AddFlags: WindowSize, SendSeq: e.sendSeq, AckSeq: e.lastRecv}
		}
		e.mu.Unlock()
		if reply != nil && (p.TACK() || reply.Type == PDUNack) {
			_ = e.transmit(context.Background(), reply)
		}
		if data != nil {
			// Only this goroutine sends on deliver and it checked the
			// room above, so the send does not block.
			select {
			case e.deliver <- data:
			case <-e.done:
			}
		}
	}
}

// Close stops the endpoint and unregisters it. It is safe to call more
// than once and from multiple goroutines.
func (e *Endpoint) Close() {
	e.once.Do(func() {
		e.mu.Lock()
		e.closed = true
		close(e.done)
		e.mu.Unlock()
		e.stopLife()
		e.mgr.remove(e)
		e.wg.Wait()
	})
}

type epKey struct {
	ref  uint16
	high bool
}

// Manager receives the RTA PDUs of a port and routes them to endpoints by
// their destination reference and priority.
type Manager struct {
	mux *l2.Mux
	sub *l2.Subscription

	mu        sync.Mutex
	endpoints map[epKey]*Endpoint
	nextRef   uint16

	malformed, unknown atomic.Uint64

	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// NewManager starts routing alarm PDUs received on mux.
func NewManager(mux *l2.Mux) (*Manager, error) {
	sub, err := mux.Subscribe(func(f *ethernet.Frame) bool {
		if f.EtherType != ethernet.EtherTypeProfinet || len(f.Payload) < 2 {
			return false
		}
		id := binary.BigEndian.Uint16(f.Payload)
		return id == pnio.FrameIDAlarmHigh || id == pnio.FrameIDAlarmLow
	}, 256)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{mux: mux, sub: sub, endpoints: map[epKey]*Endpoint{}, nextRef: 1, cancel: cancel, done: make(chan struct{})}
	go m.run(ctx)
	return m, nil
}

// AllocRef returns an alarm reference no endpoint uses, or 0 (which Open
// rejects) when all 65534 are in use.
func (m *Manager) AllocRef() uint16 {
	m.mu.Lock()
	defer m.mu.Unlock()
	for range 0xFFFE {
		r := m.nextRef
		m.nextRef++
		if m.nextRef == 0xFFFF {
			m.nextRef = 1
		}
		if m.endpoints[epKey{r, false}] == nil && m.endpoints[epKey{r, true}] == nil {
			return r
		}
	}
	return 0
}

// Open creates and registers an endpoint.
func (m *Manager) Open(cfg EndpointConfig) (*Endpoint, error) {
	if cfg.LocalRef == 0 || cfg.LocalRef == 0xFFFF {
		return nil, fmt.Errorf("alarm: %w: no free alarm reference", profinet.ErrResourceExhausted)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := epKey{cfg.LocalRef, cfg.High}
	if m.endpoints[k] != nil {
		return nil, fmt.Errorf("alarm: %w: alarm reference %d in use", profinet.ErrInvalidConfig, cfg.LocalRef)
	}
	e := newEndpoint(m, cfg)
	m.endpoints[k] = e
	return e, nil
}

func (m *Manager) remove(e *Endpoint) {
	m.mu.Lock()
	k := epKey{e.cfg.LocalRef, e.cfg.High}
	if m.endpoints[k] == e {
		delete(m.endpoints, k)
	}
	m.mu.Unlock()
}

// Malformed returns the number of PDUs that could not be decoded.
func (m *Manager) Malformed() uint64 { return m.malformed.Load() }

// Unknown returns the number of PDUs for an unknown reference.
func (m *Manager) Unknown() uint64 { return m.unknown.Load() }

// Close stops routing. Endpoints are not closed. It is safe to call more
// than once and from multiple goroutines.
func (m *Manager) Close() {
	m.once.Do(func() {
		m.cancel()
		m.sub.Close()
		<-m.done
	})
}

func (m *Manager) run(ctx context.Context) {
	defer close(m.done)
	for {
		f, err := m.sub.Recv(ctx)
		if err != nil {
			return
		}
		p, err := Decode(f.Payload)
		if err != nil {
			m.malformed.Add(1)
			continue
		}
		m.mu.Lock()
		e := m.endpoints[epKey{p.Dst, p.FrameID == pnio.FrameIDAlarmHigh}]
		m.mu.Unlock()
		if e == nil || (!e.cfg.RemoteMAC.IsZero() && f.Src != e.cfg.RemoteMAC) {
			m.unknown.Add(1)
			continue
		}
		e.handle(&p)
	}
}
