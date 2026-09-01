/*
FILE: forts/stream.go

DESCRIPTION:
Live order book via SIMBA SPECTRA: joins the Incremental multicast group(s),
feeds OrderUpdate/OrderExecution deltas into an orderbook.Engine per
instrument, and exposes a snapshot-on-change channel to the caller.
Requires exchange colocation — see moex.SIMBAConfig doc and
docs/handoff.md "Open questions".

v1.0 SCOPE AND KNOWN GAPS (documented, not silently glossed over):
  - Only ONE of the two redundant multicast groups (A) is joined. Full
    redundancy (join A+B, dedupe by MsgSeqNum, tolerate loss on either)
    is spec-recommended but deferred — see docs/handoff.md.
  - On a detected RptSeq gap (orderbook.ErrSequenceGap), the engine is
    simply Clear()-ed and rebuilt from the next OrderBookSnapshot cycle
    the gateway broadcasts periodically — the TCP Replay service (§3 of
    spectra_simba_en.pdf) that would let a client actively request the
    missing range is NOT implemented in v1.0 (see internal/simba doc).
    This means a gap costs "wait for the next snapshot round", not
    "instant recovery" — acceptable for v1.0, called out explicitly
    because it is a genuine behavioral limitation, not a rounding error.
*/
package forts

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	moex "github.com/tonymontanov/go-moex"
	"github.com/tonymontanov/go-moex/internal/simba"
	"github.com/tonymontanov/go-moex/orderbook"
	rootTypes "github.com/tonymontanov/go-moex/types"
)

// OrderBookUpdate — a snapshot-on-change notification delivered by
// WatchOrderBook. Levels are already converted to decimal.Decimal (the
// consumer should not need to know about the underlying Decimal5
// mantissa/exponent encoding).
type OrderBookUpdate struct {
	Symbol string
	Bids   []rootTypes.OrderBookLevel
	Asks   []rootTypes.OrderBookLevel
}

// ResolveSecurityID maps a human symbol to the numeric SecurityID SIMBA
// keys on, by briefly listening to the "Instruments" multicast feed for a
// matching SecurityDefinition(27) message. Requires
// Config.SIMBA.InstrumentsGroupA to be set; results are cached for the
// lifetime of the Client.
func (mc *MarketDataClient) ResolveSecurityID(ctx context.Context, symbol string) (int32, error) {
	mc.c.simbaMu.Lock()
	if id, ok := mc.c.symbolToSecurityID[symbol]; ok {
		mc.c.simbaMu.Unlock()
		return id, nil
	}
	mc.c.simbaMu.Unlock()

	var cfg moex.SIMBAConfig = mc.c.cfg.SIMBA
	if cfg.InstrumentsGroupA == "" {
		return 0, moex.NewError(moex.TransportSIMBA, moex.ErrorKindInvalidRequest, "", "forts: Config.SIMBA.InstrumentsGroupA is not set — cannot resolve SecurityID without colocation multicast access", nil)
	}

	var listener *simba.Listener
	var err error
	listener, err = simba.Listen(simba.ListenerConfig{
		GroupAddr: cfg.InstrumentsGroupA,
		Interface: cfg.NetworkInterface,
		Logger:    mc.c.logger,
	})
	if err != nil {
		return 0, moex.NewError(moex.TransportSIMBA, moex.ErrorKindNetwork, "", "forts: join Instruments multicast group", err)
	}
	defer listener.Close()

	var runCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var found int32 = -1
	var runErr error = listener.Run(runCtx, func(buf []byte) {
		var p simba.Packet
		var perr error
		p, perr = simba.ParsePacket(buf, 0)
		if perr != nil {
			return
		}
		for {
			var m simba.Message
			var ok bool
			m, ok, perr = p.Next()
			if perr != nil || !ok {
				return
			}
			var sd simba.SecurityDefinitionPrefix
			sd, ok = m.SecurityDefinition()
			if !ok {
				continue
			}
			var sym string = sd.SymbolString()
			mc.c.simbaMu.Lock()
			mc.c.symbolToSecurityID[sym] = sd.SecurityID
			mc.c.simbaMu.Unlock()
			if sym == symbol {
				found = sd.SecurityID
				cancel()
				return
			}
		}
	})
	_ = runErr // context deadline/cancel is the expected exit path, not an error to surface.

	if found < 0 {
		return 0, moex.NewError(moex.TransportSIMBA, moex.ErrorKindInvalidRequest, "", fmt.Sprintf("forts: SecurityID for %q not found on the Instruments feed within the timeout", symbol), nil)
	}
	return found, nil
}

