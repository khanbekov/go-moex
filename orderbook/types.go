/*
FILE: orderbook/types.go

DESCRIPTION:
Protocol-agnostic order-level ("L3") order book primitives. Deliberately
NOT tied to SIMBA/SBE types (internal/simba) — this package is meant to be
reusable by v2.0's shares/ section, which will speak a different market
data wire protocol (equities SIMBA ASTS, not SIMBA SPECTRA) but needs the
exact same book-maintenance semantics: per-order add/replace/remove keyed
by a numeric order id, price expressed as a raw fixed-point mantissa (the
decimal exponent is a protocol/instrument concern, resolved by the
section-specific adapter — see forts/stream.go — not by this package).
*/
package orderbook

// Side — book side.
type Side uint8

const (
	SideBid Side = iota
	SideAsk
)

// UpdateAction — what an incremental delta does to the referenced order.
type UpdateAction uint8

const (
	// ActionUpsert — add a new order, or replace the resting size of an
	// already-known order id (covers both "New" and "Change"/partial-fill
	// semantics — both carry the order's new ABSOLUTE remaining size, not
	// a delta, so they collapse to the same book operation).
	ActionUpsert UpdateAction = iota
	// ActionDelete — remove the order entirely (full fill or cancel).
	ActionDelete
)

// SnapshotEntry — one resting order, used to bulk-load Engine.LoadSnapshot.
type SnapshotEntry struct {
	OrderID       int64
	Side          Side
	PriceMantissa int64
	Size          int64
}

// Level — one aggregated price level (sum of all resting order sizes at
// PriceMantissa), returned by Engine.Levels/BestBid/BestAsk.
type Level struct {
	PriceMantissa int64
	Size          int64
}
