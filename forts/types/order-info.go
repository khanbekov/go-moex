/*
FILE: forts/types/order-info.go

DESCRIPTION:
FORTS order information, built from FIX Gate 'Execution Report' (msg type
'8', §4.1.6). Returned by CreateOrder/ModifyOrder/CancelOrder, GetOpenOrders
elements, and WatchOpenOrders events.

All quantities are whole contracts (int64), matching FIX Gate semantics
("'OrderQty', 'LeavesQty' and 'LastQty' fields are the number of contracts
for futures and options" — §2.4). Prices use decimal.Decimal (FIX
Price16.5 — 16 significant digits, 5 decimal places — losslessly).
*/
package types

import "github.com/shopspring/decimal"

// OrderInfo — FORTS order information.
type OrderInfo struct {
	// OrderID — SPECTRA order id (tag 37).
	OrderID int64
	// ClientOrderID — client order id (tag 11, ClOrdID).
	ClientOrderID string
	// Symbol — instrument symbol ID (tag 55). SDK always uses the "Symbol
	// ID" form (e.g. "Si-12.25") rather than "Short symbol ID" (e.g.
	// "SiZ5") — the FIX Gate spec (§3.2.5 note 5) recommends this
	// specifically because it survives failover to the backup data center.
	Symbol string
	Side   Side
	Price  decimal.Decimal
	// Quantity — total order size in contracts (tag 38, OrderQty).
	Quantity int64
	// LeavesQty — remaining unfilled size in contracts (tag 151).
	LeavesQty int64
	// CumQty — total filled size in contracts (tag 14).
	CumQty int64
	// AvgPx — average fill price (tag 6).
	AvgPx decimal.Decimal
	Status OrdStatus
	// Account — client account code (tag 1, up to 7 chars on Execution
	// Report; 3 chars when submitted on New Order Single).
	Account string
	// TransactTimeMs — order status change / reply time, Unix ms (tag 60).
	// Zero when OrdStatus is PendingCancel/PendingReplace or the order was
	// rejected (FIX Gate omits tag 60 in those cases — see §4.1.6).
	TransactTimeMs int64
	// RejectReasonText — free-form rejection text (tag 58), set only when
	// Status == OrdStatusRejected.
	RejectReasonText string
}
