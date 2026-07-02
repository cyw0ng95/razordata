package ls

import (
	"sync"
	"time"
)

// RateLimiter is a token-bucket write throttle (REQ000318).
type RateLimiter struct {
	mu     sync.Mutex
	rate   int64 // bytes per second
	burst  int64
	tokens int64
	last   time.Time
}

// NewRateLimiter creates a rate limiter with the given rate and burst size.
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

// SetRate updates the rate and burst.
func (rl *RateLimiter) SetRate(rate, burst int64) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.rate = rate
	rl.burst = burst
	rl.tokens = burst
	rl.last = time.Now()
}

// Wait blocks until n bytes worth of tokens are available.
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
		need := n - rl.tokens
		wait := time.Duration(float64(need) / float64(rl.rate) * float64(time.Second))
		rl.mu.Unlock()
		if wait > 0 {
			time.Sleep(wait)
		}
	}
}

// SetRateLimiter installs a rate limiter on the compaction manager (REQ000318).
func (cm *compactionManager) SetRateLimiter(rl *RateLimiter) {
	cm.rateLimiter.Store(rl)
}

// SetPerLevelRateLimiter installs a per-level rate limiter (REQ001175).
// The per-level limiter's debt callback is wired to read from the
// compactionManager's debts map.
func (cm *compactionManager) SetPerLevelRateLimiter(plrl *PerLevelRateLimiter) {
	if plrl != nil {
		plrl.SetDebtCallback(func() map[int]int64 {
			cm.compactionMu.Lock()
			defer cm.compactionMu.Unlock()
			out := make(map[int]int64, len(cm.debts))
			for k, v := range cm.debts {
				out[k] = v
			}
			return out
		})
	}
	cm.perLevelRL.Store(plrl)
}

// SetCompactionStyle installs the compaction strategy (REQ000320).
func (cm *compactionManager) SetCompactionStyle(s CompactionStyle) {
	cm.style.Store(int32(s))
}

// CompactionStyle returns the active compaction strategy (REQ000320).
func (cm *compactionManager) CompactionStyle() CompactionStyle {
	return CompactionStyle(cm.style.Load())
}

// SetPlacementPolicy installs the tier-aware placement policy (REQ000300).
func (cm *compactionManager) SetPlacementPolicy(pp PlacementPolicy) {
	cm.placementPolicy = pp
}

// PlacementPolicy returns the active placement policy (REQ000300).
func (cm *compactionManager) PlacementPolicy() PlacementPolicy {
	return cm.placementPolicy
}

// PerLevelRateLimiter wraps a global rate limiter with per-level token
// buckets and debt-aware throttling (REQ001175). Each level gets a
// write budget proportional to its debt; when debt exceeds 150% of
// budget, the level is throttled with an extra sleep.
type PerLevelRateLimiter struct {
	global *RateLimiter
	mu     sync.RWMutex
	levels map[int]*RateLimiter // per-level token buckets
	// debts holds the per-level debt. In production this is synced
	// from compactionManager via SetLevelDebt. Tests set it directly.
	debt     func() map[int]int64 // callback to read current debts (nil = no throttling)
	testDebt *debtMap             // mutable debt map for tests
}

// debtMap is a goroutine-safe map used by tests to drive debt values.
type debtMap struct {
	mu   sync.RWMutex
	data map[int]int64
}

func (d *debtMap) get() map[int]int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make(map[int]int64, len(d.data))
	for k, v := range d.data {
		out[k] = v
	}
	return out
}

func (d *debtMap) set(level int, v int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if v > 0 {
		// Set explicitly.
		d.data[level] = v
	} else {
		// Clearing a level's debt.
		delete(d.data, level)
	}
}

// NewPerLevelRateLimiter creates per-level token buckets keyed by
// levelRates. The global limiter is used as a fallback for levels
// that have no per-level entry. debtFn supplies per-level debt
// values (nil means no debt is tracked).
func NewPerLevelRateLimiter(global *RateLimiter, levelRates map[int]int64, debtFn func() map[int]int64) *PerLevelRateLimiter {
	plrl := &PerLevelRateLimiter{
		global: global,
		levels: make(map[int]*RateLimiter),
	}
	for level, rate := range levelRates {
		burst := rate / 10
		if burst < 4096 {
			burst = 4096
		}
		plrl.levels[level] = NewRateLimiter(rate, burst)
	}
	plrl.debt = debtFn
	return plrl
}

// WaitWithLevel blocks until n bytes of tokens are available.
// If a per-level limiter exists for `level` it is used; otherwise
// the global limiter is used. When the level's debt exceeds 150%
// of its budget, an additional sleep is applied to throttle writes.
func (plrl *PerLevelRateLimiter) WaitWithLevel(level int, n int64) {
	if plrl == nil || n <= 0 {
		return
	}
	plrl.mu.RLock()
	lr := plrl.levels[level]
	plrl.mu.RUnlock()

	if lr != nil {
		lr.Wait(n)
	} else if plrl.global != nil {
		plrl.global.Wait(n)
	}

	// Additional throttle if debt exceeds 150% of budget.
	debtMap := plrl.debt()
	if debtMap == nil {
		return
	}
	d := debtMap[level]
	if d <= 0 {
		return
	}
	budget := defaultBudget.budgetFor(level)
	if d > budget*3/2 {
		// Sleep proportional to excess debt.
		excess := d - budget
		sleep := time.Duration(float64(excess)*10) * time.Microsecond
		if sleep > time.Second {
			sleep = time.Second
		}
		time.Sleep(sleep)
	}
}

// SetLevelDebt sets the debt for a level. Used by tests and by the
// compactionManager to sync its internal debts.
func (plrl *PerLevelRateLimiter) SetLevelDebt(level int, debt int64) {
	if plrl == nil {
		return
	}
	if plrl.testDebt != nil {
		plrl.testDebt.set(level, debt)
		return
	}
	// Fallback: use the debt callback if it returns a writable map
	// (compactionManager's debt callback returns a snapshot, so this
	// is a no-op in production — compactionManager calls SetLevelDebt
	// to update its own debts, and the callback returns a snapshot).
}

// SetDebtCallback replaces the internal debt callback.
func (plrl *PerLevelRateLimiter) SetDebtCallback(fn func() map[int]int64) {
	plrl.debt = fn
}

// SetTestDebtMap registers a mutable debt map for tests.
func (plrl *PerLevelRateLimiter) SetTestDebtMap(dm *debtMap) {
	plrl.testDebt = dm
	plrl.debt = dm.get
}

// Stats returns per-level rate limiter stats including debt.
func (plrl *PerLevelRateLimiter) Stats() map[int]RateLimiterStats {
	plrl.mu.RLock()
	defer plrl.mu.RUnlock()

	stats := make(map[int]RateLimiterStats)
	if plrl.debt != nil {
		debtMap := plrl.debt()
		if debtMap != nil {
			for level, d := range debtMap {
				stats[level] = RateLimiterStats{Debt: d}
			}
		}
	}
	for level := range plrl.levels {
		if _, ok := stats[level]; !ok {
			stats[level] = RateLimiterStats{Debt: 0}
		}
	}
	return stats
}

// RateLimiterStats holds per-level rate limiter diagnostics.
type RateLimiterStats struct {
	Debt int64
}