// WatchOrderBook joins the SIMBA Incremental feed for securityID (see
// MarketDataClient.ResolveSecurityID) and streams book updates until ctx
// is canceled. depth <= 0 streams the full book on every change.
func (mc *MarketDataClient) WatchOrderBook(ctx context.Context, securityID int32, symbolLabel string, depth int) (<-chan OrderBookUpdate, error) {
	var cfg moex.SIMBAConfig = mc.c.cfg.SIMBA
	if cfg.IncrementalGroupA == "" {
		return nil, moex.NewError(moex.TransportSIMBA, moex.ErrorKindInvalidRequest, "", "forts: Config.SIMBA.IncrementalGroupA is not set — market data requires exchange colocation, see docs/handoff.md", nil)
	}

	var listener *simba.Listener
	var err error
	listener, err = simba.Listen(simba.ListenerConfig{
		GroupAddr: cfg.IncrementalGroupA,
		Interface: cfg.NetworkInterface,
		Logger:    mc.c.logger,
	})
	if err != nil {
		return nil, moex.NewError(moex.TransportSIMBA, moex.ErrorKindNetwork, "", "forts: join Incremental multicast group", err)
	}

	var engine *orderbook.Engine = orderbook.NewEngine()
	var out chan OrderBookUpdate = make(chan OrderBookUpdate, 32)

	var once sync.Once
	var closeListener = func() { once.Do(func() { _ = listener.Close(); close(out) }) }

	go func() {
		<-ctx.Done()
		closeListener()
	}()

	go func() {
		defer closeListener()
		var runErr error = listener.Run(ctx, func(buf []byte) {
			handleSIMBAPacket(buf, securityID, engine, func() {
				emitOrderBookUpdate(out, symbolLabel, engine, depth)
			})
		})
		if runErr != nil {
			mc.c.logger.Warn("forts: SIMBA listener stopped", moex.Err(runErr))
		}
	}()

	return out, nil
}

// handleSIMBAPacket applies every message of one datagram that concerns
// securityID to engine and calls onChange once if anything changed. An
// Incremental packet carries one or more messages (spec §2.3.1; 68% of
// production packets carry several) — all of them are processed, and
// consumers are notified once per packet so a multi-order transaction is
// not observed half-applied.
func handleSIMBAPacket(buf []byte, securityID int32, engine *orderbook.Engine, onChange func()) {
	var p simba.Packet
	var err error
	p, err = simba.ParsePacket(buf, 0)
	if err != nil {
		return
	}
	var changed bool
	for {
		var m simba.Message
		var ok bool
		m, ok, err = p.Next()
		if err != nil || !ok {
			break
		}
		switch m.Kind {
		case simba.KindOrderUpdate:
			var u simba.OrderUpdate
			u, ok = m.OrderUpdate()
			if ok && u.SecurityID == securityID {
				applyOrderUpdateDelta(engine, &u)
				changed = true
			}
		case simba.KindOrderExecution:
			var e simba.OrderExecution
			e, ok = m.OrderExecution()
			if ok && e.SecurityID == securityID {
				applyOrderExecutionDelta(engine, &e)
				changed = true
			}
		case simba.KindOrderBookSnapshot:
			var v simba.SnapshotView
			v, ok = m.Snapshot()
			if ok && v.SecurityID == securityID {
				loadOrderBookSnapshotView(engine, v)
				changed = true
			}
		case simba.KindEmptyBook:
			// EmptyBook carries no SecurityID: per spec §4.2.8 and production
			// captures it is a global "clear every book" (daily reset /
			// clearing), after which books are re-broadcast as OrderUpdate
			// with PossDupFlag.
			engine.Clear()
			changed = true
		}
	}
	if changed {
		onChange()
	}
}

