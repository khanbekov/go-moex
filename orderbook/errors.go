/*
FILE: orderbook/errors.go

DESCRIPTION:
Sentinel errors for gap/resync signaling. Kept as a plain exported var
(errors.Is-compatible) rather than a full moexerr.Error — this package has
no transport of its own to attribute the error to, and gap handling is a
control-flow decision the caller makes synchronously (see
ErrSequenceGap doc), not something that needs Kind/Transport metadata.
*/
package orderbook

import "errors"

// ErrSequenceGap is returned by Engine.ApplyDelta when seq is not exactly
// the previously seen seq + 1. The caller (forts/stream.go) must treat the
// local book as stale, discard it, and re-request a fresh snapshot
// (OrderBookSnapshot for SIMBA SPECTRA) before resuming — per spec §1.4.6:
// "if RptSeq ... is not exactly larger by one, this points to the fact
// that some updates were missed".
var ErrSequenceGap = errors.New("orderbook: sequence gap detected, resync required")

// ErrUnknownOrder is returned by ApplyDelta(ActionDelete) / decrement paths
// when the referenced OrderID is not currently tracked. Not fatal by
// itself (can legitimately happen right after a resync boundary — see
// spec's own note about the EmptyBook/OrderBookSnapshot race) — callers
// typically log and continue rather than treating it as ErrSequenceGap.
var ErrUnknownOrder = errors.New("orderbook: unknown order id")
