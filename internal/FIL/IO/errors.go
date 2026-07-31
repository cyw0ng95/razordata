package uring

import (
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

var (
	ErrIO       = errors.New("I/O error")
	ErrDiskFull = errors.New("disk full (ENOSPC)")
)

func WrapWriteError(err error) error {
	if err == nil {
		return nil
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && errno == unix.ENOSPC {
		return fmt.Errorf("%w: %v", ErrDiskFull, err)
	}
	return fmt.Errorf("%w: %v", ErrIO, err)
}