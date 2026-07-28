/*
FILE: forts/types/cancel-order-request.go

DESCRIPTION:
Parameters for TradingClient.CancelOrder → FIX 'Order Cancel Request'
(§4.1.2). Symbol and Side are mandatory on the wire even for a cancel —
this is a FORTS FIX Gate quirk, not an SDK design choice.
*/
package types

// CancelOrderRequest — order cancel parameters. Exactly one of OrderID /
// OrigClientOrderID must be set (FIX Gate: "'OrderID' ... Mandatory if the
// 'OrigClOrdID' field is missing" and vice versa).
type CancelOrderRequest struct {
	Symbol string
	Side   Side
	// ClientOrderID — new ClOrdID for this cancel command (tag 11, mandatory).
	// If empty, the SDK generates one.
	ClientOrderID string
	// OrderID — SPECTRA order id to cancel (tag 37).
	OrderID int64
	// OrigClientOrderID — ClOrdID of the order to cancel (tag 41).
	OrigClientOrderID string
	// Quantity — order size to cancel, in contracts (tag 38, mandatory per
	// spec: "38 OrderQty Y* Int64 Instrument units quantity").
	Quantity int64
}
