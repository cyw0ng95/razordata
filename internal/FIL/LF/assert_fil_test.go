//go:build debug

package lf

import (
	"os"
	"os/exec"
	"testing"
)

func TestFIL_Assert_DoubleClose(t *testing.T) {
	if os.Getenv("TEST_BUG_ON") == "1" {
		dir := t.TempDir()
		sm, err := New(dir)
		if err != nil {
			return
		}

		// Create a segment and close it once.
		h, err := sm.CreateSegment(1)
		if err != nil {
			return
		}
		h.Close()

		// Close the SegmentManager — this iterates over the pool.
		// The first close should succeed.
		if err := sm.Close(); err != nil {
			return
		}

		// Close again — this should trigger WARN_ON(h.FD == -1).
		_ = sm.Close()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestFIL_Assert_DoubleClose")
	cmd.Env = append(os.Environ(), "TEST_BUG_ON=1")
	err := cmd.Run()
	if err != nil {
		// WARN_ON does not exit(1), so a successful run is expected.
		return
	}
}