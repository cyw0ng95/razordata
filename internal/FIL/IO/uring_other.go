//go:build !linux || no_uring

// Package uring provides a non-Linux fallback that exposes the
// same interface as the Linux io_uring wrapper. On non-Linux
// platforms, the methods return ErrUnsupported.
//
// REQ000295 (iter-27): the FIL subsystem calls New on
// non-Linux platforms, gets a non-nil Ring that returns
// ErrUnsupported from SubmitWait, and the engine falls back
// to pread/pwrite.
package uring

import "errors"

// ErrUnsupported is returned by Ring methods on non-Linux
// platforms. REQ000295 (iter-27).
var ErrUnsupported = errors.New("uring: io_uring not supported on this platform")

// Ring is the platform-agnostic interface. On non-Linux, all
// methods return ErrUnsupported. REQ000295 (iter-27).
type Ring struct{}

// New is the non-Linux constructor. Returns a non-nil Ring
// whose methods return ErrUnsupported. REQ000295 (iter-27).
func New(entries int) (*Ring, error) {
	if entries <= 0 {
		return nil, errors.New("uring: entries must be > 0")
	}
	return &Ring{}, nil
}

// Close is a no-op on non-Linux. REQ000295 (iter-27).
func (r *Ring) Close() error { return nil }

// Fd returns -1 on non-Linux. REQ000295 (iter-27).
func (r *Ring) Fd() int { return -1 }

// SubmitWait returns ErrUnsupported on non-Linux. REQ000295.
func (r *Ring) SubmitWait(want int) (int, error) {
	return 0, ErrUnsupported
}

// RegisterFixedFile returns ErrUnsupported on non-Linux. REQ000296.
func (r *Ring) RegisterFixedFile(fd int) (int, error) {
	return -1, ErrUnsupported
}

// UnregisterFixedFile returns ErrUnsupported on non-Linux. REQ000296.
func (r *Ring) UnregisterFixedFile(index int) error {
	return ErrUnsupported
}
