/*
FILE: forts/account.go

DESCRIPTION:
Position tracking. See forts/types/position-info.go for the important
accuracy caveat: FORTS FIX Gate has no "Position Report" message, so
positions are derived purely from fills (ExecType=Trade Execution Reports)
observed by this process — SeedPosition exists specifically to let the
caller reconcile against an external source (broker EOD report, Plaza II
clearing feed, manual entry) at startup, before Trading() flow begins.
*/
package forts

import (
	"context"
	"sync"

	"github.com/shopspring/decimal"

	"github.com/tonymontanov/go-moex/forts/types"
)

// AccountClient — FORTS position tracking.
type AccountClient struct{ c *Client }

// GetSymbolPosition returns the current tracked position for symbol, or a
// flat (Quantity=0) PositionInfo if none is tracked yet.
func (ac *AccountClient) GetSymbolPosition(symbol string) *types.PositionInfo {
	return ac.c.positions.Get(symbol)
}

// SeedPosition sets the starting position for symbol, overwriting whatever
// was tracked so far. Call this once at startup, right after Connect and
// before the first CreateOrder, using a position figure obtained from the
// broker (see forts/types/position-info.go doc — this is NOT optional for
// correctness if the process restarts with open positions).
func (ac *AccountClient) SeedPosition(symbol string, quantity int64, avgEntryPrice decimal.Decimal) {
	ac.c.positions.Seed(symbol, quantity, avgEntryPrice)
}

// WatchPositions returns a channel receiving a PositionInfo snapshot every
// time a fill changes it. Same backpressure contract as WatchOpenOrders.
func (ac *AccountClient) WatchPositions(ctx context.Context) <-chan *types.PositionInfo {
	return ac.c.positions.Watch(ctx)
}

// positionTracker — net position per symbol, updated on every trade fill.
type positionTracker struct {
	mu        sync.Mutex
	positions map[string]*types.PositionInfo

	watchMu  sync.Mutex
	watchers []chan *types.PositionInfo
}

func newPositionTracker() *positionTracker {
	return &positionTracker{positions: make(map[string]*types.PositionInfo)}
}

func (pt *positionTracker) Get(symbol string) *types.PositionInfo {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	if p, ok := pt.positions[symbol]; ok {
		var copy types.PositionInfo = *p
		return &copy
	}
	return &types.PositionInfo{Symbol: symbol}
}

func (pt *positionTracker) Seed(symbol string, quantity int64, avgEntryPrice decimal.Decimal) {
	pt.mu.Lock()
	pt.positions[symbol] = &types.PositionInfo{Symbol: symbol, Quantity: quantity, AvgEntryPrice: avgEntryPrice}
	var snapshot types.PositionInfo = *pt.positions[symbol]
	pt.mu.Unlock()
	pt.fanOut(&snapshot)
}

// ApplyFill updates the position for symbol given one trade fill. Sign
// convention: side=Buy increases Quantity, side=Sell decreases it.
// Realized P&L is booked whenever a fill reduces the magnitude of the
// existing position (i.e. trades in the opposite direction of the current
// net position) — a standard FIFO-less "average cost" model, adequate for
// net (non-hedge-mode) FORTS accounting.
func (pt *positionTracker) ApplyFill(symbol string, side types.Side, qty int64, price decimal.Decimal, atMs int64) {
	if qty <= 0 {
		return
	}
	var signedQty int64 = qty
	if side == types.SideSell {
		signedQty = -qty
	}

	pt.mu.Lock()
	var p *types.PositionInfo = pt.positions[symbol]
	if p == nil {
		p = &types.PositionInfo{Symbol: symbol}
		pt.positions[symbol] = p
	}

	var sameDirection bool = (p.Quantity >= 0 && signedQty >= 0) || (p.Quantity <= 0 && signedQty <= 0)
	if p.Quantity == 0 || sameDirection {
		// Opening or adding to the position: roll the average entry price.
		var totalQty decimal.Decimal = decimal.NewFromInt(p.Quantity).Abs().Add(decimal.NewFromInt(qty))
		if totalQty.IsPositive() {
			var weighted decimal.Decimal = p.AvgEntryPrice.Mul(decimal.NewFromInt(p.Quantity).Abs()).Add(price.Mul(decimal.NewFromInt(qty)))
			p.AvgEntryPrice = weighted.Div(totalQty)
		}
		p.Quantity += signedQty
	} else {
		// Reducing or flipping: book realized P&L on the closed portion.
		var oldQuantity int64 = p.Quantity
		var closingQty int64 = qty
		if closingQty > absInt64(p.Quantity) {
			closingQty = absInt64(p.Quantity)
		}
		var pnlPerUnit decimal.Decimal = price.Sub(p.AvgEntryPrice)
		if p.Quantity < 0 {
			pnlPerUnit = p.AvgEntryPrice.Sub(price)
		}
		p.RealizedPnL = p.RealizedPnL.Add(pnlPerUnit.Mul(decimal.NewFromInt(closingQty)))
		p.Quantity += signedQty
		switch {
		case p.Quantity == 0:
			p.AvgEntryPrice = decimal.Zero
		case (oldQuantity > 0 && p.Quantity < 0) || (oldQuantity < 0 && p.Quantity > 0):
			// Flipped through flat: the remainder opens a new position at
			// the fill price. Sign comparison against oldQuantity (not just
			// side) is required — a partial reduce that leaves the position
			// on the SAME side (e.g. long 10 -> sell 4 -> long 6) must keep
			// the original AvgEntryPrice, which a side-only check would
			// wrongly overwrite.
			p.AvgEntryPrice = price
		}
	}
	p.UpdatedAtMs = atMs
	var snapshot types.PositionInfo = *p
	pt.mu.Unlock()

	pt.fanOut(&snapshot)
}

func (pt *positionTracker) Watch(ctx context.Context) <-chan *types.PositionInfo {
	var ch chan *types.PositionInfo = make(chan *types.PositionInfo, 64)
	pt.watchMu.Lock()
	pt.watchers = append(pt.watchers, ch)
	pt.watchMu.Unlock()

	go func() {
		<-ctx.Done()
		pt.watchMu.Lock()
		for i, w := range pt.watchers {
			if w == ch {
				pt.watchers = append(pt.watchers[:i], pt.watchers[i+1:]...)
				break
			}
		}
		pt.watchMu.Unlock()
		close(ch)
	}()
	return ch
}

func (pt *positionTracker) fanOut(p *types.PositionInfo) {
	pt.watchMu.Lock()
	defer pt.watchMu.Unlock()
	for _, ch := range pt.watchers {
		select {
		case ch <- p:
		default:
		}
	}
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
