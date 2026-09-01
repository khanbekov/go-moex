/*
FILE: forts/booksession.go

DESCRIPTION:
Multi-instrument order-book session over SIMBA SPECTRA: joins the
Incremental feed, joins the Snapshot feed while any subscribed instrument
still needs a snapshot, and keeps one orderbook.Engine per instrument in
sync with the exchange using the procedure of spec §1.4.3 / §4.2.5, as
confirmed on production captures (docs/pcap-findings-2026-09-02.md):

	Collecting  every OrderUpdate/OrderExecution for the instrument is
	            buffered (decoded, per instrument) while the snapshot
	            feed is listened to.
	──────────  a complete OrderBookSnapshot (StartOfSnapshot … EndOfSnapshot,
	            possibly hundreds of fragments) arrives: the engine is loaded
	            from it, buffered updates with MsgSeqNum >
	            LastMsgSeqNumProcessed are applied, the rest dropped.
	Live        updates are applied as they arrive; snapshots are ignored.
	──────────  an RptSeq gap (packet loss) puts the instrument back into
	            Collecting: engine cleared, snapshot feed re-joined.

Feed-level events: SequenceReset resets the per-instrument numbering;
EmptyBook clears every book — after it the Incremental feed alone
defines the books (re-broadcast with PossDupFlag and RptSeq=0), so
Collecting instruments become Live-empty and newly subscribed
instruments start Live. Messages that carry a RptSeq but must not touch
the book (technical trades with a null price, NonQuote/OTC records per
§4.2.9) still advance the sequence — production numbers them.

Concurrency: both listeners run their handlers on their own read
goroutines; the session serialises everything under one mutex. OnBook
callbacks run under that mutex on the incremental read goroutine — keep
them O(1) (read TopN into your own buffer, hand off to a channel).
*/
package forts

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	moex "github.com/tonymontanov/go-moex"
	"github.com/tonymontanov/go-moex/internal/simba"
	"github.com/tonymontanov/go-moex/orderbook"
)

// BookState — synchronisation state of one instrument's book.
type BookState uint8

const (
	// BookCollecting — waiting for a snapshot; updates are buffered.
	BookCollecting BookState = iota
	// BookLive — the book mirrors the exchange; updates applied directly.
	BookLive
)

func (s BookState) String() string {
	if s == BookLive {
		return "live"
	}
	return "collecting"
}

// BookSessionConfig — construction parameters.
type BookSessionConfig struct {
	// SIMBA — multicast groups/interface. IncrementalGroupA and
	// SnapshotGroupA are required.
	SIMBA moex.SIMBAConfig
	// LateJoinBufferMessages — per-instrument cap on buffered updates
	// while Collecting. Production snapshot lag reaches ~51k packets
	// (~15 s); the busiest instrument accumulates ~33k messages in that
	// window. Default 200000. On overflow the oldest are dropped and the
	// instrument waits for a snapshot whose LastMsgSeqNumProcessed is
	// inside the retained window.
	LateJoinBufferMessages int
	// OnBook — called after every packet that changed an instrument's
	// book (once per packet per instrument, transaction-atomic).
	OnBook func(securityID int32, engine *orderbook.Engine)
	// OnState — called on Collecting<->Live transitions.
	OnState func(securityID int32, state BookState)
	Logger  moex.Logger
}

// BookSessionStats — counters for monitoring (read with Stats()).
type BookSessionStats struct {
	IncPackets, IncGaps, IncMissing, IncDuplicates uint64
	SnapPackets, SnapCycles, SnapGaps              uint64
	ParseErrors                                    uint64
	Resyncs                                        uint64 // Live -> Collecting transitions caused by RptSeq gaps.
	Syncs                                          uint64 // Collecting -> Live transitions.
	UnknownOrders                                  uint64
	DuplicateUpdates                               uint64
	BufferOverflows                                uint64
	SnapshotsDeferred                              uint64 // completed snapshots not usable (L outside the buffered window).
	SnapshotsDiscarded                             uint64 // partial snapshots dropped (gap/cycle restart).
	EmptyBooks, SequenceResets                     uint64
}

type bufferedDelta struct {
	msgSeq uint32
	rpt    uint32
	id     int64
	px     int64
	size   int64
	flags  uint64
	action simba.MDUpdateAction
	side   simba.MDEntryType
	exec   bool
}

