package cache

import (
	"sync"
	"testing"
	"time"
)

// fakeClock returns time.Time values driven by a manually-advanced counter.
// Useful so cache expiry tests don't depend on wall-clock sleeps.
type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time          { return f.t }
func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }

func TestTTL_HitBeforeExpiry(t *testing.T) {
	c := New[string](1 * time.Minute)
	clk := &fakeClock{t: time.Now()}
	c.now = clk.now

	c.Set("k", "v")
	got, ok := c.Get("k")
	if !ok || got != "v" {
		t.Fatalf("Get before expiry: got=%q ok=%t, want v/true", got, ok)
	}

	clk.advance(30 * time.Second) // still within TTL
	got, ok = c.Get("k")
	if !ok || got != "v" {
		t.Fatalf("Get mid-TTL: got=%q ok=%t, want v/true", got, ok)
	}
}

func TestTTL_MissAfterExpiry(t *testing.T) {
	c := New[string](1 * time.Minute)
	clk := &fakeClock{t: time.Now()}
	c.now = clk.now

	c.Set("k", "v")
	clk.advance(2 * time.Minute) // past TTL

	if _, ok := c.Get("k"); ok {
		t.Errorf("Get after expiry: ok=true, want false")
	}
}

func TestTTL_Reset(t *testing.T) {
	c := New[string](1 * time.Minute)
	c.Set("k1", "v1")
	c.Set("k2", "v2")
	c.Reset()
	if _, ok := c.Get("k1"); ok {
		t.Errorf("k1 survived Reset")
	}
	if _, ok := c.Get("k2"); ok {
		t.Errorf("k2 survived Reset")
	}
}

func TestTTL_ZeroDisablesCaching(t *testing.T) {
	c := New[string](0)
	c.Set("k", "v")
	if _, ok := c.Get("k"); ok {
		t.Errorf("Get on ttl=0: ok=true, want false (no caching)")
	}
}

func TestTTL_MissingKey(t *testing.T) {
	c := New[int](time.Minute)
	got, ok := c.Get("nope")
	if ok || got != 0 {
		t.Errorf("Get missing: got=%d ok=%t, want 0/false", got, ok)
	}
}

func TestTTL_ConcurrentAccess(t *testing.T) {
	// Race detector check: many goroutines hammering Get/Set/Reset
	// must not produce data-race warnings.
	c := New[int](time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		i := i
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				c.Set("k", i+j)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_, _ = c.Get("k")
			}
		}()
	}
	wg.Wait()
}
