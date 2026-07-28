/*
FILE: orderbook/engine.go

DESCRIPTION:
Order-level ("L3") local order book with price-level ("L2") aggregation on
read. One Engine instance = one instrument's book on one side pair
(bid+ask together, since best-bid/best-ask queries need both).

DESIGN NOTES (see spec §5.3):
  - Per-order state (orders map) is the source of truth; price-level
    aggregates (bidLevels/askLevels) are derived and kept incrementally in
    sync on every mutation — O(1) per update.
  - BestBid/BestAsk are cached and only recomputed by scanning the level
    map when the cached best price is removed (amortized O(1); worst case
    O(levels) on removal of the top level, same as a naive scan but rare in
    practice since removals concentrate near the top of book).
  - Levels(n) sorts the current level map on every call — O(levels ×
    log levels). Acceptable for v1.0 (FORTS book depth per instrument is
    small relative to, say, BTC-USDT); if profiling later shows this is hot
    (e.g. streamed to consumers on every packet), replace bidLevels/
    askLevels with a sorted structure (indexed skip list / red-black tree)
    without changing the public API — see docs/handoff.md.
  - NOT safe for unsynchronized concurrent Apply* calls from multiple
    goroutines; callers must serialize per instrument (forts/stream.go runs
    one Engine per SecurityID, fed by a single dispatch goroutine, so this
    is naturally satisfied without an internal mutex — avoiding lock
    overhead on the market-data hot path).
*/
package orderbook

import "sort"

type orderState struct {
	side          Side
	priceMantissa int64
	size          int64
}

// Engine — local order book for one instrument.
type Engine struct {
	orders map[int64]orderState

	bidLevels map[int64]int64 // price mantissa -> aggregate size.
	askLevels map[int64]int64

	bestBid    int64
	haveBest_B bool
	bestAsk    int64
	haveBest_A bool

	lastSeq     uint64
	haveLastSeq bool
}

// NewEngine creates an empty book.
func NewEngine() *Engine {
	return &Engine{
		orders:    make(map[int64]orderState),
		bidLevels: make(map[int64]int64),
		askLevels: make(map[int64]int64),
	}
}

// LoadSnapshot replaces the entire book with entries and resets the
// sequence tracker to seq (the snapshot's own sequence marker — for SIMBA
// SPECTRA, OrderBookSnapshot.RptSeq). Subsequent ApplyDelta calls are
// expected to carry seq+1, seq+2, ...
func (e *Engine) LoadSnapshot(entries []SnapshotEntry, seq uint64) {
	e.orders = make(map[int64]orderState, len(entries))
	e.bidLevels = make(map[int64]int64)
	e.askLevels = make(map[int64]int64)
	e.haveBest_B = false
	e.haveBest_A = false

	for _, entry := range entries {
		e.orders[entry.OrderID] = orderState{side: entry.Side, priceMantissa: entry.PriceMantissa, size: entry.Size}
		e.addToLevel(entry.Side, entry.PriceMantissa, entry.Size)
	}
	e.lastSeq = seq
	e.haveLastSeq = true
}

// Clear empties the book without touching the sequence tracker (used when
// the feed announces EmptyBook — the caller is expected to immediately
// start feeding fresh ActionUpsert deltas per spec §4.1.2).
func (e *Engine) Clear() {
	e.orders = make(map[int64]orderState)
	e.bidLevels = make(map[int64]int64)
	e.askLevels = make(map[int64]int64)
	e.haveBest_B = false
	e.haveBest_A = false
}

// ApplyDelta applies one incremental update. seq must be strictly
// sequential (lastSeq+1); a gap returns ErrSequenceGap and leaves the book
// untouched — the caller must resync (LoadSnapshot) before calling
// ApplyDelta again, otherwise the book state is undefined.
func (e *Engine) ApplyDelta(orderID int64, side Side, priceMantissa int64, size int64, action UpdateAction, seq uint64) error {
	if e.haveLastSeq && seq != e.lastSeq+1 {
		return ErrSequenceGap
	}

	switch action {
	case ActionUpsert:
		if old, ok := e.orders[orderID]; ok {
			e.addToLevel(old.side, old.priceMantissa, -old.size)
		}
		e.orders[orderID] = orderState{side: side, priceMantissa: priceMantissa, size: size}
		e.addToLevel(side, priceMantissa, size)
	case ActionDelete:
		old, ok := e.orders[orderID]
		if !ok {
			e.lastSeq = seq
			e.haveLastSeq = true
			return ErrUnknownOrder
		}
		delete(e.orders, orderID)
		e.addToLevel(old.side, old.priceMantissa, -old.size)
	}

	e.lastSeq = seq
	e.haveLastSeq = true
	return nil
}

