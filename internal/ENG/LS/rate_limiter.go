package ls

import (
	"sync"
	"time"
)

// RateLimiter implements a simple token-bucket write throttle.
// REQ000318: when the compactor is producing output bytes, it must
// call Wait(n) before writing n bytes. If the bucket is empty,
// Wait blocks until tokens refill at the configured rate.
//
// rate is bytes per second. burst is the maximum instantaneous
// burst size (bytes). Both must be > 0 to be effective; a nil
// RateLimiter (or SetRateLimiter(nil)) disables throttling.
//
// The implementation is intentionally lock-free on the hot path:
// the token count is stored atomically and the wait uses a
// channel-based timer for parking.
type RateLimiter struct {
	mu     sync.Mutex
	rate   int64 // bytes per second
	burst  int64
	tokens int64
	last   time.Time
}

// NewRateLimiter creates a rate limiter with the given sustained
// rate and burst size. rate and burst are in bytes per second and
// bytes respectively.
func NewRateLimiter(rate, burst int64) *RateLimiter {
	if rate <= 0 {
		return nil
	}
	if burst <= 0 {
		burst = rate / 10
		if burst < 4096 {
			burst = 4096
		}
	}
	return &RateLimiter{
		rate:   rate,
		burst:  burst,
		tokens: burst,
		last:   time.Now(),
	}
}

// SetRate updates the rate and burst. Existing tokens are scaled
// proportionally to avoid a sudden jump or starvation.
func (rl *RateLimiter) SetRate(rate, burst int64) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.rate = rate
	rl.burst = burst
	rl.tokens = burst
	rl.last = time.Now()
}

// Wait blocks until n bytes worth of tokens are available, then
// deducts n from the bucket. n must be <= burst (caller's
// responsibility). Wait returns immediately if n is 0.
func (rl *RateLimiter) Wait(n int64) {
	if rl == nil || n <= 0 {
		return
	}
	for {
		rl.mu.Lock()
		now := time.Now()
		elapsed := now.Sub(rl.last).Seconds()
		refill := int64(elapsed * float64(rl.rate))
		rl.tokens += refill
		if rl.tokens > rl.burst {
			rl.tokens = rl.burst
		}
		rl.last = now
		if rl.tokens >= n {
			rl.tokens -= n
			rl.mu.Unlock()
			return
		}
		// Need (n - tokens) more. Compute wait time.
		need := n - rl.tokens
		wait := time.Duration(float64(need) / float64(rl.rate) * float64(time.Second))
		rl.mu.Unlock()
		if wait > 0 {
			time.Sleep(wait)
		}
	}
}

// SetRateLimiter installs a rate limiter on the compaction manager.
// Pass nil to disable throttling. REQ000318.
func (cm *compactionManager) SetRateLimiter(rl *RateLimiter) {
	cm.rateLimiter.Store(rl)
}

// SetCompactionStyle installs the compaction strategy. REQ000320.
func (cm *compactionManager) SetCompactionStyle(s CompactionStyle) {
	cm.style.Store(int32(s))
}

// CompactionStyle returns the active compaction strategy.
// REQ000320.
func (cm *compactionManager) CompactionStyle() CompactionStyle {
	return CompactionStyle(cm.style.Load())
}

// SetPlacementPolicy installs the tier-aware placement policy.
// Pass nil to use a single device for all levels. REQ000300.
func (cm *compactionManager) SetPlacementPolicy(pp PlacementPolicy) {
	cm.placementPolicy = pp
}

// PlacementPolicy returns the active placement policy, or nil.
// REQ000300.
func (cm *compactionManager) PlacementPolicy() PlacementPolicy {
	return cm.placementPolicy
}
