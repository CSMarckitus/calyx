package auth

import (
	"strings"
	"sync"
	"time"

	"calyx/backend/internal/clock"
)

const (
	LimitPerIP    = 10
	LimitPerEmail = 5
	LimitWindow   = time.Minute
)

const maxBuckets = 10000

type bucket struct {
	start time.Time
	count int
}

// Limiter counts attempts per IP and per email in fixed windows of one minute.
type Limiter struct {
	mu      sync.Mutex
	clk     clock.Clock
	buckets map[string]*bucket
	// lastSweep is the time of the last sweep of stale buckets.
	lastSweep time.Time
}

func NewLimiter(clk clock.Clock) *Limiter {
	return &Limiter{clk: clk, buckets: make(map[string]*bucket)}
}

// current returns the live bucket of key, or a new empty one that is not stored yet.
func (l *Limiter) current(key string, now time.Time) *bucket {
	if b, ok := l.buckets[key]; ok && now.Before(b.start.Add(LimitWindow)) {
		return b
	}
	return &bucket{start: now}
}

// Allow counts one attempt of the given kind ("login", "reset", "accept", "lookup"). It returns
// false and counts nothing when the IP or the email bucket is full. An empty ip or email skips its
// bucket.
func (l *Limiter) Allow(kind, ip, email string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clk.Now()
	if len(l.buckets) > maxBuckets && now.Sub(l.lastSweep) >= LimitWindow {
		l.lastSweep = now
		for k, b := range l.buckets {
			if !now.Before(b.start.Add(LimitWindow)) {
				delete(l.buckets, k)
			}
		}
	}
	var ipBucket, emailBucket *bucket
	var ipKey, emailKey string
	if ip != "" {
		ipKey = kind + "|ip|" + ip
		ipBucket = l.current(ipKey, now)
		if ipBucket.count >= LimitPerIP {
			return false
		}
	}
	if email != "" {
		emailKey = kind + "|email|" + strings.ToLower(email)
		emailBucket = l.current(emailKey, now)
		if emailBucket.count >= LimitPerEmail {
			return false
		}
	}
	if ipBucket != nil {
		ipBucket.count++
		l.buckets[ipKey] = ipBucket
	}
	if emailBucket != nil {
		emailBucket.count++
		l.buckets[emailKey] = emailBucket
	}
	return true
}
