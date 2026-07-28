/*
FILE: forts/types/position-info.go

DESCRIPTION:
FORTS position snapshot.

KNOWN LIMITATION (v1.0, see docs/handoff.md "Open questions"):
the FIX Gate specification (spectra_fixgate_en.pdf) exposes order/trade
lifecycle (Execution Report) but NO "Position Report" message — position
accounting at MOEX/FORTS is a clearing-side concept, historically only
available via Plaza II clearing replication streams or broker-side
reports, neither of which is in v1.0 scope. Until that is confirmed with
Finam, forts.AccountClient computes PositionInfo by accumulating fills
(ExecType=Trade Execution Reports) observed during the current process
lifetime — see forts/account.go. This means:
  - Quantity/AvgEntryPrice are ACCURATE for positions opened after the SDK
    started tracking them;
  - they are WRONG (start from zero) for positions that already existed
    before the process started, until reconciled from an external source
    (e.g. a broker EOD/clearing report) via AccountClient.SeedPosition.

This is a deliberate, documented trade-off — NOT a silent approximation.
*/
package types

import "github.com/shopspring/decimal"

// PositionInfo — FORTS position for one instrument.
type PositionInfo struct {
	Symbol string
	// Quantity — signed net position in contracts (positive = long,
	// negative = short, zero = flat).
	Quantity int64
	// AvgEntryPrice — volume-weighted average entry price of Quantity.
	// Zero when Quantity == 0.
	AvgEntryPrice decimal.Decimal
	// RealizedPnL — accumulated realized P&L (in the instrument's quote
	// currency) since the position tracker was created/last seeded.
	RealizedPnL decimal.Decimal
	// UpdatedAtMs — Unix ms of the last fill/seed that changed this snapshot.
	UpdatedAtMs int64
}
