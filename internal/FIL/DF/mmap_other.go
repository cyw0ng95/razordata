//go:build !linux

package df

// mmapBlock is not available on this platform. The caller falls
// back to pread/pwrite.
func mmapBlock(fd int, offset int64, size int) ([]byte, error) {
	return nil, errMmapUnsupported
}

// munmapBlock is a no-op when mmap is unavailable.
func munmapBlock(data []byte) error {
	return nil
}