type snapshotAssembler struct {
	active  bool
	L, R    uint32
	entries []orderbook.SnapshotEntry
}

type instrumentBook struct {
	id        int32
	engine    *orderbook.Engine
	state     BookState
	pending   []bufferedDelta
	firstSeq  uint32 // MsgSeqNum of the oldest retained pending update (after overflow).
	overflow  bool
	asm       snapshotAssembler
	changed   bool
	syncCount int
}

// BookSession — see file doc.
type BookSession struct {
	cfg    BookSessionConfig
	logger moex.Logger

	mu             sync.Mutex
	books          map[int32]*instrumentBook
	collecting     int // number of instruments in BookCollecting.
	afterEmptyBook bool

	incHave  bool
	incLast  uint32
	snapHave bool
	snapLast uint32

	stats BookSessionStats

	// snapshot listener lifecycle (nil when not running).
	snapCancel context.CancelFunc
	snapDone   chan struct{}
	runCtx     context.Context
	running    atomic.Bool
}

// NewBookSession constructs a session; call Run to start receiving.
func NewBookSession(cfg BookSessionConfig) *BookSession {
	if cfg.LateJoinBufferMessages <= 0 {
		cfg.LateJoinBufferMessages = 200_000
	}
	var logger moex.Logger = cfg.Logger
	if logger == nil {
		logger = moex.NoopLogger()
	}
	return &BookSession{cfg: cfg, logger: logger, books: make(map[int32]*instrumentBook)}
}

// Subscribe starts maintaining the book of securityID. Safe before or
// after Run. Returns the engine (valid for the session lifetime).
func (s *BookSession) Subscribe(securityID int32) *orderbook.Engine {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b *instrumentBook = s.books[securityID]
	if b != nil {
		return b.engine
	}
	b = &instrumentBook{id: securityID, engine: orderbook.NewEngine()}
	if s.afterEmptyBook {
		// The Incremental feed alone defines books after EmptyBook.
		b.state = BookLive
	} else {
		b.state = BookCollecting
		s.collecting++
	}
	s.books[securityID] = b
	s.ensureSnapshotListenerLocked()
	return b.engine
}

// Unsubscribe stops maintaining securityID.
func (s *BookSession) Unsubscribe(securityID int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b *instrumentBook = s.books[securityID]
	if b == nil {
		return
	}
	if b.state == BookCollecting {
		s.collecting--
	}
	delete(s.books, securityID)
}

// State returns the sync state of an instrument (ok=false if unknown).
func (s *BookSession) State(securityID int32) (BookState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b *instrumentBook = s.books[securityID]
	if b == nil {
		return BookCollecting, false
	}
	return b.state, true
}

// Stats returns a copy of the counters.
func (s *BookSession) Stats() BookSessionStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// Run joins the Incremental feed and serves until ctx is done. The
// Snapshot feed is joined on demand (while any instrument is Collecting).
func (s *BookSession) Run(ctx context.Context) error {
	if s.cfg.SIMBA.IncrementalGroupA == "" || s.cfg.SIMBA.SnapshotGroupA == "" {
		return moex.NewError(moex.TransportSIMBA, moex.ErrorKindInvalidRequest, "", "forts: Config.SIMBA.IncrementalGroupA and SnapshotGroupA are required for BookSession", nil)
	}
	var inc *simba.Listener
	var err error
	inc, err = simba.Listen(simba.ListenerConfig{GroupAddr: s.cfg.SIMBA.IncrementalGroupA, Interface: s.cfg.SIMBA.NetworkInterface, Logger: s.logger})
	if err != nil {
		return moex.NewError(moex.TransportSIMBA, moex.ErrorKindNetwork, "", "forts: join Incremental multicast group", err)
	}
	defer inc.Close()

	s.mu.Lock()
	s.runCtx = ctx
	s.running.Store(true)
	s.ensureSnapshotListenerLocked()
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running.Store(false)
		s.stopSnapshotListenerLocked()
		s.mu.Unlock()
	}()

	err = inc.Run(ctx, s.HandleIncrementalPacket)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}

