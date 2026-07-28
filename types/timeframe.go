/*
FILE: types/timeframe.go

DESCRIPTION:
Candle timeframe accepted by forts.MarketDataClient.GetHistoricalCandles.
Mapped to ISS `interval` query parameter values (integer minutes) in
internal/iss — see iss.IntervalFromTimeframe.

ISS /candles endpoint only supports a fixed set of intervals: 1, 10, 60
minutes, 24 hours (day), week, month, quarter — sub-minute timeframes are
not available at all (no aggTrade-equivalent public stream to aggregate
from in v1.0; SIMBA carries the full order/trade log, so a future iteration
could aggregate sub-minute candles from OrderExecution the same way
go-okx's PriceManager aggregates from aggTrade — out of scope for v1.0).
*/
package types

// Timeframe — candle timeframe.
type Timeframe string

const (
	Timeframe1m  Timeframe = "1m"
	Timeframe10m Timeframe = "10m"
	Timeframe1h  Timeframe = "1h"
	Timeframe1D  Timeframe = "1d"
	Timeframe1W  Timeframe = "1w"
	Timeframe1Mo Timeframe = "1mo"
	Timeframe1Q  Timeframe = "1q"
)
