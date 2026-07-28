package iss

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/tonymontanov/go-moex/internal/moexerr"
)

func TestClientGetParsesResponse(t *testing.T) {
	var srv *httptest.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/engines/futures/markets/forts/securities.json" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.URL.Query().Get("iss.meta") != "off" {
			t.Errorf("expected iss.meta=off, got %q", r.URL.Query().Get("iss.meta"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"securities":{"columns":["SECID"],"data":[["SiZ5"]]}}`))
	}))
	defer srv.Close()

	var client *Client
	var err error
	client, err = NewClient(Config{
		BaseURL:        srv.URL,
		RequestTimeout: 2 * time.Second,
		UserAgent:      "go-moex-test",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	var resp Response
	resp, err = client.Get(context.Background(), "/engines/futures/markets/forts/securities.json", url.Values{"iss.meta": {"off"}})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(resp["securities"].Data) != 1 {
		t.Fatalf("securities.Data = %+v, want 1 row", resp["securities"].Data)
	}
}

func TestClientGetMapsHTTPErrors(t *testing.T) {
	var srv *httptest.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer srv.Close()

	var client *Client
	var err error
	client, err = NewClient(Config{BaseURL: srv.URL, RequestTimeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.Get(context.Background(), "/x.json", nil)
	if err == nil {
		t.Fatal("expected an error for HTTP 429")
	}
	if !moexerr.IsRateLimit(err) {
		t.Fatalf("err = %v, want ErrorKindRateLimit", err)
	}
}
