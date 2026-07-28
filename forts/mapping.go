/*
FILE: forts/mapping.go

DESCRIPTION:
Wire <-> domain type conversions shared by trading.go/market.go/stream.go:
  - FIX Price16.5 (a plain decimal string on the wire) <-> decimal.Decimal.
  - SIMBA Decimal5 (int64 mantissa, implicit 1e-5 exponent) <-> decimal.Decimal.
  - FIX UTCTimestamp <-> Unix milliseconds.

Kept in one file because every one of these is a one-liner that would
otherwise be duplicated (or worse, subtly re-derived differently) across
trading.go/market.go/account.go/stream.go.
*/
package forts

import (
	"time"

	"github.com/shopspring/decimal"
)

// simbaDecimal5Exponent — Decimal5's fixed exponent per the SIMBA SPECTRA
// schema (composite Decimal5/Decimal5NULL: exponent is a wire constant,
// always -5 — see internal/simba/types.go doc).
const simbaDecimal5Exponent = -5

// decimalFromSIMBAMantissa converts a raw Decimal5(NULL) mantissa to
// decimal.Decimal. Callers must check the mantissa against
// simba.NullDecimalMantissa themselves before calling this (a null price
// has no meaningful decimal representation).
func decimalFromSIMBAMantissa(mantissa int64) decimal.Decimal {
	return decimal.New(mantissa, simbaDecimal5Exponent)
}

// mantissaFromDecimal converts a decimal.Decimal price into the raw int64
// mantissa SIMBA/FIX-adjacent code paths expect (used by
// orderbook.SnapshotEntry construction, tests, and any code that needs to
// go the other way).
func mantissaFromDecimal(d decimal.Decimal) int64 {
	return d.Shift(-simbaDecimal5Exponent).Round(0).IntPart()
}

// fixTimestampLayout — FIX UTCTimestamp with millisecond precision, per
// spectra_fixgate_en.pdf §2.1 "Data types".
const fixTimestampLayout = "20060102-15:04:05.000"

// parseFIXTimestampMs parses a FIX UTCTimestamp string into Unix
// milliseconds. Returns 0 on parse failure (callers treat 0 as "not
// present", matching the FIX spec's own convention of omitting tag 60 in
// several Execution Report scenarios — see forts/types/order-info.go).
func parseFIXTimestampMs(s string) int64 {
	if s == "" {
		return 0
	}
	var t time.Time
	var err error
	t, err = time.Parse(fixTimestampLayout, s)
	if err != nil {
		// FIX Gate may omit the sub-second fraction entirely for some
		// messages; retry without it before giving up.
		t, err = time.Parse("20060102-15:04:05", s)
		if err != nil {
			return 0
		}
	}
	return t.UTC().UnixMilli()
}
