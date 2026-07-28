/*
FILE: internal/moexerr/errors.go

DESCRIPTION:
SDK error type + categories, shared by every transport (internal/iss,
internal/fix, internal/simba). Placed in an internal package so that any
internal/* package can use it without an import cycle on the root moex
package. The root package re-exports these entities via type aliases
(see errors.go in the repository root).

MOEX has no single "exchange error code" namespace shared across
transports, unlike OKX/Bybit (one JSON envelope with `code`/`msg`):
  - ISS (REST)  reports errors purely via HTTP status (400/401/429/5xx) —
    there is no ISS-specific error code to map.
  - FIX Gate    reports business-level rejects via OrdRejReason (tag 103,
    Execution Report) and CxlRejReason (tag 102, Order Cancel Reject) —
    both small integer enums, specific to the FIX application layer.
  - SIMBA/TWIME carry no application-level "error" messages at all (market
    data only for SIMBA in v1.0; TWIME is deferred to v1.x) — failures
    surface as protocol-level Reject/session termination instead.

Transport is therefore a first-class field on Error (see Transport type
below), and FIX-specific mapping lives in MapFIXOrdRejReason /
MapFIXCxlRejReason rather than a single MapExchangeCode like in go-okx.
*/
package moexerr

import (
	"errors"
	"fmt"
)

// ErrorKind — SDK error category.
type ErrorKind uint8

const (
	ErrorKindUnknown ErrorKind = iota
	ErrorKindNetwork
	ErrorKindRateLimit
	ErrorKindAuth
	ErrorKindInvalidRequest
	ErrorKindExchange
	// ErrorKindProtocol — session/transport-layer failure that is not a
	// business rejection: FIX Reject/Logout, SIMBA sequence gap that could
	// not be resynced, malformed SBE frame, TCP Replay failure, etc.
	ErrorKindProtocol
)

// String — human-readable category name.
func (k ErrorKind) String() string {
	switch k {
	case ErrorKindNetwork:
		return "network"
	case ErrorKindRateLimit:
		return "rate_limit"
	case ErrorKindAuth:
		return "auth"
	case ErrorKindInvalidRequest:
		return "invalid_request"
	case ErrorKindExchange:
		return "exchange"
	case ErrorKindProtocol:
		return "protocol"
	default:
		return "unknown"
	}
}

// Transport — which SDK transport produced the error. Unlike OKX/Bybit,
// MOEX has no single wire protocol, so callers frequently need to know
// which gateway failed (e.g. to decide whether a FIX reconnect or an ISS
// retry is appropriate).
type Transport uint8

const (
	TransportUnknown Transport = iota
	TransportISS
	TransportFIX
	TransportSIMBA
	TransportTWIME
)

// String — human-readable transport name.
func (t Transport) String() string {
	switch t {
	case TransportISS:
		return "iss"
	case TransportFIX:
		return "fix"
	case TransportSIMBA:
		return "simba"
	case TransportTWIME:
		return "twime"
	default:
		return "unknown"
	}
}

// Error — unified SDK error type.
type Error struct {
	Kind       ErrorKind
	Transport  Transport
	HTTPStatus int
	// Code — transport-native rejection code as a string (FIX OrdRejReason/
	// CxlRejReason rendered as decimal; empty for ISS/SIMBA).
	Code    string
	Message string
	Cause   error
}

// Error implements the error interface.
func (e *Error) Error() string {
	switch {
	case e.Code != "" && e.Cause != nil:
		return fmt.Sprintf("moex %s/%s: code=%s status=%d msg=%q: %v", e.Transport, e.Kind, e.Code, e.HTTPStatus, e.Message, e.Cause)
	case e.Code != "":
		return fmt.Sprintf("moex %s/%s: code=%s status=%d msg=%q", e.Transport, e.Kind, e.Code, e.HTTPStatus, e.Message)
	case e.Cause != nil:
		return fmt.Sprintf("moex %s/%s: status=%d msg=%q: %v", e.Transport, e.Kind, e.HTTPStatus, e.Message, e.Cause)
	default:
		return fmt.Sprintf("moex %s/%s: status=%d msg=%q", e.Transport, e.Kind, e.HTTPStatus, e.Message)
	}
}

