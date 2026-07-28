/*
FILE: forts/market.go

DESCRIPTION:
Reference data and historical candles via MOEX ISS
(engines/futures/markets/forts/...). Column names below were verified
against the live ISS endpoint at SDK authoring time (see docs/handoff.md
"Verified against live ISS" for the exact curl commands), not guessed from
prose documentation — this is the one part of the SDK that could be
smoke-tested against production without any broker credentials.

IMPORTANT ISS NAMING QUIRK (do not "fix" this, it matches the exchange):
ISS calls the LONG display form ("Si-12.26") SHORTNAME and the SHORT
machine form ("SiZ6") SECID — the reverse of what the column names
suggest. types.SymbolInfo.Symbol (long form, used for FIX orders per
spectra_fixgate_en.pdf §3.2.5 note 5) is populated from ISS SHORTNAME;
types.SymbolInfo.ShortSymbol (short form, used as the ISS candles URL
path segment) is populated from ISS SECID.
*/
package forts

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	moex "github.com/tonymontanov/go-moex"
	rootTypes "github.com/tonymontanov/go-moex/types"
	"github.com/tonymontanov/go-moex/forts/types"
	"github.com/tonymontanov/go-moex/internal/iss"
)

// MarketDataClient — FORTS reference data / candles / order book.
type MarketDataClient struct{ c *Client }

type issSecurityRow struct {
	SecID           string  `iss:"SECID"`
	ShortName       string  `iss:"SHORTNAME"`
	Decimals        int32   `iss:"DECIMALS"`
	MinStep         float64 `iss:"MINSTEP"`
	LotVolume       int64   `iss:"LOTVOLUME"`
	StepPrice       float64 `iss:"STEPPRICE"`
	LastTradeDate   string  `iss:"LASTTRADEDATE"`
	AssetCode       string  `iss:"ASSETCODE"`
}

const fortsSecuritiesPath = "/engines/futures/markets/forts/securities.json"

// GetSymbols returns reference data for every currently listed FORTS
// futures instrument. See the package doc above for the Symbol/ShortSymbol
// naming convention.
func (mc *MarketDataClient) GetSymbols(ctx context.Context) ([]*types.SymbolInfo, error) {
	if mc.c.issClient == nil {
		return nil, moex.NewError(moex.TransportISS, moex.ErrorKindInvalidRequest, "", "forts: ISS client not initialized", nil)
	}
	var resp iss.Response
	var err error
	resp, err = mc.c.issClient.Get(ctx, fortsSecuritiesPath, url.Values{"iss.meta": {"off"}, "iss.only": {"securities"}})
	if err != nil {
		return nil, err
	}
	var rows []issSecurityRow
	if err = resp["securities"].ScanInto(&rows); err != nil {
		return nil, moex.NewError(moex.TransportISS, moex.ErrorKindNetwork, "", "forts: decode securities table", err)
	}

	var out []*types.SymbolInfo = make([]*types.SymbolInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, symbolInfoFromISSRow(r))
	}
	return out, nil
}

