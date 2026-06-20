package SY

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

func validateOptions(o *AP.Options) error {
	if o == nil {
		return fmt.Errorf("%w: options is nil", AP.ErrInvalidOptions)
	}
	if !o.InMemory {
		if o.Dir == "" {
			return fmt.Errorf("%w: Dir is required", AP.ErrInvalidOptions)
		}
		if !filepath.IsAbs(o.Dir) {
			abs, err := filepath.Abs(o.Dir)
			if err != nil {
				return fmt.Errorf("%w: Dir cannot be resolved to an absolute path: %v", AP.ErrInvalidOptions, err)
			}
			o.Dir = abs
		}
		if info, err := os.Stat(o.Dir); err == nil && !info.IsDir() {
			return fmt.Errorf("%w: Dir exists but is not a directory: %s", AP.ErrInvalidOptions, o.Dir)
		}
		if o.PageSize < 1024 || o.PageSize > 65536 || !isPowerOfTwo(o.PageSize) {
			return fmt.Errorf("%w: PageSize=%d must be a power of 2 in [1024, 65536]", AP.ErrInvalidOptions, o.PageSize)
		}
		if o.MemTableSize < 1<<20 || o.MemTableSize > 1<<30 {
			return fmt.Errorf("%w: MemTableSize=%d must be in [1 MiB, 1 GiB]", AP.ErrInvalidOptions, o.MemTableSize)
		}
		if o.BufferPoolMB < 64 || o.BufferPoolMB > 4096 {
			return fmt.Errorf("%w: BufferPoolMB=%d must be in [64, 4096]", AP.ErrInvalidOptions, o.BufferPoolMB)
		}
		if o.WALSizeMB < 16 || o.WALSizeMB > 256 {
			return fmt.Errorf("%w: WALSizeMB=%d must be in [16, 256]", AP.ErrInvalidOptions, o.WALSizeMB)
		}
		if o.MaxLevel < 3 || o.MaxLevel > 10 {
			return fmt.Errorf("%w: MaxLevel=%d must be in [3, 10]", AP.ErrInvalidOptions, o.MaxLevel)
		}
	}
	return nil
}

func isPowerOfTwo(n int) bool { return n > 0 && n&(n-1) == 0 }
