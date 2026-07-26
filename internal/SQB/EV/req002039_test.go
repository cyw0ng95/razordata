package EV

import (
	"sync"
	"testing"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestInHashCacheMap_ConcurrentAccess verifies that concurrent goroutines
// evaluating the same *PS.InExpr via EvalInHash do not trigger a
// "concurrent map writes" panic. REQ002039.
func TestInHashCacheMap_ConcurrentAccess(t *testing.T) {
	// Create a shared InExpr that all goroutines will evaluate.
	expr := &PS.InExpr{
		Expr: &PS.Ident{Name: "x"},
		List: []PS.Expr{
			&PS.NumberLiteral{Val: 1},
			&PS.NumberLiteral{Val: 2},
			&PS.NumberLiteral{Val: 3},
		},
	}

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, err := EvalInHash(expr, int64(1), nil, nil)
			if err != nil {
				t.Errorf("EvalInHash returned error: %v", err)
			}
		}()
	}

	wg.Wait()
}

// TestInHashCacheMap_ConcurrentDifferentExprs verifies that concurrent
// goroutines evaluating different InExpr pointers do not race.
// REQ002039.
func TestInHashCacheMap_ConcurrentDifferentExprs(t *testing.T) {
	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(val string) {
			defer wg.Done()
			expr := &PS.InExpr{
				Expr: &PS.Ident{Name: "x"},
				List: []PS.Expr{
					&PS.StringLiteral{Val: val},
				},
			}
			_, err := EvalInHash(expr, val, nil, nil)
			if err != nil {
				t.Errorf("EvalInHash returned error: %v", err)
			}
		}(string(rune('a' + i%26)))
	}

	wg.Wait()
}

// TestInHashCacheMap_DoubleCheckedLocking verifies that double-checked
// locking works: the second caller should see the cached entry without
// acquiring a write lock. REQ002039.
func TestInHashCacheMap_DoubleCheckedLocking(t *testing.T) {
	// Clear any existing entry
	inHashCacheMu.Lock()
	delete(InHashCacheMap, nil)
	inHashCacheMu.Unlock()

	expr := &PS.InExpr{
		Expr: &PS.Ident{Name: "y"},
		List: []PS.Expr{
			&PS.NumberLiteral{Val: 42},
		},
	}

	// First call should populate the cache
	result, err := EvalInHash(expr, int64(42), nil, nil)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if result != true {
		t.Fatalf("first call: expected true, got %v", result)
	}

	// Verify cache entry exists
	inHashCacheMu.RLock()
	cached := InHashCacheMap[expr]
	inHashCacheMu.RUnlock()
	if cached == nil {
		t.Fatal("cache entry not created")
	}

	// Second call should use cached entry
	result, err = EvalInHash(expr, int64(42), nil, nil)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if result != true {
		t.Fatalf("second call: expected true, got %v", result)
	}

	// Non-matching value
	result, err = EvalInHash(expr, int64(99), nil, nil)
	if err != nil {
		t.Fatalf("non-match call: %v", err)
	}
	if result != false {
		t.Fatalf("non-match call: expected false, got %v", result)
	}
}