func (e *Engine) addToLevel(side Side, priceMantissa int64, delta int64) {
	var levels map[int64]int64
	if side == SideBid {
		levels = e.bidLevels
	} else {
		levels = e.askLevels
	}
	var newSize int64 = levels[priceMantissa] + delta
	if newSize <= 0 {
		delete(levels, priceMantissa)
		e.invalidateBestIfMatches(side, priceMantissa)
		return
	}
	levels[priceMantissa] = newSize
	e.updateBestOnUpsert(side, priceMantissa)
}

func (e *Engine) updateBestOnUpsert(side Side, priceMantissa int64) {
	if side == SideBid {
		if !e.haveBest_B || priceMantissa > e.bestBid {
			e.bestBid = priceMantissa
			e.haveBest_B = true
		}
	} else {
		if !e.haveBest_A || priceMantissa < e.bestAsk {
			e.bestAsk = priceMantissa
			e.haveBest_A = true
		}
	}
}

func (e *Engine) invalidateBestIfMatches(side Side, priceMantissa int64) {
	if side == SideBid && e.haveBest_B && priceMantissa == e.bestBid {
		e.recomputeBest(SideBid)
	} else if side == SideAsk && e.haveBest_A && priceMantissa == e.bestAsk {
		e.recomputeBest(SideAsk)
	}
}

func (e *Engine) recomputeBest(side Side) {
	var levels map[int64]int64
	if side == SideBid {
		levels = e.bidLevels
	} else {
		levels = e.askLevels
	}
	if len(levels) == 0 {
		if side == SideBid {
			e.haveBest_B = false
		} else {
			e.haveBest_A = false
		}
		return
	}
	var first bool = true
	var best int64
	for price := range levels {
		if first || (side == SideBid && price > best) || (side == SideAsk && price < best) {
			best = price
			first = false
		}
	}
	if side == SideBid {
		e.bestBid, e.haveBest_B = best, true
	} else {
		e.bestAsk, e.haveBest_A = best, true
	}
}

// BestBid returns the best (highest) bid price mantissa and its aggregate
// size. ok is false if the bid side is empty.
func (e *Engine) BestBid() (priceMantissa int64, size int64, ok bool) {
	if !e.haveBest_B {
		return 0, 0, false
	}
	return e.bestBid, e.bidLevels[e.bestBid], true
}

// BestAsk returns the best (lowest) ask price mantissa and its aggregate
// size. ok is false if the ask side is empty.
func (e *Engine) BestAsk() (priceMantissa int64, size int64, ok bool) {
	if !e.haveBest_A {
		return 0, 0, false
	}
	return e.bestAsk, e.askLevels[e.bestAsk], true
}

// Levels returns up to n aggregated price levels for side, best-first
// (descending for bids, ascending for asks). n <= 0 returns every level.
func (e *Engine) Levels(side Side, n int) []Level {
	var levels map[int64]int64
	if side == SideBid {
		levels = e.bidLevels
	} else {
		levels = e.askLevels
	}

	var out []Level = make([]Level, 0, len(levels))
	for price, size := range levels {
		out = append(out, Level{PriceMantissa: price, Size: size})
	}
	sort.Slice(out, func(i, j int) bool {
		if side == SideBid {
			return out[i].PriceMantissa > out[j].PriceMantissa
		}
		return out[i].PriceMantissa < out[j].PriceMantissa
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// LastSeq returns the last applied sequence number and whether one has
// been observed yet (via LoadSnapshot or ApplyDelta).
func (e *Engine) LastSeq() (uint64, bool) { return e.lastSeq, e.haveLastSeq }

// OrderCount returns the number of individually tracked resting orders
// (diagnostics/tests).
func (e *Engine) OrderCount() int { return len(e.orders) }