// ensureSnapshotListenerLocked starts the snapshot listener if the
// session is running, any instrument is Collecting and it is not up yet.
func (s *BookSession) ensureSnapshotListenerLocked() {
	if !s.running.Load() || s.collecting == 0 || s.snapCancel != nil {
		return
	}
	var ctx context.Context
	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(s.runCtx)
	var l *simba.Listener
	var err error
	l, err = simba.Listen(simba.ListenerConfig{GroupAddr: s.cfg.SIMBA.SnapshotGroupA, Interface: s.cfg.SIMBA.NetworkInterface, Logger: s.logger})
	if err != nil {
		cancel()
		s.logger.Warn("forts: join Snapshot multicast group failed", moex.Err(err))
		return
	}
	s.snapCancel = cancel
	s.snapDone = make(chan struct{})
	var done chan struct{} = s.snapDone
	go func() {
		defer close(done)
		defer l.Close()
		var runErr error = l.Run(ctx, s.HandleSnapshotPacket)
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			s.logger.Warn("forts: Snapshot listener stopped", moex.Err(runErr))
		}
	}()
}

func (s *BookSession) stopSnapshotListenerLocked() {
	if s.snapCancel == nil {
		return
	}
	s.snapCancel()
	s.snapCancel = nil
	s.snapDone = nil
	s.snapHave = false
}

