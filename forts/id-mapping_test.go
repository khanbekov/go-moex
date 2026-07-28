package forts

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestClOrdIDGeneratorUnique(t *testing.T) {
	var gen *clOrdIDGenerator = newClOrdIDGenerator("GM")
	var seen map[string]bool = make(map[string]bool)
	for i := 0; i < 1000; i++ {
		var id string = gen.Next()
		if seen[id] {
			t.Fatalf("duplicate ClOrdID generated: %s", id)
		}
		seen[id] = true
		if len(id) == 0 || id[:2] != "GM" {
			t.Fatalf("unexpected ClOrdID format: %s", id)
		}
	}
}

func TestClOrdIDGeneratorDefaultPrefix(t *testing.T) {
	var gen *clOrdIDGenerator = newClOrdIDGenerator("")
	var id string = gen.Next()
	if id[:2] != "GM" {
		t.Errorf("expected default prefix GM, got %s", id)
	}
}

func TestClOrdIDGeneratorConcurrentUnique(t *testing.T) {
	var gen *clOrdIDGenerator = newClOrdIDGenerator("GM")
	var mu sync.Mutex
	var seen map[string]bool = make(map[string]bool)
	var wg sync.WaitGroup
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				var id string = gen.Next()
				mu.Lock()
				if seen[id] {
					t.Errorf("duplicate ClOrdID under concurrency: %s", id)
				}
				seen[id] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
}

func TestCorrelatorResolveDelivers(t *testing.T) {
	var c *correlator[string] = newCorrelator[string]()
	var ch chan result[string] = c.Register("clid-1")

	var ok bool = c.Resolve("clid-1", "hello", nil)
	if !ok {
		t.Fatal("Resolve returned false for a registered ClOrdID")
	}

	var ctx = context.Background()
	var val string
	var err error
	val, err = c.Wait(ctx, "clid-1", ch)
	if err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	if val != "hello" {
		t.Errorf("Wait returned %q, want %q", val, "hello")
	}
}

func TestCorrelatorResolveWithError(t *testing.T) {
	var c *correlator[string] = newCorrelator[string]()
	var ch chan result[string] = c.Register("clid-2")

	var wantErr error = errors.New("rejected")
	c.Resolve("clid-2", "", wantErr)

	var _, err = c.Wait(context.Background(), "clid-2", ch)
	if !errors.Is(err, wantErr) {
		t.Errorf("Wait returned error %v, want %v", err, wantErr)
	}
}

func TestCorrelatorResolveUnknownReturnsFalse(t *testing.T) {
	var c *correlator[string] = newCorrelator[string]()
	if c.Resolve("never-registered", "x", nil) {
		t.Error("Resolve returned true for an unregistered ClOrdID")
	}
}

func TestCorrelatorWaitContextCancellation(t *testing.T) {
	var c *correlator[string] = newCorrelator[string]()
	var ch chan result[string] = c.Register("clid-3")

	var ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	var _, err = c.Wait(ctx, "clid-3", ch)
	if err == nil {
		t.Fatal("expected Wait to return an error on context deadline")
	}

	c.mu.Lock()
	var _, stillPending = c.pending["clid-3"]
	c.mu.Unlock()
	if stillPending {
		t.Error("Wait did not clean up the pending registration after context cancellation")
	}
}

func TestCorrelatorDoubleResolveIsHarmless(t *testing.T) {
	var c *correlator[string] = newCorrelator[string]()
	c.Register("clid-4")

	if !c.Resolve("clid-4", "first", nil) {
		t.Fatal("first Resolve should succeed")
	}
	if c.Resolve("clid-4", "second", nil) {
		t.Error("second Resolve for the same ClOrdID should return false (already delivered/removed)")
	}
}
