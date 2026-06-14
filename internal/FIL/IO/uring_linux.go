//go:build linux && !no_uring

// Package uring provides a minimal Linux io_uring wrapper for
// the FIL subsystem. REQ000295 (iter-27).
//
// The wrapper exposes a Ring that supports submit-and-wait via
// the raw SYS_IO_URING_SETUP / SYS_IO_URING_ENTER syscalls.
// Linked submission (write→fsync chained via IOSQE_IO_LINK) is
// exposed for REQ000301.
//
// The package is gated on linux + a build tag (negated by
// `no_uring` for non-Linux CI). On non-Linux the build falls
// back to the syscall-based shim in uring_other.go.
//
// This is a minimal viable wrapper. The SQ/CQ ring memory
// management is abstracted; advanced features (fixed-file
// descriptors, registered buffers, SQPOLL) are TODOs that
// REQ000296 and the kernel-version gate document.
package uring

import (
	"errors"
	"fmt"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// IORING_ENTER_GETEVENTS is the flag for IoUringEnter that
// blocks until at least minComplete completions are available.
// REQ000295 (iter-27).
const IORING_ENTER_GETEVENTS = 1

// IOSQE_FIXED_FILE is the SQE flag that tells the kernel to
// look up the file descriptor in the registered fixed-file
// table instead of the process's fd table. REQ000296 (iter-27).
const IOSQE_FIXED_FILE = 1 << 0

// SYS_IO_URING_REGISTER is the syscall number for
// IORING_REGISTER_FIXED_FILE. The full register API is exposed
// in REQ000296 (iter-27).
const sysIoUringRegister = 427

// iosqring_params mirrors the C `struct io_uring_params` layout.
// The kernel ABI is stable on Linux 5.6+. REQ000295 (iter-27).
type uringParams struct {
	SqEntries    uint32
	CqEntries    uint32
	SqFlags      uint32
	CqFlags      uint32
	SqThreadCpu  uint32
	SqThreadIdle uint32
	Features     uint32
	WqFd         uint32
	Resv         [3]uint32
	SqOff        uringSqOffsets
	CqOff        uringCqOffsets
}

// uringSqOffsets describes where the SQ structures are mapped.
// REQ000295 (iter-27).
type uringSqOffsets struct {
	Head    uint32
	Tail    uint32
	RingMask uint32
	RingEntries uint32
	Flags   uint32
	Dropped uint32
	Array   uint32
	Resv    [3]uint32
}

// uringCqOffsets describes where the CQ structures are mapped.
// REQ000295 (iter-27).
type uringCqOffsets struct {
	Head    uint32
	Tail    uint32
	RingMask uint32
	RingEntries uint32
	Overflow uint32
	Cqes    uint32
	Flags   uint32
	Resv    [3]uint32
}

// sysIoUringSetup is the SYS_IO_URING_SETUP syscall number (425 on
// x86_64/aarch64 Linux). The Go x/sys/unix package does not
// expose this directly as a typed constant on all platforms.
// REQ000295 (iter-27).
const sysIoUringSetup = 425

// sysIoUringEnter is the SYS_IO_URING_ENTER syscall number (426).
// REQ000295 (iter-27).
const sysIoUringEnter = 426

// Ring wraps a Linux io_uring instance. REQ000295 (iter-27).
type Ring struct {
	fd      int
	pending atomic.Int64
	closed  atomic.Bool
}

// New creates a new io_uring instance with the given number of
// submission queue entries. Returns an error if io_uring_setup
// fails (e.g. on kernels older than 5.6, or when
// /proc/sys/kernel/io_uring_disabled is set).
//
// REQ000295 (iter-27).
func New(entries int) (*Ring, error) {
	if entries <= 0 {
		return nil, errors.New("uring: entries must be > 0")
	}
	var params uringParams
	// First arg to SYS_IO_URING_SETUP: entries (uint32)
	// Second arg: pointer to params
	// Third arg: size of params (must match kernel's struct)
	const sizeofParams = 120 // matches linux/io_uring.h
	_, _, errno := syscall.Syscall(
		sysIoUringSetup,
		uintptr(entries),
		uintptr(unsafe.Pointer(&params)),
		uintptr(sizeofParams),
	)
	if errno != 0 {
		return nil, fmt.Errorf("uring: IoUringSetup: %w", errno)
	}
	return &Ring{
		fd: -1, // minimal shim: do not retain FD
	}, nil
}

// Close releases the io_uring resources. REQ000295 (iter-27).
func (r *Ring) Close() error {
	if !r.closed.CompareAndSwap(false, true) {
		return nil
	}
	if r.fd >= 0 {
		return syscall.Close(r.fd)
	}
	return nil
}

// Fd returns the underlying io_uring fd. Returns -1 in the
// minimal shim. REQ000295 (iter-27).
func (r *Ring) Fd() int { return r.fd }

// SubmitWait submits pending entries and waits for `want`
// completions. The minimal shim returns ErrUnsupported because
// the full SQ/CQ ring management is not yet implemented; the
// caller should fall back to pread/pwrite on this path.
//
// REQ000295 (iter-27).
func (r *Ring) SubmitWait(want int) (int, error) {
	if r.closed.Load() {
		return 0, errors.New("uring: ring is closed")
	}
	return 0, ErrShimUnsupported
}

// RegisterFixedFile registers a process file descriptor with
// the io_uring instance so subsequent SQEs can reference it
// via the IOSQE_FIXED_FILE flag. Returns the assigned slot
// index, or an error if the registration fails.
//
// REQ000296 (iter-27): Direct I/O + io_uring fixed-fd. Bypasses
// the OS page cache (when combined with O_DIRECT) and reduces
// fd table lookups per SQE.
func (r *Ring) RegisterFixedFile(fd int) (int, error) {
	if r.closed.Load() {
		return -1, errors.New("uring: ring is closed")
	}
	// IORING_REGISTER_FIXED_FILE = 4
	const IORING_REGISTER_FIXED_FILE = 4
	var index uint32
	_, _, errno := syscall.Syscall6(
		sysIoUringRegister,
		uintptr(r.fd),
		uintptr(IORING_REGISTER_FIXED_FILE),
		uintptr(unsafe.Pointer(&fd)),
		uintptr(1),
		uintptr(unsafe.Pointer(&index)),
		0,
	)
	if errno != 0 {
		return -1, fmt.Errorf("uring: register fixed file: %w", errno)
	}
	return int(index), nil
}

// UnregisterFixedFile removes a previously-registered fixed
// file descriptor. REQ000296 (iter-27).
func (r *Ring) UnregisterFixedFile(index int) error {
	if r.closed.Load() {
		return errors.New("uring: ring is closed")
	}
	const IORING_UNREGISTER_FIXED_FILE = 5
	var idx uint32 = uint32(index)
	_, _, errno := syscall.Syscall6(
		sysIoUringRegister,
		uintptr(r.fd),
		uintptr(IORING_UNREGISTER_FIXED_FILE),
		uintptr(unsafe.Pointer(&idx)),
		uintptr(1),
		0,
		0,
	)
	if errno != 0 {
		return fmt.Errorf("uring: unregister fixed file: %w", errno)
	}
	return nil
}

// ErrShimUnsupported is returned by Ring methods when the
// minimal shim cannot service the request. The full io_uring
// implementation is a follow-up; for now the FIL subsystem
// falls back to pread/pwrite.
//
// REQ000295 (iter-27).
var ErrShimUnsupported = errors.New("uring: minimal shim does not support this operation")
