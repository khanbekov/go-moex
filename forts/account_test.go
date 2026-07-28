package forts

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tonymontanov/go-moex/forts/types"
)

func decStr(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	var d, err = decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("decimal.NewFromString(%q): %v", s, err)
	}
	return d
}

func TestPositionTrackerGetFlatByDefault(t *testing.T) {
	var pt *positionTracker = newPositionTracker()
	var p *types.PositionInfo = pt.Get("Si-12.26")
	if p.Quantity != 0 {
		t.Errorf("expected flat position, got Quantity=%d", p.Quantity)
	}
	if p.Symbol != "Si-12.26" {
		t.Errorf("expected Symbol echoed back, got %q", p.Symbol)
	}
}

func TestPositionTrackerSeed(t *testing.T) {
	var pt *positionTracker = newPositionTracker()
	pt.Seed("Si-12.26", 10, decStr(t, "95000"))

	var p *types.PositionInfo = pt.Get("Si-12.26")
	if p.Quantity != 10 {
		t.Errorf("Quantity = %d, want 10", p.Quantity)
	}
	if !p.AvgEntryPrice.Equal(decStr(t, "95000")) {
		t.Errorf("AvgEntryPrice = %s, want 95000", p.AvgEntryPrice)
	}
}

func TestPositionTrackerApplyFillOpensLong(t *testing.T) {
	var pt *positionTracker = newPositionTracker()
	pt.ApplyFill("Si-12.26", types.SideBuy, 5, decStr(t, "95000"), 1000)

	var p *types.PositionInfo = pt.Get("Si-12.26")
	if p.Quantity != 5 {
		t.Errorf("Quantity = %d, want 5", p.Quantity)
	}
	if !p.AvgEntryPrice.Equal(decStr(t, "95000")) {
		t.Errorf("AvgEntryPrice = %s, want 95000", p.AvgEntryPrice)
	}
	if p.UpdatedAtMs != 1000 {
		t.Errorf("UpdatedAtMs = %d, want 1000", p.UpdatedAtMs)
	}
}

func TestPositionTrackerApplyFillAddsToLongAveragesPrice(t *testing.T) {
	var pt *positionTracker = newPositionTracker()
	pt.ApplyFill("Si-12.26", types.SideBuy, 5, decStr(t, "100"), 1000)
	pt.ApplyFill("Si-12.26", types.SideBuy, 5, decStr(t, "200"), 2000)

	var p *types.PositionInfo = pt.Get("Si-12.26")
	if p.Quantity != 10 {
		t.Errorf("Quantity = %d, want 10", p.Quantity)
	}
	// (5*100 + 5*200) / 10 = 150.
	if !p.AvgEntryPrice.Equal(decStr(t, "150")) {
		t.Errorf("AvgEntryPrice = %s, want 150", p.AvgEntryPrice)
	}
}

func TestPositionTrackerApplyFillReducesLongBooksPnL(t *testing.T) {
	var pt *positionTracker = newPositionTracker()
	pt.ApplyFill("Si-12.26", types.SideBuy, 10, decStr(t, "100"), 1000)
	pt.ApplyFill("Si-12.26", types.SideSell, 4, decStr(t, "120"), 2000)

	var p *types.PositionInfo = pt.Get("Si-12.26")
	if p.Quantity != 6 {
		t.Errorf("Quantity = %d, want 6", p.Quantity)
	}
	if !p.AvgEntryPrice.Equal(decStr(t, "100")) {
		t.Errorf("AvgEntryPrice = %s, want unchanged 100", p.AvgEntryPrice)
	}
	// Closed 4 contracts at (120-100) = 20 profit each => 80.
	if !p.RealizedPnL.Equal(decStr(t, "80")) {
		t.Errorf("RealizedPnL = %s, want 80", p.RealizedPnL)
	}
}

