package share

import (
	"sync"
	"time"
)

// Failure limits for claim. Only wrong PINs count. The global limit stops
// guessing spread over many IPs: 10 000 PINs could otherwise be walked
// through within one 3-minute TTL.
const (
	IPFailures     = 10
	IPWindow       = 15 * time.Minute
	GlobalFailures = 30
	GlobalWindow   = time.Minute
)

// FailureLimiter counts failed claims in fixed windows, per IP and in total.
type FailureLimiter struct {
	now              func() time.Time
	ipMax, globalMax int

	mu     sync.Mutex
	ips    map[string]*window
	global window
}

type window struct {
	start time.Time
	count int
}

func NewFailureLimiter() *FailureLimiter {
	return &FailureLimiter{now: time.Now, ipMax: IPFailures, globalMax: GlobalFailures, ips: map[string]*window{}}
}

// Blocked reports whether ip may not try another PIN, and for how long.
func (l *FailureLimiter) Blocked(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweepLocked(now)

	var wait time.Duration
	if w, ok := l.ips[ip]; ok && w.count >= l.ipMax {
		wait = w.start.Add(IPWindow).Sub(now)
	}
	if l.global.count >= l.globalMax && now.Sub(l.global.start) < GlobalWindow {
		wait = max(wait, l.global.start.Add(GlobalWindow).Sub(now))
	}
	return wait > 0, wait
}

// Fail records one wrong PIN from ip.
func (l *FailureLimiter) Fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()

	w, ok := l.ips[ip]
	if !ok || now.Sub(w.start) >= IPWindow {
		w = &window{start: now}
		l.ips[ip] = w
	}
	w.count++

	if now.Sub(l.global.start) >= GlobalWindow {
		l.global = window{start: now}
	}
	l.global.count++
}

func (l *FailureLimiter) sweepLocked(now time.Time) {
	for ip, w := range l.ips {
		if now.Sub(w.start) >= IPWindow {
			delete(l.ips, ip)
		}
	}
}