// GetSymbolInfo looks up one instrument by either its long form ("Si-12.26")
// or short form ("SiZ6"). Returns ErrorKindInvalidRequest if not found.
func (mc *MarketDataClient) GetSymbolInfo(ctx context.Context, symbol string) (*types.SymbolInfo, error) {
	var all []*types.SymbolInfo
	var err error
	all, err = mc.GetSymbols(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range all {
		if s.Symbol == symbol || s.ShortSymbol == symbol {
			return s, nil
		}
	}
	return nil, moex.NewError(moex.TransportISS, moex.ErrorKindInvalidRequest, "", fmt.Sprintf("forts: unknown symbol %q", symbol), nil)
}

func symbolInfoFromISSRow(r issSecurityRow) *types.SymbolInfo {
	var lastTradeMs int64
	if t, err := time.Parse("2006-01-02", r.LastTradeDate); err == nil {
		lastTradeMs = t.UnixMilli()
	}
	return &types.SymbolInfo{
		Symbol:          r.ShortName, // see package doc: ISS SHORTNAME is the LONG form.
		ShortSymbol:     r.SecID,     // ISS SECID is the SHORT form.
		ShortName:       r.ShortName,
		LotSize:         decimal.NewFromInt(r.LotVolume),
		MinPriceStep:    decimal.NewFromFloat(r.MinStep),
		StepPrice:       decimal.NewFromFloat(r.StepPrice),
		Decimals:        r.Decimals,
		LastTradeDateMs: lastTradeMs,
		MarketSegment:   types.MarketSegmentFutures,
	}
}

// intervalFromTimeframe maps rootTypes.Timeframe to the ISS `interval`
// query parameter. Values per ISS convention: 1/10/60 minutes, 24 = day,
// 7 = week, 31 = month, 4 = quarter.
func intervalFromTimeframe(tf rootTypes.Timeframe) (string, error) {
	switch tf {
	case rootTypes.Timeframe1m:
		return "1", nil
	case rootTypes.Timeframe10m:
		return "10", nil
	case rootTypes.Timeframe1h:
		return "60", nil
	case rootTypes.Timeframe1D:
		return "24", nil
	case rootTypes.Timeframe1W:
		return "7", nil
	case rootTypes.Timeframe1Mo:
		return "31", nil
	case rootTypes.Timeframe1Q:
		return "4", nil
	default:
		return "", fmt.Errorf("forts: unsupported timeframe %q", tf)
	}
}

type issCandleRow struct {
	Open   float64 `iss:"open"`
	Close  float64 `iss:"close"`
	High   float64 `iss:"high"`
	Low    float64 `iss:"low"`
	Volume int64   `iss:"volume"`
	Begin  string  `iss:"begin"`
	End    string  `iss:"end"`
}

// GetHistoricalCandles returns up to limit candles for symbol (either
// long or short form — resolved to the short form via one GetSymbolInfo
// call, since the ISS candles endpoint keys on it in the URL path),
// oldest first. limit <= 0 returns every candle ISS has for the requested
// interval (ISS paginates internally; the SDK issues one request per
// v1.0 — see docs/handoff.md for pagination as a follow-up).
func (mc *MarketDataClient) GetHistoricalCandles(ctx context.Context, symbol string, tf rootTypes.Timeframe, limit int) (rootTypes.Candles, error) {
	if mc.c.issClient == nil {
		return nil, moex.NewError(moex.TransportISS, moex.ErrorKindInvalidRequest, "", "forts: ISS client not initialized", nil)
	}
	var interval string
	var err error
	interval, err = intervalFromTimeframe(tf)
	if err != nil {
		return nil, moex.NewError(moex.TransportISS, moex.ErrorKindInvalidRequest, "", err.Error(), err)
	}

	var info *types.SymbolInfo
	info, err = mc.GetSymbolInfo(ctx, symbol)
	if err != nil {
		return nil, err
	}

	var query url.Values = url.Values{"iss.meta": {"off"}, "interval": {interval}}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}

	var resp iss.Response
	resp, err = mc.c.issClient.Get(ctx, fmt.Sprintf("/engines/futures/markets/forts/securities/%s/candles.json", info.ShortSymbol), query)
	if err != nil {
		return nil, err
	}

	var rows []issCandleRow
	if err = resp["candles"].ScanInto(&rows); err != nil {
		return nil, moex.NewError(moex.TransportISS, moex.ErrorKindNetwork, "", "forts: decode candles table", err)
	}

	var out rootTypes.Candles = make(rootTypes.Candles, 0, len(rows))
	for _, r := range rows {
		out = append(out, rootTypes.Candle{
			OpenTimeMs:  parseISSDateTimeMs(r.Begin),
			CloseTimeMs: parseISSDateTimeMs(r.End),
			Open:        decimal.NewFromFloat(r.Open),
			High:        decimal.NewFromFloat(r.High),
			Low:         decimal.NewFromFloat(r.Low),
			Close:       decimal.NewFromFloat(r.Close),
			Volume:      decimal.NewFromInt(r.Volume),
		})
	}
	return out, nil
}

// parseISSDateTimeMs parses ISS's "YYYY-MM-DD HH:MM:SS" timestamps
// (Moscow local time, per ISS convention — not UTC) into Unix ms.
func parseISSDateTimeMs(s string) int64 {
	if s == "" {
		return 0
	}
	var loc *time.Location
	var err error
	loc, err = time.LoadLocation("Europe/Moscow")
	if err != nil {
		loc = time.UTC
	}
	var t time.Time
	t, err = time.ParseInLocation("2006-01-02 15:04:05", s, loc)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}
