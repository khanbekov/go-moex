# go-moex — handoff

Living document. Updated after every significant task (architecture change,
module addition, refactor) per project convention — see repository root
`AGENTS`/chat history. Read this before touching FORTS internals; most
"why is this weird" questions are answered here rather than re-litigated
in code comments.

## 1. Project scope and staging

- **v1.0 (current)** — FORTS (Derivatives market: futures + options,
  `engine=futures&market=forts` on ISS) MVP: reference data + historical
  candles (ISS), order entry (FIX Gate), market data (SIMBA SPECTRA).
- **v2.0** — Shares (equities, T+ mode) as an independent `shares/`
  package on the same internal transport layer (internal/iss,
  internal/fix, internal/simba, orderbook) — never by reusing `forts/*`
  code (see architecture principle below).
- **v2.5** — full exchange coverage (FX market, Money market, remaining
  ISS-only instruments).

## 2. Architecture principles (do not violate)

1. **Two layers, no cross-section reuse.** `internal/*` (iss, fix, simba)
   and `orderbook` are protocol/transport primitives with zero knowledge
   of "futures" vs "shares". Section packages (`forts/`, future `shares/`)
   own their own domain types, wire mapping, and business logic. A future
   `shares/` package must NOT import or call into `forts/*` — if two
   sections need the same behavior, the shared code belongs in an
   `internal/*` package, not in one section reused by the other.
2. **Naming mirrors the exchange, not a generic template.** ISS calls the
   derivatives section "FORTS" — so does this SDK (`forts/`), not
   `futures/` or `swap/`. The planned equities package will be `shares/`
   (ISS: `engine=stock`), not `spot/`.
3. **No import cycle between root and sections.** `moex.Client` cannot
   import `forts` (would need Config/Client types from root first). Every
   section registers a lazy factory via `moex.RegisterFortsFactory` in its
   own `init()` — identical to `okx.RegisterSwapFactory` in go-okx. Caller
   idiom: `client.Forts().(*forts.Client)`.
3. **Allocation-conscious on hot paths.** No generic/reflection-based
   codecs on the SIMBA/FIX read paths (reflection is used exactly once,
   in `internal/iss/table.go`, where ISS is inherently low-frequency).
   Hand-written FIX tag=value codec and SBE decoders instead of
   `quickfixgo` — see `internal/fix/message.go` and
   `internal/simba/decode.go` doc comments for the rationale.

## 3. MOEX connectivity model (why there's no single REST+WS client)

Unlike Binance/OKX/Bybit, MOEX has no unified "API key + REST/WS" surface.
Three independent transports, each solving one concern:

| Concern | Transport | Package | Notes |
|---|---|---|---|
| Reference data, candles, history | **ISS** (HTTP/REST, JSON) | `internal/iss` | Public. Free tier: ~15 min delayed, ~10 req/s. Paid tier: passport-cookie auth (`internal/iss/auth.go`), higher limits — exact ceiling unconfirmed, see §5. |
| Order entry (New/Cancel/Replace/MassCancel) | **FIX Gate** (FIX 4.4, TCP) | `internal/fix` | Requires SenderCompID issued by MOEX/broker (see §5). No Market OrdType — see `forts/types/enums.go`. |
| Market data / order book (FORTS) | **SIMBA SPECTRA** (SBE over UDP multicast) | `internal/simba` | **Requires exchange colocation** or a broker low-latency circuit. Outside that environment, `simba.Listen` joins the socket successfully but receives nothing — this is expected, not a bug. |

Not implemented (out of v1.0 scope, listed for completeness):
**TWIME** (binary FORTS order entry, faster than FIX Gate — a v1.x upgrade
path once FIX-based trading is proven out), **Plaza II** (legacy universal
FORTS gateway), **ASTS Bridge** / equities FIX Gate (v2.0, Shares).

## 4. What v1.0 actually implements (status: code-complete, unit-tested)