func TestPositionTrackerApplyFillFlipsThroughFlat(t *testing.T) {
	var pt *positionTracker = newPositionTracker()
	pt.ApplyFill("Si-12.26", types.SideBuy, 5, decStr(t, "100"), 1000)
	pt.ApplyFill("Si-12.26", types.SideSell, 8, decStr(t, "110"), 2000)

	var p *types.PositionInfo = pt.Get("Si-12.26")
	if p.Quantity != -3 {
		t.Errorf("Quantity = %d, want -3 (flipped short)", p.Quantity)
	}
	// Closed 5 long contracts at (110-100)=10 profit each => 50.
	if !p.RealizedPnL.Equal(decStr(t, "50")) {
		t.Errorf("RealizedPnL = %s, want 50", p.RealizedPnL)
	}
	// The remainder (3 contracts short) opens at the fill price.
	if !p.AvgEntryPrice.Equal(decStr(t, "110")) {
		t.Errorf("AvgEntryPrice = %s, want 110 (new short opened at fill price)", p.AvgEntryPrice)
	}
}

func TestPositionTrackerApplyFillClosesToFlatResetsAvgPrice(t *testing.T) {
	var pt *positionTracker = newPositionTracker()
	pt.ApplyFill("Si-12.26", types.SideBuy, 5, decStr(t, "100"), 1000)
	pt.ApplyFill("Si-12.26", types.SideSell, 5, decStr(t, "110"), 2000)

	var p *types.PositionInfo = pt.Get("Si-12.26")
	if p.Quantity != 0 {
		t.Errorf("Quantity = %d, want 0 (flat)", p.Quantity)
	}
	if !p.AvgEntryPrice.IsZero() {
		t.Errorf("AvgEntryPrice = %s, want 0 once flat", p.AvgEntryPrice)
	}
}

func TestPositionTrackerApplyFillIgnoresNonPositiveQty(t *testing.T) {
	var pt *positionTracker = newPositionTracker()
	pt.ApplyFill("Si-12.26", types.SideBuy, 0, decStr(t, "100"), 1000)
	pt.ApplyFill("Si-12.26", types.SideBuy, -5, decStr(t, "100"), 1000)

	var p *types.PositionInfo = pt.Get("Si-12.26")
	if p.Quantity != 0 {
		t.Errorf("Quantity = %d, want 0 (non-positive fills ignored)", p.Quantity)
	}
}

func TestPositionTrackerGetReturnsCopyNotSharedPointer(t *testing.T) {
	var pt *positionTracker = newPositionTracker()
	pt.Seed("Si-12.26", 10, decStr(t, "100"))

	var p1 *types.PositionInfo = pt.Get("Si-12.26")
	p1.Quantity = 999 // Mutate the caller's copy.

	var p2 *types.PositionInfo = pt.Get("Si-12.26")
	if p2.Quantity != 10 {
		t.Errorf("mutating a Get() result leaked into internal state: Quantity = %d, want 10", p2.Quantity)
	}
}

func TestPositionTrackerWatchReceivesUpdatesAndClosesOnCancel(t *testing.T) {
	var pt *positionTracker = newPositionTracker()
	var ctx, cancel = context.WithCancel(context.Background())
	var ch <-chan *types.PositionInfo = pt.Watch(ctx)

	pt.ApplyFill("Si-12.26", types.SideBuy, 1, decStr(t, "100"), 1000)

	select {
	case p := <-ch:
		if p.Symbol != "Si-12.26" {
			t.Errorf("watched update has Symbol=%q, want Si-12.26", p.Symbol)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a position update")
	}

	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			// A buffered pending value may still be readable once; drain once more.
			_, ok = <-ch
			if ok {
				t.Error("expected channel to be closed after context cancellation")
			}
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Watch channel to close after cancellation")
	}
}

func TestAbsInt64(t *testing.T) {
	if absInt64(-5) != 5 {
		t.Errorf("absInt64(-5) = %d, want 5", absInt64(-5))
	}
	if absInt64(5) != 5 {
		t.Errorf("absInt64(5) = %d, want 5", absInt64(5))
	}
	if absInt64(0) != 0 {
		t.Errorf("absInt64(0) = %d, want 0", absInt64(0))
	}
}
