/*
FILE: internal/iss/client.go

DESCRIPTION:
Generic HTTP client for MOEX ISS (Informational & Statistical Server).
Knows nothing about FORTS/Shares-specific endpoints — it only knows how to
GET a path with query parameters, optionally authenticate via the ISS
passport cookie for paid real-time access, throttle client-side, and parse
the columnar table envelope (table.go). Section-specific path building
(e.g. "/engines/futures/markets/forts/securities.json") lives in
forts/market.go, exactly mirroring how internal/rest.Client in go-okx
knows nothing about "/api/v5/trade/order" being a swap vs spot endpoint.
*/
package iss

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/tonymontanov/go-moex/internal/moexerr"
	"github.com/tonymontanov/go-moex/internal/moexlog"
	"github.com/tonymontanov/go-moex/internal/moexmet"
)

// Config — ISS transport settings, populated from moex.ISSConfig in
// forts/client.go (explicit conversion, no import cycle).
type Config struct {
	BaseURL             string
	Login               string
	Password            string
	RequestTimeout      time.Duration
	MaxIdleConns        int
	MaxIdleConnsPerHost int
	IdleConnTimeout     time.Duration
	RequestsPerSecond   float64
	UserAgent           string

	Logger  moexlog.Logger
	Metrics moexmet.CounterFactory
}

// Client — generic ISS REST client.
type Client struct {
	cfg     Config
	http    *http.Client
	limiter *limiter

	authMu sync.Mutex
	authAt time.Time // zero until the first successful passport authenticate.
}

// NewClient creates a Client. If cfg.Login/Password are set, the first
// request triggers passport authentication (see auth.go); construction
// itself never blocks on the network.
func NewClient(cfg Config) (*Client, error) {
	var jar *cookiejar.Jar
	var err error
	jar, err = cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("iss: create cookie jar: %w", err)
	}

	var transport *http.Transport = &http.Transport{
		MaxIdleConns:        cfg.MaxIdleConns,
		MaxIdleConnsPerHost: cfg.MaxIdleConnsPerHost,
		IdleConnTimeout:     cfg.IdleConnTimeout,
	}

	return &Client{
		cfg: cfg,
		http: &http.Client{
			Transport: transport,
			Jar:       jar,
			Timeout:   cfg.RequestTimeout,
		},
		limiter: newLimiter(cfg.RequestsPerSecond),
	}, nil
}

// Get issues a GET request to cfg.BaseURL+path with the given query
// parameters (".json" format is assumed — every ISS endpoint supports it
// and it is far cheaper to parse than the default XML) and returns the
// decoded table Response.
func (c *Client) Get(ctx context.Context, path string, query url.Values) (Response, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	if err := c.ensureAuthenticated(ctx); err != nil {
		// Authentication failure degrades to the free/delayed tier rather
		// than failing the call outright — ISS still serves public data
		// without a passport cookie, just delayed and more rate-limited.
		c.logger().Warn("iss: passport authentication failed, continuing unauthenticated", moexlog.Err(err))
	}

	var fullURL string = c.cfg.BaseURL + path
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}

	var req *http.Request
	var err error
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, moexerr.New(moexerr.TransportISS, moexerr.ErrorKindInvalidRequest, "", fmt.Sprintf("build request: %v", err), err)
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Accept", "application/json")

	var resp *http.Response
	resp, err = c.http.Do(req)
	if err != nil {
		if c.cfg.Metrics != nil {
			c.cfg.Metrics.Counter("moex_iss_requests_total", "result", "network_error").Inc()
		}
		return nil, moexerr.New(moexerr.TransportISS, moexerr.ErrorKindNetwork, "", fmt.Sprintf("GET %s: %v", path, err), err)
	}
	defer resp.Body.Close()

	var body []byte
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, moexerr.New(moexerr.TransportISS, moexerr.ErrorKindNetwork, "", "read response body", err)
	}

	if c.cfg.Metrics != nil {
		c.cfg.Metrics.Counter("moex_iss_requests_total", "result", "status_"+strconv.Itoa(resp.StatusCode)).Inc()
	}

	if resp.StatusCode >= 400 {
		var kind moexerr.ErrorKind = moexerr.MapHTTPStatus(resp.StatusCode)
		return nil, moexerr.New(moexerr.TransportISS, kind, strconv.Itoa(resp.StatusCode), fmt.Sprintf("GET %s: HTTP %d: %s", path, resp.StatusCode, truncate(body, 300)), nil)
	}

	var out Response
	out, err = ParseResponse(body)
	if err != nil {
		return nil, moexerr.New(moexerr.TransportISS, moexerr.ErrorKindNetwork, "", "parse response", err)
	}
	return out, nil
}

func (c *Client) logger() moexlog.Logger {
	if c.cfg.Logger != nil {
		return c.cfg.Logger
	}
	return moexlog.Noop()
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
