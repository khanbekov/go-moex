/*
FILE: types/candle.go

DESCRIPTION:
Historical candle structs, mapped from the ISS `candles` table
(engines/futures/markets/forts/securities/{symbol}/candles.json).
*/
package types

import "github.com/shopspring/decimal"

// Candle — one candle.
type Candle struct {
	OpenTimeMs  int64
	CloseTimeMs int64
	Open        decimal.Decimal
	High        decimal.Decimal
	Low         decimal.Decimal
	Close       decimal.Decimal
	Volume      decimal.Decimal
}

// Candles — slice of candles, ascending by OpenTimeMs.
type Candles []Candle
