// Package ratelimit is a small fixed-window limiter keyed by client address.
package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	n     int
	reset time.Time
}

type Limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	m      map[string]*bucket
}

func New(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, m: map[string]*bucket{}}
}

// Allow counts one request for key and reports whether it is within the limit.
func (l *Limiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.m) > 10000 { // an attacker rotating addresses must not grow memory without bound
		for k, b := range l.m {
			if now.After(b.reset) {
				delete(l.m, k)
			}
		}
	}
	b := l.m[key]
	if b == nil || now.After(b.reset) {
		b = &bucket{reset: now.Add(l.window)}
		l.m[key] = b
	}
	b.n++
	return b.n <= l.max
}
