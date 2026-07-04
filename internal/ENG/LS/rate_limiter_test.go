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
	origBudget := defaultBudget
	defaultBudget = levelBudget{
		l0: 1024,
		l1: 1024 * 10,
		l2: 1024 * 100,
	}
	t.Cleanup(func() { defaultBudget = origBudget })

	plrl, dm := makeTestPLRL(100*1024*1024, map[int]int64{
		0: 100 * 1024 * 1024,
		1: 50 * 1024 * 1024,
		2: 10 * 1024 * 1024,
	})

	dm.set(2, defaultBudget.l2*2)

	startTime := time.Now()
	plrl.WaitWithLevel(2, 1024*1024)
	l2Time := time.Since(startTime)

	dm.set(2, 0)
	startTime = time.Now()
	plrl.WaitWithLevel(2, 1024*1024)
	l2NormalTime := time.Since(startTime)

	if l2Time <= l2NormalTime {
		t.Errorf("L2 with debt should be slower: throttled=%v normal=%v", l2Time, l2NormalTime)
	}
}

func TestRateLimiter_NoStallUnderNormalLoad(t *testing.T) {
	// Burst is rate/10 = 100MB. Each call consumes 1MB, so after 100
	// calls the burst is exhausted and subsequent calls sleep ~1ms.
	// Run 100 iterations: they should complete well under 50ms.
	rl := NewRateLimiter(1000*1024*1024, 100*1024*1024) // 1GB/s, 100MB burst
	if rl == nil {
		t.Fatal("RateLimiter should not be nil")
	}

	const iters = 100
	start := time.Now()
	for i := 0; i < iters; i++ {
		rl.Wait(1024 * 1024)
	}
	elapsed := time.Since(start)
	if elapsed > 50*time.Millisecond {
		t.Errorf("%d iterations took %v, expected <50ms", iters, elapsed)
	}
}

func TestRateLimiter_PerLevelThrottle_MultipleLevels(t *testing.T) {
	origBudget := defaultBudget
	defaultBudget = levelBudget{
		l0: 1024,
		l1: 1024 * 10,
		l2: 1024 * 100,
	}
	t.Cleanup(func() { defaultBudget = origBudget })

	plrl, dm := makeTestPLRL(100*1024*1024, map[int]int64{
		0: 100 * 1024 * 1024,
		1: 50 * 1024 * 1024,
		2: 10 * 1024 * 1024,
	})

	dm.set(2, defaultBudget.l2*2)
	dm.set(0, defaultBudget.l0/2)

	startTime := time.Now()
	plrl.WaitWithLevel(0, 1024*1024)
	l0Time := time.Since(startTime)

	startTime = time.Now()
	dm.set(2, defaultBudget.l2*2)
	plrl.WaitWithLevel(2, 1024*1024)
	l2Time := time.Since(startTime)

	if l2Time <= l0Time {
		t.Errorf("L2 (high debt) should be slower than L0 (low debt): L2=%v L0=%v", l2Time, l0Time)
	}
}

func TestRateLimiter_FallbackToGlobal(t *testing.T) {
	plrl, _ := makeTestPLRL(100*1024*1024, map[int]int64{
		0: 100 * 1024 * 1024,
	})

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
	plrl.WaitWithLevel(0, 1024)
	plrl2 := (*PerLevelRateLimiter)(nil)
	plrl2.WaitWithLevel(0, 1024)
}
