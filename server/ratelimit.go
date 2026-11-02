package main

import (
	"container/list"
	"sync"
	"time"
)

// rateLimiter is an in-memory per-key sliding-window counter, LRU-bounded to
// rateLimiterMaxKeys so a flood of rotating keys costs O(1) per request and
// bounded memory. Best-effort abuse damping, not a hard cap (see rateLimitKey).
type rateLimiter struct {
	mu      sync.Mutex
	maxKeys int
	hits    map[string]*list.Element // key -> element wrapping *limiterEntry
	order   *list.List               // front = most recently touched
}

type limiterEntry struct {
	key string
	ts  []time.Time
}

// rateLimiterMaxKeys caps the key count; far above any real concurrent source count.
const rateLimiterMaxKeys = 4096

func newRateLimiter() *rateLimiter {
	return newBoundedRateLimiter(rateLimiterMaxKeys)
}

func newBoundedRateLimiter(maxKeys int) *rateLimiter {
	return &rateLimiter{
		maxKeys: maxKeys,
		hits:    map[string]*list.Element{},
		order:   list.New(),
	}
}

// touch moves key to the front of the LRU, evicting the oldest key at capacity. Caller holds rl.mu.
func (rl *rateLimiter) touch(key string) *limiterEntry {
	if el, ok := rl.hits[key]; ok {
		rl.order.MoveToFront(el)
		return el.Value.(*limiterEntry)
	}
	if rl.maxKeys > 0 && len(rl.hits) >= rl.maxKeys {
		if back := rl.order.Back(); back != nil {
			rl.order.Remove(back)
			delete(rl.hits, back.Value.(*limiterEntry).key)
		}
	}
	entry := &limiterEntry{key: key}
	rl.hits[key] = rl.order.PushFront(entry)
	return entry
}

// allow reports whether key may make another attempt within window, recording it if so.
func (rl *rateLimiter) allow(key string, limit int, window time.Duration) bool {
	now := time.Now()
	cutoff := now.Add(-window)

	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry := rl.touch(key)
	kept := entry.ts[:0]
	for _, t := range entry.ts {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		entry.ts = kept
		return false
	}
	entry.ts = append(kept, now)
	return true
}

// exhausted reports whether key has used its full limit within window, without recording an attempt.
// handleLogin uses it to delay (not reject) once exhausted; a mere check must never count as a hit.
func (rl *rateLimiter) exhausted(key string, limit int, window time.Duration) bool {
	now := time.Now()
	cutoff := now.Add(-window)

	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry := rl.touch(key)
	count := 0
	for _, t := range entry.ts {
		if t.After(cutoff) {
			count++
		}
	}
	return count >= limit
}

// refund removes key's most recent recorded attempt, undoing an allow that turned out not to count (a
// successful login reserved its failure slot before the compare). A no-op when key has no live attempts.
func (rl *rateLimiter) refund(key string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	el, ok := rl.hits[key]
	if !ok {
		return
	}
	entry := el.Value.(*limiterEntry)
	if n := len(entry.ts); n > 0 {
		entry.ts = entry.ts[:n-1]
	}
}
