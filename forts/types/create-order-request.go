/*
FILE: forts/types/create-order-request.go

DESCRIPTION:
Parameters for TradingClient.CreateOrder → FIX 'New Order Single' (§4.1.1).
*/
package types

import "github.com/shopspring/decimal"

// CreateOrderRequest — new order parameters.
type CreateOrderRequest struct {
	Symbol string
	Side   Side
	// Price — limit price. Mandatory: FORTS FIX Gate has no Market OrdType
	// (see enums.go doc) — pass an aggressive price + TimeInForceIOC/FOK to
	// emulate one.
	Price decimal.Decimal
	// Quantity — order size in contracts (must be > 0).
	Quantity int64
	// ClientOrderID — idempotency key (tag 11). If empty, the SDK generates
	// one (see forts/id-mapping.go), matching the spec's ClientOrderId
	// requirement (§5.4 Order lifecycle).
	ClientOrderID string
	// TimeInForce — default TimeInForceDay if zero value.
	TimeInForce TimeInForce
	// Account — 3-symbol client code (tag 1). Required by FIX Gate for New
	// Order Single ("1 Account Y* String3").
	Account string
	// ExpireDateYYYYMMDD — mandatory when TimeInForce == TimeInForceGTD.
	ExpireDateYYYYMMDD string
}