func applyOrderUpdateDelta(engine *orderbook.Engine, u *simba.OrderUpdate) {
	var side orderbook.Side = sideFromMDEntryType(u.MDEntryType)
	var action orderbook.UpdateAction = orderbook.ActionUpsert
	if u.MDUpdateAction == simba.MDUpdateActionDelete {
		action = orderbook.ActionDelete
	}
	var err error = engine.ApplyDelta(u.MDEntryID, side, u.MDEntryPx, u.MDEntrySize, action, uint64(u.RptSeq))
	if err != nil {
		engine.Clear() // gap or unknown order — see file doc "Known gaps".
	}
}

func applyOrderExecutionDelta(engine *orderbook.Engine, e *simba.OrderExecution) {
	if e.MDEntryPx == simba.NullDecimalMantissa {
		return // technical/multi-leg trade with no resting-order impact.
	}
	var side orderbook.Side = sideFromMDEntryType(e.MDEntryType)
	var action orderbook.UpdateAction = orderbook.ActionUpsert
	if e.MDUpdateAction == simba.MDUpdateActionDelete {
		action = orderbook.ActionDelete
	}
	var err error = engine.ApplyDelta(e.MDEntryID, side, e.MDEntryPx, e.MDEntrySize, action, uint64(e.RptSeq))
	if err != nil {
		engine.Clear()
	}
}

func loadOrderBookSnapshot(engine *orderbook.Engine, snap *simba.OrderBookSnapshot) {
	var entries []orderbook.SnapshotEntry = make([]orderbook.SnapshotEntry, 0, len(snap.Entries))
	for _, e := range snap.Entries {
		entries = appendSnapshotEntry(entries, e)
	}
	engine.LoadSnapshot(entries, uint64(snap.RptSeq))
}

func loadOrderBookSnapshotView(engine *orderbook.Engine, v simba.SnapshotView) {
	var entries []orderbook.SnapshotEntry = make([]orderbook.SnapshotEntry, 0, v.Len())
	for i := 0; i < v.Len(); i++ {
		entries = appendSnapshotEntry(entries, v.Entry(i))
	}
	engine.LoadSnapshot(entries, uint64(v.RptSeq))
}

func appendSnapshotEntry(entries []orderbook.SnapshotEntry, e simba.OrderBookSnapshotEntry) []orderbook.SnapshotEntry {
	if e.MDEntryType == simba.MDEntryTypeEmptyBook || e.MDEntryID == simba.NullInt64 {
		return entries
	}
	return append(entries, orderbook.SnapshotEntry{
		OrderID:       e.MDEntryID,
		Side:          sideFromMDEntryType(e.MDEntryType),
		PriceMantissa: e.MDEntryPx,
		Size:          e.MDEntrySize,
	})
}

func sideFromMDEntryType(t simba.MDEntryType) orderbook.Side {
	if t == simba.MDEntryTypeBid {
		return orderbook.SideBid
	}
	return orderbook.SideAsk
}

func emitOrderBookUpdate(out chan OrderBookUpdate, symbol string, engine *orderbook.Engine, depth int) {
	var update OrderBookUpdate = OrderBookUpdate{
		Symbol: symbol,
		Bids:   levelsToDecimal(engine.Levels(orderbook.SideBid, depth)),
		Asks:   levelsToDecimal(engine.Levels(orderbook.SideAsk, depth)),
	}
	select {
	case out <- update:
	default:
		// Slow consumer: drop this intermediate snapshot, the next one
		// will still reflect the latest book state (Engine is authoritative,
		// the channel is best-effort notification — same contract as
		// WatchOpenOrders/WatchPositions).
	}
}

func levelsToDecimal(levels []orderbook.Level) []rootTypes.OrderBookLevel {
	var out []rootTypes.OrderBookLevel = make([]rootTypes.OrderBookLevel, len(levels))
	for i, l := range levels {
		out[i] = rootTypes.OrderBookLevel{Price: decimalFromSIMBAMantissa(l.PriceMantissa), Size: decimal.NewFromInt(l.Size)}
	}
	return out
}
