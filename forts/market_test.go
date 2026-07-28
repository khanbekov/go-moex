package forts

import (
	"testing"
	"time"

	"github.com/tonymontanov/go-moex/forts/types"
	rootTypes "github.com/tonymontanov/go-moex/types"
)

func TestIntervalFromTimeframe(t *testing.T) {
	var cases = []struct {
		tf   rootTypes.Timeframe
		want string
	}{
		{rootTypes.Timeframe1m, "1"},
		{rootTypes.Timeframe10m, "10"},
		{rootTypes.Timeframe1h, "60"},
		{rootTypes.Timeframe1D, "24"},
		{rootTypes.Timeframe1W, "7"},
		{rootTypes.Timeframe1Mo, "31"},
		{rootTypes.Timeframe1Q, "4"},
	}
	for _, c := range cases {
		var got, err = intervalFromTimeframe(c.tf)
		if err != nil {
			t.Fatalf("intervalFromTimeframe(%s): unexpected error %v", c.tf, err)
		}
		if got != c.want {
			t.Errorf("intervalFromTimeframe(%s) = %s, want %s", c.tf, got, c.want)
		}
	}
}

func TestIntervalFromTimeframeUnsupported(t *testing.T) {
	var _, err = intervalFromTimeframe(rootTypes.Timeframe("5m"))
	if err == nil {
		t.Fatal("expected an error for an unsupported timeframe")
	}
}

func TestSymbolInfoFromISSRow(t *testing.T) {
	var row issSecurityRow = issSecurityRow{
		SecID:         "SiZ6",
		ShortName:     "Si-12.26",
		Decimals:      0,
		MinStep:       1,
		LotVolume:     1000,
		StepPrice:     1,
		LastTradeDate: "2026-12-18",
	}
	var info *types.SymbolInfo = symbolInfoFromISSRow(row)

	// See forts/market.go doc: ISS SHORTNAME is the LONG wire form,
	// ISS SECID is the SHORT display form — deliberately swapped.
	if info.Symbol != "Si-12.26" {
		t.Errorf("Symbol = %q, want %q (from ISS SHORTNAME)", info.Symbol, "Si-12.26")
	}
	if info.ShortSymbol != "SiZ6" {
		t.Errorf("ShortSymbol = %q, want %q (from ISS SECID)", info.ShortSymbol, "SiZ6")
	}
	if !info.LotSize.Equal(decStr(t, "1000")) {
		t.Errorf("LotSize = %s, want 1000", info.LotSize)
	}
	if info.MarketSegment != types.MarketSegmentFutures {
		t.Errorf("MarketSegment = %s, want %s", info.MarketSegment, types.MarketSegmentFutures)
	}
	if info.LastTradeDateMs == 0 {
		t.Error("LastTradeDateMs should be non-zero for a valid LASTTRADEDATE")
	}
}

func TestSymbolInfoFromISSRowInvalidDate(t *testing.T) {
	var row issSecurityRow = issSecurityRow{SecID: "SiZ6", ShortName: "Si-12.26", LastTradeDate: "garbage"}
	var info *types.SymbolInfo = symbolInfoFromISSRow(row)
	if info.LastTradeDateMs != 0 {
		t.Errorf("LastTradeDateMs = %d, want 0 for an unparseable date", info.LastTradeDateMs)
	}
}

func TestParseISSDateTimeMs(t *testing.T) {
	var ms int64 = parseISSDateTimeMs("2026-07-28 16:30:00")
	if ms == 0 {
		t.Fatal("parseISSDateTimeMs returned 0 for a valid timestamp")
	}
	var loc, err = time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Skip("Europe/Moscow tzdata not available in this environment")
	}
	var want time.Time = time.Date(2026, 7, 28, 16, 30, 0, 0, loc)
	if ms != want.UnixMilli() {
		t.Errorf("parseISSDateTimeMs = %d, want %d", ms, want.UnixMilli())
	}
}

func TestParseISSDateTimeMsEmpty(t *testing.T) {
	if got := parseISSDateTimeMs(""); got != 0 {
		t.Errorf("parseISSDateTimeMs(\"\") = %d, want 0", got)
	}
}

func TestParseISSDateTimeMsGarbage(t *testing.T) {
	if got := parseISSDateTimeMs("not-a-date"); got != 0 {
		t.Errorf("parseISSDateTimeMs(garbage) = %d, want 0", got)
	}
}
