package ls

import (
	"sync"
	"testing"
	"time"
)

func makeTestPLRL(globalRate int64, levelRates map[int]int64) (*PerLevelRateLimiter, *debtMap) {
	rl := NewRateLimiter(globalRate, globalRate/10)
	dm := &debtMap{data: make(map[int]int64)}
	plrl := NewPerLevelRateLimiter(rl, levelRates, nil)
	plrl.SetTestDebtMap(dm)
	return plrl, dm
}

func TestRateLimiter_PerLevelThrottle(t *testing.T) {
	plrl, dm := makeTestPLRL(100*1024*1024, map[int]int64{
		0: 100 * 1024 * 1024, // L0: 100MB/s
		1: 50 * 1024 * 1024,  // L1: 50MB/s
		2: 10 * 1024 * 1024,  // L2: 10MB/s
	})

	// Set L2 debt to 200% of budget (640MB debt on 320MB budget)
	dm.set(2, defaultBudget.l2*2)

	// L2 should be throttled more aggressively
	startTime := time.Now()
	plrl.WaitWithLevel(2, 1024*1024)
	l2Time := time.Since(startTime)

	// Reset debt
	dm.set(2, 0)
	startTime = time.Now()
	plrl.WaitWithLevel(2, 1024*1024)
	l2NormalTime := time.Since(startTime)

	// L2 with debt should take longer (throttled)
	if l2Time <= l2NormalTime {
		t.Errorf("L2 with debt should be slower: throttled=%v normal=%v", l2Time, l2NormalTime)
	}
}

func TestRateLimiter_NoStallUnderNormalLoad(t *testing.T) {
	rl := NewRateLimiter(1000*1024*1024, 100*1024*1024) // 1GB/s
	if rl == nil {
		t.Fatal("RateLimiter should not be nil")
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rl.Wait(1024 * 1024)
	}
	// If we get here without stalling for more than 5s, throughput is OK
}

func TestRateLimiter_PerLevelThrottle_MultipleLevels(t *testing.T) {
	plrl, dm := makeTestPLRL(100*1024*1024, map[int]int64{
		0: 100 * 1024 * 1024,
		1: 50 * 1024 * 1024,
		2: 10 * 1024 * 1024,
	})

	// Set L2 debt high, L0 debt low
	dm.set(2, defaultBudget.l2*2) // 200% — throttled
	dm.set(0, defaultBudget.l0/2) // 50% — normal

	// L0 should NOT be throttled
	startTime := time.Now()
	plrl.WaitWithLevel(0, 1024*1024)
	l0Time := time.Since(startTime)

	// L2 should be throttled
	startTime = time.Now()
	plrl.WaitWithLevel(2, 1024*1024)
	l2Time := time.Since(startTime)

	// L2 should take longer than L0
	if l2Time <= l0Time {
		t.Errorf("L2 (high debt) should be slower than L0 (low debt): L2=%v L0=%v", l2Time, l0Time)
	}
}

func TestRateLimiter_FallbackToGlobal(t *testing.T) {
	plrl, _ := makeTestPLRL(100*1024*1024, map[int]int64{
		0: 100 * 1024 * 1024, // only L0 has per-level limiter
	})

	// L1 has no per-level limiter — should fall back to global
	// No debt means no extra sleep, so this should be fast.
	start := time.Now()
	plrl.WaitWithLevel(1, 1024*1024)
	elapsed := time.Since(start)
	if elapsed >= time.Second {
		t.Errorf("Fallback to global rate limiter took too long: %v", elapsed)
	}
}

func TestRateLimiterStats(t *testing.T) {
	plrl, dm := makeTestPLRL(100*1024*1024, map[int]int64{
		0: 100 * 1024 * 1024,
		2: 10 * 1024 * 1024,
	})
	dm.set(2, 12345)

	stats := plrl.Stats()
	if stats[2].Debt != 12345 {
		t.Errorf("Stats[2].Debt = %d, want 12345", stats[2].Debt)
	}
	if stats[0].Debt != 0 {
		t.Errorf("Stats[0].Debt = %d, want 0", stats[0].Debt)
	}
}

func TestRateLimiter_ConcurrentAccess(t *testing.T) {
	plrl, dm := makeTestPLRL(100*1024*1024, map[int]int64{
		0: 100 * 1024 * 1024,
		1: 50 * 1024 * 1024,
	})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				plrl.WaitWithLevel(0, 1024)
			}
		}()
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				dm.set(1, int64(n*1000+j*100))
			}
		}(i)
	}
	wg.Wait()
}

func TestPerLevelRateLimiter_NilReceiver(t *testing.T) {
	var plrl *PerLevelRateLimiter
	// Should not panic
	plrl.WaitWithLevel(0, 1024)
	plrl2 := (*PerLevelRateLimiter)(nil)
	plrl2.WaitWithLevel(0, 1024)
}
