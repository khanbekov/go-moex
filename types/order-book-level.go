/*
FILE: types/order-book-level.go

DESCRIPTION:
A single order book level (price + remaining size, in lots/contracts —
FORTS quantities are always whole contracts, see OrderQty in the FIX Gate
spec §2.4 "Order Qty Data group": "'OrderQty', 'LeavesQty' and 'LastQty'
fields are the number of contracts for futures and options").
*/
package types

import "github.com/shopspring/decimal"

// OrderBookLevel — one order book level.
type OrderBookLevel struct {
	Price decimal.Decimal
	// Size — remaining quantity at this level, in contracts.
	Size decimal.Decimal
}
