package httpx

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimiter is an in-memory token bucket per client IP.
type RateLimiter struct {
	capacity float64
	perSec   float64
	now      func() time.Time

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewRateLimiter allows a burst of n requests, refilled evenly over per.
func NewRateLimiter(n int, per time.Duration) *RateLimiter {
	return &RateLimiter{
		capacity: float64(n),
		perSec:   float64(n) / per.Seconds(),
		now:      time.Now,
		buckets:  map[string]*bucket{},
	}
}

// Allow takes one token for key. When empty it reports how long until the
// next token.
func (l *RateLimiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.capacity, last: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(l.capacity, b.tokens+now.Sub(b.last).Seconds()*l.perSec)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.perSec * float64(time.Second))
	return false, wait
}

// sweep drops buckets that have been idle long enough to be full again.
func (l *RateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	full := time.Duration(l.capacity / l.perSec * float64(time.Second))
	for k, b := range l.buckets {
		if now.Sub(b.last) > full {
			delete(l.buckets, k)
		}
	}
}

func (l *RateLimiter) Middleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ok, wait := l.Allow(ClientIP(r))
			if !ok {
				w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
				WriteError(w, http.StatusTooManyRequests, CodeRateLimited, "too many requests")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