- `internal/moexlog`, `internal/moexmet`, `internal/moexerr` — shared
  logging/metrics/error plumbing (mirrors go-okx's `okxlog`/`okxmet`/`okxerr`).
- Root `moex` package — `Config`/`DefaultConfig`/`Client`/`Logger`/`Error`
  public surface.
- `internal/iss` — generic columnar-JSON table parser + REST client,
  token-bucket limiter, passport cookie auth.
- `internal/fix` — FIX 4.4 tag=value codec, TCP framing, session layer
  (Logon/Heartbeat/MsgSeqNum/ResendRequest, jittered reconnect backoff).
- `internal/simba` — SBE decoders for Heartbeat/SequenceReset/BestPrices/
  EmptyBook/OrderUpdate/OrderExecution/OrderBookSnapshot + a **partial**
  SecurityDefinition decode (Symbol/SecurityID prefix only — see §6), UDP
  multicast listener.
- `orderbook` — protocol-agnostic L3 engine (per-order state + L2
  aggregation, sequence-gap detection), reusable by `shares/` in v2.0.
- `forts/` — `Client` (lifecycle/Connect/Close), `TradingClient` (order
  entry via FIX, ClOrdID correlation, open-order cache, WatchOpenOrders),
  `AccountClient` (fill-derived position tracking + SeedPosition),
  `MarketDataClient` (ISS reference data/candles, SIMBA order book via
  ResolveSecurityID/WatchOrderBook).
- `examples/market-data`, `examples/trading`, `examples/orderbook` —
  runnable demonstrations of each transport tier (market-data needs no
  credentials at all; trading needs FIX Gate credentials; orderbook needs
  colocation).
- Every internal package plus `forts/` has unit tests (`go test ./...`
  passes clean under `-race`); FIX/SIMBA tests use synthetic wire bytes,
  no live connection needed.

## 5. Open questions — need input from the exchange/broker/user

These block moving from "compiles and unit-tests pass" to "trades on the
real FORTS test circuit." Tracked here so they don't get silently
forgotten between sessions.

1. **Test circuit credentials** (FIX Gate SenderCompID, host/port for the
   test environment; ISS paid-tier login). Public documentation does not
   expose test-circuit connectivity details — these must come from
   MOEX/broker directly. User: will request; agent should keep developing
   against publicly available info (live production ISS, public FIX Gate
   spec, public SIMBA schema) in the meantime — this is exactly what v1.0
   did.
2. **Colocation ETA** (required for any real SIMBA traffic). User: ETA to
   be confirmed; **decision made: SIMBA ships in v1.0 regardless** (code
   is written and unit-tested against the schema now; it simply won't
   receive UDP data until colocation/broker circuit is live — same
   "compiles, doesn't have a live socket yet" state as the FIX trading
   path before test credentials arrive).
3. **Qualified investor status.** User confirmed they are a qualified
   investor at broker BKS (БКС) and is in the process of transferring that
   status to the broker that will provide MOEX/FORTS connectivity for this
   project (status transfer between brokers is possible per user). FORTS
   crypto-index and Si futures are qualified-investor-only instruments —
   this transfer must complete before live trading, not before development.
4. **Currency hedging (SI1!/SI2!).** User flagged this specifically for
   the case where crypto-index futures (BTC/ETH/SOL/XRP/TRX) are RUB-margined
   — a USD/RUB hedge (via Si futures) would then be needed to keep USD-
   denominated crypto exposure clean of FX risk. Not yet implemented:
   this is a **trading-strategy concern for `sleipnir-trading-core`**, not
   an SDK concern — `go-moex` just needs to support trading Si alongside
   the crypto-index contracts, which it already does (no instrument-type
   restriction in `forts.TradingClient`). Revisit once margining currency
   is confirmed for the target contracts.
5. **SIMBA A/B redundancy.** Spec §1.4.2 recommends joining both redundant
   multicast groups (A and B carry identical data) and deduplicating by
   MsgSeqNum, tolerating loss on either. v1.0's `forts/stream.go` joins
   only group A. Deferred, not forgotten — becomes important once real
   colocation traffic is flowing and packet loss is observed in practice.
6. **SIMBA TCP Replay service.** Spec provides a Replay service to
   actively request retransmission of a missed sequence range. v1.0
   doesn't implement it — on a detected `RptSeq` gap, `orderbook.Engine`
   is simply cleared and rebuilt from the next periodic
   `OrderBookSnapshot`. Functionally safe, but recovery latency is "wait
   for next snapshot cycle" instead of "instant". Revisit if that gap
   matters in practice once trading live.
7. **SIMBA socket buffer sizing.** `SIMBAConfig`/`ListenerConfig` expose
   `SocketReadBufferBytes` (SO_RCVBUF) but v1.0 has no MOEX-recommended
   value — needs to come from the colocation provisioning docs or MOEX
   support once that environment exists.
8. **`EmptyBook` per-instrument scoping.** The SBE `EmptyBook(4)` message
   carries no `SecurityID` field. Per spec §4.2.8 it should apply only to
   whatever instrument the stream's current position concerns; v1.0
   conservatively clears the book for **every** tracked instrument on any
   `EmptyBook`, which is safe (worst case: an extra unnecessary resync
   wait) but coarser than the spec allows. Needs a second look once a
   multi-instrument `WatchOrderBook` session is actually observed against
   live traffic.
9. **`TimeInForceFOK` (tag 59 = "4").** Follows the FIX 4.4 standard
   enumeration; not explicitly confirmed against MOEX FORTS FIX Gate spec
   text at authoring time. Confirm against the test circuit once available.
10. **FIX OrdRejReason/CxlRejReason → ErrorKind mapping**
    (`internal/moexerr/errors.go`) is a best-effort mapping from the FIX
    4.4 standard enumeration; needs contract-test verification against
    real FIX Gate rejects once test-circuit access exists (crypto-index
    futures and Si specifically, since those are the target instruments).
11. **`SecurityDefinition` is only partially decoded** (Symbol + SecurityID
    prefix of the root block — see `internal/simba/types.go` doc). Margins,
    price limits, CFICode, event dates etc. are not decoded because v1.0
    sources all of that from ISS instead. Revisit if a use case needs a
    field only SIMBA carries (e.g. real-time trading-status changes
    intraday, which ISS reference data does not push).
12. **ISS candles pagination.** `GetHistoricalCandles` issues exactly one
    ISS request per call. Empirically (see §7), ISS silently ignores a
    `limit` parameter larger than its internal page size and returns
    however much history it has buffered for that interval rather than
    the exact requested count — proper pagination (loop on the `history.cursor`
    equivalent / iss.meta paging block) is a v1.x follow-up, not done here.

## 6. Known limitations (intentional trade-offs, not bugs)

- **Position tracking accuracy** (`forts/types/position-info.go`,
  `forts/account.go`): FORTS FIX Gate has no "Position Report" message.
  Positions are derived purely from Execution Reports (`ExecType=Trade`)
  observed during the current process's lifetime. Accurate for fills
  since the SDK started tracking; **wrong (starts at zero) for pre-existing
  positions** until reconciled via `AccountClient.SeedPosition` at
  startup. This is a hard external-data-source limitation, not something
  more code can fix without a Plaza II clearing feed or broker EOD report
  integration (out of scope for v1.0).
- **`orderbook.Engine.Levels(n)` is O(levels log levels) per call** (sorts
  a map on every invocation). Fine for FORTS book depth; would need a
  sorted structure (skip list / red-black tree) if profiling shows this
  hot on a high-depth, high-frequency `WatchOrderBook` consumer. Public
  API is stable either way.
- **`Engine` is not internally synchronized** — callers must serialize
  `ApplyDelta`/`LoadSnapshot`/`Clear` calls per instrument. Satisfied
  naturally today because `forts/stream.go` runs one `Engine` per
  `SecurityID`, fed by a single dispatch path (the SIMBA listener's read
  goroutine) — do not start calling `Engine` methods from multiple
  goroutines without adding your own locking.
- **One SIMBA multicast interface (`NetworkInterface`) shared by every
  `Listen` call** — fine for a single-NIC colocation host; a multi-circuit
  redundant setup would need per-group interface selection, not modeled
  in v1.0's `SIMBAConfig`.

## 7. Verified against live ISS (production, no credentials)

Confirms the SDK's column mapping and the SECID/SHORTNAME naming
inversion documented in `forts/market.go` against real data (checked
2026-07-28):

```bash
curl 'https://iss.moex.com/iss/engines/futures/markets/forts/securities.json?iss.meta=off&iss.only=securities&securities.columns=SECID,SHORTNAME,DECIMALS,MINSTEP,LOTVOLUME,STEPPRICE,LASTTRADEDATE,ASSETCODE&limit=5'
# columns: ["SECID","SHORTNAME","DECIMALS","MINSTEP","LOTVOLUME","STEPPRICE","LASTTRADEDATE","ASSETCODE"]
# row: ["SiZ6","Si-12.26",0,1.00000,1000,1.00000,"2026-12-17","Si"]
#        ^short (wire "Symbol")  ^^^^^^^^ long (used as FIX Symbol/tag 55 per spec §3.2.5 note 5)
```

```bash
curl 'https://iss.moex.com/iss/engines/futures/markets/forts/securities/SiZ6/candles.json?iss.meta=off&interval=60&limit=3'
# columns: ["open","close","high","low","value","volume","begin","end"]
```

Both confirm `forts/market.go`'s `issSecurityRow`/`issCandleRow` struct
tags and the SECID (short)/SHORTNAME (long) mapping are correct against
the live exchange, not just against the spec prose. `Si-12.26` is the
example symbol used throughout `examples/` for this reason — it is a
real, currently-listed instrument.

## 8. Next steps

1. `sleipnir-trading-core`: create branch `moex-connector` from `qa`
   (localize all MOEX-related changes there per project convention).
2. Add `internal/connectors/moex/{common,forts}` + `Exchange` enum entries
   (`moex_forts`, naming decided per §2 principle 2) + a
   `factory.ProduceExchange` case wiring `forts.Client` into the existing
   `ExchangeConnector` composite interface (`TradingConnector` +
   `MarketDataConnector` + `FuturesAccountConnector`).
3. Once test-circuit credentials arrive (§5.1): smoke-test `Connect` +
   `CreateOrder`/`CancelOrder` against the FORTS test environment, then
   revisit items in §5 that are marked "confirm against test circuit."
4. Once colocation/broker circuit is live (§5.2): validate `WatchOrderBook`
   against real SIMBA traffic, then prioritize §5 items 5/6/8 (A/B
   redundancy, TCP Replay, EmptyBook scoping) based on observed behavior
   rather than speculative correctness.
