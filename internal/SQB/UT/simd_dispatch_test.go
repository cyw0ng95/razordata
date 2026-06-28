package UT

import (
	"runtime"
	"testing"
)

// TestDetectFeatures_AnyArch returns a feature set appropriate
// for the current GOARCH. REQ000310.
func TestDetectFeatures_AnyArch(t *testing.T) {
	f := DetectFeatures()
	// At least one of the boolean fields should be plausible
	// for a real target. We just verify the function returns
	// without panicking and the struct is populated.
	t.Logf("features: %+v", f)
	// On amd64 we expect AVX2; on arm64 we expect NEON; on
	// other arches all flags may be false.
	switch runtime.GOARCH {
	case "amd64":
		if !f.HasAVX2 {
			t.Errorf("amd64 should report HasAVX2")
		}
	case "arm64":
		if !f.HasNEON {
			t.Errorf("arm64 should report HasNEON")
		}
	}
}
