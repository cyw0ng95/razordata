//go:build debug

package fl

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/LOG/LG"
)

func TestWAL_Assert_LSNRegression_ZeroLSN(t *testing.T) {
	d := newTestDeps(t)
	log := lg.New(lg.Options{Output: &nullWriter{}})
	f, err := New(d.root, d.sm, d.fm, log)
	if err != nil {
		t.Fatalf("fl.New: %v", err)
	}
	defer f.Close()

	if err := f.Sync(); err != nil {
		t.Logf("Sync returned error (expected with zero LSN): %v", err)
	}
}