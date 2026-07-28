/*
FILE: internal/iss/auth.go

DESCRIPTION:
ISS paid-subscription authentication (see moex.ISSConfig.Login/Password
doc). MOEX ISS does not use a request header/API key — it uses a session
cookie ("MicexPassportCert") issued by a dedicated passport host after an
HTTP Basic Auth challenge:

	GET https://passport.moex.com/authenticate   (Authorization: Basic ...)

The response's Set-Cookie carries Domain=.moex.com, so once captured by
the shared cookiejar (see client.go), it is automatically attached to
subsequent requests to iss.moex.com by net/http — no manual cookie
plumbing needed beyond triggering the initial authenticate call.

The passport cookie has a server-side TTL (observed empirically to be on
the order of tens of minutes, not documented precisely by MOEX) — this SDK
conservatively re-authenticates every authRefreshInterval regardless of
whether the previous cookie technically expired yet, trading a few extra
cheap requests for not having to detect expiry from a 401 mid-session.
*/
package iss

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

const (
	passportURL         = "https://passport.moex.com/authenticate"
	authRefreshInterval = 20 * time.Minute
)

func (c *Client) ensureAuthenticated(ctx context.Context) error {
	if c.cfg.Login == "" || c.cfg.Password == "" {
		return nil // free tier — nothing to do.
	}

	c.authMu.Lock()
	defer c.authMu.Unlock()

	if !c.authAt.IsZero() && time.Since(c.authAt) < authRefreshInterval {
		return nil
	}

	var req *http.Request
	var err error
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, passportURL, nil)
	if err != nil {
		return fmt.Errorf("iss: build passport request: %w", err)
	}
	req.SetBasicAuth(c.cfg.Login, c.cfg.Password)

	var resp *http.Response
	resp, err = c.http.Do(req)
	if err != nil {
		return fmt.Errorf("iss: passport authenticate: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		return fmt.Errorf("iss: passport authenticate: HTTP %d", resp.StatusCode)
	}

	c.authAt = time.Now()
	return nil
}
