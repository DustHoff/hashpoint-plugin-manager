// Package cache provides a small generic in-memory TTL cache used by
// the catalog and github clients. Per spec §8: in-memory per plugin
// instance, no disk; configured TTL; manual bust on Configure().
package cache

import (
	"sync"
	"time"
)

// TTL is a concurrent map cache with per-instance expiration. A zero TTL
// means "no cache": every Get returns miss. The "no expiry" use case is
// rare for this plugin (the spec mandates a TTL), so we deliberately
// pick the more useful interpretation of 0.
type TTL[V any] struct {
	ttl   time.Duration
	mu    sync.RWMutex
	items map[string]entry[V]
	now   func() time.Time
}

type entry[V any] struct {
	val    V
	expiry time.Time
}

// New returns a TTL cache. ttl == 0 disables caching entirely.
func New[V any](ttl time.Duration) *TTL[V] {
	return &TTL[V]{
		ttl:   ttl,
		items: make(map[string]entry[V]),
		now:   time.Now,
	}
}

// Get returns the cached value for key. Returns (zero, false) when the
// key is absent, the entry has expired, or the cache TTL is zero.
func (c *TTL[V]) Get(key string) (V, bool) {
	var zero V
	if c.ttl == 0 {
		return zero, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.items[key]
	if !ok {
		return zero, false
	}
	if c.now().After(e.expiry) {
		return zero, false
	}
	return e.val, true
}

// Set stores val under key. Calls are no-ops when ttl == 0.
func (c *TTL[V]) Set(key string, val V) {
	if c.ttl == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = entry[V]{val: val, expiry: c.now().Add(c.ttl)}
}

// Reset drops every entry. Used by Configure() to bust caches on
// settings change (spec §8).
func (c *TTL[V]) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]entry[V])
}
