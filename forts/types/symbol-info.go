/*
FILE: forts/types/symbol-info.go

DESCRIPTION:
FORTS instrument reference data. Sourced from ISS
(engines/futures/markets/forts/securities) via internal/iss — see
forts/market.go GetSymbolInfo. Field set intentionally mirrors the columns
ISS actually returns for the futures market rather than inventing a
generic cross-exchange shape (see docs/handoff.md — "SymbolInfo is not
shared with a future shares/types.SymbolInfo").
*/
package types

import "github.com/shopspring/decimal"

// SymbolInfo — FORTS instrument reference data.
type SymbolInfo struct {
	// Symbol — "Symbol ID" form used on the wire (e.g. "Si-12.25"), see
	// OrderInfo.Symbol doc for why this form is preferred over "Short symbol ID".
	Symbol string
	// ShortSymbol — "Short symbol ID" form (e.g. "SiZ5"), useful for display.
	ShortSymbol string
	ShortName   string
	// LotSize — contract multiplier: how much of the underlying one
	// contract represents (ISS column LOTVOLUME). For currency futures
	// (Si) this is the USD notional per contract; for crypto index futures
	// (BTC/ETH/SOL/XRP/TRX) this is the index-point multiplier.
	LotSize decimal.Decimal
	// MinPriceStep — minimal price increment (ISS column MINSTEP).
	MinPriceStep decimal.Decimal
	// StepPrice — ruble value of one MinPriceStep move (ISS column STEPPRICE),
	// needed because FORTS quotes are often in points/USD, not RUB directly.
	StepPrice decimal.Decimal
	// Decimals — number of significant decimal places in the quoted price
	// (ISS column DECIMALS).
	Decimals int32
	// LastTradeDateMs — contract expiration/delivery date, Unix ms (ISS
	// column LASTTRADEDATE). Zero for perpetual-like series that ISS
	// reports without a last trade date (should not occur on FORTS —
	// MOEX has no perpetual futures, see docs/handoff.md).
	LastTradeDateMs int64
	// MarketSegment — Futures or Options (derived from CFICode / ISS
	// section), see MarketSegmentID.
	MarketSegment MarketSegmentID
}
