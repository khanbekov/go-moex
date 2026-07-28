/*
FILE: logger.go

DESCRIPTION:
Public re-export of the Logger interface and typed Field factories. The
interface/type itself lives in internal/moexlog; here — thin aliases.
*/
package moex

import "github.com/tonymontanov/go-moex/internal/moexlog"

// Logger — SDK logging interface. Alias.
type Logger = moexlog.Logger

// Field — typed log field. Alias.
type Field = moexlog.Field

// FieldKind — Field discriminator. Alias.
type FieldKind = moexlog.FieldKind

// FieldKind values.
const (
	FieldKindString = moexlog.FieldKindString
	FieldKindInt    = moexlog.FieldKindInt
	FieldKindFloat  = moexlog.FieldKindFloat
	FieldKindBool   = moexlog.FieldKindBool
	FieldKindError  = moexlog.FieldKindError
)

// NoopLogger returns a no-op logger. Used as the default.
func NoopLogger() Logger { return moexlog.Noop() }

// Str / Int / Float / Bool / Err — Field factories.
func Str(key, value string) Field       { return moexlog.Str(key, value) }
func Int(key string, v int64) Field     { return moexlog.Int(key, v) }
func Float(key string, v float64) Field { return moexlog.Float(key, v) }
func Bool(key string, v bool) Field     { return moexlog.Bool(key, v) }
func Err(err error) Field               { return moexlog.Err(err) }
