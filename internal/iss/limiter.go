/*
FILE: internal/iss/limiter.go

DESCRIPTION:
Minimal token-bucket limiter for client-side ISS throttling (see
moex.ISSConfig.RequestsPerSecond doc — protects against exceeding the
free-tier ~10 req/s ceiling before MOEX's own server-side limiter would
return 429). Intentionally not golang.org/x/time/rate to keep the SDK's
dependency footprint at zero beyond shopspring/decimal, matching the
go-okx/go-bybit convention of hand-rolled infra pieces this small.
*/
package iss

import (
	"context"
	"sync"
	"time"
)

// limiter — simple token bucket, refilled continuously (rate tokens/sec),
// capacity == rate (i.e. up to 1 second of burst).
type limiter struct {
	mu         sync.Mutex
	rate       float64 // tokens per second. <= 0 disables throttling.
	tokens     float64
	lastRefill time.Time
}

func newLimiter(ratePerSecond float64) *limiter {
	return &limiter{rate: ratePerSecond, tokens: ratePerSecond, lastRefill: time.Now()}
}

// Wait blocks until a token is available or ctx is done.
func (l *limiter) Wait(ctx context.Context) error {
	if l.rate <= 0 {
		return nil
	}
	for {
		var wait time.Duration = l.tryAcquire()
		if wait <= 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

func (l *limiter) tryAcquire() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	var now time.Time = time.Now()
	var elapsed float64 = now.Sub(l.lastRefill).Seconds()
	l.lastRefill = now
	l.tokens += elapsed * l.rate
	if l.tokens > l.rate {
		l.tokens = l.rate
	}

	if l.tokens >= 1 {
		l.tokens--
		return 0
	}
	var deficit float64 = 1 - l.tokens
	return time.Duration(deficit / l.rate * float64(time.Second))
}
