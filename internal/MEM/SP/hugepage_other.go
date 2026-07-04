//go:build !linux

package sp

func mmapHugePages(pageCount int) (data []byte, fd int, err error) {
	return nil, -1, nil // not supported
}

func unlockHugePages(data []byte, fd int) {}

func hugePageEnabled() bool { return false }