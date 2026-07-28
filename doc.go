/*
Package moex is a high-performance Go SDK for the Moscow Exchange (MOEX),
targeting HFT/algorithmic trading, in the spirit of adshao/go-binance and
the sibling tonymontanov/go-okx and tonymontanov/go-bybit SDKs.

# Scope (v1.0)

v1.0 covers the FORTS (Derivatives) market — futures only (options are out
of scope until v2.5), via the forts sub-package:

  - Reference data & historical candles — internal/iss (MOEX ISS REST).
  - Order entry & lifecycle           — internal/fix (FIX 4.4, FIX Gate).
  - Market data / order book          — internal/simba (SIMBA SPECTRA,
    UDP multicast + FIX SBE), requires exchange colocation.

v2.0 will add the Shares (equities, T+ mode) market as an independent
`shares` sub-package built on the SAME internal/iss, internal/fix,
internal/simba, orderbook layers — never by reusing forts/* code directly
(see docs/handoff.md for the architecture rationale).

# Why three transports, not one

Unlike crypto exchanges (Binance/OKX/Bybit), MOEX has no single REST+WS API
that covers both order entry and market data with an API-key. Order entry
requires a FIX/TWIME session issued by a trading/clearing member (broker);
low-latency market data requires SIMBA SPECTRA, which requires exchange
colocation; only reference/historical data is available over plain HTTPS
(ISS). See docs/handoff.md "MOEX connectivity model" for the full picture.

# Usage

	cfg := moex.DefaultConfig()
	cfg.FIX.SenderCompID = "MYCOMPID"
	client, err := moex.NewClient(cfg)
	if err != nil {
	    log.Fatal(err)
	}
	defer client.Close()

	fortsClient := client.Forts().(*forts.Client)
	candles, err := fortsClient.MarketData().GetHistoricalCandles(ctx, "SiZ5", types.Timeframe1m, 100)

Note the blank import required to register the FORTS factory when only the
root package is referenced directly:

	import _ "github.com/tonymontanov/go-moex/forts"
*/
package moex
