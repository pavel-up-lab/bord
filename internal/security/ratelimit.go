package security

import (
	"fmt"
	"math"
	"sync"
	"time"
)

type Clock interface{ Now() time.Time }

type Rate struct {
	Capacity    float64
	RefillEvery time.Duration
}

type BucketKey struct {
	Dimension string
	Subject   string
	Operation string
	Class     string
}

func (k BucketKey) String() string {
	return k.Dimension + "\x00" + k.Subject + "\x00" + k.Operation + "\x00" + k.Class
}

type bucket struct {
	tokens    float64
	at        time.Time
	expiresAt time.Time
}

const maxLimiterBuckets = 10_000

type Limiter struct {
	mu         sync.Mutex
	clock      Clock
	rates      map[string]Rate
	buckets    map[string]bucket
	nextSweep  time.Time
	sweepEvery time.Duration
}

func NewLimiter(clock Clock, rates map[string]Rate) (*Limiter, error) {
	configured := make(map[string]Rate, len(rates))
	sweepEvery := time.Minute
	for class, rate := range rates {
		if class == "" || rate.Capacity < 1 || rate.Capacity > 1_000_000 || math.IsNaN(rate.Capacity) || math.IsInf(rate.Capacity, 0) || rate.RefillEvery <= 0 || rate.RefillEvery > 365*24*time.Hour {
			return nil, fmt.Errorf("invalid rate for class %q", class)
		}
		configured[class] = rate
		if rate.RefillEvery < sweepEvery {
			sweepEvery = rate.RefillEvery
		}
	}
	if len(configured) == 0 {
		return nil, fmt.Errorf("no rates configured")
	}
	return &Limiter{clock: clock, rates: configured, buckets: make(map[string]bucket), sweepEvery: sweepEvery}, nil
}

// Allow atomically consumes one token from every key. A rejected request does
// not consume any key, and retryAfter is the longest wait among exhausted keys.
func (l *Limiter) Allow(keys ...BucketKey) (allowed bool, retryAfter time.Duration) {
	if len(keys) == 0 {
		return false, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	if !l.nextSweep.After(now) || len(l.buckets) >= maxLimiterBuckets {
		for id, state := range l.buckets {
			if !state.expiresAt.After(now) {
				delete(l.buckets, id)
			}
		}
		l.nextSweep = now.Add(l.sweepEvery)
	}
	updated := make(map[string]bucket, len(keys))
	for _, key := range keys {
		rate, ok := l.rates[key.Class]
		if !ok {
			return false, 0
		}
		id := key.String()
		state, exists := l.buckets[id]
		if !exists {
			if len(l.buckets)+len(updated) >= maxLimiterBuckets {
				return false, rate.RefillEvery
			}
			state = bucket{tokens: rate.Capacity, at: now}
		} else if now.After(state.at) {
			state.tokens += float64(now.Sub(state.at)) / float64(rate.RefillEvery)
			if state.tokens > rate.Capacity {
				state.tokens = rate.Capacity
			}
			state.at = now
		}
		state.expiresAt = now.Add(time.Duration(rate.Capacity * float64(rate.RefillEvery)))
		updated[id] = state
		if state.tokens < 1 {
			wait := time.Duration((1 - state.tokens) * float64(rate.RefillEvery))
			if wait > retryAfter {
				retryAfter = wait
			}
		}
	}
	if retryAfter > 0 {
		return false, retryAfter
	}
	for id, state := range updated {
		state.tokens--
		l.buckets[id] = state
	}
	return true, 0
}
