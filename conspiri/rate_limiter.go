package conspiribot

import (
	"sync"
	"time"
)

type RateLimiter struct {
	mu      sync.Mutex
	clients map[string][]time.Time
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		clients: make(map[string][]time.Time),
	}
}

func (r *RateLimiter) Allow(hostmask string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	threshold := now.Add(-30 * time.Second)

	// Filter and prune old timestamps
	var valid []time.Time
	for _, t := range r.clients[hostmask] {
		if t.After(threshold) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= 5 {
		r.clients[hostmask] = valid // update pruned list
		return false                // Rate limit exceeded
	}

	valid = append(valid, now)
	r.clients[hostmask] = valid
	return true
}
