//go:build debug

package SY

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

func TestSYS_Assert_DoubleOpen(t *testing.T) {
	if os.Getenv("TEST_BUG_ON") == "1" {
		dir := filepath.Join(os.TempDir(), "razor-double-open-test")
		os.MkdirAll(dir, 0o755)
		defer os.RemoveAll(dir)

		eng, err := Open(context.Background(), dir, AP.Options{
			PageSize:     4096,
			MemTableSize: 1 << 20,
			BufferPoolMB: 64,
			WALSizeMB:    16,
			MaxLevel:     3,
			LogLevel:     8,
			LogFormat:    "text",
		})
		if err != nil {
			return
		}
		defer eng.Close(context.Background())
		eng.open(context.Background())
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestSYS_Assert_DoubleOpen")
	cmd.Env = append(os.Environ(), "TEST_BUG_ON=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return
	}
	t.Fatal("expected BUG_ON to exit(1), but it didn't")
}
