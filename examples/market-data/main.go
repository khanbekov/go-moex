// Command market-data demonstrates read-only FORTS reference data and
// historical candles via MOEX ISS — the one part of go-moex that works
// with zero credentials (free, rate-limited, ~15 min delayed tier) and
// zero exchange colocation, so it's the easiest way to smoke-test that
// the SDK is wired up correctly.
//
// Run:
//
//	go run ./examples/market-data
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	moex "github.com/tonymontanov/go-moex"
	"github.com/tonymontanov/go-moex/forts"
	"github.com/tonymontanov/go-moex/types"
)

func main() {
	var client, err = moex.NewClient(moex.DefaultConfig())
	if err != nil {
		log.Fatalf("moex.NewClient: %v", err)
	}
	defer client.Close()

	// client.Forts() returns `any` (the root package cannot import forts,
	// see client.go doc) — importing forts anywhere in the program (as
	// this file does) is what registers the factory that makes this
	// assertion succeed.
	var fortsClient *forts.Client = client.Forts().(*forts.Client)

	var ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	printSymbolsAndCandles(ctx, fortsClient)
}

func printSymbolsAndCandles(ctx context.Context, fc *forts.Client) {
	var symbols, err = fc.MarketData().GetSymbols(ctx)
	if err != nil {
		log.Fatalf("GetSymbols: %v", err)
	}
	fmt.Printf("FORTS lists %d instruments; showing the first 5:\n", len(symbols))
	for i, s := range symbols {
		if i >= 5 {
			break
		}
		fmt.Printf("  %-15s (short: %-8s) lot=%s minStep=%s\n", s.Symbol, s.ShortSymbol, s.LotSize, s.MinPriceStep)
	}
	if len(symbols) == 0 {
		return
	}

	var first = symbols[0]
	var candles, cErr = fc.MarketData().GetHistoricalCandles(ctx, first.Symbol, types.Timeframe1h, 5)
	if cErr != nil {
		log.Fatalf("GetHistoricalCandles(%s): %v", first.Symbol, cErr)
	}
	fmt.Printf("\nLast %d hourly candles for %s:\n", len(candles), first.Symbol)
	for _, c := range candles {
		fmt.Printf("  open=%-10s high=%-10s low=%-10s close=%-10s volume=%s\n", c.Open, c.High, c.Low, c.Close, c.Volume)
	}
}
