/*
FILE: metrics.go

DESCRIPTION:
Public re-export of the metrics interface. The interface itself lives in
internal/moexmet so that fix/simba/iss can use it without an import cycle.
See internal/moexmet for the Prometheus adapter pattern (identical to
go-okx/go-bybit — Counter is Inc/Add-shaped, so prometheus.Counter already
satisfies it).
*/
package moex

import "github.com/tonymontanov/go-moex/internal/moexmet"

// Counter — metrics counter. Alias.
type Counter = moexmet.Counter

// CounterFactory — counter factory. Alias.
type CounterFactory = moexmet.CounterFactory

// NoopMetrics returns a no-op counter factory. Used as the default.
func NoopMetrics() CounterFactory { return moexmet.Noop() }