// Unwrap — for errors.Is/As.
func (e *Error) Unwrap() error { return e.Cause }

// New creates a *Error.
func New(transport Transport, kind ErrorKind, code, msg string, cause error) *Error {
	return &Error{Kind: kind, Transport: transport, Code: code, Message: msg, Cause: cause}
}

// IsNetwork returns true if err has category Network.
func IsNetwork(err error) bool { return matchKind(err, ErrorKindNetwork) }

// IsRateLimit returns true if err has category RateLimit.
func IsRateLimit(err error) bool { return matchKind(err, ErrorKindRateLimit) }

// IsAuth returns true if err has category Auth.
func IsAuth(err error) bool { return matchKind(err, ErrorKindAuth) }

// IsInvalidRequest returns true if err has category InvalidRequest.
func IsInvalidRequest(err error) bool { return matchKind(err, ErrorKindInvalidRequest) }

// IsExchange returns true if err has category Exchange.
func IsExchange(err error) bool { return matchKind(err, ErrorKindExchange) }

// IsProtocol returns true if err has category Protocol.
func IsProtocol(err error) bool { return matchKind(err, ErrorKindProtocol) }

func matchKind(err error, kind ErrorKind) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind == kind
	}
	return false
}

// MapHTTPStatus returns the SDK error category for an ISS HTTP status code.
func MapHTTPStatus(status int) ErrorKind {
	switch {
	case status == 429:
		return ErrorKindRateLimit
	case status == 401 || status == 403:
		return ErrorKindAuth
	case status >= 500:
		return ErrorKindNetwork
	case status >= 400:
		return ErrorKindInvalidRequest
	default:
		return ErrorKindUnknown
	}
}

/*
MapFIXOrdRejReason maps the FIX Gate 'OrdRejReason' (tag 103, carried in
Execution Report where ExecType=8/Rejected) to an SDK ErrorKind.

The FIX Gate specification (spectra_fixgate_en.pdf, §2.10 'Other fields')
enumerates the reason codes; MOEX groups them loosely as "broker/limits"
(risk checks failed on the broker/clearing side) vs "instrument/session"
(bad symbol, closed trading session) vs "unknown". Until the exact codes
used by a specific broker's risk profile are confirmed against the test
circuit, everything not explicitly known maps conservatively to
ErrorKindExchange (i.e. "the exchange refused the request", not a bug in
the SDK) rather than ErrorKindInvalidRequest.

TODO(v1.0, needs FIX test circuit access): once broker connectivity from
Finam is live, capture the actual OrdRejReason values returned for the
target instruments (crypto index futures / Si currency futures) and refine
this mapping with contract tests (see docs/handoff.md, "Open questions").
*/
func MapFIXOrdRejReason(reasonCode int) ErrorKind {
	switch reasonCode {
	case 0: // Broker option (generic risk/limits rejection).
		return ErrorKindExchange
	case 1: // Unknown symbol.
		return ErrorKindInvalidRequest
	case 5: // Duplicate order (ClOrdID reuse).
		return ErrorKindInvalidRequest
	default:
		return ErrorKindExchange
	}
}

// MapFIXCxlRejReason maps the FIX Gate 'CxlRejReason' (tag 102, carried in
// Order Cancel Reject) to an SDK ErrorKind. See MapFIXOrdRejReason for the
// same caveat regarding exact code capture from the live test circuit.
func MapFIXCxlRejReason(reasonCode int) ErrorKind {
	switch reasonCode {
	case 0: // Too late to cancel (already filled/canceled).
		return ErrorKindExchange
	case 1: // Unknown order (ClOrdID/OrderID not found).
		return ErrorKindInvalidRequest
	default:
		return ErrorKindExchange
	}
}
