//go:build debug

package SE

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	executor "github.com/cyw0ng95/razordata/internal/SQB/EX"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	"github.com/cyw0ng95/razordata/internal/SYS/SY"
)

func TestSYS_Assert_SessionBeginWithTxn(t *testing.T) {
	if os.Getenv("TEST_BUG_ON") == "1" {
		executor.UnregisterAll()
		dir := filepath.Join(os.TempDir(), "razor-se-bugon-test")
		os.MkdirAll(dir, 0o755)
		defer os.RemoveAll(dir)

		eng, err := SY.Open(context.Background(), dir, AP.Options{
			PageSize:     4096,
			MemTableSize: 1024 * 1024,
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

		s, err := eng.Begin(context.Background())
		if err != nil {
			return
		}
		tx, err := s.Begin(context.Background())
		if err != nil {
			return
		}
		_ = tx
		_, _ = s.Begin(context.Background())
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestSYS_Assert_SessionBeginWithTxn")
	cmd.Env = append(os.Environ(), "TEST_BUG_ON=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return
	}
	t.Fatal("expected BUG_ON to exit(1), but it didn't")
}
