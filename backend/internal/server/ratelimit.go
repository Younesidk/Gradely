package server

import (
	"sync"
	"time"
)

// slidingLimiter is an exact sliding-window log for the integrations endpoint. It keeps the time of every accepted
// request still inside the window, so "100 per 5 minutes" holds for every 5-minute span and there is no burst at a
// fixed-window boundary. Memory is bounded by the limit: a key never holds more timestamps than its limit.
//
// Gradly stores its data in one SQLite file and runs as a single process, so an in-process limiter sees every
// request. A deployment with several instances would need a shared store instead.
type slidingLimiter struct {
	mu        sync.Mutex
	window    time.Duration
	hits      map[string][]time.Time // ascending accept times, all within the window
	rejected  map[string]time.Time   // last time a rejection was logged for the key
	lastSweep time.Time
}

type limitCheck struct {
	key   string
	limit int
}

// limitResult describes the most restrictive of the checked limits.
type limitResult struct {
	Allowed    bool
	Limit      int
	Remaining  int
	Reset      time.Time // when the next slot frees up
	RetryAfter time.Duration
}

func newSlidingLimiter(window time.Duration) *slidingLimiter {
	return &slidingLimiter{window: window, hits: map[string][]time.Time{}, rejected: map[string]time.Time{}}
}

// prune drops timestamps that have left the window. Callers hold the lock.
func (l *slidingLimiter) prune(key string, now time.Time) []time.Time {
	list := l.hits[key]
	cut := 0
	for cut < len(list) && !list[cut].After(now.Add(-l.window)) {
		cut++
	}
	if cut > 0 {
		list = append(list[:0:0], list[cut:]...)
		if len(list) == 0 {
			delete(l.hits, key)
		} else {
			l.hits[key] = list
		}
	}
	return list
}

// sweep removes idle keys so memory follows the number of active credentials. Callers hold the lock.
func (l *slidingLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	for k := range l.hits {
		l.prune(k, now)
	}
	for k, t := range l.rejected {
		if now.Sub(t) >= l.window {
			delete(l.rejected, k)
		}
	}
}

// take checks every limit and, only if all of them have room, records the request against all of them. The check
// and the update happen under one lock, so concurrent requests cannot both squeeze into the last slot, and a
// rejected request consumes nothing.
func (l *slidingLimiter) take(now time.Time, checks ...limitCheck) limitResult {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)

	best := limitResult{Allowed: true, Remaining: -1}
	lists := make([][]time.Time, len(checks))
	for i, c := range checks {
		list := l.prune(c.key, now)
		lists[i] = list
		remaining := c.limit - len(list)
		reset := now.Add(l.window)
		if len(list) > 0 {
			reset = list[0].Add(l.window)
		}
		if remaining <= 0 {
			retry := reset.Sub(now)
			if !best.Allowed && retry <= best.RetryAfter {
				continue // an earlier failing limit already blocks for longer
			}
			best = limitResult{Allowed: false, Limit: c.limit, Remaining: 0, Reset: reset, RetryAfter: retry}
			continue
		}
		if best.Allowed && (best.Remaining < 0 || remaining-1 < best.Remaining) {
			best = limitResult{Allowed: true, Limit: c.limit, Remaining: remaining - 1, Reset: reset}
		}
	}
	if !best.Allowed {
		return best
	}
	for i, c := range checks {
		l.hits[c.key] = append(lists[i], now)
	}
	return best
}

// count reports how many requests the key made in the current window.
func (l *slidingLimiter) count(key string, now time.Time) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(key, now))
}

// shouldLogRejection limits activity-log writes for a flood of rejected requests to one per key per minute.
func (l *slidingLimiter) shouldLogRejection(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t, ok := l.rejected[key]; ok && now.Sub(t) < time.Minute {
		return false
	}
	l.rejected[key] = now
	return true
}
