/*
FILE: client.go

DESCRIPTION:
The root public SDK Client. Holds shared resources (logger, metrics, config)
and provides a lazy domain sub-client per market section. In v1.0 only the
FORTS profile (Derivatives market — futures) is supported; Shares (v2.0,
equities T+ market) is reserved and implemented in a separate iteration.

Unlike go-okx/go-bybit, Client does NOT own a single shared low-level
transport: FORTS itself owns its FIX session, ISS REST client and SIMBA
listener (see forts.Client), because in v2.0 Shares will use a *different*
FIX Gate / SIMBA endpoint set (Equities market, not Derivatives) — sharing
transport instances across sections would leak Derivatives-specific
connectivity into Shares. What IS shared is the two-layer architecture:
Shares will reuse the internal/iss, internal/fix, internal/simba,
orderbook packages exactly as forts does — never section-specific code.

MAIN FUNCTIONS:
  - NewClient(cfg)     : constructor with Config validation and defaults.
  - (Client).Forts()   : returns the FORTS-profile sub-client. Created
    lazily on first access.
  - (Client).Close()   : gracefully shuts down background operations
    (FIX sessions, SIMBA listeners). Non-blocking.

MAIN ENTITIES:
  - Client            : root SDK object.
  - fortsClientCtor    : internal contract through which the forts package
    creates its client. Avoids an import cycle between
    the root (where Client lives) and forts (where
    forts.Client lives) — identical pattern to
    okx.RegisterSwapFactory in go-okx.

DEPENDENCIES:
  - sync: lazy sub-client initialization.
*/
package moex

import "sync"

// Client — root SDK object.
type Client struct {
	cfg    Config
	logger Logger

	fortsOnce sync.Once
	fortsVal  any

	closers   []func() error
	closersMu sync.Mutex
}

// NewClient creates the root SDK client. cfg goes through withDefaults +
// validate.
func NewClient(cfg Config) (*Client, error) {
	cfg = cfg.withDefaults()
	var err error = cfg.validate()
	if err != nil {
		return nil, err
	}
	return &Client{cfg: cfg, logger: cfg.Logger}, nil
}

// Config returns a copy of the final config (after withDefaults). Useful
// for diagnostics/metrics.
func (c *Client) Config() Config { return c.cfg }

// Logger returns the current logger.
func (c *Client) Logger() Logger { return c.logger }

// RegisterCloser records a cleanup function to run on Client.Close(). Called
// by section sub-clients (forts.Client) once they lazily create a FIX
// session or SIMBA listener.
func (c *Client) RegisterCloser(fn func() error) {
	c.closersMu.Lock()
	defer c.closersMu.Unlock()
	c.closers = append(c.closers, fn)
}

// Close gracefully shuts down every background transport created by any
// section sub-client (FIX sessions, SIMBA listeners, idle HTTP connections).
// Safe to call multiple times. Does not cancel in-flight Watch* calls —
// callers are expected to cancel their own context first (see spec §6
// "no goroutine leaks on context cancellation").
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.closersMu.Lock()
	var closers []func() error = c.closers
	c.closers = nil
	c.closersMu.Unlock()

	var firstErr error
	for _, closeFn := range closers {
		if err := closeFn(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// fortsClientFactory — FORTS client builder function. Registered by the
// forts package via RegisterFortsFactory in init(). Avoids the import cycle
// (forts imports moex for Config/Error/Logger; moex cannot import forts).
var fortsClientFactory func(c *Client) any

// RegisterFortsFactory registers the FORTS client factory. Must be called
// from the forts package's init(). Idempotent.
func RegisterFortsFactory(f func(c *Client) any) {
	if fortsClientFactory == nil {
		fortsClientFactory = f
	}
}

// Forts returns the FORTS (Derivatives market — futures) sub-client. The
// return type is any because the root package cannot import forts. The
// caller immediately type-asserts to *forts.Client.
//
// Usage idiom:
//
//	var fortsClient *forts.Client = client.Forts().(*forts.Client)
//
// Lazy: created on first access via the registered factory. If the forts
// package is not imported (factory not registered), returns nil and logs
// a warning.
func (c *Client) Forts() any {
	c.fortsOnce.Do(func() {
		if fortsClientFactory == nil {
			c.logger.Warn("moex.Client.Forts: forts factory is not registered; import _ \"github.com/tonymontanov/go-moex/forts\"")
			return
		}
		c.fortsVal = fortsClientFactory(c)
	})
	return c.fortsVal
}