// HandleIncrementalPacket processes one Incremental-feed datagram. Exposed
// for replay/testing; Run feeds it from the multicast listener.
func (s *BookSession) HandleIncrementalPacket(buf []byte) {
	var p simba.Packet
	var err error
	p, err = simba.ParsePacket(buf, 0)
	if err != nil {
		s.mu.Lock()
		s.stats.ParseErrors++
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.IncPackets++

	var seq uint32 = p.Header().MsgSeqNum
	// A SequenceReset packet restarts the numbering and is itself numbered
	// in the NEW sequence (production: SequenceReset(NewSeqNo=1) arrives
	// with MsgSeqNum=1 after millions, and the next packet is also 1), so
	// it must be recognised before the duplicate/gap check.
	if newSeq, isReset := simba.SequenceResetIn(buf); isReset {
		s.onSequenceReset(newSeq)
		s.notifyChangedLocked()
		return
	}
	if s.incHave {
		switch {
		case seq == s.incLast+1:
		case seq <= s.incLast:
			s.stats.IncDuplicates++
			return
		default:
			s.stats.IncGaps++
			s.stats.IncMissing += uint64(seq - s.incLast - 1)
			s.logger.Warn("forts: Incremental feed gap", moex.Int("expected", int64(s.incLast+1)), moex.Int("got", int64(seq)))
			// Which instruments are affected shows up as RptSeq gaps below.
		}
	}
	s.incHave, s.incLast = true, seq

	for {
		var m simba.Message
		var ok bool
		m, ok, err = p.Next()
		if err != nil {
			s.stats.ParseErrors++
			break
		}
		if !ok {
			break
		}
		switch m.Kind {
		case simba.KindOrderUpdate:
			var u simba.OrderUpdate
			if u, ok = m.OrderUpdate(); ok {
				s.onDelta(u.SecurityID, bufferedDelta{msgSeq: seq, rpt: u.RptSeq, id: u.MDEntryID, px: u.MDEntryPx, size: u.MDEntrySize, flags: u.MDFlags, action: u.MDUpdateAction, side: u.MDEntryType})
			}
		case simba.KindOrderExecution:
			var e simba.OrderExecution
			if e, ok = m.OrderExecution(); ok {
				s.onDelta(e.SecurityID, bufferedDelta{msgSeq: seq, rpt: e.RptSeq, id: e.MDEntryID, px: e.MDEntryPx, size: e.MDEntrySize, flags: e.MDFlags, action: e.MDUpdateAction, side: e.MDEntryType, exec: true})
			}
		case simba.KindEmptyBook:
			s.onEmptyBook()
		}
	}
	s.notifyChangedLocked()
}

func (s *BookSession) onDelta(sec int32, d bufferedDelta) {
	var b *instrumentBook = s.books[sec]
	if b == nil {
		return
	}
	if b.state == BookCollecting {
		if len(b.pending) >= s.cfg.LateJoinBufferMessages {
			// Drop the oldest quarter to keep amortised cost low.
			var drop int = len(b.pending) / 4
			copy(b.pending, b.pending[drop:])
			b.pending = b.pending[:len(b.pending)-drop]
			b.overflow = true
			s.stats.BufferOverflows++
		}
		b.pending = append(b.pending, d)
		if b.overflow {
			b.firstSeq = b.pending[0].msgSeq
		}
		return
	}
	s.applyLocked(b, &d)
}

// applyLocked applies one update to a Live book; on a gap the instrument
// goes back to Collecting.
func (s *BookSession) applyLocked(b *instrumentBook, d *bufferedDelta) {
	var err error
	var seq uint64 = uint64(d.rpt)
	var filtered bool = d.flags&mdFlagNonQuote != 0 || (d.exec && d.px == simba.NullDecimalMantissa)
	if filtered {
		err = b.engine.AdvanceSeq(seq)
	} else {
		var action orderbook.UpdateAction = orderbook.ActionUpsert
		if d.action == simba.MDUpdateActionDelete {
			action = orderbook.ActionDelete
		}
		err = b.engine.ApplyDelta(d.id, sideFromMDEntryType(d.side), d.px, d.size, action, seq)
	}
	switch {
	case err == nil:
		b.changed = !filtered
	case errors.Is(err, orderbook.ErrDuplicate):
		s.stats.DuplicateUpdates++
	case errors.Is(err, orderbook.ErrUnknownOrder):
		s.stats.UnknownOrders++
		b.changed = true
		s.logger.Warn("forts: update for unknown order — book may be out of sync", moex.Int("security_id", int64(b.id)), moex.Int("order_id", d.id), moex.Int("rpt_seq", int64(d.rpt)))
	case errors.Is(err, orderbook.ErrSequenceGap):
		var last uint64
		last, _ = b.engine.LastSeq()
		s.logger.Warn("forts: RptSeq gap, resyncing from snapshot", moex.Int("security_id", int64(b.id)), moex.Int("expected", int64(last+1)), moex.Int("got", int64(d.rpt)))
		s.stats.Resyncs++
		s.toCollectingLocked(b)
		// The update that revealed the gap belongs to the post-snapshot
		// stream: keep it.
		b.pending = append(b.pending, *d)
	}
}

func (s *BookSession) toCollectingLocked(b *instrumentBook) {
	if b.state == BookCollecting {
		return
	}
	b.state = BookCollecting
	b.engine.Clear()
	b.pending = b.pending[:0]
	b.overflow = false
	b.asm = snapshotAssembler{}
	b.changed = true
	s.collecting++
	s.ensureSnapshotListenerLocked()
	if s.cfg.OnState != nil {
		s.cfg.OnState(b.id, BookCollecting)
	}
}

func (s *BookSession) toLiveLocked(b *instrumentBook) {
	if b.state == BookLive {
		return
	}
	b.state = BookLive
	b.pending = b.pending[:0]
	b.overflow = false
	b.asm = snapshotAssembler{}
	b.changed = true
	b.syncCount++
	s.collecting--
	if s.collecting == 0 {
		s.stopSnapshotListenerLocked()
	}
	if s.cfg.OnState != nil {
		s.cfg.OnState(b.id, BookLive)
	}
}

// onSequenceReset — spec §4.2.7: set the packet counter to NewSeqNo (the
// next packet carries exactly NewSeqNo) and restart per-instrument
// numbering; books stay until the EmptyBook that follows.
func (s *BookSession) onSequenceReset(newSeq uint32) {
	s.stats.SequenceResets++
	if newSeq > 0 {
		s.incHave, s.incLast = true, newSeq-1
	} else {
		s.incHave = false
	}
	for _, b := range s.books {
		b.engine.ResetSeq()
	}
}

func (s *BookSession) onEmptyBook() {
	s.stats.EmptyBooks++
	s.afterEmptyBook = true
	for _, b := range s.books {
		b.engine.Clear()
		if b.state == BookCollecting {
			s.toLiveLocked(b)
		}
		b.changed = true
	}
}

func (s *BookSession) notifyChangedLocked() {
	if s.cfg.OnBook == nil {
		for _, b := range s.books {
			b.changed = false
		}
		return
	}
	for _, b := range s.books {
		if b.changed {
			b.changed = false
			s.cfg.OnBook(b.id, b.engine)
		}
	}
}

// HandleSnapshotPacket processes one Snapshot-feed datagram. Exposed for
// replay/testing; the on-demand snapshot listener feeds it.
func (s *BookSession) HandleSnapshotPacket(buf []byte) {
	var p simba.Packet
	var err error
	p, err = simba.ParsePacket(buf, 0)
	if err != nil {
		s.mu.Lock()
		s.stats.ParseErrors++
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.SnapPackets++

	var seq uint32 = p.Header().MsgSeqNum
	var restart bool = seq == 1
	if s.snapHave && !restart && seq != s.snapLast+1 {
		s.stats.SnapGaps++
		s.discardPartialSnapshotsLocked()
	}
	if restart {
		s.stats.SnapCycles++
		s.discardPartialSnapshotsLocked()
	}
	s.snapHave, s.snapLast = true, seq

	var m simba.Message
	var ok bool
	m, ok, err = p.Next()
	if err != nil || !ok {
		return
	}
	var v simba.SnapshotView
	if v, ok = m.Snapshot(); !ok {
		return
	}
	var b *instrumentBook = s.books[v.SecurityID]
	if b == nil || b.state != BookCollecting {
		return
	}
	var h simba.PacketHeader = p.Header()
	if h.IsStartOfSnapshot() {
		b.asm = snapshotAssembler{active: true, L: v.LastMsgSeqNumProcessed, R: v.RptSeq, entries: b.asm.entries[:0]}
	}
	if !b.asm.active {
		return // fragment of a snapshot whose start we missed.
	}
	if b.asm.L != v.LastMsgSeqNumProcessed || b.asm.R != v.RptSeq {
		b.asm = snapshotAssembler{}
		s.stats.SnapshotsDiscarded++
		return
	}
	for i := 0; i < v.Len(); i++ {
		var e simba.OrderBookSnapshotEntry = v.Entry(i)
		if e.MDEntryType == simba.MDEntryTypeEmptyBook || e.MDEntryID == simba.NullInt64 || e.MDFlags&mdFlagNonQuote != 0 {
			continue
		}
		b.asm.entries = append(b.asm.entries, orderbook.SnapshotEntry{OrderID: e.MDEntryID, Side: sideFromMDEntryType(e.MDEntryType), PriceMantissa: e.MDEntryPx, Size: e.MDEntrySize})
	}
	if h.IsEndOfSnapshot() {
		s.completeSnapshotLocked(b)
		s.notifyChangedLocked()
	}
}

func (s *BookSession) discardPartialSnapshotsLocked() {
	for _, b := range s.books {
		if b.asm.active {
			b.asm = snapshotAssembler{}
			s.stats.SnapshotsDiscarded++
		}
	}
}

// completeSnapshotLocked performs the §4.2.5 merge for one instrument.
func (s *BookSession) completeSnapshotLocked(b *instrumentBook) {
	var a snapshotAssembler = b.asm
	b.asm = snapshotAssembler{}
	if b.overflow && a.L < b.firstSeq {
		// Updates between L and the retained window were dropped: this
		// snapshot cannot be merged. Wait for a fresher cycle.
		s.stats.SnapshotsDeferred++
		return
	}
	b.engine.LoadSnapshot(a.entries, uint64(a.R))
	var pending []bufferedDelta = b.pending
	s.stats.Syncs++
	s.toLiveLocked(b)
	for i := range pending {
		if pending[i].msgSeq <= a.L {
			continue
		}
		s.applyLocked(b, &pending[i])
		if b.state != BookLive {
			return // gap while replaying the buffer: back to Collecting.
		}
	}
	b.changed = true
}

// SIMBA MDFlags bits used here (MDFlagsSet in simba_spectra-9.0.xml).
const (
	mdFlagNonQuote uint64 = 1 << 2
)

// String renders stats for logs.
func (st BookSessionStats) String() string {
	return fmt.Sprintf("inc{packets=%d gaps=%d missing=%d dups=%d} snap{packets=%d cycles=%d gaps=%d} parse_errors=%d syncs=%d resyncs=%d unknown_orders=%d dup_updates=%d overflows=%d deferred=%d discarded=%d empty_books=%d seq_resets=%d",
		st.IncPackets, st.IncGaps, st.IncMissing, st.IncDuplicates, st.SnapPackets, st.SnapCycles, st.SnapGaps, st.ParseErrors, st.Syncs, st.Resyncs, st.UnknownOrders, st.DuplicateUpdates, st.BufferOverflows, st.SnapshotsDeferred, st.SnapshotsDiscarded, st.EmptyBooks, st.SequenceResets)
}
