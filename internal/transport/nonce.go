package transport

import (
	"sync"
	"time"
)

type nonceCache struct {
	mu   sync.Mutex
	ttl  time.Duration
	seen map[string]time.Time
}

func newNonceCache(ttl time.Duration) *nonceCache {
	return &nonceCache{
		ttl:  ttl,
		seen: make(map[string]time.Time),
	}
}

func (c *nonceCache) accept(nonce string, now time.Time) bool {
	if nonce == "" {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	expiresBefore := now.Add(-c.ttl)
	for seenNonce, seenAt := range c.seen {
		if !seenAt.After(expiresBefore) {
			delete(c.seen, seenNonce)
		}
	}

	if seenAt, ok := c.seen[nonce]; ok && seenAt.Add(c.ttl).After(now) {
		return false
	}
	c.seen[nonce] = now
	return true
}
