/*
FILE: internal/fix/config.go

DESCRIPTION:
Transport-level FIX session config. Populated from the public
moex.FIXConfig in the root package (explicit struct conversion happens
there, forts/client.go) to avoid an import cycle — identical pattern to
internal/rest.Config in go-okx.
*/
package fix

import (
	"time"

	"github.com/tonymontanov/go-moex/internal/moexlog"
	"github.com/tonymontanov/go-moex/internal/moexmet"
)

// Config — FIX session parameters.
type Config struct {
	Host         string
	Port         int
	SenderCompID string
	TargetCompID string
	// HeartBtInt — heartbeat interval sent in Logon (tag 108) and used to
	// schedule outgoing Heartbeat messages.
	HeartBtInt time.Duration
	// ResetSeqNumFlag — send Logon with tag 141=Y. Per spec §3.2.1 this MUST
	// be true for the first Logon of a trading day.
	ResetSeqNumFlag bool
	DialTimeout     time.Duration

	ReconnectInitialBackoff time.Duration
	ReconnectMaxBackoff     time.Duration
	ReconnectJitter         float64

	Logger  moexlog.Logger
	Metrics moexmet.CounterFactory
}
