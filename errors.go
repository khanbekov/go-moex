/*
FILE: errors.go

DESCRIPTION:
Public re-export of entities from internal/moexerr. The type itself,
categories, transports, and FIX reject-code mappings live in
internal/moexerr (see documentation there); here — only type aliases so
that the user works through the familiar root-package import:

	import moex "github.com/tonymontanov/go-moex"

	if moex.IsRateLimit(err) { ... }
	if moex.IsProtocol(err)  { ... } // FIX/SIMBA session-level failure
*/
package moex

import "github.com/tonymontanov/go-moex/internal/moexerr"

// Error — SDK error type. Alias.
type Error = moexerr.Error

// ErrorKind — SDK error category. Alias.
type ErrorKind = moexerr.ErrorKind

// Categories.
const (
	ErrorKindUnknown        = moexerr.ErrorKindUnknown
	ErrorKindNetwork        = moexerr.ErrorKindNetwork
	ErrorKindRateLimit      = moexerr.ErrorKindRateLimit
	ErrorKindAuth           = moexerr.ErrorKindAuth
	ErrorKindInvalidRequest = moexerr.ErrorKindInvalidRequest
	ErrorKindExchange       = moexerr.ErrorKindExchange
	ErrorKindProtocol       = moexerr.ErrorKindProtocol
)

// Transport — which SDK transport (ISS/FIX/SIMBA/TWIME) produced the error. Alias.
type Transport = moexerr.Transport

// Transports.
const (
	TransportUnknown = moexerr.TransportUnknown
	TransportISS     = moexerr.TransportISS
	TransportFIX     = moexerr.TransportFIX
	TransportSIMBA   = moexerr.TransportSIMBA
	TransportTWIME   = moexerr.TransportTWIME
)

// NewError creates a *Error.
func NewError(transport Transport, kind ErrorKind, code, msg string, cause error) *Error {
	return moexerr.New(transport, kind, code, msg, cause)
}

// IsNetwork / IsRateLimit / IsAuth / IsInvalidRequest / IsExchange / IsProtocol
// — error category predicates.
func IsNetwork(err error) bool        { return moexerr.IsNetwork(err) }
func IsRateLimit(err error) bool      { return moexerr.IsRateLimit(err) }
func IsAuth(err error) bool           { return moexerr.IsAuth(err) }
func IsInvalidRequest(err error) bool { return moexerr.IsInvalidRequest(err) }
func IsExchange(err error) bool       { return moexerr.IsExchange(err) }
func IsProtocol(err error) bool       { return moexerr.IsProtocol(err) }

// MapHTTPStatus returns the SDK error category for an ISS HTTP status code.
func MapHTTPStatus(status int) ErrorKind { return moexerr.MapHTTPStatus(status) }

// MapFIXOrdRejReason / MapFIXCxlRejReason — FIX Gate reject-code mapping.
func MapFIXOrdRejReason(code int) ErrorKind { return moexerr.MapFIXOrdRejReason(code) }
func MapFIXCxlRejReason(code int) ErrorKind { return moexerr.MapFIXCxlRejReason(code) }
