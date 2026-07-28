package iss

import "testing"

type testSecurity struct {
	SecID     string  `iss:"SECID"`
	ShortName string  `iss:"SHORTNAME"`
	LotSize   int64   `iss:"LOTSIZE"`
	MinStep   float64 `iss:"MINSTEP"`
}

func TestParseResponseAndScanInto(t *testing.T) {
	var body []byte = []byte(`{
		"securities": {
			"columns": ["SECID", "SHORTNAME", "LOTSIZE", "MINSTEP", "IGNOREME"],
			"data": [
				["SiZ5", "Si-12.25", 1, 1.0, "x"],
				["RIZ5", "RTS-12.25", 2, 5.0, null]
			]
		},
		"metadata": {"SECID": {"type": "string"}}
	}`)

	var resp Response
	var err error
	resp, err = ParseResponse(body)
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}

	var table Table
	var ok bool
	table, ok = resp["securities"]
	if !ok {
		t.Fatal("expected 'securities' block")
	}
	if _, exists := resp["metadata"]; exists {
		t.Fatal("'metadata' is not a table block and must be skipped")
	}

	var out []testSecurity
	if err = table.ScanInto(&out); err != nil {
		t.Fatalf("ScanInto: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if out[0].SecID != "SiZ5" || out[0].LotSize != 1 || out[0].MinStep != 1.0 {
		t.Errorf("out[0] = %+v", out[0])
	}
	if out[1].SecID != "RIZ5" || out[1].LotSize != 2 {
		t.Errorf("out[1] = %+v", out[1])
	}
}
