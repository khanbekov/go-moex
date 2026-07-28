/*
FILE: forts/types/modify-order-request.go

DESCRIPTION:
Parameters for TradingClient.ModifyOrder → FIX 'Order Cancel/Replace
Request' (§4.1.4). FORTS supports price/quantity replace only (no side
switch, no symbol switch).
*/
package types

import "github.com/shopspring/decimal"

// ModifyOrderRequest — order price/quantity replace parameters. Exactly one
// of OrderID / OrigClientOrderID must be set, same rule as CancelOrderRequest.
type ModifyOrderRequest struct {
	Symbol string
	Side   Side
	// ClientOrderID — new ClOrdID for the replaced order (tag 11, mandatory).
	// If empty, the SDK generates one.
	ClientOrderID     string
	OrderID           int64
	OrigClientOrderID string
	// NewPrice — new limit price (tag 44, mandatory for OrdType=Limit).
	NewPrice decimal.Decimal
	// NewQuantity — new order size in contracts (tag 38, mandatory).
	NewQuantity int64
}
