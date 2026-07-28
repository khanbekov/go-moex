/*
FILE: forts/client.go

DESCRIPTION:
FORTS (Derivatives market — futures) section client. Owns the three v1.0
transports (ISS, FIX, SIMBA) and the shared state that ties them together:
open-order cache, position tracker, per-instrument order-book engines.
Registers itself with the root moex.Client via moex.RegisterFortsFactory in
init() — see client.go in the repository root for why this indirection
exists (avoids an import cycle).

CONNECTION LIFECYCLE:
  - ISS is stateless HTTP — the iss.Client is created eagerly in NewClient.
  - FIX requires an explicit Client.Connect(ctx) call before any
    TradingClient method works — Logon is a network operation that can
    fail, so it is never triggered implicitly from a lazy getter (unlike
    go-okx's Swap()/Spot(), which don't need a handshake at all).
  - SIMBA listeners are started per-instrument by
    MarketDataClient.WatchOrderBook, not eagerly — a process that only
    trades on FIX/ISS without market data has no reason to join any
    multicast group.
*/
package forts

import (
	"context"
	"sync"

	moex "github.com/tonymontanov/go-moex"
	"github.com/tonymontanov/go-moex/forts/types"
	"github.com/tonymontanov/go-moex/internal/fix"
	"github.com/tonymontanov/go-moex/internal/iss"
	"github.com/tonymontanov/go-moex/orderbook"
)

func init() {
	moex.RegisterFortsFactory(func(c *moex.Client) any { return NewClient(c) })
}

// Client — FORTS section client.
type Client struct {
	root   *moex.Client
	cfg    moex.Config
	logger moex.Logger

	issClient *iss.Client

	fixMu      sync.RWMutex
	fixSession *fix.Session
	clOrdIDGen *clOrdIDGenerator

	orderCorrelator      *correlator[*types.OrderInfo]
	cancelCorrelator     *correlator[*types.OrderInfo]
	massCancelCorrelator *correlator[massCancelResult]

	openOrdersMu sync.RWMutex
	openOrders   map[int64]*types.OrderInfo
	clOrdToOrder map[string]int64 // ClOrdID -> OrderID, once known.

	orderWatchMu  sync.Mutex
	orderWatchers []chan *types.OrderInfo

	positions *positionTracker

	simbaMu            sync.Mutex
	engines            map[int32]*orderbook.Engine
	symbolToSecurityID map[string]int32

	market  *MarketDataClient
	trading *TradingClient
	account *AccountClient
}

// NewClient constructs the FORTS client from the root moex.Client. Exposed
// (rather than only reachable via moex.Client.Forts()) so tests and
// advanced users can construct it directly against a *moex.Client without
// a type assertion.
func NewClient(root *moex.Client) *Client {
	var cfg moex.Config = root.Config()
	var logger moex.Logger = root.Logger()

	var issClient *iss.Client
	var err error
	issClient, err = iss.NewClient(iss.Config{
		BaseURL:             cfg.ISS.BaseURL,
		Login:               cfg.ISS.Login,
		Password:            cfg.ISS.Password,
		RequestTimeout:      cfg.ISS.RequestTimeout,
		MaxIdleConns:        cfg.ISS.MaxIdleConns,
		MaxIdleConnsPerHost: cfg.ISS.MaxIdleConnsPerHost,
		IdleConnTimeout:     cfg.ISS.IdleConnTimeout,
		RequestsPerSecond:   cfg.ISS.RequestsPerSecond,
		UserAgent:           cfg.UserAgent,
		Logger:              logger,
		Metrics:             cfg.Metrics,
	})
	if err != nil {
		// iss.NewClient only fails on cookiejar construction, which never
		// happens with a nil PublicSuffixList — kept as an error return for
		// API hygiene, degrade to a nil client + logged error rather than
		// panicking a lazily-constructed sub-client.
		logger.Error("forts: failed to construct ISS client", moex.Err(err))
	}

	var c *Client = &Client{
		root:                 root,
		cfg:                  cfg,
		logger:               logger,
		issClient:            issClient,
		clOrdIDGen:           newClOrdIDGenerator("GM"),
		orderCorrelator:      newCorrelator[*types.OrderInfo](),
		cancelCorrelator:     newCorrelator[*types.OrderInfo](),
		massCancelCorrelator: newCorrelator[massCancelResult](),
		openOrders:           make(map[int64]*types.OrderInfo),
		clOrdToOrder:         make(map[string]int64),
		positions:            newPositionTracker(),
		engines:              make(map[int32]*orderbook.Engine),
		symbolToSecurityID:   make(map[string]int32),
	}
	c.market = &MarketDataClient{c: c}
	c.trading = &TradingClient{c: c}
	c.account = &AccountClient{c: c}
	return c
}

