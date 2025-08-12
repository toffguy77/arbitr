package network

import (
	"sync"
	"time"
)

// Adaptive token bucket that reduces burst when median RTT degrades >2x baseline

type TokenBucket struct {
	mu       sync.Mutex
	capacity int
	tokens   float64
	rate     float64 // tokens per second
	last     time.Time
	baselineRTTms float64
}

func NewTokenBucket(capacity int, rate float64, baselineRTTms float64) *TokenBucket {
	return &TokenBucket{capacity: capacity, tokens: float64(capacity), rate: rate, last: time.Now(), baselineRTTms: baselineRTTms}
}

func (b *TokenBucket) Allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill(now)
	if b.tokens >= 1 {
		b.tokens -= 1
		return true
	}
	return false
}

func (b *TokenBucket) refill(now time.Time) {
	dt := now.Sub(b.last).Seconds()
	b.last = now
	b.tokens += b.rate * dt
	if b.tokens > float64(b.capacity) {
		b.tokens = float64(b.capacity)
	}
}

func (b *TokenBucket) AdjustForRTT(medianRTTms float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.baselineRTTms <= 0 { return }
	ratio := medianRTTms / b.baselineRTTms
	if ratio > 2.0 {
		// reduce burst and rate by up to 50%
		b.capacity = max(1, b.capacity/2)
		b.rate = b.rate * 0.5
		if b.tokens > float64(b.capacity) { b.tokens = float64(b.capacity) }
	}
}

func max(a, b int) int { if a>b {return a}; return b }
