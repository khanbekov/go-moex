/*
FILE: internal/iss/table.go

DESCRIPTION:
MOEX ISS responses use a distinctive columnar-table JSON shape shared by
every endpoint (securities lists, candles, order books, trades, ...):

	{
	  "securities": {
	    "metadata": {"SECID": {"type": "string"}, ...},
	    "columns": ["SECID", "BOARDID", "SHORTNAME", "LOTSIZE", ...],
	    "data": [["SiZ5", "SPBFUT", "Si-12.25", 1, ...], ...]
	  },
	  "marketdata": { ... same shape ... }
	}

Table + ScanInto below decode this shape generically ONCE, so that every
ISS-backed method (forts/market.go: GetSymbolInfo, GetHistoricalCandles,
...) writes a plain tagged struct instead of hand-rolling column-index
lookups per endpoint — the single biggest source of copy-paste bugs when
wrapping ISS by hand.
*/
package iss

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
)

// Table — one named block of the ISS response (e.g. "securities", "candles").
type Table struct {
	Columns []string
	Data    [][]json.RawMessage
}

// Response — the full decoded ISS payload: block name -> Table.
type Response map[string]Table

// rawBlock mirrors the {"columns":[...],"data":[...]} shape for a single
// block; Response.UnmarshalJSON below is one level up (block name -> block).
type rawBlock struct {
	Columns []string            `json:"columns"`
	Data    [][]json.RawMessage `json:"data"`
}

// ParseResponse decodes a full ISS JSON body into a Response. Blocks that
// don't match the {"columns":[...],"data":[...]} shape (e.g. "metadata" —
// itself present as a sibling key in real ISS responses under some
// endpoints) are silently skipped rather than failing the whole decode,
// since callers only ever read the block(s) they asked for by name.
func ParseResponse(body []byte) (Response, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("iss: decode response envelope: %w", err)
	}
	var out Response = make(Response, len(raw))
	for name, blockRaw := range raw {
		var block rawBlock
		if err := json.Unmarshal(blockRaw, &block); err != nil {
			continue // not a table block (e.g. "metadata") — skip.
		}
		if block.Columns == nil {
			continue
		}
		out[name] = Table{Columns: block.Columns, Data: block.Data}
	}
	return out, nil
}

// columnIndex builds a name->index lookup once per ScanInto call.
func (t Table) columnIndex() map[string]int {
	var idx map[string]int = make(map[string]int, len(t.Columns))
	for i, c := range t.Columns {
		idx[c] = i
	}
	return idx
}

// ScanInto decodes every row of t into a newly appended element of the
// slice pointed to by dest (dest must be a *[]StructType). Fields are
// matched by the `iss:"COLUMN_NAME"` struct tag; fields without the tag
// are left untouched. Supported destination field kinds: string, int64,
// int32, int, float64, bool. Missing/NULL ISS values leave the field at
// its zero value rather than erroring — ISS legitimately omits fields for
// delisted/illiquid instruments.
func (t Table) ScanInto(dest any) error {
	var destVal reflect.Value = reflect.ValueOf(dest)
	if destVal.Kind() != reflect.Ptr || destVal.Elem().Kind() != reflect.Slice {
		return fmt.Errorf("iss: ScanInto: dest must be a pointer to a slice, got %T", dest)
	}
	var sliceVal reflect.Value = destVal.Elem()
	var elemType reflect.Type = sliceVal.Type().Elem()

	var idx map[string]int = t.columnIndex()

	// Pre-compute (fieldIndex -> columnIndex) once — the struct shape is
	// identical for every row.
	type mapping struct {
		fieldIndex int
		colIndex   int
	}
	var mappings []mapping
	for i := 0; i < elemType.NumField(); i++ {
		var tag string = elemType.Field(i).Tag.Get("iss")
		if tag == "" {
			continue
		}
		if colIdx, ok := idx[tag]; ok {
			mappings = append(mappings, mapping{fieldIndex: i, colIndex: colIdx})
		}
	}

	for _, row := range t.Data {
		var elem reflect.Value = reflect.New(elemType).Elem()
		for _, m := range mappings {
			if m.colIndex >= len(row) {
				continue
			}
			var raw json.RawMessage = row[m.colIndex]
			if err := setField(elem.Field(m.fieldIndex), raw); err != nil {
				return fmt.Errorf("iss: field %q: %w", elemType.Field(m.fieldIndex).Name, err)
			}
		}
		sliceVal.Set(reflect.Append(sliceVal, elem))
	}
	return nil
}

func setField(field reflect.Value, raw json.RawMessage) error {
	if raw == nil || string(raw) == "null" {
		return nil // leave zero value.
	}
	switch field.Kind() {
	case reflect.String:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			// ISS sometimes returns numeric-looking values unquoted for
			// string columns (e.g. board ids) — fall back to raw text.
			field.SetString(string(raw))
			return nil
		}
		field.SetString(s)
	case reflect.Int, reflect.Int32, reflect.Int64:
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil {
			var s string
			if err2 := json.Unmarshal(raw, &s); err2 == nil {
				parsed, perr := strconv.ParseInt(s, 10, 64)
				if perr != nil {
					return fmt.Errorf("parse int from %q: %w", s, perr)
				}
				n = parsed
			} else {
				return fmt.Errorf("decode int: %w", err)
			}
		}
		field.SetInt(n)
	case reflect.Float32, reflect.Float64:
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil {
			return fmt.Errorf("decode float: %w", err)
		}
		field.SetFloat(f)
	case reflect.Bool:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return fmt.Errorf("decode bool: %w", err)
		}
		field.SetBool(b)
	default:
		return fmt.Errorf("unsupported destination kind %s", field.Kind())
	}
	return nil
}