// MarketData returns the reference-data/candles/order-book sub-client.
func (c *Client) MarketData() *MarketDataClient { return c.market }

// Trading returns the order-entry sub-client. Panics-free but every method
// returns ErrorKindInvalidRequest until Connect has succeeded.
func (c *Client) Trading() *TradingClient { return c.trading }

// Account returns the position-tracking sub-client (see
// forts/types/position-info.go for its accuracy caveat).
func (c *Client) Account() *AccountClient { return c.account }

// Connect dials the FIX Gate session (Logon) used by Trading(). Safe to
// call once; subsequent calls return nil if already connected.
func (c *Client) Connect(ctx context.Context) error {
	c.fixMu.Lock()
	defer c.fixMu.Unlock()
	if c.fixSession != nil {
		return nil
	}
	if c.cfg.FIX.SenderCompID == "" {
		return moex.NewError(moex.TransportFIX, moex.ErrorKindInvalidRequest, "", "forts: Config.FIX.SenderCompID is required to connect (see moex.FIXConfig doc)", nil)
	}

	var session *fix.Session
	var err error
	session, err = fix.Dial(fix.Config{
		Host:                    c.cfg.FIX.Host,
		Port:                    c.cfg.FIX.Port,
		SenderCompID:            c.cfg.FIX.SenderCompID,
		TargetCompID:            c.cfg.FIX.TargetCompID,
		HeartBtInt:              c.cfg.FIX.HeartBtInt,
		ResetSeqNumFlag:         c.cfg.FIX.ResetSeqNumFlag,
		DialTimeout:             c.cfg.FIX.DialTimeout,
		ReconnectInitialBackoff: c.cfg.FIX.ReconnectInitialBackoff,
		ReconnectMaxBackoff:     c.cfg.FIX.ReconnectMaxBackoff,
		ReconnectJitter:         c.cfg.FIX.ReconnectJitter,
	})
	if err != nil {
		return moex.NewError(moex.TransportFIX, moex.ErrorKindNetwork, "", "forts: dial FIX Gate", err)
	}
	session.SetAppHandler(c.handleFIXAppMessage)

	if err = session.Logon(ctx); err != nil {
		_ = session.Close()
		return moex.NewError(moex.TransportFIX, moex.ErrorKindNetwork, "", "forts: FIX Logon", err)
	}

	c.fixSession = session
	c.root.RegisterCloser(c.Close)
	return nil
}

// Close shuts down the FIX session (best-effort Logout). SIMBA listeners
// started by WatchOrderBook are stopped via their own ctx cancellation, not
// here — see stream.go.
func (c *Client) Close() error {
	c.fixMu.Lock()
	defer c.fixMu.Unlock()
	if c.fixSession == nil {
		return nil
	}
	var err error = c.fixSession.Close()
	c.fixSession = nil
	return err
}

func (c *Client) session() (*fix.Session, error) {
	c.fixMu.RLock()
	defer c.fixMu.RUnlock()
	if c.fixSession == nil {
		return nil, moex.NewError(moex.TransportFIX, moex.ErrorKindInvalidRequest, "", "forts: not connected — call Client.Connect(ctx) before using Trading()", nil)
	}
	return c.fixSession, nil
}
