package forts

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestDecimalFromSIMBAMantissa(t *testing.T) {
	var cases = []struct {
		mantissa int64
		want     string
	}{
		{123450, "1.2345"},
		{100000, "1"},
		{-50000, "-0.5"},
		{0, "0"},
	}
	for _, c := range cases {
		var got decimal.Decimal = decimalFromSIMBAMantissa(c.mantissa)
		if got.String() != c.want {
			t.Errorf("decimalFromSIMBAMantissa(%d) = %s, want %s", c.mantissa, got.String(), c.want)
		}
	}
}

func TestMantissaFromDecimal(t *testing.T) {
	var cases = []struct {
		in   string
		want int64
	}{
		{"1.2345", 123450},
		{"1", 100000},
		{"-0.5", -50000},
		{"0", 0},
	}
	for _, c := range cases {
		var d decimal.Decimal
		var err error
		d, err = decimal.NewFromString(c.in)
		if err != nil {
			t.Fatalf("decimal.NewFromString(%q): %v", c.in, err)
		}
		var got int64 = mantissaFromDecimal(d)
		if got != c.want {
			t.Errorf("mantissaFromDecimal(%s) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestMantissaDecimalRoundTrip(t *testing.T) {
	var mantissas = []int64{0, 123450, -987650, 100000, 1}
	for _, m := range mantissas {
		var got int64 = mantissaFromDecimal(decimalFromSIMBAMantissa(m))
		if got != m {
			t.Errorf("round trip mismatch: mantissa=%d -> decimal -> mantissa=%d", m, got)
		}
	}
}

func TestParseFIXTimestampMs(t *testing.T) {
	var withMillis int64 = parseFIXTimestampMs("20260728-16:30:00.500")
	if withMillis == 0 {
		t.Error("parseFIXTimestampMs with milliseconds returned 0")
	}

	var withoutMillis int64 = parseFIXTimestampMs("20260728-16:30:00")
	if withoutMillis == 0 {
		t.Error("parseFIXTimestampMs without milliseconds returned 0")
	}

	if withMillis-withoutMillis != 500 {
		t.Errorf("expected 500ms difference between the two timestamps, got %dms", withMillis-withoutMillis)
	}

	if got := parseFIXTimestampMs(""); got != 0 {
		t.Errorf("parseFIXTimestampMs(\"\") = %d, want 0", got)
	}

	if got := parseFIXTimestampMs("not-a-timestamp"); got != 0 {
		t.Errorf("parseFIXTimestampMs(garbage) = %d, want 0", got)
	}
}

func TestFixNowTimestampFormat(t *testing.T) {
	var s string = fixNowTimestamp()
	if len(s) != len("20060102-15:04:05.000") {
		t.Errorf("fixNowTimestamp() = %q, unexpected length %d", s, len(s))
	}
	if parseFIXTimestampMs(s) == 0 {
		t.Errorf("fixNowTimestamp() produced a string that parseFIXTimestampMs rejects: %q", s)
	}
}
